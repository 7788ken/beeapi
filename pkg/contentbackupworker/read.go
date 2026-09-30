package contentbackupworker

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"gorm.io/gorm"
)

// ReadErrorCode classifies read failures for the admin UI (design doc 7.2).
const (
	ReadCodeBusy          = "busy"
	ReadCodeRemoteMissing = "remote_missing"
	// 远端给不出可用响应（网络、凭据、证书 pin、超时、权限）。必须与 remote_missing
	// 分开：只有 ErrFTPSMissing 才能断言"文件没了"，其余一律不许冒充缺失，
	// 否则客查会据此判定归档已丢失。
	ReadCodeRemoteError = "remote_error"
	// 本节点自己的目标配置不可用（未配置凭据、pin 非法）。不是远端的问题。
	ReadCodeTargetUnavailable = "target_unavailable"
	ReadCodeHashMismatch      = "hash_mismatch"
	ReadCodeTooLarge          = "too_large"
	ReadCodeNotUploaded       = "not_uploaded"
	// 库里根本没有这个 job（请求未开启备份、已被清理、ID 抄错）。跨进程之后
	// gorm.ErrRecordNotFound 的类型信息不会传到网关，不在这里分类就只能是 500。
	ReadCodeNotFound = "not_found"
)

var (
	ErrReadBusy        = errors.New("content backup read: the read slot is busy")
	ErrReadNotUploaded = errors.New("content backup read: job is not uploaded")
	ErrReadTooLarge    = errors.New("content backup read: decompressed body exceeds the budget")
	ErrReadNotFound    = errors.New("content backup read: no archive record for this request")
)

// ReadError carries a machine-readable code alongside the cause.
type ReadError struct {
	Code string
	Err  error
}

func (e *ReadError) Error() string { return e.Err.Error() }
func (e *ReadError) Unwrap() error { return e.Err }

// ReaderConfig wires the bounded read worker: one slot per node, independent
// from the upload workers (design doc 4.4 read_workers=1).
type ReaderConfig struct {
	SiteID        string
	StorageNodeID string
	Store         *model.ContentBackupStore
	RemoteFactory RemoteFactory
	MaxDecompress int64
	Timeout       time.Duration
}

// Reader serves preview/download through the single read slot. Decompression
// and hash verification happen here in the daemon, never in the gateway
// (design doc 4.1).
type Reader struct {
	cfg  ReaderConfig
	slot chan struct{}
}

