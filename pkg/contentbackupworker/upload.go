package contentbackupworker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
)

var ErrUploadStopped = errors.New("content backup upload: uploader is stopped")

// contentBackupUploadMaxSlots 是常驻上传槽位数上限，与 ValidateConfig 的
// upload_workers 上限一致。Start 按这个数起 goroutine，活槽位数跟配置走。
// 上传是纯延迟受限的：单文件的十来个往返跟文件大小无关，带宽和 CPU 都用不满，
// 所以并发基本线性换吞吐。198ms 的跨境 RTT 下 16 个槽位撑不住 5 文件/秒。
const contentBackupUploadMaxSlots = 48

// RemoteFactory lets tests inject a fake RemoteStore; production builds one from config.
type RemoteFactory func(targetID string) (RemoteStore, error)

// UploadConfig wires the upload state machine; RunCycle is called by runtime periodically.
type UploadConfig struct {
	SiteID        string
	StorageNodeID string
	ProcessID     string
	Store         *model.ContentBackupStore
	Spool         *SpoolManager
	Config        func() contentbackup.Config
	RemoteFactory RemoteFactory
	Now           func() time.Time
	Jitter        func() float64
	Logf          func(format string, args ...any)

	LeaseSeconds      time.Duration
	LeaseRenewEvery   time.Duration
	ScanLimit         int
	UploadTimeout     time.Duration
	IncomingReapEvery time.Duration
}

func (c *UploadConfig) defaults() {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Jitter == nil {
		c.Jitter = func() float64 { return 0 }
	}
	if c.Logf == nil {
		c.Logf = func(string, ...any) {}
	}
	if c.LeaseSeconds <= 0 {
		c.LeaseSeconds = 180 * time.Second
	}
	if c.LeaseRenewEvery <= 0 {
		c.LeaseRenewEvery = 30 * time.Second
	}
	if c.ScanLimit <= 0 {
		c.ScanLimit = 100
	}
	if c.UploadTimeout <= 0 {
		c.UploadTimeout = 120 * time.Second
	}
	if c.IncomingReapEvery <= 0 {
		c.IncomingReapEvery = incomingReapEvery
	}
}

// CycleResult reports one work cycle for tests and runtime observability.
type CycleResult struct {
	Claimed    int
	Uploaded   int
	Retried    int
	Failed     int
	Skipped    int
	LeasesLost int
	PausedJobs int
}

// Uploader is the upload state machine from design doc 6.2/6.3. It never deletes
// local files; space reclamation is the cleaner's separate job (6.4).
type Uploader struct {
	cfg UploadConfig

	mu      sync.Mutex
	stopped bool
	wg      sync.WaitGroup

	holdMu      sync.Mutex
	held        RemoteStore
	heldTarget  string
	heldTimeout time.Duration

	// lastPutOK is the Unix second of the last verified transfer; the incoming reaper only
	// runs while it is recent.
	lastPutOK atomic.Int64
}

func NewUploader(cfg UploadConfig) (*Uploader, error) {
	if cfg.SiteID == "" || cfg.StorageNodeID == "" {
		return nil, fmt.Errorf("content backup upload: site and storage node identity are required")
	}
	if cfg.Store == nil {
		return nil, fmt.Errorf("content backup upload: store is required")
	}
	if cfg.Spool == nil {
		return nil, fmt.Errorf("content backup upload: spool is required")
	}
	if cfg.Config == nil {
		return nil, fmt.Errorf("content backup upload: config source is required")
	}
	if cfg.RemoteFactory == nil {
		return nil, fmt.Errorf("content backup upload: remote factory is required")
	}
	cfg.defaults()
	return &Uploader{cfg: cfg}, nil
}

