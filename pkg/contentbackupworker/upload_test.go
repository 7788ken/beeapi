package contentbackupworker

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	t05SiteID   = "ai"
	t05NodeID   = "node-a"
	t05TargetID = "target-1"
	t05Process  = "00000000-0000-4000-8000-0000000000aa"
)

func t05Config() contentbackup.Config {
	cfg := contentbackup.DefaultConfig()
	cfg.Version = 1
	cfg.Enabled = true
	cfg.TargetID = t05TargetID
	cfg.FTPSHost = "ftps.invalid"
	cfg.CertSHA256 = strings.Repeat("a", 64)
	return cfg
}

func t05Meta(jobID string) contentbackup.Metadata {
	missing := contentbackup.SessionMissingReasonAbsent
	return contentbackup.Metadata{
		Version:              contentbackup.MetadataVersion,
		SiteID:               t05SiteID,
		StorageNodeID:        t05NodeID,
		JobID:                jobID,
		RequestID:            "req-" + jobID,
		TargetID:             t05TargetID,
		ConfigVersion:        1,
		RequestStartedAt:     time.Date(2026, 9, 15, 4, 34, 56, 789000000, time.UTC),
		UserID:               10086,
		TokenID:              42,
		ChannelID:            12,
		ChannelName:          "example",
		Model:                "example-model",
		Endpoint:             "/v1/chat/completions",
		TerminalReason:       "complete",
		ChannelType:          1,
		HTTPStatus:           200,
		SessionMissingReason: &missing,
	}
}