func NewReader(cfg ReaderConfig) (*Reader, error) {
	if cfg.SiteID == "" || cfg.StorageNodeID == "" {
		return nil, fmt.Errorf("content backup read: site and storage node identity are required")
	}
	if cfg.Store == nil {
		return nil, fmt.Errorf("content backup read: store is required")
	}
	if cfg.RemoteFactory == nil {
		return nil, fmt.Errorf("content backup read: remote factory is required")
	}
	if cfg.MaxDecompress <= 0 {
		cfg.MaxDecompress = 25 * 1024 * 1024
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &Reader{cfg: cfg, slot: make(chan struct{}, 1)}, nil
}

// Acquire is non-blocking: a second concurrent read gets ErrReadBusy instead
// of queueing goroutines (design doc T10 acceptance).
func (r *Reader) Acquire() error {
	select {
	case r.slot <- struct{}{}:
		return nil
	default:
		return &ReadError{Code: ReadCodeBusy, Err: ErrReadBusy}
	}
}

func (r *Reader) Release() {
	<-r.slot
}

// PreviewSide is one bounded preview half.
type PreviewSide struct {
	ContentType   string `json:"content_type"`
	Encoding      string `json:"encoding"`
	Body          []byte `json:"-"`
	BodyBase64    string `json:"body"`
	CapturedBytes int64  `json:"captured_bytes"`
	Truncated     bool   `json:"truncated"`
	Complete      bool   `json:"complete"`
}

// PreviewResult carries both bounded sides.
type PreviewResult struct {
	JobID    string      `json:"job_id"`
	Request  PreviewSide `json:"request"`
	Response PreviewSide `json:"response"`
}

// ProbeStage is one named step of the connection probe.
type ProbeStage struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// ProbeResult reports the staged connection check.
type ProbeResult struct {
	Target string       `json:"target"`
	Stages []ProbeStage `json:"stages"`
}

// Preview fetches the remote gzip, verifies its digest, decompresses the
// envelope and returns at most maxBytesPerSide of each body. The complete
// read-back happens before any bytes are returned: a hash mismatch must
// abort, never stream half a file (design doc 7.2).
func (r *Reader) Preview(ctx context.Context, jobID string, maxBytesPerSide int64) (PreviewResult, error) {
	if err := r.Acquire(); err != nil {
		return PreviewResult{}, err
	}
	defer r.Release()

	raw, job, err := r.fetchVerified(ctx, jobID)
	if err != nil {
		return PreviewResult{}, err
	}

	envelope, err := decodeEnvelopeBytes(raw, r.cfg.MaxDecompress)
	if err != nil {
		if errors.Is(err, ErrReadTooLarge) {
			return PreviewResult{}, &ReadError{Code: ReadCodeTooLarge, Err: err}
		}
		return PreviewResult{}, &ReadError{Code: ReadCodeHashMismatch, Err: fmt.Errorf("content backup read: envelope: %w", err)}
	}
	result := PreviewResult{
		JobID:    jobID,
		Request:  boundedSide(envelope.Request, maxBytesPerSide),
		Response: boundedSide(envelope.Response, maxBytesPerSide),
	}
	_ = job
	return result, nil
}

// Download streams the verified gzip to the caller while hashing on the fly;
// a mismatch aborts the stream with a failure record instead of pretending a
// complete download (design doc 7.2).
func (r *Reader) Download(ctx context.Context, jobID string, sink io.Writer) error {
	if err := r.Acquire(); err != nil {
		return err
	}
	defer r.Release()

	job, remote, err := r.openJobRemote(ctx, jobID)
	if err != nil {
		return err
	}
	defer closeIfCloser(remote)

	body, err := remote.Open(ctx, job)
	if err != nil {
		return classifyRemoteErr(err)
	}
	defer body.Close()

	hasher := sha256.New()
	written, copyErr := io.Copy(sink, io.TeeReader(body, hasher))
	if copyErr != nil {
		return &ReadError{Code: ReadCodeHashMismatch, Err: fmt.Errorf("content backup read: stream aborted after %d bytes: %w", written, copyErr)}
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if got != job.CompressedSHA256 {
		return &ReadError{Code: ReadCodeHashMismatch, Err: fmt.Errorf("content backup read: download digest %s does not match compressed_sha256", got)}
	}
	return nil
}

// Probe runs the staged connection check against the saved target only:
// connect/write/read-back/rename/delete with a synthetic probe file. It can
// never probe an arbitrary host chosen by the caller (design doc 7.2).
func (r *Reader) Probe(ctx context.Context, targetID, host string) (ProbeResult, error) {
	if targetID == "" || host == "" {
		return ProbeResult{}, errors.New("content backup read: probe needs the saved target (target_id and the selected protocol's host) first")
	}
	remote, err := r.cfg.RemoteFactory(targetID)
	if err != nil {
		return ProbeResult{Target: targetID, Stages: []ProbeStage{probeStage("connect", false, err)}}, err
	}
	defer closeIfCloser(remote)

	now := time.Now().UTC()
	payload := []byte(fmt.Sprintf("content backup connection probe %s", now.Format(time.RFC3339)))
	payloadSHA := sha256.Sum256(payload)
	probeJobID := contentbackup.NewJobID()
	// 合成任务必须满足适配器的路径与摘要约束，否则写入阶段会被 checkJob 直接拒绝，
	// 「测试连接」在真实目标上永远不可能成功。
	probeJob := model.ContentBackupJob{
		SiteID:        r.cfg.SiteID,
		JobID:         probeJobID,
		StorageNodeID: r.cfg.StorageNodeID,
		TargetID:      targetID,
		RemotePath: fmt.Sprintf("/%s/probe/%s/%s%s", r.cfg.SiteID, now.Format("20060102"),
			probeJobID, contentbackup.RemoteFileExtension),
		CompressedSHA256: hex.EncodeToString(payloadSHA[:]),
		CompressedBytes:  int64(len(payload)),
		Status:           model.ContentBackupStatusUploaded,
	}
	probeLease := model.ContentBackupLease{
		SiteID:        r.cfg.SiteID,
		JobID:         probeJob.JobID,
		StorageNodeID: r.cfg.StorageNodeID,
		Owner:         "probe",
		// 令牌要进临时文件名，必须满足适配器的安全字符与长度约束。
		Token:      "probe-" + probeJobID,
		Generation: 1,
		Until:      now.Add(time.Minute),
	}

	result := ProbeResult{Target: targetID, Stages: []ProbeStage{probeStage("connect", true, nil)}}

	if err := remote.PutVerified(ctx, probeJob, probeLease, bytesReader(payload)); err != nil {
		result.Stages = append(result.Stages, probeStage("write", false, err))
		return result, err
	}
	result.Stages = append(result.Stages, probeStage("write", true, nil))

	body, err := remote.Open(ctx, probeJob)
	if err != nil {
		result.Stages = append(result.Stages, probeStage("read", false, err))
		return result, err
	}
	readBack, readErr := io.ReadAll(io.LimitReader(body, r.cfg.MaxDecompress))
	body.Close()
	if readErr != nil {
		result.Stages = append(result.Stages, probeStage("read", false, readErr))
		return result, readErr
	}
	result.Stages = append(result.Stages, probeStage("read", true, nil))

	if sha256.Sum256(readBack) != payloadSHA {
		err := errors.New("content backup read: probe read-back digest does not match what was written")
		result.Stages = append(result.Stages, probeStage("hash", false, err))
		return result, err
	}
	result.Stages = append(result.Stages, probeStage("hash", true, nil))
	// PutVerified 内部就是 STOR → 读回校验 → RNTO，写入成功即证明改名可用。
	result.Stages = append(result.Stages, probeStage("rename", true, nil))

	if err := remote.Remove(ctx, probeJob); err != nil {
		result.Stages = append(result.Stages, probeStage("delete", false, err))
		return result, err
	}
	result.Stages = append(result.Stages, probeStage("delete", true, nil))
	return result, nil
}

func (r *Reader) fetchVerified(ctx context.Context, jobID string) ([]byte, model.ContentBackupJob, error) {
	job, remote, err := r.openJobRemote(ctx, jobID)
	if err != nil {
		return nil, model.ContentBackupJob{}, err
	}
	defer closeIfCloser(remote)

	body, err := remote.Open(ctx, job)
	if err != nil {
		return nil, model.ContentBackupJob{}, classifyRemoteErr(err)
	}
	defer body.Close()

	raw, err := io.ReadAll(io.LimitReader(body, r.cfg.MaxDecompress*2+1024))
	if err != nil {
		return nil, model.ContentBackupJob{}, &ReadError{Code: ReadCodeHashMismatch, Err: err}
	}
	got := sha256.Sum256(raw)
	if hex.EncodeToString(got[:]) != job.CompressedSHA256 {
		return nil, model.ContentBackupJob{}, &ReadError{Code: ReadCodeHashMismatch, Err: errors.New("content backup read: remote gzip digest does not match compressed_sha256")}
	}
	return raw, job, nil
}

// classifyRemoteErr 把远端读取失败翻译成运营看得懂的状态。断言"文件没了"是
// 取证结论，只有适配器明确报 ErrFTPSMissing 时才允许下；其余（连不上、认证失败、
// 证书 pin 不符、超时、权限不足）都是远端接口失败，原始原因随消息一起带出去。
func classifyRemoteErr(err error) *ReadError {
	if errors.Is(err, ErrFTPSMissing) {
		return &ReadError{Code: ReadCodeRemoteMissing, Err: err}
	}
	return &ReadError{Code: ReadCodeRemoteError, Err: err}
}

func (r *Reader) openJobRemote(ctx context.Context, jobID string) (model.ContentBackupJob, RemoteStore, error) {
	job, err := r.cfg.Store.GetJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.ContentBackupJob{}, nil, &ReadError{Code: ReadCodeNotFound, Err: ErrReadNotFound}
		}
		return model.ContentBackupJob{}, nil, err
	}
	if job.Status != model.ContentBackupStatusUploaded {
		return model.ContentBackupJob{}, nil, &ReadError{Code: ReadCodeNotUploaded, Err: ErrReadNotUploaded}
	}
	remote, err := r.cfg.RemoteFactory(job.TargetID)
	if err != nil {
		return model.ContentBackupJob{}, nil, &ReadError{Code: ReadCodeTargetUnavailable, Err: err}
	}
	return job, remote, nil
}

