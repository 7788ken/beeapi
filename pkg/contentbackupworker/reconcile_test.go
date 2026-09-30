package contentbackupworker

import (
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"gorm.io/gorm"
)

func t03Reconciler(db *gorm.DB, spool *SpoolManager) *Reconciler {
	return &Reconciler{
		spool:    spool,
		store:    model.NewContentBackupStore(db, t03SiteID),
		nodeID:   t03NodeID,
		configFn: func() contentbackup.Config { return t03Config() },
	}
}

// t03WriteComplete lays down a real {job_id}.json.gz exactly as ingest would, so the
// recovery scan sees a self describing envelope it can rebuild a pending row from.
func t03WriteComplete(t *testing.T, spool *SpoolManager, capture *contentbackup.Capture, frameSHA string) string {
	t.Helper()
	finalPath, err := spool.JobPath(capture.Meta.JobID)
	if err != nil {
		t.Fatalf("JobPath: %v", err)
	}
	file, err := os.OpenFile(finalPath, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("create spool file: %v", err)
	}
	gz := gzip.NewWriter(file)
	if err := contentbackup.WriteEnvelope(gz, capture.Meta, capture.RequestChunks, capture.ResponseChunks, frameSHA); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	return finalPath
}

func TestContentBackupRecoverRebuildsOrphanPending(t *testing.T) {
	db := t03OpenDB(t)
	spool, _ := t03Spool(t, nil)
	jobID := contentbackup.NewJobID()
	capture := t03Capture(t03Meta(jobID), []byte{0x00, 'r', 'e', 'q'}, []byte{'r', 'e', 's', 'p'})
	_, frameSHA := t03Frame(t, capture)
	finalPath := t03WriteComplete(t, spool, capture, frameSHA)
	fileBytes, _ := os.ReadFile(finalPath)

	result, err := t03Reconciler(db, spool).Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected recover errors: %v", result.Errors)
	}
	if result.OrphansRebuilt != 1 || result.Scanned != 1 {
		t.Fatalf("result = %+v, want 1 orphan rebuilt from 1 scanned", result)
	}

	store := model.NewContentBackupStore(db, t03SiteID)
	job, err := store.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("orphan row missing: %v", err)
	}
	if job.Status != model.ContentBackupStatusPending {
		t.Fatalf("status = %q, want pending", job.Status)
	}
	if job.FrameSHA256 != frameSHA {
		t.Fatalf("frame_sha256 = %q, want %q", job.FrameSHA256, frameSHA)
	}
	if job.CompressedSHA256 != common.Sha256(fileBytes) {
		t.Fatalf("compressed_sha256 must be recomputed from the file: got %q want %q", job.CompressedSHA256, common.Sha256(fileBytes))
	}
	if job.CompressedBytes != int64(len(fileBytes)) {
		t.Fatalf("compressed_bytes = %d, want %d", job.CompressedBytes, len(fileBytes))
	}
	if job.LocalPath != finalPath {
		t.Fatalf("local_path = %q, want %q", job.LocalPath, finalPath)
	}
	if want, _ := capture.Meta.RemotePath(); job.RemotePath != want {
		t.Fatalf("remote_path = %q, want %q", job.RemotePath, want)
	}
	if !t03FileExists(t, finalPath) {
		t.Fatal("recovery must never delete the complete backup it just rebuilt")
	}
}

func TestContentBackupRecoverLeavesUploadedForCleanup(t *testing.T) {
	db := t03OpenDB(t)
	spool, _ := t03Spool(t, nil)
	store := model.NewContentBackupStore(db, t03SiteID)
	jobID := contentbackup.NewJobID()
	capture := t03Capture(t03Meta(jobID), []byte("request"), []byte("response"))
	_, frameSHA := t03Frame(t, capture)
	finalPath := t03WriteComplete(t, spool, capture, frameSHA)

	// Seed an already uploaded row whose local copy has not been released yet.
	seed, err := buildJob(capture.Meta, frameSHA, common.Sha256([]byte("stale")), 5, finalPath)
	if err != nil {
		t.Fatalf("buildJob: %v", err)
	}
	if err := store.EnsurePending(context.Background(), seed); err != nil {
		t.Fatalf("EnsurePending: %v", err)
	}
	if err := db.Model(&model.ContentBackupJob{}).
		Where("site_id = ? AND job_id = ?", t03SiteID, jobID).
		Updates(map[string]any{
			"status":        model.ContentBackupStatusUploaded,
			"cleanup_state": model.ContentBackupCleanupPending,
			"uploaded_at":   time.Now().UTC().Unix(),
		}).Error; err != nil {
		t.Fatalf("mark uploaded: %v", err)
	}

	result, err := t03Reconciler(db, spool).Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result.Uploaded != 1 || result.OrphansRebuilt != 0 {
		t.Fatalf("result = %+v, want 1 uploaded and no orphan rebuild", result)
	}
	job, err := store.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != model.ContentBackupStatusUploaded {
		t.Fatalf("recovery must not change an uploaded row, status = %q", job.Status)
	}
	if job.CompressedSHA256 != common.Sha256([]byte("stale")) {
		t.Fatalf("recovery must not rewrite an existing row's compressed_sha256, got %q", job.CompressedSHA256)
	}
	if !t03FileExists(t, finalPath) {
		t.Fatal("recovery identifies the local copy for T06 but must not delete it")
	}
}