func t05OpenDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "content-backup.db")
	db, err := gorm.Open(
		sqlite.Open(path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql database: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(model.ContentBackupModels()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// t05RemoteHub hands each worker its own fake so "one control connection per worker" is
// observable, and aggregates the call counts the assertions read.
type t05RemoteHub struct {
	mu      sync.Mutex
	err     error
	hook    func(ctx context.Context, job model.ContentBackupJob, lease model.ContentBackupLease, src io.Reader) error
	created int
	stores  []*t05FakeRemote
	// temps is the remote incoming tree, keyed by "jobID.token"; every store the factory
	// hands out sees the same server.
	temps      map[string]IncomingTemp
	listErr    error
	removed    []string
	failRemove string // "jobID.token" whose RemoveIncoming always fails
}

func (h *t05RemoteHub) factory(targetID string) (RemoteStore, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if targetID != t05TargetID {
		return nil, fmt.Errorf("%w: hub only knows %q", ErrFTPSInvalidJob, t05TargetID)
	}
	store := &t05FakeRemote{hub: h, targetID: targetID, bodies: map[string][][]byte{}}
	h.created++
	h.stores = append(h.stores, store)
	return store, nil
}

func (h *t05RemoteHub) setErr(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.err = err
	h.hook = nil
}

func (h *t05RemoteHub) setHook(hook func(ctx context.Context, job model.ContentBackupJob, lease model.ContentBackupLease, src io.Reader) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hook = hook
	h.err = nil
}

func (h *t05RemoteHub) failure() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

func (h *t05RemoteHub) counts() (created, put, closed int) {
	h.mu.Lock()
	stores := append([]*t05FakeRemote(nil), h.stores...)
	created = h.created
	h.mu.Unlock()
	for _, store := range stores {
		put += store.putCalls()
		closed += store.closeCalls()
	}
	return created, put, closed
}

func (h *t05RemoteHub) uploadedBodies(jobID string) [][]byte {
	h.mu.Lock()
	stores := append([]*t05FakeRemote(nil), h.stores...)
	h.mu.Unlock()
	var out [][]byte
	for _, store := range stores {
		out = append(out, store.bodiesFor(jobID)...)
	}
	return out
}

type t05FakeRemote struct {
	hub      *t05RemoteHub
	targetID string

	mu     sync.Mutex
	put    int
	closed int
	opened int
	bodies map[string][][]byte
	leases []model.ContentBackupLease
	cancel []bool
}

func (f *t05FakeRemote) Remove(context.Context, model.ContentBackupJob) error { return nil }

func (f *t05FakeRemote) ListIncoming(_ context.Context, shard string) ([]IncomingTemp, error) {
	f.hub.mu.Lock()
	defer f.hub.mu.Unlock()
	if f.hub.listErr != nil {
		return nil, f.hub.listErr
	}
	var out []IncomingTemp
	for _, temp := range f.hub.temps {
		if temp.JobID[:2] == shard {
			out = append(out, temp)
		}
	}
	// Real servers list in a stable order; the map would shuffle it for free.
	sort.Slice(out, func(i, j int) bool { return out[i].JobID+out[i].Token < out[j].JobID+out[j].Token })
	return out, nil
}

func (f *t05FakeRemote) RemoveIncoming(_ context.Context, temp IncomingTemp) error {
	f.hub.mu.Lock()
	defer f.hub.mu.Unlock()
	key := temp.JobID + "." + temp.Token
	if key == f.hub.failRemove {
		return fmt.Errorf("%w: remove %s", ErrFTPSPermission, key)
	}
	delete(f.hub.temps, key)
	f.hub.removed = append(f.hub.removed, key)
	return nil
}

func (f *t05FakeRemote) PutVerified(ctx context.Context, job model.ContentBackupJob, lease model.ContentBackupLease, src io.Reader) error {
	f.mu.Lock()
	f.put++
	f.leases = append(f.leases, lease)
	f.mu.Unlock()

	f.hub.mu.Lock()
	hookFn := f.hub.hook
	err := f.hub.err
	f.hub.mu.Unlock()

	if hookFn != nil {
		return hookFn(ctx, job, lease, src)
	}
	if ctx.Err() != nil {
		f.mu.Lock()
		f.cancel = append(f.cancel, true)
		f.mu.Unlock()
		return fmt.Errorf("content backup ftps: stor aborted: %w", ctx.Err())
	}
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(src)
	if readErr != nil {
		return readErr
	}
	f.mu.Lock()
	f.bodies[job.JobID] = append(f.bodies[job.JobID], data)
	f.mu.Unlock()
	return nil
}

func (f *t05FakeRemote) Open(ctx context.Context, job model.ContentBackupJob) (io.ReadCloser, error) {
	f.mu.Lock()
	f.opened++
	f.mu.Unlock()
	return nil, fmt.Errorf("%w: %s", ErrFTPSMissing, job.RemotePath)
}

func (f *t05FakeRemote) Close() error {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
	return nil
}

func (f *t05FakeRemote) putCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.put
}

func (f *t05FakeRemote) closeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *t05FakeRemote) bodiesFor(jobID string) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.bodies[jobID]...)
}

type t05Fixture struct {
	db       *gorm.DB
	store    *model.ContentBackupStore
	spool    *SpoolManager
	spoolDir string
	hub      *t05RemoteHub
	cfg      contentbackup.Config
}

func t05NewFixture(t *testing.T) *t05Fixture {
	t.Helper()
	db := t05OpenDB(t)
	cfg := t05Config()
	spoolDir := filepath.Join(t.TempDir(), "spool")
	spool, err := NewSpoolManager(spoolDir, cfg)
	if err != nil {
		t.Fatalf("NewSpoolManager: %v", err)
	}
	return &t05Fixture{
		db:       db,
		store:    model.NewContentBackupStore(db, t05SiteID),
		spool:    spool,
		spoolDir: spoolDir,
		hub:      &t05RemoteHub{},
		cfg:      cfg,
	}
}

