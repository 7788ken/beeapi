package contentbackupworker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/shirou/gopsutil/v3/disk"
	"gorm.io/gorm"
)

// RuntimeConfig wires the in-process backup pipeline. Only the spool directory is a
// deployment fact; the storage node id is read from (or created in) the spool volume, and
// everything else (site label, target, credentials, budgets) comes from the config snapshot.
type RuntimeConfig struct {
	SpoolDir string
	DB       *gorm.DB
	Config   func() contentbackup.Config
	Now      func() time.Time
	Logf     func(format string, args ...any)

	// QueueDepth bounds captures waiting for the spool worker; a full queue rejects
	// (the relay is never blocked, the capture budget is released immediately).
	QueueDepth int
	// SuperviseInterval is how often the runtime re-reads the config to (re)build the
	// site-scoped workers; tests shorten it.
	SuperviseInterval time.Duration
}

// RuntimeCounters are the process-local handoff counters the node heartbeat publishes.
// They restart at zero with the process and are trend indicators, not exact loss counts.
type RuntimeCounters struct {
	Queued     int64 `json:"queued"`
	Written    int64 `json:"written"`
	Registered int64 `json:"registered"`
	Rejected   int64 `json:"rejected"`
	Failed     int64 `json:"failed"`
}

// Runtime is the whole pipeline inside beeapi (2026-09-18 simplification of design doc 4.1):
// one spool writer fed from a bounded queue, and per site label a reconciler, uploader,
// cleaner and reader. The site-scoped workers are built lazily and rebuilt when the label
// changes, so a fresh install can be configured entirely from the UI without a restart.
type Runtime struct {
	cfg       RuntimeConfig
	nodeID    string
	processID string
	spool     *SpoolManager
	writer    *SpoolWriter

	queue   chan *contentbackup.Capture
	stopped atomic.Bool
	workers sync.WaitGroup

	loopCancel context.CancelFunc
	loops      sync.WaitGroup

	mu         sync.Mutex
	site       *siteComponents
	lastRecon  ReconcileResult
	reconAt    time.Time
	reconError error

	queued, written, registered, rejected, failed atomic.Int64
}

// siteComponents are the workers that need a site label (they all go through the
// site-scoped store). They live as long as the label stays the same.
type siteComponents struct {
	siteID     string
	store      *model.ContentBackupStore
	reconciler *Reconciler
	uploader   *Uploader
	cleaner    *Cleaner
	reader     *Reader
	factory    RemoteFactory
	cancel     context.CancelFunc
	done       chan struct{}
}

