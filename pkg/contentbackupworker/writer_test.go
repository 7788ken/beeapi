package contentbackupworker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 2026-09-18 起备份管道跑在 beeapi 进程内：这里的 SpoolWriter 接替了 UDS ingest 的落盘职责。
// 落盘的持久化规则一个不能少（gzip 关闭、文件 fsync、原子改名、父目录 fsync，然后才入库），
// 下面把原 ingest 的故障矩阵逐条搬到进程内写入器上。

const (
	t03SiteID = "ai"
	t03NodeID = "node-a"
)

func t03Config() contentbackup.Config {
	cfg := contentbackup.DefaultConfig()
	cfg.Version = 1
	cfg.Enabled = true
	cfg.SiteLabel = t03SiteID
	cfg.TargetID = "target-1"
	cfg.RemoteUsername = "backup-user"
	cfg.RemotePassword = "backup-pass"
	cfg.FTPSHost = "ftps.example.com"
	cfg.CertSHA256 = strings.Repeat("a", 64)
	return cfg
}

func t03Meta(jobID string) contentbackup.Metadata {
	missing := contentbackup.SessionMissingReasonAbsent
	return contentbackup.Metadata{
		Version:              contentbackup.MetadataVersion,
		SiteID:               t03SiteID,
		StorageNodeID:        t03NodeID,
		JobID:                jobID,
		RequestID:            "req-" + jobID,
		TargetID:             "target-1",
		ConfigVersion:        1,
		RequestStartedAt:     time.Date(2026, 9, 15, 4, 34, 56, 789000000, time.UTC),
		UserID:               10086,
		TokenID:              42,
		ChannelID:            12,
		ChannelName:          "example",
		Model:                "example-model",
		Endpoint:             "/v1/chat/completions",
		TerminalReason:       "ok",
		ChannelType:          1,
		HTTPStatus:           200,
		SessionMissingReason: &missing,
	}
}

func t03Capture(meta contentbackup.Metadata, request, response []byte) *contentbackup.Capture {
	meta.Request = contentbackup.BodyMeta{ContentType: "application/octet-stream", CapturedBytes: int64(len(request)), ObservedBytes: int64(len(request)), Complete: true}
	meta.Response = contentbackup.BodyMeta{ContentType: "application/octet-stream", CapturedBytes: int64(len(response)), ObservedBytes: int64(len(response)), Complete: true}
	return &contentbackup.Capture{
		Meta:           meta,
		RequestChunks:  [][]byte{request},
		ResponseChunks: [][]byte{response},
	}
}

// t03Frame keeps the old helper shape for the reconcile tests: it returns the bytes the
// writer hashes (unused by callers) and the content hash the envelope carries.
func t03Frame(t *testing.T, capture *contentbackup.Capture) ([]byte, string) {
	t.Helper()
	sha, err := CaptureSHA256(capture)
	if err != nil {
		t.Fatalf("CaptureSHA256: %v", err)
	}
	return nil, sha
}

func t03OpenDB(t *testing.T) *gorm.DB {
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

func t03RegularFileCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read dir %s: %v", dir, err)
	}
	count := 0
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			count++
		}
	}
	return count
}

func t03FileExists(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

func t03Writer(t *testing.T, db *gorm.DB, mutate func(*SpoolWriter)) (*SpoolWriter, *SpoolManager) {
	t.Helper()
	spool, _ := t03Spool(t, nil)
	writer, err := NewSpoolWriter(spool, func(siteID string) *model.ContentBackupStore {
		return model.NewContentBackupStore(db, siteID)
	}, func() contentbackup.Config { return t03Config() })
	if err != nil {
		t.Fatalf("NewSpoolWriter: %v", err)
	}
	if mutate != nil {
		mutate(writer)
	}
	return writer, spool
}

func TestContentBackupWriterDurableWriteRegistersPending(t *testing.T) {
	db := t03OpenDB(t)
	writer, spool := t03Writer(t, db, nil)
	jobID := contentbackup.NewJobID()
	capture := t03Capture(t03Meta(jobID), []byte{0x00, 0xff, 'r', 'e', 'q'}, []byte("resp"))
	_, wantSHA := t03Frame(t, capture)

	outcome, err := writer.Write(context.Background(), capture)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !outcome.Durable || !outcome.Registered || outcome.JobID != jobID {
		t.Fatalf("outcome = %+v, want durable + registered", outcome)
	}
	finalPath, _ := spool.JobPath(jobID)
	if !t03FileExists(t, finalPath) {
		t.Fatal("final spool file missing")
	}
	if t03RegularFileCount(t, spool.PartsDir()) != 0 {
		t.Fatal("no .part may remain after a durable write")
	}
	// 文件必须是自描述的：对账扫描能从它重建同一条任务。
	envelope, compressedSHA, compressedBytes, err := readSpoolEnvelope(finalPath, t03Config().MaxDecompressBytes)
	if err != nil {
		t.Fatalf("readSpoolEnvelope: %v", err)
	}
	if envelope.FrameSHA256 != wantSHA || envelope.JobID != jobID {
		t.Fatalf("envelope = %+v, want frame sha %s", envelope, wantSHA)
	}
	if compressedSHA != outcome.CompressedSHA || compressedBytes != outcome.CompressedBytes {
		t.Fatalf("outcome digest %s/%d differs from file %s/%d", outcome.CompressedSHA, outcome.CompressedBytes, compressedSHA, compressedBytes)
	}
	job, err := model.NewContentBackupStore(db, t03SiteID).GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("pending row missing: %v", err)
	}
	if job.Status != model.ContentBackupStatusPending || job.LocalPath != finalPath || job.FrameSHA256 != wantSHA ||
		job.CompressedSHA256 != compressedSHA || job.StorageNodeID != t03NodeID || job.SiteID != t03SiteID {
		t.Fatalf("row = %+v", job)
	}
	used, reserved := spool.Snapshot()
	if used != compressedBytes || reserved != 0 {
		t.Fatalf("spool accounting used=%d reserved=%d, want used=%d reserved=0", used, reserved, compressedBytes)
	}
}

