package contentbackupworker

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
)

// read.go 此前一个用例都没有。FTPS 协议层有 25 个用例，但把协议错误翻译成
// 运营看得懂的状态的这一层（预览/下载/连接探测）从没被证伪过 —— 典型的
// “测零件不测装配”。设计文档 9.3 要求“节点离线、目标错误、索引过期、远端缺失、
// 接口失败显示真实状态而非假 0/成功”，下面每个用例锁一条。

const t16Site = "sitea"
const t16Node = "node-a"
const t16Target = "target-a"

func t16Reader(t *testing.T, factory RemoteFactory, mutate func(*ReaderConfig)) *Reader {
	t.Helper()
	cfg := ReaderConfig{
		SiteID:        t16Site,
		StorageNodeID: t16Node,
		Store:         model.NewContentBackupStore(t05OpenDB(t), t16Site),
		RemoteFactory: factory,
		Timeout:       10 * time.Second,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	reader, err := NewReader(cfg)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	return reader
}

func t16Stage(t *testing.T, result ProbeResult, name string) ProbeStage {
	t.Helper()
	for _, stage := range result.Stages {
		if stage.Name == name {
			return stage
		}
	}
	t.Fatalf("probe 缺少 %q 阶段，实际阶段: %+v", name, result.Stages)
	return ProbeStage{}
}

// 「测试连接」按钮打到真实 FTPS 适配器上必须真的跑通。合成任务如果不满足
// checkJob 的路径/摘要约束，写入阶段会被适配器直接拒绝，按钮在生产上永远不可能成功。
func TestContentBackupProbeSucceedsAgainstSavedTarget(t *testing.T) {
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, nil)
	reader := t16Reader(t, func(string) (RemoteStore, error) { return store, nil }, nil)

	result, err := reader.Probe(context.Background(), t16Target, "127.0.0.1")
	if err != nil {
		t.Fatalf("Probe 必须能跑通已保存目标: %v (阶段: %+v)", err, result.Stages)
	}
	for _, name := range []string{"connect", "write", "read", "hash", "rename", "delete"} {
		stage := t16Stage(t, result, name)
		if !stage.OK {
			t.Fatalf("阶段 %s 应当成功: %s", name, stage.Message)
		}
	}
}

// 探测文件必须真的被删掉。此前 delete 阶段是写死的 true，从不发 DELE：
// 客户的 FTPS 上每点一次「测试连接」就永久多一个探测文件，界面还显示删除成功。
func TestContentBackupProbeRemovesSyntheticFile(t *testing.T) {
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, nil)
	reader := t16Reader(t, func(string) (RemoteStore, error) { return store, nil }, nil)

	if _, err := reader.Probe(context.Background(), t16Target, "127.0.0.1"); err != nil {
		t.Fatalf("Probe: %v", err)
	}

	if paths := srv.listPaths(); len(paths) != 0 {
		t.Fatalf("探测文件必须被删除，远端仍残留: %v", paths)
	}
	if !srv.hasEvent("DELETED ") {
		t.Fatalf("必须真的发出 DELE，事件: %v", srv.eventSnapshot())
	}
}

// 目标不允许删除时，界面必须显示真实失败，而不是绿勾。
func TestContentBackupProbeReportsDeleteFailure(t *testing.T) {
	srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) { cfg.refuseDelete = true })
	store := cbNewStore(t, srv, srv.pin, nil)
	reader := t16Reader(t, func(string) (RemoteStore, error) { return store, nil }, nil)

	result, err := reader.Probe(context.Background(), t16Target, "127.0.0.1")
	if err == nil {
		t.Fatalf("删除被拒时 Probe 必须报错，实际阶段: %+v", result.Stages)
	}
	stage := t16Stage(t, result, "delete")
	if stage.OK {
		t.Fatalf("删除被拒却报成功，这就是 9.3 禁止的假成功")
	}
	if stage.Message == "" {
		t.Fatalf("失败阶段必须带真实原因，便于运营判断是权限还是路径问题")
	}
}

