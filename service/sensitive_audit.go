package service

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
)

// SensitiveAuditJob 异步审计任务。channel 中只携带元数据，body 全文已写到 dump 文件。
//
// 注意：channel 容量 4096，故意不带 body 字节，避免高并发下 channel 缓冲爆 RAM。
// dump 写盘失败的请求直接丢弃（calls 内打 SysError），不走"内存 fallback"路径。
type SensitiveAuditJob struct {
	JobID       string
	StorageNode string
	LeaseToken  string
	RequestID   string
	UserID      int
	Username    string
	TokenID     int
	TokenName   string
	ChannelID   int
	ChannelName string
	ModelName   string
	Path        string
	IP          string
	UserAgent   string
	DumpPath    string
	BodySha256  string
	BodySize    int64
	CreatedAt   int64
}

// SensitiveDumpPayload 写盘 / 读盘的统一结构，gzip(JSON) 编码。
type SensitiveDumpPayload struct {
	SchemaVersion int    `json:"schema_version"`
	JobID         string `json:"job_id"`
	RequestID     string `json:"request_id"`
	UserID        int    `json:"user_id"`
	ChannelID     int    `json:"channel_id"`
	Path          string `json:"path"`
	Body          string `json:"body"`
	BodySHA256    string `json:"body_sha256"`
	BodySize      int64  `json:"body_size"`
	Timestamp     int64  `json:"timestamp"`
}

const (
	defaultSensitiveDumpRoot = "/var/lib/newapi/sensitive_dump"
	envSensitiveDumpRoot     = "SENSITIVE_DUMP_ROOT"
	envSensitiveAuditWorkers = "SENSITIVE_AUDIT_WORKERS"
	defaultSensitiveQueueCap = 4096
	sensitiveAuditLease      = 2 * time.Minute
	sensitiveAuditPoll       = time.Second
	sensitiveDumpMagic       = "SAJ2\n"
	sensitiveDumpHeaderLimit = 64 * 1024
)

var (
	ErrSensitiveAuditNotAccepting = errors.New("sensitive audit pipeline is not accepting submissions")
	ErrSensitiveAuditJobsFailed   = errors.New("sensitive audit pipeline jobs failed")
)

type sensitiveLifecycleState uint8

const (
	sensitiveLifecycleNew sensitiveLifecycleState = iota
	sensitiveLifecycleRunning
	sensitiveLifecycleStopping
	sensitiveLifecycleStopped
)

type SensitiveAuditPipeline struct {
	mu        sync.Mutex
	state     sensitiveLifecycleState
	queueCap  int
	queue     chan SensitiveAuditJob
	stopped   chan struct{}
	stopErr   error
	durable   bool
	nodeID    string
	processID string

	producers    sync.WaitGroup
	workers      sync.WaitGroup
	recovery     sync.WaitGroup
	stopRecovery chan struct{}

	writeDump    func(SensitiveAuditJob, []byte) (string, error)
	process      func(SensitiveAuditJob) error
	asyncEnabled func() bool
	dumpEnabled  func() bool

	dropped   atomic.Int64
	enqueued  atomic.Int64
	processed atomic.Int64
	failed    atomic.Int64
}

func NewSensitiveAuditPipeline(queueCapacity int) *SensitiveAuditPipeline {
	if queueCapacity <= 0 {
		queueCapacity = defaultSensitiveQueueCap
	}
	return &SensitiveAuditPipeline{
		state:        sensitiveLifecycleNew,
		queueCap:     queueCapacity,
		stopped:      make(chan struct{}),
		stopRecovery: make(chan struct{}),
		writeDump:    writeSensitiveDump,
		process:      processSensitiveAuditJob,
		asyncEnabled: setting.GetSensitiveAsyncEnabled,
		dumpEnabled:  setting.GetSensitiveDumpToFile,
	}
}