// Start 常驻 upload_workers 条独立上传槽。以前整轮先认领最多 100 条再 wg.Wait，
// 一条大文件卡满超时就会让其余空闲 worker 也跟着空等、新任务堆在 pending。
func (u *Uploader) Start(ctx context.Context) {
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		defer u.closeHeld()

		var workers sync.WaitGroup
		workerCtx, stopWorkers := context.WithCancel(ctx)
		defer stopWorkers()
		for i := 0; i < contentBackupUploadMaxSlots; i++ {
			workers.Add(1)
			go func(slot int) {
				defer workers.Done()
				u.workerLoop(workerCtx, slot)
			}(i)
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			u.reapIncomingLoop(workerCtx)
		}()
		<-ctx.Done()
		stopWorkers()
		workers.Wait()
	}()
}

func (u *Uploader) workerLoop(ctx context.Context, slot int) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		claimed, skipped := 0, 0
		if slot < u.liveUploadWorkers() {
			result, err := u.runCycle(ctx, 1, slot)
			if err != nil {
				if errors.Is(err, ErrUploadStopped) || errors.Is(err, context.Canceled) {
					return
				}
				if ctx.Err() != nil {
					return
				}
				u.cfg.Logf("content backup upload cycle: %v", err)
			} else {
				claimed = result.Claimed
				skipped = result.Skipped
			}
		}
		if claimed > 0 {
			continue
		}
		// 抢输不等于没活：别的槽位刚把这几行领走，说明队列里还有东西。空睡满
		// 一整拍会让并发在积压时塌成串行。真的没活时 skipped 是 0，照常睡。
		if skipped > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (u *Uploader) liveUploadWorkers() int {
	n := u.cfg.Config().UploadWorkers
	if n < 1 {
		n = 1
	}
	if n > contentBackupUploadMaxSlots {
		n = contentBackupUploadMaxSlots
	}
	return n
}

func (u *Uploader) Wait() {
	u.wg.Wait()
}

// Stop rejects further RunCycle claims.
func (u *Uploader) Stop() {
	u.mu.Lock()
	u.stopped = true
	u.mu.Unlock()
	u.closeHeld()
}

func (u *Uploader) closeHeld() {
	u.holdMu.Lock()
	defer u.holdMu.Unlock()
	if u.held == nil {
		return
	}
	if closer, ok := u.held.(io.Closer); ok {
		if closeErr := closer.Close(); closeErr != nil {
			u.cfg.Logf("content backup upload: close remote: %v", closeErr)
		}
	}
	u.held = nil
	u.heldTarget = ""
	u.heldTimeout = 0
}

func (u *Uploader) remoteFor(targetID string, timeout time.Duration) (RemoteStore, error) {
	u.holdMu.Lock()
	defer u.holdMu.Unlock()
	if u.held != nil && u.heldTarget == targetID && u.heldTimeout == timeout {
		return u.held, nil
	}
	if u.held != nil {
		if closer, ok := u.held.(io.Closer); ok {
			if closeErr := closer.Close(); closeErr != nil {
				u.cfg.Logf("content backup upload: close remote: %v", closeErr)
			}
		}
		u.held = nil
		u.heldTarget = ""
		u.heldTimeout = 0
	}
	remote, err := u.cfg.RemoteFactory(targetID)
	if err != nil {
		return nil, err
	}
	u.held = remote
	u.heldTarget = targetID
	u.heldTimeout = timeout
	return remote, nil
}

// RunCycle claims due jobs and uploads them. Tests drive it directly with a fixed Now.
func (u *Uploader) RunCycle(ctx context.Context) (CycleResult, error) {
	return u.runCycle(ctx, 0, 0)
}