// 正文超出解压预算时，必须报 too_large。此前被统一贴成 hash_mismatch，
// 等于告诉客查「这份取证归档损坏/被篡改」，而事实只是正文比预览预算大。
func TestContentBackupPreviewTooLargeNotReportedAsHashMismatch(t *testing.T) {
	gzBytes, job := t16SeedUploadedJob(t, strings.Repeat("A", 64*1024))
	remote := &t16FakeRemote{body: gzBytes}
	reader := t16Reader(t, func(string) (RemoteStore, error) { return remote, nil }, func(cfg *ReaderConfig) {
		cfg.MaxDecompress = 4 * 1024
		cfg.Store = job.store
	})

	_, err := reader.Preview(context.Background(), job.jobID, 1024)
	if err == nil {
		t.Fatalf("解压后超过 max_decompress 必须报错")
	}
	var readErr *ReadError
	if !errors.As(err, &readErr) {
		t.Fatalf("必须是带码的 ReadError，实际: %v", err)
	}
	if readErr.Code != ReadCodeTooLarge {
		t.Fatalf("错误码 = %q，应为 %q（把体积过大说成哈希不符会让取证结论反向）", readErr.Code, ReadCodeTooLarge)
	}
}

// 读槽被占用时必须带 busy 码，否则 controller 的 errors.As 落空，
// 返回 500「内部错误」而不是 429「稍后重试」，运营会以为系统坏了。
func TestContentBackupReadBusyCarriesCode(t *testing.T) {
	gzBytes, job := t16SeedUploadedJob(t, "hello")
	remote := &t16FakeRemote{body: gzBytes}
	reader := t16Reader(t, func(string) (RemoteStore, error) { return remote, nil }, func(cfg *ReaderConfig) {
		cfg.Store = job.store
	})

	if err := reader.Acquire(); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer reader.Release()

	_, err := reader.Preview(context.Background(), job.jobID, 1024)
	var readErr *ReadError
	if !errors.As(err, &readErr) || readErr.Code != ReadCodeBusy {
		t.Fatalf("读槽占用必须带 %q 码，实际: %v", ReadCodeBusy, err)
	}
	if !errors.Is(err, ErrReadBusy) {
		t.Fatalf("仍需可用 errors.Is(ErrReadBusy) 判定，实际: %v", err)
	}
}

// 本节点凭据缺失 / pin 非法时，RemoteFactory 在建连之前就构造失败。此前这个错误
// 裸奔到 controller 被兜底成 500，而前端模板对任何 500 都会把整个后台跳到 /500
// 错误页：运营点一次「加载预览」就丢掉整个管理页面。这比显示假状态更糟，
// 因为连"哪个目标配错了"都看不到。
func TestContentBackupPreviewTargetMisconfiguredCarriesCode(t *testing.T) {
	_, job := t16SeedUploadedJob(t, "hello")
	reader := t16Reader(t, func(string) (RemoteStore, error) {
		return nil, errors.New("content backup ftps: username is required")
	}, func(cfg *ReaderConfig) {
		cfg.Store = job.store
	})

	_, err := reader.Preview(context.Background(), job.jobID, 1024)
	var readErr *ReadError
	if !errors.As(err, &readErr) {
		t.Fatalf("目标不可用必须是带码的 ReadError，否则 controller 兜底 500 会把整个后台跳到错误页，实际: %v", err)
	}
	if readErr.Code != ReadCodeTargetUnavailable {
		t.Fatalf("错误码 = %q，应为 %q", readErr.Code, ReadCodeTargetUnavailable)
	}
}

// 连不上/认证失败/证书不匹配，被统一贴成 remote_missing 等于告诉客查
// 「这份取证归档在远端已经没了」，而真相只是这台节点连不上存储。
// 取证结论会因此反向，且没人会去查网络与凭据。
func TestContentBackupPreviewRemoteFailureNotReportedAsMissing(t *testing.T) {
	_, job := t16SeedUploadedJob(t, "hello")
	remote := &t16FakeRemote{openErr: fmt.Errorf("content backup ftps: retr: %w", ErrFTPSAuth)}
	reader := t16Reader(t, func(string) (RemoteStore, error) { return remote, nil }, func(cfg *ReaderConfig) {
		cfg.Store = job.store
	})

	_, err := reader.Preview(context.Background(), job.jobID, 1024)
	var readErr *ReadError
	if !errors.As(err, &readErr) {
		t.Fatalf("必须是带码的 ReadError，实际: %v", err)
	}
	if readErr.Code != ReadCodeRemoteError {
		t.Fatalf("错误码 = %q，应为 %q（把连不上说成远端缺失会让客查判定归档已丢失）", readErr.Code, ReadCodeRemoteError)
	}
}

// 反向锁死：真的远端缺失仍必须是 remote_missing，不能被上一条修复顺手改掉。
func TestContentBackupPreviewRemoteMissingKeepsItsCode(t *testing.T) {
	_, job := t16SeedUploadedJob(t, "hello")
	remote := &t16FakeRemote{openErr: fmt.Errorf("content backup ftps: retr /x: %w (550)", ErrFTPSMissing)}
	reader := t16Reader(t, func(string) (RemoteStore, error) { return remote, nil }, func(cfg *ReaderConfig) {
		cfg.Store = job.store
	})

	_, err := reader.Preview(context.Background(), job.jobID, 1024)
	var readErr *ReadError
	if !errors.As(err, &readErr) || readErr.Code != ReadCodeRemoteMissing {
		t.Fatalf("远端真缺失必须保持 %q，实际: %v", ReadCodeRemoteMissing, err)
	}
}