// Start creates the spool, derives the node id, starts the spool worker and the supervisor
// loop. It never fails because of a missing or incomplete config: an unconfigured site just
// has no workers until the operator saves one.
func Start(ctx context.Context, cfg RuntimeConfig) (*Runtime, error) {
	if cfg.SpoolDir == "" {
		return nil, errors.New("content backup runtime: spool dir is required")
	}
	if cfg.DB == nil {
		return nil, errors.New("content backup runtime: database handle is required")
	}
	if cfg.Config == nil {
		return nil, errors.New("content backup runtime: config source is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = func(format string, args ...any) { common.SysLog(fmt.Sprintf(format, args...)) }
	}
	if cfg.QueueDepth <= 0 {
		cfg.QueueDepth = 128
	}
	if cfg.SuperviseInterval <= 0 {
		cfg.SuperviseInterval = 5 * time.Second
	}

	nodeID, err := LoadOrCreateNodeID(cfg.SpoolDir)
	if err != nil {
		return nil, err
	}
	snapshot := cfg.Config()
	spool, err := NewSpoolManager(cfg.SpoolDir, snapshot)
	if err != nil {
		return nil, fmt.Errorf("content backup runtime: spool: %w", err)
	}
	r := &Runtime{
		cfg:       cfg,
		nodeID:    nodeID,
		processID: contentbackup.NewJobID(),
		spool:     spool,
		queue:     make(chan *contentbackup.Capture, cfg.QueueDepth),
	}
	r.writer, err = NewSpoolWriter(spool, func(siteID string) *model.ContentBackupStore {
		return model.NewContentBackupStore(cfg.DB, siteID)
	}, cfg.Config)
	if err != nil {
		return nil, err
	}

	workers := snapshot.SpoolWorkers
	if workers < 1 {
		workers = 1
	}
	r.workers.Add(workers)
	for i := 0; i < workers; i++ {
		go r.spoolWorker()
	}

	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r.loopCancel = cancel
	r.loops.Add(1)
	go r.supervise(loopCtx)
	cfg.Logf("content backup runtime started: node %s, spool %s", nodeID, cfg.SpoolDir)
	return r, nil
}

func (r *Runtime) StorageNodeID() string { return r.nodeID }
func (r *Runtime) ProcessID() string     { return r.processID }
func (r *Runtime) Spool() *SpoolManager  { return r.spool }

// Enqueue is the non-blocking ownership transfer the capture layer calls (its Enqueuer
// hook). true means the runtime now owns the capture and will Release it exactly once.
func (r *Runtime) Enqueue(capture *contentbackup.Capture) bool {
	if capture == nil || r.stopped.Load() {
		r.rejected.Add(1)
		return false
	}
	select {
	case r.queue <- capture:
		r.queued.Add(1)
		return true
	default:
		// Queue full means the disk/worker is behind; refuse now so the capture budget is
		// returned immediately and the relay never waits.
		r.rejected.Add(1)
		return false
	}
}

func (r *Runtime) spoolWorker() {
	defer r.workers.Done()
	for capture := range r.queue {
		r.persist(capture)
	}
}

func (r *Runtime) persist(capture *contentbackup.Capture) {
	defer func() {
		if capture.Release != nil {
			capture.Release()
		}
	}()
	outcome, err := r.writer.Write(context.Background(), capture)
	if err != nil {
		r.failed.Add(1)
		r.cfg.Logf("content backup spool write failed for job %s: %v", capture.Meta.JobID, err)
		return
	}
	if outcome.Durable {
		r.written.Add(1)
	}
	if outcome.Registered {
		r.registered.Add(1)
	}
}

// Reader returns the site-scoped reader, or nil when no site label is configured yet.
func (r *Runtime) Reader() *Reader {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.site == nil {
		return nil
	}
	return r.site.reader
}

func (r *Runtime) Counters() RuntimeCounters {
	return RuntimeCounters{
		Queued:     r.queued.Load(),
		Written:    r.written.Load(),
		Registered: r.registered.Load(),
		Rejected:   r.rejected.Load(),
		Failed:     r.failed.Load(),
	}
}

// supervise keeps the site-scoped workers in step with the config: build them when a valid
// site label appears, rebuild when it changes, tear them down when it is cleared. It also
// runs the periodic reconcile and heartbeat while a site is active.
func (r *Runtime) supervise(ctx context.Context) {
	defer r.loops.Done()
	ticker := time.NewTicker(r.cfg.SuperviseInterval)
	defer ticker.Stop()
	var lastReconcile, lastHeartbeat time.Time
	for {
		r.ensureSite(ctx)
		snapshot := r.cfg.Config()
		r.spool.ApplyBudget(snapshot)
		if site := r.currentSite(); site != nil {
			now := r.cfg.Now()
			if reconcileEvery := time.Duration(snapshot.ReconcileIntervalSeconds) * time.Second; lastReconcile.IsZero() || now.Sub(lastReconcile) >= reconcileEvery {
				r.reconcile(ctx, site)
				lastReconcile = now
			}
			if heartbeatEvery := time.Duration(snapshot.HeartbeatIntervalSeconds) * time.Second; lastHeartbeat.IsZero() || now.Sub(lastHeartbeat) >= heartbeatEvery {
				r.heartbeat(ctx, site, snapshot)
				lastHeartbeat = now
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runtime) currentSite() *siteComponents {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.site
}

func (r *Runtime) ensureSite(ctx context.Context) {
	label := r.cfg.Config().SiteLabel
	current := r.currentSite()
	if current != nil && current.siteID == label {
		return
	}
	if current != nil {
		r.teardownSite(current)
	}
	if label == "" {
		return
	}
	if err := contentbackup.ValidateSiteLabel(label); err != nil {
		r.cfg.Logf("content backup runtime: site label %q rejected: %v", label, err)
		return
	}
	site, err := r.buildSite(ctx, label)
	if err != nil {
		r.cfg.Logf("content backup runtime: cannot start workers for site %q: %v", label, err)
		return
	}
	r.mu.Lock()
	r.site = site
	r.mu.Unlock()
	r.cfg.Logf("content backup workers running for site %s on node %s", label, r.nodeID)
}

func (r *Runtime) buildSite(ctx context.Context, siteID string) (*siteComponents, error) {
	snapshot := r.cfg.Config()
	store := model.NewContentBackupStore(r.cfg.DB, siteID)
	factory := newRemoteFactory(siteID, r.cfg.Config)
	reader, err := NewReader(ReaderConfig{
		SiteID:        siteID,
		StorageNodeID: r.nodeID,
		Store:         store,
		RemoteFactory: factory,
		MaxDecompress: snapshot.MaxDecompressBytes,
		Timeout:       time.Duration(snapshot.ReadTimeoutSeconds) * time.Second,
	})
	if err != nil {
		return nil, err
	}
	uploader, err := NewUploader(UploadConfig{
		SiteID:        siteID,
		StorageNodeID: r.nodeID,
		ProcessID:     r.processID,
		Store:         store,
		Spool:         r.spool,
		Config:        r.cfg.Config,
		RemoteFactory: factory,
	})
	if err != nil {
		return nil, err
	}
	cleaner, err := NewCleaner(CleanerConfig{
		SiteID:        siteID,
		StorageNodeID: r.nodeID,
		Store:         store,
		Spool:         r.spool,
	})
	if err != nil {
		return nil, err
	}
	siteCtx, cancel := context.WithCancel(ctx)
	site := &siteComponents{
		siteID:     siteID,
		store:      store,
		reconciler: &Reconciler{spool: r.spool, store: store, nodeID: r.nodeID, configFn: r.cfg.Config},
		uploader:   uploader,
		cleaner:    cleaner,
		reader:     reader,
		factory:    factory,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	// Recover before uploading so rows for files written while no site was configured (or
	// while a previous process died mid-registration) exist before the uploader looks.
	r.reconcile(siteCtx, site)
	uploader.Start(siteCtx)
	go func() {
		defer close(site.done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			// Folding first: a large cleanup backlog keeps drainCleanup busy for many batches.
			r.foldStats(siteCtx, site)
			r.drainCleanup(siteCtx, cleaner)
			r.expireIndex(siteCtx, site)
			select {
			case <-siteCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return site, nil
}

// drainCleanup keeps releasing local files while the cleaner comes back with full batches.
// One batch per 30s tick capped local release at limit/30 files a second (100 → 3.3/s);
// once uploads ran faster than that, uploaded-but-unreleased files piled up and the spool
// was on course to hit its stop watermark again within a day.
func (r *Runtime) drainCleanup(ctx context.Context, cleaner *Cleaner) {
	for ctx.Err() == nil {
		result, err := cleaner.RunCycle(ctx)
		if err != nil {
			if ctx.Err() == nil {
				r.cfg.Logf("content backup cleanup cycle: %v", err)
			}
			return
		}
		// 没领满一批说明积压已清空；一批里一个都没清掉（整批冲突或反复失败）也停下，
		// 交给下一拍，保证这里每转一圈都在推进，不会对着同一批行空转。
		if result.Considered < cleaner.limit || result.Cleaned == 0 {
			return
		}
	}
}

// foldStats moves this node's upload deltas into the daily stats until a batch comes back
// short. The status bar's "today uploaded" trails real commits by at most one tick.
func (r *Runtime) foldStats(ctx context.Context, site *siteComponents) {
	for ctx.Err() == nil {
		n, err := site.store.FoldStatDeltas(ctx, r.nodeID, contentBackupStatFoldBatch, r.cfg.Now())
		if err != nil {
			if ctx.Err() == nil {
				r.cfg.Logf("content backup stat fold: %v", err)
			}
			return
		}
		if n < contentBackupStatFoldBatch {
			return
		}
	}
}

// contentBackupStatFoldBatch is one fold transaction; 500 ids keep the IN list inside
// SQLite's bound-parameter limit.
const contentBackupStatFoldBatch = 500

// contentBackupIndexExpireBatch bounds one expiry pass; at one pass per cleaner tick it
// removes several times the steady inflow, and a backlog simply takes more ticks.
const contentBackupIndexExpireBatch = 1000

// expireIndex drops archive index rows older than index_retention_days.
func (r *Runtime) expireIndex(ctx context.Context, site *siteComponents) {
	days := r.cfg.Config().IndexRetentionDays
	if days < 1 {
		// 删除路径的保险：保留期异常时宁可不删，也不能把 cutoff 算成"现在"。
		return
	}
	cutoff := r.cfg.Now().AddDate(0, 0, -days)
	n, err := site.store.ExpireUploadedIndex(ctx, r.nodeID, cutoff, contentBackupIndexExpireBatch)
	if err != nil {
		if ctx.Err() == nil {
			r.cfg.Logf("content backup index expiry: %v", err)
		}
		return
	}
	if n > 0 {
		r.cfg.Logf("content backup index expiry removed %d rows uploaded before %s", n, cutoff.UTC().Format(time.RFC3339))
	}
}

func (r *Runtime) teardownSite(site *siteComponents) {
	r.mu.Lock()
	if r.site == site {
		r.site = nil
	}
	r.mu.Unlock()
	site.cancel()
	site.uploader.Wait()
	<-site.done
	r.cfg.Logf("content backup workers stopped for site %s", site.siteID)
}

func (r *Runtime) reconcile(ctx context.Context, site *siteComponents) {
	result, err := site.reconciler.Recover(ctx)
	r.mu.Lock()
	r.lastRecon = result
	r.reconAt = r.cfg.Now()
	r.reconError = err
	r.mu.Unlock()
	if err != nil && ctx.Err() == nil {
		r.cfg.Logf("content backup reconcile: %v", err)
	}
}

// heartbeat publishes the node snapshot (design doc 6.1 content_backup_node_status).
func (r *Runtime) heartbeat(ctx context.Context, site *siteComponents, snapshot contentbackup.Config) {
	spoolUsed, _ := r.spool.Snapshot()
	r.mu.Lock()
	recon := r.lastRecon
	r.mu.Unlock()
	counters := r.Counters()
	status := model.ContentBackupNodeStatus{
		SiteID:               site.siteID,
		StorageNodeID:        r.nodeID,
		ProcessID:            r.processID,
		ConfigVersion:        snapshot.Version,
		AppliedConfigVersion: snapshot.Version,
		LastSeenAt:           r.cfg.Now().Unix(),
		SampledAt:            r.cfg.Now().Unix(),
		SpoolBytes:           spoolUsed,
		SpoolLimitBytes:      r.spool.Limit(),
		OrphanCount:          int64(recon.OrphansRebuilt),
		IncompleteSpoolCount: int64(recon.IncompleteSpool),
		HandoffRejectedCount: counters.Rejected,
		HandoffUnknownCount:  counters.Failed,
		// 凭据现在是配置的一部分，站内所有节点看到同一份。
		FTPSCredentialsSet: snapshot.RemoteCredentialsSet(),
	}
	if stat, err := diskStats(r.cfg.SpoolDir); err == nil {
		status.FreeBytes = stat.freeBytes
		status.FreeInodes = stat.freeInodes
		status.DiskTotalBytes = stat.totalBytes
		status.InodeTotal = stat.totalInodes
	}
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if counts, err := site.store.CountJobs(dbCtx, r.nodeID); err == nil {
		status.PendingCount = counts.PendingCount
		status.ProcessingCount = counts.ProcessingCount
		status.FailedCount = counts.FailedCount
		status.OldestPendingAt = counts.OldestPendingAt
		status.CleanupPendingCount = counts.CleanupPendingCount
		status.CleanupPendingBytes = counts.CleanupPendingBytes
	}
	if err := site.store.SaveNodeStatus(dbCtx, status); err != nil && ctx.Err() == nil {
		r.cfg.Logf("content backup heartbeat: %v", err)
	}
}

// Shutdown stops accepting captures, drains the queued ones into the spool within ctx,
// stops the site workers and returns. Uploads in flight are cancelled (their leases
// expire and the next process resumes them); nothing is deleted.
func (r *Runtime) Shutdown(ctx context.Context) error {
	if r.stopped.Swap(true) {
		return nil
	}
	close(r.queue)
	drained := make(chan struct{})
	go func() {
		r.workers.Wait()
		close(drained)
	}()
	var drainErr error
	select {
	case <-drained:
	case <-ctx.Done():
		drainErr = ctx.Err()
		// Whatever is still queued is released without being written; the capture budget
		// must not outlive the process either way.
		for capture := range r.queue {
			if capture.Release != nil {
				capture.Release()
			}
		}
	}
	r.loopCancel()
	r.loops.Wait()
	if site := r.currentSite(); site != nil {
		r.teardownSite(site)
	}
	r.cfg.Logf("content backup runtime stopped (node %s)", r.nodeID)
	return drainErr
}

// newRemoteFactory builds the transport for the configured protocol. Uploads and reads share
// it so target identity and credentials can never drift between the two paths.
func newRemoteFactory(siteID string, config func() contentbackup.Config) RemoteFactory {
	return func(targetID string) (RemoteStore, error) {
		snapshot := config()
		if snapshot.EffectiveRemoteProtocol() == contentbackup.RemoteProtocolSFTP {
			return NewSFTPRemoteStore(SFTPTarget{
				TargetID:         snapshot.TargetID,
				SiteID:           siteID,
				Host:             snapshot.SFTPHost,
				Port:             snapshot.SFTPPort,
				HostKeySHA256:    snapshot.SFTPHostKeySHA256,
				BaseDir:          snapshot.SFTPBaseDir,
				Username:         snapshot.RemoteUsername,
				Password:         snapshot.RemotePassword,
				ConnectTimeout:   time.Duration(snapshot.FTPSConnectTimeoutSeconds) * time.Second,
				OperationTimeout: time.Duration(snapshot.UploadTimeoutSeconds) * time.Second,
			})
		}
		return NewFTPSRemoteStore(FTPSTarget{
			TargetID:         snapshot.TargetID,
			SiteID:           siteID,
			Host:             snapshot.FTPSHost,
			Port:             snapshot.FTPSPort,
			CertSHA256:       snapshot.CertSHA256,
			Username:         snapshot.RemoteUsername,
			Password:         snapshot.RemotePassword,
			ConnectTimeout:   time.Duration(snapshot.FTPSConnectTimeoutSeconds) * time.Second,
			OperationTimeout: time.Duration(snapshot.UploadTimeoutSeconds) * time.Second,
		})
	}
}

type runtimeDiskStats struct {
	freeBytes   int64
	totalBytes  int64
	freeInodes  int64
	totalInodes int64
}

func diskStats(dir string) (runtimeDiskStats, error) {
	usage, err := disk.Usage(dir)
	if err != nil {
		return runtimeDiskStats{}, err
	}
	return runtimeDiskStats{
		freeBytes:   int64(usage.Free),
		totalBytes:  int64(usage.Total),
		freeInodes:  int64(usage.InodesFree),
		totalInodes: int64(usage.InodesTotal),
	}, nil
}
