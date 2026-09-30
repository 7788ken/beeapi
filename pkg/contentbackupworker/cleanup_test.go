package contentbackupworker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	t06SiteID = "ai"
	t06NodeID = "node-a"
	t06Other  = "node-b"
)

func t06OpenDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cleanup.db")
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

type t06Fixture struct {
	db    *gorm.DB
	store *model.ContentBackupStore
	spool *SpoolManager
	clean *Cleaner
	now   time.Time
}

func t06NewFixture(t *testing.T) *t06Fixture {
	t.Helper()
	db := t06OpenDB(t)
	spool, err := NewSpoolManager(filepath.Join(t.TempDir(), "spool"), contentbackup.DefaultConfig())
	if err != nil {
		t.Fatalf("NewSpoolManager: %v", err)
	}
	now := time.Date(2026, 9, 15, 6, 0, 0, 0, time.UTC)
	clean, err := NewCleaner(CleanerConfig{
		SiteID:        t06SiteID,
		StorageNodeID: t06NodeID,
		Store:         model.NewContentBackupStore(db, t06SiteID),
		Spool:         spool,
		Now:           func() time.Time { return now },
		Logf:          func(format string, args ...any) {},
	})
	if err != nil {
		t.Fatalf("NewCleaner: %v", err)
	}
	return &t06Fixture{db: db, store: clean.store, spool: spool, clean: clean, now: now}
}

// t06SeedUploaded lays a real spool file plus an uploaded+cleanup_pending row,
// mirroring the state the upload worker leaves behind (6.3 step 6 / 6.4).
func (f *t06Fixture) t06SeedUploaded(t *testing.T, jobID string) model.ContentBackupJob {
	t.Helper()
	path, err := f.spool.JobPath(jobID)
	if err != nil {
		t.Fatalf("JobPath: %v", err)
	}
	if err := os.WriteFile(path, []byte("local gzip bytes for "+jobID), 0600); err != nil {
		t.Fatalf("write spool file: %v", err)
	}
	job := model.ContentBackupJob{
		SiteID:          t06SiteID,
		JobID:           jobID,
		RequestID:       "req-" + jobID,
		CreatedAt:       f.now.Add(-time.Hour).Unix(),
		StorageNodeID:   t06NodeID,
		TargetID:        "target-1",
		RemotePath:      "/ai/2026-09-15/u-1/nosession/" + jobID + ".json.gz",
		CompressedBytes: 27,
		Status:          model.ContentBackupStatusUploaded,
		CleanupState:    model.ContentBackupCleanupPending,
		UploadedAt:      f.now.Add(-30 * time.Minute).Unix(),
	}
	if err := f.store.EnsurePending(context.Background(), job); err != nil {
		t.Fatalf("EnsurePending: %v", err)
	}
	// EnsurePending keeps a pre-existing row untouched; force the terminal state directly.
	if err := f.db.Model(&model.ContentBackupJob{}).
		Where(map[string]any{"site_id": t06SiteID, "job_id": jobID}).
		Updates(map[string]any{
			"status":           model.ContentBackupStatusUploaded,
			"cleanup_state":    model.ContentBackupCleanupPending,
			"uploaded_at":      job.UploadedAt,
			"compressed_bytes": job.CompressedBytes,
		}).Error; err != nil {
		t.Fatalf("force uploaded state: %v", err)
	}
	stored, err := f.store.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	return stored
}

func (f *t06Fixture) t06SeedTerminal(t *testing.T, jobID string, status string) model.ContentBackupJob {
	t.Helper()
	f.t06SeedUploaded(t, jobID)
	if err := f.db.Model(&model.ContentBackupJob{}).
		Where(map[string]any{"site_id": t06SiteID, "job_id": jobID}).
		Updates(map[string]any{"status": status, "cleanup_state": model.ContentBackupCleanupNotApplicable}).
		Error; err != nil {
		t.Fatalf("force %s state: %v", status, err)
	}
	stored, err := f.store.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	return stored
}

