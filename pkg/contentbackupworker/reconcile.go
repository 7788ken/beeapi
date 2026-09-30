package contentbackupworker

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"gorm.io/gorm"
)

// Reconciler runs the design doc 6.3 step 7 recovery scan: the unique daemon, holding the
// volume lock, rebuilds the spool inventory before any new handoff is accepted.
type Reconciler struct {
	spool    *SpoolManager
	store    *model.ContentBackupStore
	nodeID   string
	configFn func() contentbackup.Config
}

// ReconcileResult reports what the recovery scan did. IncompleteSpool counts quarantined
// .part files and maps to the node status incomplete_spool_count; Uploaded counts complete
// local copies whose row is already uploaded and whose deletion belongs to T06.
type ReconcileResult struct {
	Scanned         int
	OrphansRebuilt  int
	Uploaded        int
	Existing        int
	IncompleteSpool int
	// ActiveSpool 是被判为"可能仍在途"而跳过的 .part，周期对账下每轮都可能非零，
	// 它不是故障，只是说明这一轮没有对这些文件下结论。
	ActiveSpool int
	Errors      []error
}

// Recover scans the whole spool, rebuilds missing pending rows from the self describing
// envelopes, and quarantines stale .part files. It never deletes a file that might still be
// an active backup, and a per-file failure is recorded without aborting the whole scan so a
// single corrupt file cannot keep the daemon from becoming ready.
func (r *Reconciler) Recover(ctx context.Context) (ReconcileResult, error) {
	scan, err := r.spool.Scan()
	if err != nil {
		return ReconcileResult{}, err
	}
	var result ReconcileResult
	maxDecompress := int64(contentbackup.DefaultConfig().MaxDecompressBytes)
	if r.configFn != nil {
		if cfg := r.configFn(); cfg.MaxDecompressBytes > 0 {
			maxDecompress = cfg.MaxDecompressBytes
		}
	}
	// 孤儿判定只看 job id，而 job id 就写在文件名里，所以"这份 spool 是不是已经登记过"
	// 一条批量查询就能回答。以前每轮都先把每个文件全量解压、重算 SHA256、再去查库，
	// 99% 的答案是"已登记"；积压越深这一轮越慢，越慢积压越深。
	known, knownErr := r.knownJobStatuses(ctx, scan.CompleteFiles)
	if knownErr != nil {
		result.Errors = append(result.Errors, fmt.Errorf("lookup known jobs: %w", knownErr))
		known = nil
	}
	for _, file := range scan.CompleteFiles {
		result.Scanned++
		if status, ok := known[file.JobID]; ok {
			if status == model.ContentBackupStatusUploaded {
				result.Uploaded++
			} else {
				result.Existing++
			}
			continue
		}
		if err := r.recoverComplete(ctx, file, maxDecompress, &result); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("recover %s: %w", file.JobID, err))
		}
	}
	partCutoff := time.Now().Add(-r.partStaleAfter())
	for _, file := range scan.PartFiles {
		// 只隔离已经不可能在途的 .part：业务侧超过 handoff_deadline_seconds 就已放弃，
		// 更新的文件可能正被 handleCapture 写入，抢走它等于毁掉一次在途捕获。
		if file.ModTime.After(partCutoff) {
			result.ActiveSpool++
			continue
		}
		if err := r.quarantinePart(file, &result); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("quarantine %s: %w", file.JobID, err))
		}
	}
	return result, nil
}