var (
	defaultSensitiveAuditPipeline = newDurableSensitiveAuditPipeline(defaultSensitiveQueueCap)

	// 可疑用户 LRU：阶段 1 仅实现接口，命中标记由阶段 4 启用；阶段 1 始终返回 false。
	suspiciousUsers sync.Map // userID(int) -> expiresUnix(int64)
	suspiciousTTL   int64    = 30 * 60
)

func newDurableSensitiveAuditPipeline(queueCapacity int) *SensitiveAuditPipeline {
	pipeline := NewSensitiveAuditPipeline(queueCapacity)
	pipeline.durable = true
	pipeline.nodeID = strings.TrimSpace(common.NodeName)
	pipeline.processID = common.GetUUID()
	return pipeline
}

func (p *SensitiveAuditPipeline) Start(workers int) error {
	if p == nil {
		return errors.New("sensitive audit pipeline is nil")
	}
	if workers <= 0 {
		return fmt.Errorf("sensitive audit worker count must be positive, got %d", workers)
	}
	if p.durable {
		p.nodeID = strings.TrimSpace(common.NodeName)
		if p.nodeID == "" && p.asyncEnabled() {
			return errors.New("NODE_NAME is required for durable sensitive audit")
		}
		if p.nodeID != "" {
			if err := model.CheckSensitiveAuditSchemaReady(); err != nil {
				return err
			}
		}
	}

	p.mu.Lock()
	switch p.state {
	case sensitiveLifecycleRunning:
		p.mu.Unlock()
		return nil
	case sensitiveLifecycleStopping, sensitiveLifecycleStopped:
		p.mu.Unlock()
		return ErrSensitiveAuditNotAccepting
	}
	p.queue = make(chan SensitiveAuditJob, p.queueCap)
	p.workers.Add(workers)
	p.state = sensitiveLifecycleRunning
	queue := p.queue
	p.mu.Unlock()

	for i := 0; i < workers; i++ {
		go p.runWorker(queue)
	}
	if p.durable && p.nodeID != "" {
		p.recovery.Add(1)
		go p.runRecoveryPoller()
		p.notifyDurableWork(SensitiveAuditJob{})
	}
	return nil
}

func (p *SensitiveAuditPipeline) runWorker(queue <-chan SensitiveAuditJob) {
	defer p.workers.Done()
	for job := range queue {
		if p.durable {
			p.drainDurableJobs()
			continue
		}
		p.runJob(job)
	}
}

func (p *SensitiveAuditPipeline) runJob(job SensitiveAuditJob) (resultErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			p.failed.Add(1)
			common.SysError(fmt.Sprintf("[sensitive] audit worker panic recovered request_id=%s: %v", job.RequestID, recovered))
			resultErr = fmt.Errorf("sensitive audit worker panic: %v", recovered)
		}
	}()
	if err := p.process(job); err != nil {
		p.failed.Add(1)
		common.SysError("[sensitive] audit job failed request_id=" + job.RequestID + " err=" + err.Error())
		return err
	}
	p.processed.Add(1)
	return nil
}

func (p *SensitiveAuditPipeline) runRecoveryPoller() {
	defer p.recovery.Done()
	ticker := time.NewTicker(sensitiveAuditPoll)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopRecovery:
			return
		case <-ticker.C:
			p.notifyDurableWork(SensitiveAuditJob{})
		}
	}
}

func (p *SensitiveAuditPipeline) notifyDurableWork(job SensitiveAuditJob) {
	p.mu.Lock()
	queue := p.queue
	running := p.state == sensitiveLifecycleRunning
	p.mu.Unlock()
	if !running || queue == nil {
		return
	}
	select {
	case queue <- job:
	default:
		// The channel is only a wake hint. A durable pending row must never be
		// removed because the hint buffer is full.
	}
}