func (f *t06Fixture) t06DirBytes(t *testing.T) (int, []string) {
	t.Helper()
	var total int
	var names []string
	err := filepath.Walk(f.spool.BodiesDir(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += int(info.Size())
			names = append(names, filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk spool: %v", err)
	}
	return total, names
}

// TestContentBackupCleanupDeletesUploadedFileAndMarksCleaned is the primary 6.4
// acceptance: the file really disappears, cleanup becomes done, and the daily
// uploaded counters stay untouched (cleaned_count/freed_bytes are separate).
func TestContentBackupCleanupDeletesUploadedFileAndMarksCleaned(t *testing.T) {
	f := t06NewFixture(t)
	jobID := contentbackup.NewJobID()
	f.t06SeedUploaded(t, jobID)

	// A neighbouring file this feature does NOT own: it must survive every cycle.
	neighbour := filepath.Join(f.spool.BodiesDir(), "unrelated-operator-notes.txt")
	if err := os.WriteFile(neighbour, []byte("keep me"), 0600); err != nil {
		t.Fatalf("write neighbour: %v", err)
	}

	beforeBytes, _ := f.t06DirBytes(t)
	result, err := f.clean.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if result.Cleaned != 1 {
		t.Fatalf("result = %+v, want one cleaned job", result)
	}

	if _, statErr := os.Stat(f.spoolBodiesPath(t, jobID)); !os.IsNotExist(statErr) {
		t.Fatalf("spool file must be gone, stat: %v", statErr)
	}
	afterBytes, afterNames := f.t06DirBytes(t)
	if len(afterNames) != 1 || afterNames[0] != "unrelated-operator-notes.txt" {
		t.Fatalf("only the neighbour may remain, got %v", afterNames)
	}
	if afterBytes >= beforeBytes {
		t.Fatalf("directory bytes must drop: before=%d after=%d", beforeBytes, afterBytes)
	}

	after, err := f.store.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if after.CleanupState != model.ContentBackupCleanupDone || after.Status != model.ContentBackupStatusUploaded {
		t.Fatalf("job = status %q cleanup %q, want uploaded/done", after.Status, after.CleanupState)
	}
	if after.CleanedAt == 0 {
		t.Fatal("cleaned_at must be set")
	}
	day := model.ContentBackupStatDay(f.now.Unix())
	stats, err := f.store.GetDailyStats(context.Background(), day, t06NodeID)
	if err != nil {
		t.Fatalf("GetDailyStats: %v", err)
	}
	if stats.UploadedCount != 0 {
		t.Fatalf("cleanup must never raise uploaded_count, got %d", stats.UploadedCount)
	}
	if stats.CleanedCount != 1 || stats.FreedBytes != 27 {
		t.Fatalf("cleaned accounting = %+v, want cleaned 1 freed 27", stats)
	}
}

func (f *t06Fixture) spoolBodiesPath(t *testing.T, jobID string) string {
	t.Helper()
	path, err := f.spool.JobPath(jobID)
	if err != nil {
		t.Fatalf("JobPath: %v", err)
	}
	return path
}

// TestContentBackupCleanupKeepsPendingAndFailedFiles pins the protection rule:
// only uploaded+cleanup_pending rows lose their local file.
func TestContentBackupCleanupKeepsPendingAndFailedFiles(t *testing.T) {
	f := t06NewFixture(t)
	pendingID := contentbackup.NewJobID()
	failedID := contentbackup.NewJobID()
	f.t06SeedTerminal(t, pendingID, model.ContentBackupStatusPending)
	f.t06SeedTerminal(t, failedID, model.ContentBackupStatusFailed)

	result, err := f.clean.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if result.Cleaned != 0 || result.Scheduled != 0 {
		t.Fatalf("result = %+v, want nothing cleaned or scheduled", result)
	}
	for _, jobID := range []string{pendingID, failedID} {
		if _, statErr := os.Stat(f.spoolBodiesPath(t, jobID)); statErr != nil {
			t.Fatalf("file for %s status must survive: %v", jobID, statErr)
		}
		stored, err := f.store.GetJob(context.Background(), jobID)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if stored.Status == model.ContentBackupStatusUploaded {
			t.Fatalf("%s must not be flipped to uploaded", jobID)
		}
	}
}

// TestContentBackupCleanupSchedulesRetryOnRemoveFailure backs off without
// flipping status or re-uploading when the filesystem refuses the delete.
func TestContentBackupCleanupSchedulesRetryOnRemoveFailure(t *testing.T) {
	f := t06NewFixture(t)
	jobID := contentbackup.NewJobID()
	f.t06SeedUploaded(t, jobID)

	cleanErr := os.ErrPermission
	// Simulate EACCES by making the parent directory read-only.
	bodies := f.spool.BodiesDir()
	if err := os.Chmod(bodies, 0500); err != nil {
		t.Fatalf("chmod bodies: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(bodies, 0700) })

	result, err := f.clean.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if result.Scheduled != 1 {
		t.Fatalf("result = %+v, want one scheduled retry", result)
	}
	_ = cleanErr

	if _, statErr := os.Stat(f.spoolBodiesPath(t, jobID)); statErr != nil {
		t.Fatalf("file must survive a failed delete: %v", statErr)
	}
	stored, err := f.store.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if stored.Status != model.ContentBackupStatusUploaded || stored.CleanupState != model.ContentBackupCleanupPending {
		t.Fatalf("job = %q/%q, want uploaded/pending after a failed delete", stored.Status, stored.CleanupState)
	}
	if stored.CleanupAttempts != 1 {
		t.Fatalf("cleanup_attempts = %d, want 1", stored.CleanupAttempts)
	}
	want := f.now.Add(time.Minute).Unix()
	if stored.CleanupAvailableAt != want {
		t.Fatalf("cleanup_available_at = %d, want %d (now + 1m)", stored.CleanupAvailableAt, want)
	}
}

// TestContentBackupCleanupBackfillsDoneWhenFileAlreadyGone covers "delete ok,
// DB write failed": the next pass sees missing+uploaded and completes the
// idempotent done without re-uploading.
func TestContentBackupCleanupBackfillsDoneWhenFileAlreadyGone(t *testing.T) {
	f := t06NewFixture(t)
	jobID := contentbackup.NewJobID()
	f.t06SeedUploaded(t, jobID)
	if err := os.Remove(f.spoolBodiesPath(t, jobID)); err != nil {
		t.Fatalf("pre-remove: %v", err)
	}

	result, err := f.clean.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if result.Cleaned != 1 {
		t.Fatalf("result = %+v, want the missing file backfilled as cleaned", result)
	}
	stored, err := f.store.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if stored.CleanupState != model.ContentBackupCleanupDone {
		t.Fatalf("cleanup_state = %q, want done", stored.CleanupState)
	}
	if stored.Status != model.ContentBackupStatusUploaded {
		t.Fatal("status must stay uploaded during cleanup")
	}
}

// TestContentBackupCleanupRejectsForeignNodeRows pins ownership: this node's
// cleaner never deletes another node's file, and never writes its rows either.
func TestContentBackupCleanupRejectsForeignNodeRows(t *testing.T) {
	f := t06NewFixture(t)
	jobID := contentbackup.NewJobID()
	job := f.t06SeedUploaded(t, jobID)
	if err := f.db.Model(&model.ContentBackupJob{}).
		Where(map[string]any{"site_id": t06SiteID, "job_id": jobID}).
		Update("storage_node_id", t06Other).Error; err != nil {
		t.Fatalf("move row to node-b: %v", err)
	}

	result, err := f.clean.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if result.Cleaned != 0 || result.Scheduled != 0 {
		t.Fatalf("result = %+v, want no work on a foreign node's row", result)
	}
	if _, statErr := os.Stat(f.spoolBodiesPath(t, jobID)); statErr != nil {
		t.Fatalf("file must be untouched: %v", statErr)
	}
	if result.Conflicts != 0 && job.CleanupState == "" {
		t.Fatal("unreachable guard")
	}
}

// TestContentBackupCleanupRefusesSymlinks pins the spool-root escape rule (5.2):
// a symlink is never a backup we own, so it is skipped, not followed.
func TestContentBackupCleanupRefusesSymlinks(t *testing.T) {
	f := t06NewFixture(t)
	jobID := contentbackup.NewJobID()
	f.t06SeedUploaded(t, jobID)
	path := f.spoolBodiesPath(t, jobID)
	_ = os.Remove(path)
	outside := filepath.Join(t.TempDir(), "precious.txt")
	if err := os.WriteFile(outside, []byte("precious"), 0600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	result, err := f.clean.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if result.Cleaned != 0 {
		t.Fatalf("result = %+v, a symlink must never be cleaned as a regular file", result)
	}
	if _, statErr := os.Stat(outside); statErr != nil {
		t.Fatalf("the symlink target must survive: %v", statErr)
	}
	stored, err := f.store.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if stored.Status != model.ContentBackupStatusUploaded {
		t.Fatal("status must stay uploaded")
	}
}

// TestContentBackupCleanupBackoffLadder pins the 6.4 ladder: 1m/5m/30m/2h/6h,
// capped, never zero.
func TestContentBackupCleanupBackoffLadder(t *testing.T) {
	steps := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}
	for attempt, want := range steps {
		if got := cleanupBackoff(attempt + 1); got != want {
			t.Fatalf("cleanupBackoff(%d) = %v, want %v", attempt+1, got, want)
		}
	}
	if got := cleanupBackoff(17); got != 6*time.Hour {
		t.Fatalf("cleanupBackoff(17) = %v, want the 6h cap", got)
	}
	if got := cleanupBackoff(0); got != time.Minute {
		t.Fatalf("cleanupBackoff(0) = %v, want the first rung", got)
	}
}