// t05SeedJob lays down a real {job_id}.json.gz and registers the matching pending row, so
// the worker verifies an actual on-disk backup rather than a stub.
func (f *t05Fixture) seedJob(t *testing.T, body string) model.ContentBackupJob {
	t.Helper()
	jobID := contentbackup.NewJobID()
	meta := t05Meta(jobID)
	request := []byte(`{"prompt":` + fmt.Sprintf("%q", body) + `}`)
	response := []byte(`{"completion":` + fmt.Sprintf("%q", body) + `}`)
	meta.Request = contentbackup.BodyMeta{ContentType: "application/json", CapturedBytes: int64(len(request)), ObservedBytes: int64(len(request)), Complete: true}
	meta.Response = contentbackup.BodyMeta{ContentType: "application/json", CapturedBytes: int64(len(response)), ObservedBytes: int64(len(response)), Complete: true}

	path, err := f.spool.JobPath(jobID)
	if err != nil {
		t.Fatalf("JobPath: %v", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("create spool file: %v", err)
	}
	gz := gzip.NewWriter(file)
	frame := append(append([]byte(nil), request...), response...)
	frameSHA := fmt.Sprintf("%x", sha256.Sum256(frame))
	if err := contentbackup.WriteEnvelope(gz, meta, [][]byte{request}, [][]byte{response}, frameSHA); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spool file: %v", err)
	}
	remotePath, err := meta.RemotePath()
	if err != nil {
		t.Fatalf("RemotePath: %v", err)
	}
	compressed := sha256.Sum256(raw)
	job := model.ContentBackupJob{
		SiteID:           t05SiteID,
		JobID:            jobID,
		RequestID:        meta.RequestID,
		UserID:           meta.UserID,
		TokenID:          meta.TokenID,
		ChannelID:        meta.ChannelID,
		ChannelName:      meta.ChannelName,
		ChannelType:      meta.ChannelType,
		Model:            meta.Model,
		Endpoint:         meta.Endpoint,
		TerminalReason:   meta.TerminalReason,
		HTTPStatus:       meta.HTTPStatus,
		CreatedAt:        meta.RequestStartedAt.Unix(),
		StorageNodeID:    t05NodeID,
		LocalPath:        path,
		TargetID:         t05TargetID,
		ConfigVersion:    meta.ConfigVersion,
		RemotePath:       remotePath,
		FrameSHA256:      frameSHA,
		CompressedSHA256: hex.EncodeToString(compressed[:]),
		CompressedBytes:  int64(len(raw)),
		Status:           model.ContentBackupStatusPending,
		RequestComplete:  true,
		ResponseComplete: true,
	}
	if err := f.store.EnsurePending(context.Background(), job); err != nil {
		t.Fatalf("EnsurePending: %v", err)
	}
	stored := f.job(t, jobID)
	if stored.AvailableAt != job.CreatedAt {
		t.Fatalf("available_at = %d, want %d", stored.AvailableAt, job.CreatedAt)
	}
	return stored
}

func (f *t05Fixture) job(t *testing.T, jobID string) model.ContentBackupJob {
	t.Helper()
	job, err := f.store.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob(%s): %v", jobID, err)
	}
	return job
}