func (p *SensitiveAuditPipeline) drainDurableJobs() {
	for {
		now := time.Now().Unix()
		records, err := model.ListRecoverableSensitiveAuditJobs(p.nodeID, now, p.queueCap)
		if err != nil {
			p.failed.Add(1)
			common.SysError("[sensitive] recover durable jobs failed node=" + p.nodeID + " err=" + err.Error())
			return
		}
		if len(records) == 0 {
			return
		}

		claimedAny := false
		for _, record := range records {
			leaseToken := common.GetUUID()
			claimed, err := model.TryClaimSensitiveAuditJob(
				record.JobID,
				p.nodeID,
				p.processID,
				leaseToken,
				now,
				now+int64(sensitiveAuditLease/time.Second),
			)
			if err != nil {
				p.failed.Add(1)
				common.SysError("[sensitive] claim durable job failed job_id=" + record.JobID + " err=" + err.Error())
				continue
			}
			if !claimed {
				continue
			}
			claimedAny = true
			job := sensitiveAuditJobFromRecord(record)
			job.LeaseToken = leaseToken
			if err := p.runJob(job); err != nil {
				retryAt := time.Now().Add(sensitiveAuditPoll).Unix()
				if retryErr := model.RetrySensitiveAuditJob(job.JobID, leaseToken, retryAt, err.Error()); retryErr != nil {
					common.SysError("[sensitive] persist durable retry failed job_id=" + job.JobID + " err=" + retryErr.Error())
				}
			}
		}
		if !claimedAny {
			return
		}
	}
}

func sensitiveAuditJobFromRecord(record model.SensitiveAuditJobRecord) SensitiveAuditJob {
	return SensitiveAuditJob{
		JobID:       record.JobID,
		StorageNode: record.StorageNode,
		RequestID:   record.RequestID,
		UserID:      record.UserID,
		Username:    record.Username,
		TokenID:     record.TokenID,
		TokenName:   record.TokenName,
		ChannelID:   record.ChannelID,
		ChannelName: record.ChannelName,
		ModelName:   record.ModelName,
		Path:        record.Path,
		IP:          record.IP,
		UserAgent:   record.UserAgent,
		DumpPath:    record.DumpPath,
		BodySha256:  record.BodySHA256,
		BodySize:    record.BodySize,
		CreatedAt:   record.CreatedAt,
	}
}

func persistSensitiveAuditJob(job SensitiveAuditJob) error {
	return model.InsertSensitiveAuditJob(&model.SensitiveAuditJobRecord{
		JobID:       job.JobID,
		StorageNode: job.StorageNode,
		RequestID:   job.RequestID,
		UserID:      job.UserID,
		Username:    job.Username,
		TokenID:     job.TokenID,
		TokenName:   job.TokenName,
		ChannelID:   job.ChannelID,
		ChannelName: job.ChannelName,
		ModelName:   job.ModelName,
		Path:        job.Path,
		IP:          job.IP,
		UserAgent:   job.UserAgent,
		DumpPath:    job.DumpPath,
		BodySHA256:  job.BodySha256,
		BodySize:    job.BodySize,
		CreatedAt:   job.CreatedAt,
	})
}

func (p *SensitiveAuditPipeline) Submit(meta SensitiveAuditJob, body []byte) error {
	if p == nil {
		return errors.New("sensitive audit pipeline is nil")
	}

	p.mu.Lock()
	if p.state != sensitiveLifecycleRunning {
		p.mu.Unlock()
		return ErrSensitiveAuditNotAccepting
	}
	if !p.asyncEnabled() || len(body) == 0 {
		p.mu.Unlock()
		return nil
	}
	if p.durable && p.nodeID == "" {
		p.mu.Unlock()
		return errors.New("NODE_NAME is required for durable sensitive audit")
	}
	if !p.dumpEnabled() {
		p.dropped.Add(1)
		p.mu.Unlock()
		return nil
	}
	p.producers.Add(1)
	p.mu.Unlock()

	bodyCopy := append([]byte(nil), body...)
	if p.durable {
		err := p.writeAndEnqueue(meta, bodyCopy)
		p.producers.Done()
		return err
	}
	go func() {
		defer p.producers.Done()
		_ = p.writeAndEnqueue(meta, bodyCopy)
	}()
	return nil
}