func TestContentBackupRecoverQuarantinesStalePart(t *testing.T) {
	db := t03OpenDB(t)
	spool, _ := t03Spool(t, nil)
	jobID := contentbackup.NewJobID()
	partPath, err := spool.PartPath(jobID)
	if err != nil {
		t.Fatalf("PartPath: %v", err)
	}
	if err := os.WriteFile(partPath, []byte("truncated gzip that never finished"), 0600); err != nil {
		t.Fatalf("write part: %v", err)
	}
	// 真的把它做旧：周期对账按 mtime 判断 .part 是否还可能在途，刚写下的文件会被
	// 当成活动写入而跳过。此前这里没有做旧，测的其实是"见 .part 就搬"的旧行为。
	stale := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(partPath, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	result, err := t03Reconciler(db, spool).Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result.IncompleteSpool != 1 {
		t.Fatalf("incomplete_spool = %d, want 1", result.IncompleteSpool)
	}
	if t03FileExists(t, partPath) {
		t.Fatal("the stale .part must be moved out of the active parts dir")
	}
	if t03RegularFileCount(t, spool.QuarantineDir()) != 1 {
		t.Fatal("the stale .part must be quarantined, not deleted")
	}
	store := model.NewContentBackupStore(db, t03SiteID)
	if _, err := store.GetJob(context.Background(), jobID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("an incomplete .part must never enter pending, err = %v", err)
	}
}

func TestContentBackupRecoverSurvivesCorruptCompleteFile(t *testing.T) {
	db := t03OpenDB(t)
	spool, _ := t03Spool(t, nil)
	jobID := contentbackup.NewJobID()
	finalPath, err := spool.JobPath(jobID)
	if err != nil {
		t.Fatalf("JobPath: %v", err)
	}
	if err := os.WriteFile(finalPath, []byte("this is not a gzip stream"), 0600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	result, err := t03Reconciler(db, spool).Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover must not abort the whole scan over one corrupt file: %v", err)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("want exactly one recorded error, got %v", result.Errors)
	}
	if result.OrphansRebuilt != 0 {
		t.Fatalf("a corrupt file must not rebuild a row, got %d", result.OrphansRebuilt)
	}
	if !t03FileExists(t, finalPath) {
		t.Fatal("a corrupt file must not be deleted; it may be evidence and is not ours to destroy")
	}
	store := model.NewContentBackupStore(db, t03SiteID)
	if _, err := store.GetJob(context.Background(), jobID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("a corrupt file must not enter pending, err = %v", err)
	}
}

func TestContentBackupRecoverEmptySpoolIsReady(t *testing.T) {
	db := t03OpenDB(t)
	spool, _ := t03Spool(t, nil)
	result, err := t03Reconciler(db, spool).Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover on an empty spool: %v", err)
	}
	if result.Scanned != 0 || len(result.Errors) != 0 {
		t.Fatalf("result = %+v, want an empty clean scan", result)
	}
}

func TestContentBackupRecoverScanFailurePropagates(t *testing.T) {
	db := t03OpenDB(t)
	spool, root := t03Spool(t, nil)
	// Removing the bodies dir makes the startup scan fail loudly rather than silently
	// reporting an empty spool and becoming ready over unseen files.
	if err := os.RemoveAll(filepath.Join(root, spoolBodiesSubdir)); err != nil {
		t.Fatalf("remove bodies dir: %v", err)
	}
	if _, err := t03Reconciler(db, spool).Recover(context.Background()); err == nil {
		t.Fatal("Recover must fail when the spool cannot be scanned")
	}
}

// 已登记的任务不该再被解压。用一个内容完全读不出来的 spool 文件当探针：只要对账还去
// 读它，这一轮就必然产生错误、也数不出 Existing。摘掉"先查库"的修复，本用例必红。
func TestContentBackupRecoverSkipsDecompressForKnownJob(t *testing.T) {
	db := t03OpenDB(t)
	spool, _ := t03Spool(t, nil)
	store := model.NewContentBackupStore(db, t03SiteID)
	jobID := contentbackup.NewJobID()
	capture := t03Capture(t03Meta(jobID), []byte("request"), []byte("response"))
	_, frameSHA := t03Frame(t, capture)

	seed, err := buildJob(capture.Meta, frameSHA, common.Sha256([]byte("seed")), 4, "")
	if err != nil {
		t.Fatalf("buildJob: %v", err)
	}
	if err := store.EnsurePending(context.Background(), seed); err != nil {
		t.Fatalf("EnsurePending: %v", err)
	}

	finalPath, err := spool.JobPath(jobID)
	if err != nil {
		t.Fatalf("JobPath: %v", err)
	}
	if err := os.WriteFile(finalPath, []byte("not gzip at all"), 0600); err != nil {
		t.Fatalf("write spool file: %v", err)
	}

	result, err := t03Reconciler(db, spool).Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("a registered job must not be decompressed again: %v", result.Errors)
	}
	if result.Scanned != 1 || result.Existing != 1 || result.OrphansRebuilt != 0 {
		t.Fatalf("result = %+v, want 1 scanned / 1 existing / 0 rebuilt", result)
	}
}