// 下载走的是另一条分支，同一类误判必须一起锁住。
func TestContentBackupDownloadRemoteFailureNotReportedAsMissing(t *testing.T) {
	_, job := t16SeedUploadedJob(t, "hello")
	remote := &t16FakeRemote{openErr: fmt.Errorf("content backup ftps: dial: %w", ErrFTPSNetwork)}
	reader := t16Reader(t, func(string) (RemoteStore, error) { return remote, nil }, func(cfg *ReaderConfig) {
		cfg.Store = job.store
	})

	err := reader.Download(context.Background(), job.jobID, io.Discard)
	var readErr *ReadError
	if !errors.As(err, &readErr) || readErr.Code != ReadCodeRemoteError {
		t.Fatalf("下载连不上必须报 %q，实际: %v", ReadCodeRemoteError, err)
	}
}

type t16SeededJob struct {
	jobID string
	store *model.ContentBackupStore
}

// t16SeedUploadedJob 造一条真的 uploaded 记录 + 与之摘要一致的 gzip 信封。
func t16SeedUploadedJob(t *testing.T, body string) ([]byte, t16SeededJob) {
	t.Helper()
	jobID := contentbackup.NewJobID()
	meta := t05Meta(jobID)
	request := []byte(`{"prompt":` + fmt.Sprintf("%q", body) + `}`)
	response := []byte(`{"completion":` + fmt.Sprintf("%q", body) + `}`)
	meta.Request = contentbackup.BodyMeta{ContentType: "application/json", CapturedBytes: int64(len(request)), ObservedBytes: int64(len(request)), Complete: true}
	meta.Response = contentbackup.BodyMeta{ContentType: "application/json", CapturedBytes: int64(len(response)), ObservedBytes: int64(len(response)), Complete: true}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	frame := append(append([]byte(nil), request...), response...)
	frameSHA := fmt.Sprintf("%x", sha256.Sum256(frame))
	if err := contentbackup.WriteEnvelope(gz, meta, [][]byte{request}, [][]byte{response}, frameSHA); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	raw := buf.Bytes()
	remotePath, err := meta.RemotePath()
	if err != nil {
		t.Fatalf("RemotePath: %v", err)
	}
	sum := sha256.Sum256(raw)

	store := model.NewContentBackupStore(t05OpenDB(t), t05SiteID)
	job := model.ContentBackupJob{
		SiteID:           t05SiteID,
		JobID:            jobID,
		RequestID:        meta.RequestID,
		UserID:           meta.UserID,
		StorageNodeID:    t05NodeID,
		TargetID:         t05TargetID,
		RemotePath:       remotePath,
		FrameSHA256:      frameSHA,
		CompressedSHA256: hex.EncodeToString(sum[:]),
		CompressedBytes:  int64(len(raw)),
		CreatedAt:        meta.RequestStartedAt.Unix(),
		Status:           model.ContentBackupStatusPending,
	}
	if err := store.EnsurePending(context.Background(), job); err != nil {
		t.Fatalf("EnsurePending: %v", err)
	}
	now := time.Unix(meta.RequestStartedAt.Unix(), 0).UTC()
	lease, ok, err := store.Claim(context.Background(), jobID, t05NodeID, "t16", now, now.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("Claim: ok=%v err=%v", ok, err)
	}
	if err := store.MarkUploaded(context.Background(), lease, now); err != nil {
		t.Fatalf("MarkUploaded: %v", err)
	}
	return raw, t16SeededJob{jobID: jobID, store: store}
}

type t16FakeRemote struct {
	body    []byte
	openErr error
}

func (f *t16FakeRemote) PutVerified(context.Context, model.ContentBackupJob, model.ContentBackupLease, io.Reader) error {
	return nil
}

func (f *t16FakeRemote) Open(context.Context, model.ContentBackupJob) (io.ReadCloser, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

func (f *t16FakeRemote) Remove(context.Context, model.ContentBackupJob) error { return nil }

func (f *t16FakeRemote) ListIncoming(context.Context, string) ([]IncomingTemp, error) {
	return nil, nil
}

func (f *t16FakeRemote) RemoveIncoming(context.Context, IncomingTemp) error { return nil }