func (p *SensitiveAuditPipeline) writeAndEnqueue(meta SensitiveAuditJob, body []byte) (resultErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			p.dropped.Add(1)
			common.SysError(fmt.Sprintf("[sensitive] dump producer panic recovered request_id=%s: %v", meta.RequestID, recovered))
			resultErr = fmt.Errorf("sensitive audit dump producer panic: %v", recovered)
		}
	}()

	meta.BodySize = int64(len(body))
	meta.CreatedAt = time.Now().Unix()
	if meta.JobID == "" {
		meta.JobID = common.GetUUID()
	}
	meta.StorageNode = p.nodeID
	sum := sha256.Sum256(body)
	meta.BodySha256 = hex.EncodeToString(sum[:])

	path, err := p.writeDump(meta, body)
	if err != nil {
		p.dropped.Add(1)
		common.SysError("[sensitive] dump 写盘失败 request_id=" + meta.RequestID + " err=" + err.Error())
		return err
	}
	meta.DumpPath = path
	if p.durable {
		if err := persistSensitiveAuditJob(meta); err != nil {
			p.dropped.Add(1)
			common.SysError("[sensitive] durable admission failed job_id=" + meta.JobID + " err=" + err.Error())
			return err
		}
		p.enqueued.Add(1)
		p.notifyDurableWork(meta)
		return nil
	}

	select {
	case p.queue <- meta:
		p.enqueued.Add(1)
	default:
		p.dropped.Add(1)
		if meta.DumpPath != "" {
			_ = os.Remove(meta.DumpPath)
		}
		common.SysError("[sensitive] audit queue 满，丢弃 request_id=" + meta.RequestID)
	}
	return nil
}

func (p *SensitiveAuditPipeline) Stop(ctx context.Context) error {
	if p == nil {
		return errors.New("sensitive audit pipeline is nil")
	}
	if ctx == nil {
		return errors.New("sensitive audit stop context is nil")
	}

	p.mu.Lock()
	alreadyStopped := false
	switch p.state {
	case sensitiveLifecycleNew:
		p.state = sensitiveLifecycleStopped
		close(p.stopped)
		alreadyStopped = true
	case sensitiveLifecycleRunning:
		p.state = sensitiveLifecycleStopping
		go p.finishStop()
	case sensitiveLifecycleStopping:
	case sensitiveLifecycleStopped:
		alreadyStopped = true
	default:
		p.mu.Unlock()
		return errors.New("sensitive audit pipeline has invalid state")
	}
	stopped := p.stopped
	p.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	if alreadyStopped {
		return p.stopResult()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-stopped:
		if err := ctx.Err(); err != nil {
			return err
		}
		return p.stopResult()
	}
}

func (p *SensitiveAuditPipeline) finishStop() {
	if p.durable && p.nodeID != "" {
		close(p.stopRecovery)
		p.recovery.Wait()
	}
	p.producers.Wait()
	if p.durable && p.nodeID != "" {
		select {
		case p.queue <- SensitiveAuditJob{}:
		default:
			// An existing wake is sufficient because every durable worker
			// drains the DB source until no claimable job remains.
		}
	}
	close(p.queue)
	p.workers.Wait()

	p.mu.Lock()
	if p.state == sensitiveLifecycleStopping {
		failed := p.failed.Load()
		if p.durable && p.nodeID != "" {
			unfinished, err := model.CountUnfinishedSensitiveAuditJobs(p.nodeID)
			if err != nil {
				p.stopErr = fmt.Errorf("%w: count unavailable: %v", ErrSensitiveAuditJobsFailed, err)
			} else if unfinished > 0 {
				p.stopErr = fmt.Errorf("%w: count=%d", ErrSensitiveAuditJobsFailed, unfinished)
			}
		} else if failed > 0 {
			p.stopErr = fmt.Errorf("%w: count=%d", ErrSensitiveAuditJobsFailed, failed)
		}
		p.state = sensitiveLifecycleStopped
		close(p.stopped)
	}
	p.mu.Unlock()
}