func (f *t05Fixture) uploader(now time.Time, jitter float64, mutate func(*UploadConfig)) *Uploader {
	cfg := UploadConfig{
		SiteID:        t05SiteID,
		StorageNodeID: t05NodeID,
		ProcessID:     t05Process,
		Store:         f.store,
		Spool:         f.spool,
		Config:        func() contentbackup.Config { return f.cfg },
		RemoteFactory: f.hub.factory,
		Now:           func() time.Time { return now },
		Jitter:        func() float64 { return jitter },
		Logf:          func(string, ...any) {},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	uploader, err := NewUploader(cfg)
	if err != nil {
		panic(fmt.Sprintf("NewUploader: %v", err))
	}
	return uploader
}

func TestContentBackupUploadNetworkTimeoutBacksOffToPending(t *testing.T) {
	f := t05NewFixture(t)
	job := f.seedJob(t, "timeout")
	f.hub.setErr(fmt.Errorf("content backup ftps: stor: %w (i/o timeout)", ErrFTPSTimeout))

	now := time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC)
	u := f.uploader(now, 0, nil)

	result, err := u.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if result.Claimed != 1 || result.Retried != 1 {
		t.Fatalf("cycle result = %+v, want one claim and one retry", result)
	}

	after := f.job(t, job.JobID)
	if after.Status != model.ContentBackupStatusPending {
		t.Fatalf("status = %q, want pending", after.Status)
	}
	if after.LastErrorCode != UploadCodeTimeout {
		t.Fatalf("last_error_code = %q, want %q", after.LastErrorCode, UploadCodeTimeout)
	}
	want := now.Add(RetryDelay(1, 0)).Unix()
	if after.AvailableAt != want {
		t.Fatalf("available_at = %d, want %d (now + RetryDelay(1,0))", after.AvailableAt, want)
	}
	if after.Attempts != 1 || after.TotalAttempts != 1 {
		t.Fatalf("attempts = %d/%d, want 1/1: Claim raises them once and ScheduleRetry must not",
			after.Attempts, after.TotalAttempts)
	}
	if after.LeaseToken != "" || after.LeaseOwner != "" {
		t.Fatalf("lease must be released, got owner %q token %q", after.LeaseOwner, after.LeaseToken)
	}
	if _, err := os.Stat(job.LocalPath); err != nil {
		t.Fatalf("local backup must survive a retryable failure: %v", err)
	}
	if bodies := f.hub.uploadedBodies(job.JobID); len(bodies) != 0 {
		t.Fatalf("no body may reach the remote on failure, got %d", len(bodies))
	}
	if _, err := f.store.FoldStatDeltas(context.Background(), t05NodeID, 0, now); err != nil {
		t.Fatalf("FoldStatDeltas: %v", err)
	}
	stats, err := f.store.GetDailyStats(context.Background(), model.ContentBackupStatDay(now.Unix()), t05NodeID)
	if err == nil && stats.UploadedCount != 0 {
		t.Fatalf("uploaded_count = %d, want 0", stats.UploadedCount)
	}
}

func TestContentBackupUploadTimeoutFollowsLiveConfig(t *testing.T) {
	f := t05NewFixture(t)
	job := f.seedJob(t, "live-timeout")
	var (
		deadline   time.Time
		leaseUntil time.Time
	)
	f.hub.mu.Lock()
	f.hub.hook = func(ctx context.Context, _ model.ContentBackupJob, lease model.ContentBackupLease, src io.Reader) error {
		if d, ok := ctx.Deadline(); ok {
			deadline = d
		}
		leaseUntil = lease.Until
		_, _ = io.Copy(io.Discard, src)
		return nil
	}
	f.hub.mu.Unlock()

	f.cfg.UploadTimeoutSeconds = 600
	f.cfg.LeaseSeconds = 720
	now := time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC)
	u := f.uploader(now, 0, func(cfg *UploadConfig) {
		cfg.UploadTimeout = 120 * time.Second
		cfg.LeaseSeconds = 180 * time.Second
	})
	if _, err := u.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if deadline.IsZero() {
		t.Fatal("PutVerified was not called")
	}
	if remain := time.Until(deadline); remain < 590*time.Second || remain > 610*time.Second {
		t.Fatalf("upload ctx remaining %s, want ~600s from live config (not the 120s construction default)", remain)
	}
	if got := leaseUntil.Sub(now); got != 720*time.Second {
		t.Fatalf("lease until = %s from Now(), want 720s from live config", got)
	}
	stored := f.job(t, job.JobID)
	if stored.Status != model.ContentBackupStatusUploaded {
		t.Fatalf("status = %s, want uploaded", stored.Status)
	}
}