// knownJobStatuses 批量回答哪些 spool 文件已经有任务行。一次查询失败不能让整轮对账
// 停摆，调用方退回逐个文件的原路径即可。
func (r *Reconciler) knownJobStatuses(ctx context.Context, files []SpoolFile) (map[string]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(files))
	for _, file := range files {
		// 文件名不是合法 job id 时不猜，交给原路径报出真实错误。
		if contentbackup.ValidateJobID(file.JobID) != nil {
			continue
		}
		ids = append(ids, file.JobID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return r.store.JobStatusesByIDs(ctx, ids)
}

func (r *Reconciler) recoverComplete(ctx context.Context, file SpoolFile, maxDecompress int64, result *ReconcileResult) error {
	envelope, compressedSHA, compressedBytes, err := readSpoolEnvelope(file.Path, maxDecompress)
	if err != nil {
		return err
	}
	meta := envelope.Metadata()
	if err := contentbackup.ValidateMetadata(meta); err != nil {
		return err
	}
	if err := contentbackup.ValidateFrameSHA256(envelope.FrameSHA256); err != nil {
		return err
	}

	existing, err := r.store.GetJob(ctx, meta.JobID)
	if err == nil {
		if existing.Status == model.ContentBackupStatusUploaded {
			// Already uploaded: only the local copy remains, and releasing it is T06's job.
			result.Uploaded++
		} else {
			result.Existing++
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	job, err := buildJob(meta, envelope.FrameSHA256, compressedSHA, compressedBytes, file.Path)
	if err != nil {
		return err
	}
	if err := r.store.EnsurePending(ctx, job); err != nil {
		return err
	}
	result.OrphansRebuilt++
	return nil
}

// partStaleAfter is the age past which a .part cannot still be in flight: the business side
// gives up on a handoff at handoff_deadline_seconds, so anything older is an interrupted write.
// The doubling is slack for clock skew and a slow fsync on the final rename.
func (r *Reconciler) partStaleAfter() time.Duration {
	deadline := contentbackup.DefaultConfig().HandoffDeadlineSeconds
	if r.configFn != nil {
		if cfg := r.configFn(); cfg.HandoffDeadlineSeconds > 0 {
			deadline = cfg.HandoffDeadlineSeconds
		}
	}
	return 2 * time.Duration(deadline) * time.Second
}

// quarantinePart parks a stale .part outside the active ingest path. A partial file is never
// a complete backup and must not enter pending, but it is not deleted either: it may be the
// only evidence of an interrupted handoff and the operator decides its fate.
func (r *Reconciler) quarantinePart(file SpoolFile, result *ReconcileResult) error {
	dest := filepath.Join(r.spool.quarantineDir,
		filepath.Base(file.Path)+"."+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := os.Rename(file.Path, dest); err != nil {
		return err
	}
	if err := fsyncDir(r.spool.quarantineDir); err != nil {
		return err
	}
	if err := fsyncDir(r.spool.partsDir); err != nil {
		return err
	}
	result.IncompleteSpool++
	return nil
}

// readSpoolEnvelope recovers the metadata from a complete .json.gz and recomputes the
// compressed hash and size from the actual file bytes (design doc 5.2). The decompressed
// envelope is bounded by maxDecompress; this reads the self describing envelope, not a frame.
func readSpoolEnvelope(path string, maxDecompress int64) (contentbackup.Envelope, string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return contentbackup.Envelope{}, "", 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return contentbackup.Envelope{}, "", 0, err
	}
	hasher := sha256.New()
	tee := io.TeeReader(file, hasher)
	gz, err := gzip.NewReader(tee)
	if err != nil {
		return contentbackup.Envelope{}, "", 0, fmt.Errorf("open gzip: %w", err)
	}
	decompressed, err := io.ReadAll(io.LimitReader(gz, maxDecompress+1))
	_ = gz.Close()
	if err != nil {
		return contentbackup.Envelope{}, "", 0, fmt.Errorf("read envelope: %w", err)
	}
	if int64(len(decompressed)) > maxDecompress {
		return contentbackup.Envelope{}, "", 0,
			fmt.Errorf("envelope decompresses beyond the %d byte limit", maxDecompress)
	}
	// Drain any trailing compressed bytes so the recomputed hash covers the whole file.
	_, _ = io.Copy(io.Discard, tee)
	compressedSHA := hex.EncodeToString(hasher.Sum(nil))

	envelope, err := contentbackup.DecodeEnvelope(decompressed)
	if err != nil {
		return contentbackup.Envelope{}, "", 0, err
	}
	return envelope, compressedSHA, info.Size(), nil
}