func (p *SensitiveAuditPipeline) stopResult() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopErr
}

func (p *SensitiveAuditPipeline) Stats() (enqueued, processed, dropped, failed int64) {
	return p.enqueued.Load(), p.processed.Load(), p.dropped.Load(), p.failed.Load()
}

func (p *SensitiveAuditPipeline) QueueDepth() int {
	p.mu.Lock()
	queue := p.queue
	p.mu.Unlock()
	if queue == nil {
		return -1
	}
	return len(queue)
}

func (p *SensitiveAuditPipeline) QueueCap() int {
	p.mu.Lock()
	queue := p.queue
	p.mu.Unlock()
	if queue == nil {
		return 0
	}
	return cap(queue)
}

// StartSensitiveAuditWorkers starts the process-wide pipeline. A missing
// stable node identity is a startup error because local-volume jobs would not
// be recoverable after restart without it.
func StartSensitiveAuditWorkers(workers int) error {
	if workers <= 0 {
		if value := strings.TrimSpace(os.Getenv(envSensitiveAuditWorkers)); value != "" {
			if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
				workers = parsed
			}
		}
	}
	if workers <= 0 {
		workers = 4
	}
	if err := defaultSensitiveAuditPipeline.Start(workers); err != nil {
		return fmt.Errorf("start sensitive audit workers: %w", err)
	}
	common.SysLog("[sensitive] async audit workers started count=" + strconv.Itoa(workers))
	return nil
}

func StopSensitiveAudit(ctx context.Context) error {
	return defaultSensitiveAuditPipeline.Stop(ctx)
}

func SubmitSensitiveAudit(meta SensitiveAuditJob, body []byte) error {
	return defaultSensitiveAuditPipeline.Submit(meta, body)
}

// EnqueueSensitiveAudit is retained for callers that intentionally ignore
// admission errors.
func EnqueueSensitiveAudit(meta SensitiveAuditJob, body []byte) {
	_ = SubmitSensitiveAudit(meta, body)
}

// SensitiveDumpRoot 解析 dump 根目录（env 优先）。
func SensitiveDumpRoot() string {
	if v := strings.TrimSpace(os.Getenv(envSensitiveDumpRoot)); v != "" {
		return v
	}
	return defaultSensitiveDumpRoot
}