// 目录 fsync 失败：文件已改名到位，但不能声称持久；文件也不能被删——对账扫描会补建。
func TestContentBackupWriterFsyncDirFailureKeepsFileNotDurable(t *testing.T) {
	db := t03OpenDB(t)
	writer, spool := t03Writer(t, db, func(w *SpoolWriter) {
		w.hookFsyncDir = func(string) error { return errors.New("injected fsync dir failure") }
	})
	jobID := contentbackup.NewJobID()
	outcome, err := writer.Write(context.Background(), t03Capture(t03Meta(jobID), []byte("a"), []byte("b")))
	if err == nil || outcome.Durable {
		t.Fatalf("fsync dir failure must not be reported durable: outcome=%+v err=%v", outcome, err)
	}
	finalPath, _ := spool.JobPath(jobID)
	if !t03FileExists(t, finalPath) {
		t.Fatal("the renamed file must stay for the reconcile scan")
	}
	if _, err := model.NewContentBackupStore(db, t03SiteID).GetJob(context.Background(), jobID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("a non-durable file must not be registered, got %v", err)
	}
	// 对账扫描把它补成 pending，闭合这条路径。
	result, err := t03Reconciler(db, spool).Recover(context.Background())
	if err != nil || result.OrphansRebuilt != 1 {
		t.Fatalf("reconcile = %+v err=%v, want 1 orphan rebuilt", result, err)
	}
}

// 入库失败：文件已持久，绝不回滚；返回 Durable=true/Registered=false，对账补建。
func TestContentBackupWriterEnsurePendingFailureStillDurableThenOrphanRebuild(t *testing.T) {
	db := t03OpenDB(t)
	writer, spool := t03Writer(t, db, func(w *SpoolWriter) {
		w.hookEnsurePending = func(context.Context, model.ContentBackupJob) error { return errors.New("db down") }
	})
	jobID := contentbackup.NewJobID()
	outcome, err := writer.Write(context.Background(), t03Capture(t03Meta(jobID), []byte("a"), []byte("b")))
	if err != nil || !outcome.Durable || outcome.Registered {
		t.Fatalf("outcome=%+v err=%v, want durable but unregistered", outcome, err)
	}
	finalPath, _ := spool.JobPath(jobID)
	if !t03FileExists(t, finalPath) {
		t.Fatal("durable file must survive a registration failure")
	}
	result, err := t03Reconciler(db, spool).Recover(context.Background())
	if err != nil || result.OrphansRebuilt != 1 {
		t.Fatalf("reconcile = %+v err=%v", result, err)
	}
	if _, err := model.NewContentBackupStore(db, t03SiteID).GetJob(context.Background(), jobID); err != nil {
		t.Fatalf("orphan must be rebuilt: %v", err)
	}
}