// offset 是本槽位在候选表里的起扫位置。8 个槽位拿到的是同一批、同一序的行，
// 都从第 0 行开始抢的话，第 k 个槽位要先输 k 次才轮到自己那条。
func (u *Uploader) runCycle(ctx context.Context, maxClaim, offset int) (CycleResult, error) {
	u.mu.Lock()
	stopped := u.stopped
	u.mu.Unlock()
	if stopped {
		return CycleResult{}, ErrUploadStopped
	}

	cfg := u.cfg.Config()
	if cfg.UploadPaused {
		return CycleResult{}, nil
	}
	if cfg.TargetID == "" {
		return CycleResult{}, nil
	}
	now := u.cfg.Now()
	store := u.cfg.Store
	node := u.cfg.StorageNodeID

	result := CycleResult{}
	if maxClaim <= 0 {
		maxClaim = u.liveUploadWorkers()
	}
	if u.cfg.ScanLimit > 0 && maxClaim > u.cfg.ScanLimit {
		maxClaim = u.cfg.ScanLimit
	}
	// 常驻槽位每次只领 1 条，但扫描必须覆盖 worker 数：8 个槽同时 List(..., 1)
	// 会拿到同一条最旧任务，7 个 Claim 失败后再睡 2s，并行塌成近似串行。
	listLimit := u.liveUploadWorkers()
	if listLimit < maxClaim {
		listLimit = maxClaim
	}
	if u.cfg.ScanLimit > 0 && listLimit > u.cfg.ScanLimit {
		listLimit = u.cfg.ScanLimit
	}

	claimable, err := store.ListClaimableJobs(ctx, node, now, listLimit)
	if err != nil {
		return result, fmt.Errorf("list claimable jobs: %w", err)
	}
	expired, err := store.ListExpiredLeases(ctx, node, now, listLimit)
	if err != nil {
		return result, fmt.Errorf("list expired leases: %w", err)
	}

	// Due pending and expired processing are two independent inputs; Claim settles
	// the race so exactly one winner proceeds even when both lists hold the same row.
	candidates := append(append([]model.ContentBackupJob(nil), claimable...), expired...)
	seen := make(map[string]bool, len(candidates))
	leases := make([]model.ContentBackupLease, 0, maxClaim)
	if len(candidates) > 0 {
		offset = ((offset % len(candidates)) + len(candidates)) % len(candidates)
	}
	for i := range candidates {
		job := candidates[(i+offset)%len(candidates)]
		if result.Claimed >= maxClaim {
			break
		}
		if seen[job.JobID] {
			continue
		}
		seen[job.JobID] = true

		lease, ok, err := store.Claim(ctx, job.JobID, node, u.owner(), now, now.Add(u.leaseDuration(cfg)))
		if err != nil {
			u.cfg.Logf("content backup claim %s: %v", job.JobID, err)
			continue
		}
		if !ok {
			result.Skipped++
			continue
		}
		result.Claimed++
		leases = append(leases, lease)
	}
	if len(leases) == 0 {
		return result, nil
	}

	remote, remoteErr := u.remoteFor(cfg.TargetID, u.uploadTimeout(cfg))

	workers := cfg.UploadWorkers
	if workers < 1 {
		workers = 1
	}
	if workers > len(leases) {
		workers = len(leases)
	}

	var mu sync.Mutex
	add := func(fn func(*CycleResult)) {
		mu.Lock()
		defer mu.Unlock()
		fn(&result)
	}

	var wg sync.WaitGroup
	queue := make(chan model.ContentBackupLease, len(leases))
	for _, lease := range leases {
		queue <- lease
	}
	close(queue)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for lease := range queue {
				u.upload(ctx, lease, remote, remoteErr, add)
			}
		}()
	}
	wg.Wait()
	return result, nil
}

func (u *Uploader) owner() string {
	return "uploader-" + u.cfg.ProcessID
}

// leaseDuration and uploadTimeout prefer the live config snapshot so a UI
// change to lease_seconds / upload_timeout_seconds takes effect on the next
// cycle. Construction defaults (180s / 120s) stay as fallback when the
// snapshot is incomplete. This used to be ignored: NewUploader never copied
// those fields from Config(), so raising the setting left large SFTP puts
// dying at the hardcoded 120s context (watchOnce then reports connection lost).
func (u *Uploader) leaseDuration(cfg contentbackup.Config) time.Duration {
	if cfg.LeaseSeconds > 0 {
		return time.Duration(cfg.LeaseSeconds) * time.Second
	}
	return u.cfg.LeaseSeconds
}