func writeSensitiveDump(meta SensitiveAuditJob, body []byte) (string, error) {
	now := time.Unix(meta.CreatedAt, 0).UTC()
	prefix := meta.BodySha256
	if len(prefix) >= 2 {
		prefix = prefix[:2]
	} else {
		prefix = "00"
	}
	dir := filepath.Join(
		SensitiveDumpRoot(),
		now.Format("2006"),
		now.Format("01"),
		now.Format("02"),
		prefix,
	)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	fileName := meta.JobID
	if fileName == "" {
		fileName = meta.RequestID
	}
	if fileName == "" {
		fileName = meta.BodySha256
	}
	if fileName == "" {
		fileName = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	full := filepath.Join(dir, fileName+".json.gz")

	payload := SensitiveDumpPayload{
		SchemaVersion: 1,
		JobID:         meta.JobID,
		RequestID:     meta.RequestID,
		UserID:        meta.UserID,
		ChannelID:     meta.ChannelID,
		Path:          meta.Path,
		BodySHA256:    meta.BodySha256,
		BodySize:      meta.BodySize,
		Timestamp:     meta.CreatedAt,
	}
	jsonBytes, err := common.Marshal(payload)
	if err != nil {
		return "", err
	}

	tmp := full + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte(sensitiveDumpMagic)); err != nil {
		_ = gz.Close()
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if _, err := gz.Write(jsonBytes); err != nil {
		_ = gz.Close()
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if _, err := gz.Write([]byte{'\n'}); err != nil {
		_ = gz.Close()
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if _, err := gz.Write(body); err != nil {
		_ = gz.Close()
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if err := gz.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, full); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	dirHandle, err := os.Open(dir)
	if err != nil {
		return "", err
	}
	syncErr := dirHandle.Sync()
	closeErr := dirHandle.Close()
	if syncErr != nil {
		return "", syncErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return full, nil
}

// ReadSensitiveDump 反向读取（阶段 3 dump 接口 + worker 兜底用）。
func ReadSensitiveDump(path string) (SensitiveDumpPayload, error) {
	var payload SensitiveDumpPayload
	f, err := os.Open(path)
	if err != nil {
		return payload, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return payload, err
	}
	defer gz.Close()
	prefix := make([]byte, len(sensitiveDumpMagic))
	if _, err := io.ReadFull(gz, prefix); err != nil {
		return payload, err
	}
	if string(prefix) == sensitiveDumpMagic {
		reader := bufio.NewReaderSize(gz, 32*1024)
		header, err := readSensitiveDumpHeader(reader)
		if err != nil {
			return payload, err
		}
		if err := common.Unmarshal(header, &payload); err != nil {
			return payload, err
		}
		maxBodySize := int64(constant.MaxRequestBodyMB) << 20
		if maxBodySize <= 0 {
			maxBodySize = 128 << 20
		}
		if payload.BodySize < 0 || payload.BodySize > maxBodySize {
			return payload, errSensitiveDumpTooLarge
		}
		body, err := io.ReadAll(io.LimitReader(reader, payload.BodySize+1))
		if err != nil {
			return payload, err
		}
		if int64(len(body)) != payload.BodySize {
			return payload, errors.New("sensitive dump body size mismatch")
		}
		payload.Body = string(body)
		return payload, nil
	}

	// Legacy dumps are gzip(JSON). Keep their existing hard limit; all new
	// durable dumps use the streaming envelope above and do not JSON-expand
	// request bodies.
	const legacyMaxRead = 64 * 1024 * 1024
	legacy, err := io.ReadAll(io.LimitReader(io.MultiReader(bytes.NewReader(prefix), gz), legacyMaxRead+1))
	if err != nil {
		return payload, err
	}
	if len(legacy) > legacyMaxRead {
		return payload, errSensitiveDumpTooLarge
	}
	if err := common.Unmarshal(legacy, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func readSensitiveDumpHeader(reader *bufio.Reader) ([]byte, error) {
	header := make([]byte, 0, 512)
	for len(header) <= sensitiveDumpHeaderLimit {
		value, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		if value == '\n' {
			return header, nil
		}
		header = append(header, value)
	}
	return nil, errors.New("sensitive dump header too large")
}

var errSensitiveDumpTooLarge = errors.New("sensitive dump too large")

func processSensitiveAuditJob(job SensitiveAuditJob) error {
	if job.JobID == "" || job.LeaseToken == "" || job.DumpPath == "" {
		return errors.New("sensitive audit durable identity is incomplete")
	}
	payload, err := ReadSensitiveDump(job.DumpPath)
	if err != nil {
		return fmt.Errorf("read sensitive dump %s: %w", job.DumpPath, err)
	}
	body := []byte(payload.Body)
	if len(body) == 0 {
		return errors.New("sensitive audit dump body is empty")
	}
	if payload.JobID != job.JobID {
		return fmt.Errorf("sensitive audit dump job mismatch: got %s want %s", payload.JobID, job.JobID)
	}
	sum := sha256.Sum256(body)
	if bodySHA256 := hex.EncodeToString(sum[:]); bodySHA256 != job.BodySha256 || payload.BodySHA256 != job.BodySha256 {
		return errors.New("sensitive audit dump body hash mismatch")
	}

	entries, err := loadSensitiveWordsFresh()
	if err != nil {
		return fmt.Errorf("load sensitive audit rules: %w", err)
	}
	bodyStr := string(body)
	lowerBody := strings.ToLower(bodyStr)

	// Phase 2: 分桶后用 AC 自动机一次扫所有 substring 规则；regex 桶单独循环。
	// 同一规则多处命中只记一条；不同规则各自记录（突破 Phase 1 "命中即 break" 限制）。
	var (
		substringDict    []string
		substringByLower = make(map[string][]CompiledSensitiveWord, len(entries))
		regexEntries     []CompiledSensitiveWord
	)
	for _, e := range entries {
		if e.IsRegex {
			regexEntries = append(regexEntries, e)
			continue
		}
		if e.LowerPattern == "" {
			continue
		}
		if _, exists := substringByLower[e.LowerPattern]; !exists {
			substringDict = append(substringDict, e.LowerPattern)
		}
		substringByLower[e.LowerPattern] = append(substringByLower[e.LowerPattern], e)
	}

	seen := make(map[int]struct{}, 4) // 单请求内同规则去重
	type matchedSensitiveAuditHit struct {
		entry   CompiledSensitiveWord
		snippet string
	}
	matches := make([]matchedSensitiveAuditHit, 0, 4)

	if len(substringDict) > 0 {
		hit, words := AcSearch(lowerBody, substringDict, false)
		if hit {
			for _, w := range words {
				rules, ok := substringByLower[w]
				if !ok {
					continue
				}
				idx := strings.Index(lowerBody, w)
				snippet := ""
				if idx >= 0 {
					snippet = ExtractSensitiveSnippet(bodyStr, idx, idx+len(w))
				}
				for _, entry := range rules {
					if _, dup := seen[entry.Id]; dup {
						continue
					}
					seen[entry.Id] = struct{}{}
					matches = append(matches, matchedSensitiveAuditHit{entry: entry, snippet: snippet})
				}
			}
		}
	}

	for _, e := range regexEntries {
		if _, dup := seen[e.Id]; dup {
			continue
		}
		snippet, ok := ScanSensitive(e, bodyStr, lowerBody)
		if !ok {
			continue
		}
		seen[e.Id] = struct{}{}
		matches = append(matches, matchedSensitiveAuditHit{entry: e, snippet: snippet})
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].entry.Id < matches[j].entry.Id
	})
	logs := make([]*model.SensitiveBlockLog, 0, len(matches))
	rules := make([]model.SensitiveAuditRuleVersion, 0, len(matches))
	for _, match := range matches {
		logs = append(logs, buildSensitiveAuditHit(job, match.entry, match.snippet))
		rules = append(rules, model.SensitiveAuditRuleVersion{
			ID:        match.entry.Id,
			Pattern:   match.entry.Pattern,
			IsRegex:   match.entry.IsRegex,
			Action:    match.entry.Action,
			UpdatedAt: match.entry.UpdatedAt,
		})
	}
	if err := model.CompleteSensitiveAuditJob(job.JobID, job.LeaseToken, job.TokenID, logs, rules, time.Now().Unix()); err != nil {
		return fmt.Errorf("complete sensitive audit job %s: %w", job.JobID, err)
	}
	if len(matches) > 0 {
		MarkUserSuspicious(job.UserID)
	}
	return nil
}

func buildSensitiveAuditHit(job SensitiveAuditJob, e CompiledSensitiveWord, snippet string) *model.SensitiveBlockLog {
	jobID := job.JobID
	return &model.SensitiveBlockLog{
		AuditJobId:     &jobID,
		RequestId:      job.RequestID,
		UserId:         job.UserID,
		Username:       job.Username,
		TokenId:        job.TokenID,
		TokenName:      job.TokenName,
		ChannelId:      job.ChannelID,
		ChannelName:    job.ChannelName,
		ModelName:      job.ModelName,
		Path:           job.Path,
		MatchedWordId:  e.Id,
		MatchedPattern: e.Pattern,
		IsRegex:        e.IsRegex,
		Action:         e.Action,
		MatchedSnippet: snippet,
		// RequestBody 不再写入（保留字段为存量数据兼容；阶段 4 删列）
		DumpPath:      job.DumpPath,
		BodySha256:    job.BodySha256,
		BodySize:      job.BodySize,
		DumpExists:    job.DumpPath != "",
		Ip:            job.IP,
		UserAgent:     job.UserAgent,
		TokenDisabled: false,
		CreatedAt:     job.CreatedAt,
	}
}

// SetSensitiveTokenStatus 由前端"启用/禁用 Token"按钮通过 controller 调用：
// 根据命中记录关联的 token_id 直接调整状态。返回 token 当前是否处于禁用态。
func SetSensitiveTokenStatus(tokenID int, disabled bool) bool {
	currentDisabled, _ := setSensitiveTokenStatus(tokenID, disabled)
	return currentDisabled
}

func setSensitiveTokenStatus(tokenID int, disabled bool) (bool, error) {
	if tokenID <= 0 {
		return false, nil
	}
	token, err := model.GetTokenById(tokenID)
	if err != nil || token == nil {
		if err == nil {
			err = errors.New("token not found")
		}
		return false, fmt.Errorf("load sensitive token %d: %w", tokenID, err)
	}
	target := common.TokenStatusEnabled
	if disabled {
		target = common.TokenStatusDisabled
	}
	if token.Status == target {
		return disabled, nil
	}
	token.Status = target
	if err := token.Update(); err != nil {
		common.SysError("[sensitive] 调整 token 状态失败 token_id=" + strconv.Itoa(tokenID) + " err=" + err.Error())
		return false, fmt.Errorf("update sensitive token %d status: %w", tokenID, err)
	}
	return disabled, nil
}

// ShouldSampleSensitive 决定本次请求是否进入异步审计。
//   - userID <= 0：返回 false（匿名/未登录请求不审）
//   - 在 suspiciousUsers 内：返回 true（阶段 4 启用，阶段 1 LRU 始终为空）
//   - 其余：fnv32a(userID) % 100 < SensitiveSampleRate
func ShouldSampleSensitive(userID int) bool {
	if userID <= 0 {
		return false
	}
	if isSuspiciousUser(userID) {
		return true
	}
	rate := setting.GetSensitiveSampleRate()
	if rate <= 0 {
		return false
	}
	if rate >= 100 {
		return true
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strconv.Itoa(userID)))
	return int(h.Sum32()%100) < rate
}

// MarkUserSuspicious 把用户标记为可疑 N 分钟，下次请求会被强制采样。
func MarkUserSuspicious(userID int) {
	if userID <= 0 {
		return
	}
	suspiciousUsers.Store(userID, time.Now().Unix()+suspiciousTTL)
}

func isSuspiciousUser(userID int) bool {
	v, ok := suspiciousUsers.Load(userID)
	if !ok {
		return false
	}
	exp, ok := v.(int64)
	if !ok {
		suspiciousUsers.Delete(userID)
		return false
	}
	if exp < time.Now().Unix() {
		suspiciousUsers.Delete(userID)
		return false
	}
	return true
}

// SensitiveAuditStats 用于 metrics / 调试 endpoint。
func SensitiveAuditStats() (enqueued, processed, dropped, failed int64) {
	return defaultSensitiveAuditPipeline.Stats()
}

// SensitiveAuditQueueDepth 当前 channel 内未消费的 job 数（实时近似值）。
// channel 未启动时返回 -1。
func SensitiveAuditQueueDepth() int {
	return defaultSensitiveAuditPipeline.QueueDepth()
}

// SensitiveAuditQueueCap channel 容量（hard-coded 4096）。
func SensitiveAuditQueueCap() int {
	return defaultSensitiveAuditPipeline.QueueCap()
}

// SensitiveSuspiciousUserCount 当前可疑用户 LRU 数量（粗略：sync.Map 没有 len）。
func SensitiveSuspiciousUserCount() int {
	count := 0
	suspiciousUsers.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}