// decodeEnvelopeBytes gunzips the verified blob and parses the 5.2 envelope.
func decodeEnvelopeBytes(raw []byte, maxDecompress int64) (contentbackup.Envelope, error) {
	gz, err := gzip.NewReader(bytesReader(raw))
	if err != nil {
		return contentbackup.Envelope{}, fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	// 多读 1 字节才能把「正好等于预算」和「超出预算」区分开。
	plain, err := io.ReadAll(io.LimitReader(gz, maxDecompress+1))
	if err != nil {
		return contentbackup.Envelope{}, fmt.Errorf("read gzip: %w", err)
	}
	if int64(len(plain)) > maxDecompress {
		return contentbackup.Envelope{}, ErrReadTooLarge
	}
	return contentbackup.DecodeEnvelope(plain)
}

// boundedSide trims one decoded preview half to the display budget.
func boundedSide(side contentbackup.EnvelopeBody, max int64) PreviewSide {
	body, _ := base64.StdEncoding.DecodeString(side.Body)
	truncated := int64(len(body)) > max || side.Truncated
	if int64(len(body)) > max {
		body = body[:max]
	}
	return PreviewSide{
		ContentType:   side.ContentType,
		Encoding:      "base64",
		Body:          body,
		BodyBase64:    base64.StdEncoding.EncodeToString(body),
		CapturedBytes: side.CapturedBytes,
		Truncated:     truncated,
		Complete:      side.Complete,
	}
}

func probeStage(name string, ok bool, err error) ProbeStage {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return ProbeStage{Name: name, OK: ok, Message: message}
}

func closeIfCloser(remote RemoteStore) {
	if closer, ok := remote.(io.Closer); ok {
		_ = closer.Close()
	}
}

func bytesReader(b []byte) io.Reader {
	return &byteSliceReader{data: b}
}

type byteSliceReader struct {
	data []byte
	pos  int
}

func (r *byteSliceReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// ReadErrorStatus 是读取失败到 HTTP 状态的唯一映射点。前端模板对任何 500 都整页跳 /500，
// 所以每个已知状态都必须有自己的码，绝不能兜底成 500。
func ReadErrorStatus(err error) int {
	var readErr *ReadError
	if errors.As(err, &readErr) {
		switch readErr.Code {
		case ReadCodeBusy:
			return http.StatusTooManyRequests
		case ReadCodeRemoteMissing, ReadCodeHashMismatch:
			return http.StatusConflict
		case ReadCodeNotUploaded:
			return http.StatusBadRequest
		case ReadCodeNotFound:
			return http.StatusNotFound
		case ReadCodeTooLarge:
			return http.StatusRequestEntityTooLarge
		case ReadCodeRemoteError:
			return http.StatusBadGateway
		case ReadCodeTargetUnavailable:
			return http.StatusServiceUnavailable
		}
	}
	return http.StatusInternalServerError
}

// ReadErrorCode 取出机器可读的码；没有码时返回空串，调用方据此判断是未分类失败。
func ReadErrorCode(err error) string {
	var readErr *ReadError
	if errors.As(err, &readErr) {
		return readErr.Code
	}
	return ""
}