func (u *Uploader) uploadTimeout(cfg contentbackup.Config) time.Duration {
	if cfg.UploadTimeoutSeconds > 0 {
		return time.Duration(cfg.UploadTimeoutSeconds) * time.Second
	}
	return u.cfg.UploadTimeout
}

// leaseRenewEvery is the configured renew interval, capped at a third of the lease so two
// renewals can fail and a third still lands in time. Validation only asks for the interval
// to be shorter than the lease; one a second shorter would renew after the lease expired.
func (u *Uploader) leaseRenewEvery(cfg contentbackup.Config) time.Duration {
	every := u.cfg.LeaseRenewEvery
	if cfg.LeaseRenewSeconds > 0 {
		every = time.Duration(cfg.LeaseRenewSeconds) * time.Second
	}
	if limit := u.leaseDuration(cfg) / 3; every > limit {
		every = limit
	}
	return every
}

// putHoldingLease runs the transfer while renewing the lease. Without renewal a transfer
// that outlived lease_seconds was re-claimed from the expired-lease list and uploaded a
// second time by another slot. FTP has no fencing, so once the lease is gone, or would
// lapse before the next renewal, the transfer is stopped (design doc 6.3).
func (u *Uploader) putHoldingLease(ctx context.Context, remote RemoteStore, job model.ContentBackupJob, lease model.ContentBackupLease, src io.Reader) error {
	putCtx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		u.renewLease(putCtx, lease, stop)
	}()
	err := remote.PutVerified(putCtx, job, lease, src)
	stop(nil)
	<-renewDone
	// A transfer that finished anyway goes on to MarkUploaded, whose lease guard decides.
	if err != nil && errors.Is(context.Cause(putCtx), model.ErrLeaseLost) {
		return fmt.Errorf("%w: %v", model.ErrLeaseLost, err)
	}
	return err
}

func (u *Uploader) renewLease(ctx context.Context, lease model.ContentBackupLease, stop context.CancelCauseFunc) {
	for {
		cfg := u.cfg.Config()
		every := u.leaseRenewEvery(cfg)
		timer := time.NewTimer(every)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		renewed, err := u.renewOnce(ctx, lease, cfg)
		if err == nil {
			lease = renewed
			continue
		}
		if ctx.Err() != nil {
			return
		}
		// Judge by the clock after the call returned: a renewal that hung must not count as on time.
		if errors.Is(err, model.ErrLeaseLost) || !u.cfg.Now().Add(every).Before(lease.Until) {
			u.cfg.Logf("content backup upload %s: lease cannot be held, stopping transfer: %v", lease.JobID, err)
			stop(model.ErrLeaseLost)
			return
		}
		u.cfg.Logf("content backup upload %s: renew lease: %v", lease.JobID, err)
	}
}

// renewOnce extends the lease by lease_seconds from now. The call may not outlive the lease
// it extends: from lease_until on, another slot is free to claim the job and upload it.
func (u *Uploader) renewOnce(ctx context.Context, lease model.ContentBackupLease, cfg contentbackup.Config) (model.ContentBackupLease, error) {
	now := u.cfg.Now()
	remaining := lease.Until.Sub(now)
	if remaining <= 0 {
		return lease, model.ErrLeaseLost
	}
	renewCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	return u.cfg.Store.Renew(renewCtx, lease, now, now.Add(u.leaseDuration(cfg)))
}