func TestContentBackupUploadWorkersClaimDifferentJobs(t *testing.T) {
	f := t05NewFixture(t)
	f.cfg.UploadWorkers = 2
	for i := 0; i < 4; i++ {
		f.seedJob(t, fmt.Sprintf("parallel-%d", i))
	}
	var inflight, maxInflight atomic.Int32
	f.hub.setHook(func(ctx context.Context, _ model.ContentBackupJob, _ model.ContentBackupLease, src io.Reader) error {
		n := inflight.Add(1)
		for {
			old := maxInflight.Load()
			if n <= old || maxInflight.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
		inflight.Add(-1)
		_, _ = io.Copy(io.Discard, src)
		return nil
	})
	now := time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	u := f.uploader(now, 0, nil)
	u.Start(ctx)
	defer u.Wait()
	defer cancel()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if maxInflight.Load() >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("max concurrent uploads = %d, want at least 2 independent claims", maxInflight.Load())
}

func TestContentBackupUploadCycleClaimsAtMostWorkers(t *testing.T) {
	f := t05NewFixture(t)
	f.cfg.UploadWorkers = 2
	for i := 0; i < 6; i++ {
		f.seedJob(t, fmt.Sprintf("batch-%d", i))
	}
	now := time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC)
	u := f.uploader(now, 0, nil)
	result, err := u.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if result.Claimed != 2 {
		t.Fatalf("claimed %d, want 2 (live upload_workers), not the old scan-limit batch", result.Claimed)
	}
	if result.Uploaded != 2 {
		t.Fatalf("uploaded %d, want 2", result.Uploaded)
	}
}

// 所有槽位拿到的是同一批、同一序的候选行。不错位的话每个槽位都从第 0 行开始抢，
// 第 k 个要先输 k 次再轮到自己。这里把每轮的认领还原成 pending，让四个槽位看到
// 同一批候选，断言它们各自挑中不同的一条。
func TestContentBackupUploadSlotOffsetSpreadsClaims(t *testing.T) {
	f := t05NewFixture(t)
	f.cfg.UploadWorkers = 4
	for i := 0; i < 4; i++ {
		f.seedJob(t, fmt.Sprintf("offset-%d", i))
	}
	var picked []string
	f.hub.setHook(func(_ context.Context, job model.ContentBackupJob, _ model.ContentBackupLease, src io.Reader) error {
		picked = append(picked, job.JobID)
		_, _ = io.Copy(io.Discard, src)
		return nil
	})
	now := time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC)
	u := f.uploader(now, 0, nil)
	for slot := 0; slot < 4; slot++ {
		result, err := u.runCycle(context.Background(), 1, slot)
		if err != nil {
			t.Fatalf("slot %d: %v", slot, err)
		}
		if result.Claimed != 1 {
			t.Fatalf("slot %d claimed %d, want 1", slot, result.Claimed)
		}
		if err := f.db.Model(&model.ContentBackupJob{}).
			Where("site_id = ?", t05SiteID).
			Updates(map[string]any{
				"status":        model.ContentBackupStatusPending,
				"lease_owner":   "",
				"lease_token":   "",
				"lease_until":   0,
				"available_at":  0,
				"uploaded_at":   0,
				"cleanup_state": model.ContentBackupCleanupNotApplicable,
			}).Error; err != nil {
			t.Fatalf("reset rows: %v", err)
		}
	}
	unique := map[string]bool{}
	for _, id := range picked {
		unique[id] = true
	}
	if len(picked) != 4 || len(unique) != 4 {
		t.Fatalf("four slots must pick four different jobs, got %v", picked)
	}
}

// 槽位上限和配置校验上界必须是同一个数：校验放行得更宽的话，多出来的那几个
// worker 会被 liveUploadWorkers 静默截掉，后台填了 32 实际只跑 16 且不报错。
func TestContentBackupUploadSlotCeilingMatchesConfigBound(t *testing.T) {
	cfg := contentbackup.DefaultConfig()
	cfg.UploadWorkers = contentBackupUploadMaxSlots
	if err := contentbackup.ValidateConfig(cfg); err != nil {
		t.Fatalf("upload_workers=%d must be accepted: %v", contentBackupUploadMaxSlots, err)
	}
	cfg.UploadWorkers = contentBackupUploadMaxSlots + 1
	if err := contentbackup.ValidateConfig(cfg); err == nil {
		t.Fatalf("upload_workers=%d is past the slot ceiling and must be rejected", cfg.UploadWorkers)
	}
}