// gzip 没写完（磁盘写失败）：只留 .part，不能有正式文件，预留要释放。
func TestContentBackupWriterGzipFailureLeavesIncompletePartOnly(t *testing.T) {
	db := t03OpenDB(t)
	writer, spool := t03Writer(t, db, func(w *SpoolWriter) {
		w.hookGzipWrite = func(out io.Writer, capture *contentbackup.Capture, frameSHA string) error {
			_, _ = out.Write([]byte("half"))
			return errors.New("injected write failure")
		}
	})
	jobID := contentbackup.NewJobID()
	outcome, err := writer.Write(context.Background(), t03Capture(t03Meta(jobID), []byte("a"), []byte("b")))
	if err == nil || outcome.Durable {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	finalPath, _ := spool.JobPath(jobID)
	if t03FileExists(t, finalPath) {
		t.Fatal("no final file may exist after a failed gzip")
	}
	if t03RegularFileCount(t, spool.PartsDir()) != 1 {
		t.Fatal("the incomplete .part must stay for the reconcile scan to quarantine")
	}
	if used, reserved := spool.Snapshot(); used != 0 || reserved != 0 {
		t.Fatalf("reservation must be released after a failed write: used=%d reserved=%d", used, reserved)
	}
}

func TestContentBackupWriterRenameFailureLeavesPartNotFinal(t *testing.T) {
	db := t03OpenDB(t)
	writer, spool := t03Writer(t, db, func(w *SpoolWriter) {
		w.hookRename = func(string, string) error { return errors.New("injected rename failure") }
	})
	jobID := contentbackup.NewJobID()
	if outcome, err := writer.Write(context.Background(), t03Capture(t03Meta(jobID), []byte("a"), []byte("b"))); err == nil || outcome.Durable {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	finalPath, _ := spool.JobPath(jobID)
	if t03FileExists(t, finalPath) {
		t.Fatal("final must not exist when rename failed")
	}
	if t03RegularFileCount(t, spool.PartsDir()) != 1 {
		t.Fatal("part must remain")
	}
}

// 空间不足：预留被拒，什么都不写。
func TestContentBackupWriterRefusesWhenSpoolIsFull(t *testing.T) {
	db := t03OpenDB(t)
	spool, _ := t03Spool(t, func(cfg *contentbackup.Config) { cfg.MaxSpoolMB = 1 })
	writer, err := NewSpoolWriter(spool, func(siteID string) *model.ContentBackupStore {
		return model.NewContentBackupStore(db, siteID)
	}, func() contentbackup.Config { return t03Config() })
	if err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 2*1024*1024)
	jobID := contentbackup.NewJobID()
	if outcome, err := writer.Write(context.Background(), t03Capture(t03Meta(jobID), big, []byte("b"))); err == nil || outcome.Durable {
		t.Fatalf("a capture larger than the spool budget must be refused: %+v %v", outcome, err)
	}
	if t03RegularFileCount(t, spool.BodiesDir()) != 0 || t03RegularFileCount(t, spool.PartsDir()) != 0 {
		t.Fatal("nothing may be written when the reservation is refused")
	}
}

// 元数据非法（例如 site_id 与当前站点不符的空值）在落盘前就拒绝。
func TestContentBackupWriterRejectsInvalidMetadata(t *testing.T) {
	db := t03OpenDB(t)
	writer, spool := t03Writer(t, db, nil)
	meta := t03Meta(contentbackup.NewJobID())
	meta.SiteID = ""
	if _, err := writer.Write(context.Background(), t03Capture(meta, []byte("a"), []byte("b"))); err == nil {
		t.Fatal("invalid metadata must be rejected")
	}
	if t03RegularFileCount(t, spool.BodiesDir()) != 0 || t03RegularFileCount(t, spool.PartsDir()) != 0 {
		t.Fatal("nothing may be written for invalid metadata")
	}
}

// 同一 job 再写一次（worker 循环重试）不能覆盖已有文件。
func TestContentBackupWriterNeverOverwritesExistingFinal(t *testing.T) {
	db := t03OpenDB(t)
	writer, spool := t03Writer(t, db, nil)
	jobID := contentbackup.NewJobID()
	capture := t03Capture(t03Meta(jobID), []byte("first"), []byte("b"))
	if _, err := writer.Write(context.Background(), capture); err != nil {
		t.Fatal(err)
	}
	finalPath, _ := spool.JobPath(jobID)
	before, _ := os.ReadFile(finalPath)
	again := t03Capture(t03Meta(jobID), []byte("second-different"), []byte("b"))
	outcome, err := writer.Write(context.Background(), again)
	if err != nil || !outcome.Durable {
		t.Fatalf("second write must report the existing durable file: %+v %v", outcome, err)
	}
	after, _ := os.ReadFile(finalPath)
	if string(before) != string(after) {
		t.Fatal("an existing final file must never be overwritten")
	}
}

func TestContentBackupLoadOrCreateNodeIDIsStablePerVolume(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	first, err := LoadOrCreateNodeID(dir)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := contentbackup.ValidateSiteLabel(first); err != nil {
		t.Fatalf("node id %q must fit the label charset: %v", first, err)
	}
	second, err := LoadOrCreateNodeID(dir)
	if err != nil || second != first {
		t.Fatalf("second read = %q err=%v, want %q (blue/green slots must share it)", second, err, first)
	}
	other, _ := LoadOrCreateNodeID(filepath.Join(t.TempDir(), "spool"))
	if other == first {
		t.Fatal("different volumes must get different ids")
	}
	if err := os.WriteFile(filepath.Join(dir, nodeIDFileName), []byte("Bad Id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateNodeID(dir); err == nil {
		t.Fatal("a corrupt node id file must be an error, not silently replaced")
	}
}