func (u *Uploader) upload(ctx context.Context, lease model.ContentBackupLease, remote RemoteStore, remoteErr error, add func(func(*CycleResult))) {
	store := u.cfg.Store
	now := u.cfg.Now()

	job, err := store.GetJob(ctx, lease.JobID)
	if err != nil {
		u.cfg.Logf("content backup upload %s: reload job: %v", lease.JobID, err)
		add(func(result *CycleResult) { result.Skipped++ })
		return
	}

	cfg := u.cfg.Config()
	if job.TargetID != cfg.TargetID {
		// Target identity is frozen per job (design doc 6.3 step 1): a changed target
		// must drain in-flight handoffs first, so a mismatched job never reaches the
		// new server; the classifier decides between requeue and terminal.
		u.applyClass(ctx, lease, job, errUploadTargetMismatch, add)
		return
	}

	path, pathErr := u.cfg.Spool.JobPath(lease.JobID)
	if pathErr != nil {
		u.applyClass(ctx, lease, job, pathErr, add)
		return
	}
	file, fileErr := openFileReader(path)
	if fileErr != nil {
		u.applyClass(ctx, lease, job, fileErr, add)
		return
	}
	defer file.Close()

	if remoteErr != nil {
		u.applyClass(ctx, lease, job, remoteErr, add)
		return
	}
	if remote == nil {
		u.applyClass(ctx, lease, job, errors.New("content backup upload: remote store is nil"), add)
		return
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, u.uploadTimeout(cfg))
	defer cancel()
	if err := u.putHoldingLease(timeoutCtx, remote, job, lease, file); err != nil {
		u.applyClass(ctx, lease, job, err, add)
		return
	}
	u.lastPutOK.Store(u.cfg.Now().Unix())

	if err := store.MarkUploaded(ctx, lease, now); err != nil {
		if errors.Is(err, model.ErrLeaseLost) {
			add(func(result *CycleResult) { result.LeasesLost++ })
			return
		}
		// The commit result is unknown: re-read the job before doing anything else
		// (design doc 6.3 step 6); never re-upload and never delete on an unknown result.
		current, lookupErr := store.GetJob(ctx, lease.JobID)
		if lookupErr == nil && current.Status == model.ContentBackupStatusUploaded {
			add(func(result *CycleResult) { result.Uploaded++ })
			return
		}
		u.cfg.Logf("content backup upload %s: mark uploaded: %v", lease.JobID, err)
		return
	}
	add(func(result *CycleResult) { result.Uploaded++ })
}

// applyClass routes one failure through the 6.2 state machine: retryable errors back off
// to pending (the 16th attempt fails the round), target-unusable errors fail the job and
// pause the target, local/remote conflicts fail it for good. The local file is never touched.
func (u *Uploader) applyClass(ctx context.Context, lease model.ContentBackupLease, job model.ContentBackupJob, cause error, add func(func(*CycleResult))) {
	now := u.cfg.Now()
	store := u.cfg.Store
	info := ClassifyUploadError(cause)
	message := uploadErrorMessage(cause)

	switch {
	case info.Class == UploadClassLeaseLost:
		add(func(result *CycleResult) { result.LeasesLost++ })
	case info.Class == UploadClassAborted:
		// Shutdown raced the transfer; leave the lease to expire so the next round reclaims.
		add(func(result *CycleResult) { result.Skipped++ })
	case info.Retryable:
		if job.Attempts >= uploadAttemptLimit {
			if err := store.MarkFailed(ctx, lease, UploadCodeRetriesExhausted, message, now); err != nil {
				u.handleTerminalWriteError(lease, err, add)
			}
			add(func(result *CycleResult) { result.Failed++ })
			return
		}
		next := now.Add(RetryDelay(job.Attempts, u.cfg.Jitter()))
		if err := store.ScheduleRetry(ctx, lease, info.Code, message, now, next); err != nil {
			u.handleTerminalWriteError(lease, err, add)
			return
		}
		add(func(result *CycleResult) { result.Retried++ })
	default:
		if err := store.MarkFailed(ctx, lease, info.Code, message, now); err != nil {
			u.handleTerminalWriteError(lease, err, add)
			return
		}
		add(func(result *CycleResult) {
			result.Failed++
			if info.PauseTarget {
				result.PausedJobs++
			}
		})
	}
}

func (u *Uploader) handleTerminalWriteError(lease model.ContentBackupLease, err error, add func(func(*CycleResult))) {
	if errors.Is(err, model.ErrLeaseLost) {
		add(func(result *CycleResult) { result.LeasesLost++ })
		return
	}
	u.cfg.Logf("content backup upload %s: terminal write: %v", lease.JobID, err)
}
