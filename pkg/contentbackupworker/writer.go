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
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
)

// SpoolWriter turns a frozen capture into one durable gzip envelope on the spool plus a
// pending row. It is the in-process successor of the UDS ingest server: same file format,
// same durability rule (gzip closed, file fsynced, atomic rename, parent dir fsynced, only
// then registered), minus the frame protocol and the handoff retries a process boundary
// required. Errors are surfaced to the caller for counting; nothing here ever blocks the
// relay because the runtime feeds it from a bounded queue.
type SpoolWriter struct {
	spool    *SpoolManager
	configFn func() contentbackup.Config

	// Fault injection seams; tests override them to reproduce the durability matrix.
	hookGzipWrite     func(w io.Writer, capture *contentbackup.Capture, frameSHA string) error
	hookFsyncFile     func(f *os.File) error
	hookRename        func(oldpath, newpath string) error
	hookFsyncDir      func(dir string) error
	hookEnsurePending func(ctx context.Context, job model.ContentBackupJob) error
}

// WriteOutcome reports what happened to one capture.
type WriteOutcome struct {
	JobID           string
	Durable         bool
	Registered      bool
	CompressedSHA   string
	CompressedBytes int64
}

func NewSpoolWriter(spool *SpoolManager, storeFor func(siteID string) *model.ContentBackupStore, configFn func() contentbackup.Config) (*SpoolWriter, error) {
	if spool == nil {
		return nil, errors.New("content backup writer: spool is required")
	}
	if storeFor == nil {
		return nil, errors.New("content backup writer: store factory is required")
	}
	if configFn == nil {
		return nil, errors.New("content backup writer: config source is required")
	}
	w := &SpoolWriter{spool: spool, configFn: configFn}
	w.hookGzipWrite = func(out io.Writer, capture *contentbackup.Capture, frameSHA string) error {
		return contentbackup.WriteEnvelope(out, capture.Meta, capture.RequestChunks, capture.ResponseChunks, frameSHA)
	}
	w.hookFsyncFile = func(f *os.File) error { return f.Sync() }
	w.hookRename = os.Rename
	w.hookFsyncDir = fsyncDir
	w.hookEnsurePending = func(ctx context.Context, job model.ContentBackupJob) error {
		return storeFor(job.SiteID).EnsurePending(ctx, job)
	}
	return w, nil
}

// Write persists one capture. It never calls capture.Release; ownership stays with the
// caller (the runtime's worker), which releases exactly once after Write returns.
func (w *SpoolWriter) Write(ctx context.Context, capture *contentbackup.Capture) (WriteOutcome, error) {
	if capture == nil {
		return WriteOutcome{}, errors.New("content backup writer: nil capture")
	}
	meta := capture.Meta
	if err := contentbackup.ValidateMetadata(meta); err != nil {
		return WriteOutcome{JobID: meta.JobID}, fmt.Errorf("content backup writer: invalid metadata: %w", err)
	}
	frameSHA, err := CaptureSHA256(capture)
	if err != nil {
		return WriteOutcome{JobID: meta.JobID}, err
	}

	estimate := WorstCaseSpoolBytes(meta)
	if err := w.spool.Reserve(estimate); err != nil {
		return WriteOutcome{JobID: meta.JobID}, fmt.Errorf("content backup writer: spool reservation refused: %w", err)
	}
	outcome, writeErr := w.writeSpoolFile(capture, frameSHA)
	if outcome.finalOnDisk {
		w.spool.Commit(estimate, outcome.finalSize)
	} else {
		w.spool.Release(estimate)
	}
	result := WriteOutcome{JobID: meta.JobID, CompressedSHA: outcome.compressedSHA, CompressedBytes: outcome.compressedBytes}
	if writeErr != nil {
		// A renamed-but-not-durable file stays in place: the reconcile scan will either
		// register it (it is complete) or quarantine it, so nothing is lost silently.
		return result, writeErr
	}
	result.Durable = true

	job, err := buildJob(meta, frameSHA, outcome.compressedSHA, outcome.compressedBytes, outcome.finalPath)
	if err != nil {
		return result, fmt.Errorf("content backup writer: build job row: %w", err)
	}
	job.SiteID = meta.SiteID
	// Registration failure after the file is durable must not roll the file back; the orphan
	// scan rebuilds the pending row (design doc 4.2 step 5). Detached from the caller's ctx so
	// a shutdown in progress cannot cancel registration of a file that is already on disk.
	regCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.dbTimeout())
	defer cancel()
	if err := w.hookEnsurePending(regCtx, job); err != nil {
		common.SysLog(fmt.Sprintf("content backup job %s persisted but EnsurePending failed (orphan scan will rebuild): %v", meta.JobID, err))
		return result, nil
	}
	result.Registered = true
	return result, nil
}

func (w *SpoolWriter) dbTimeout() time.Duration {
	if seconds := w.configFn().DaemonDBTimeoutSeconds; seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return 3 * time.Second
}

func (w *SpoolWriter) writeSpoolFile(capture *contentbackup.Capture, frameSHA string) (spoolWriteOutcome, error) {
	jobID := capture.Meta.JobID
	partPath, err := w.spool.PartPath(jobID)
	if err != nil {
		return spoolWriteOutcome{}, err
	}
	finalPath, err := w.spool.JobPath(jobID)
	if err != nil {
		return spoolWriteOutcome{}, err
	}
	if _, statErr := os.Stat(finalPath); statErr == nil {
		// Job ids are server generated uuids; a file already there means this very capture
		// was persisted before (a retried worker loop). Treat as durable, never overwrite.
		info, _ := os.Stat(finalPath)
		return spoolWriteOutcome{finalPath: finalPath, finalOnDisk: true, finalSize: info.Size(), durable: true}, nil
	}
	_ = os.Remove(partPath)

	file, err := os.OpenFile(partPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, spoolFilePerm)
	if err != nil {
		return spoolWriteOutcome{}, fmt.Errorf("create part file: %w", err)
	}
	counter := &countingWriter{}
	hasher := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(file, hasher, counter))
	if err := w.hookGzipWrite(gz, capture, frameSHA); err != nil {
		_ = gz.Close()
		_ = file.Close()
		return spoolWriteOutcome{}, fmt.Errorf("gzip envelope: %w", err)
	}
	if err := gz.Close(); err != nil {
		_ = file.Close()
		return spoolWriteOutcome{}, fmt.Errorf("close gzip: %w", err)
	}
	compressedSHA := hex.EncodeToString(hasher.Sum(nil))
	compressedBytes := counter.n

	if err := w.hookFsyncFile(file); err != nil {
		_ = file.Close()
		return spoolWriteOutcome{compressedSHA: compressedSHA, compressedBytes: compressedBytes}, fmt.Errorf("fsync part file: %w", err)
	}
	if err := file.Close(); err != nil {
		return spoolWriteOutcome{compressedSHA: compressedSHA, compressedBytes: compressedBytes}, fmt.Errorf("close part file: %w", err)
	}
	if err := w.hookRename(partPath, finalPath); err != nil {
		return spoolWriteOutcome{compressedSHA: compressedSHA, compressedBytes: compressedBytes}, fmt.Errorf("rename part to final: %w", err)
	}
	if err := w.hookFsyncDir(w.spool.bodiesDir); err != nil {
		return spoolWriteOutcome{finalPath: finalPath, finalOnDisk: true, finalSize: compressedBytes,
			compressedSHA: compressedSHA, compressedBytes: compressedBytes}, fmt.Errorf("fsync spool dir: %w", err)
	}
	return spoolWriteOutcome{finalPath: finalPath, finalOnDisk: true, finalSize: compressedBytes,
		compressedSHA: compressedSHA, compressedBytes: compressedBytes, durable: true}, nil
}

type spoolWriteOutcome struct {
	finalPath       string
	finalOnDisk     bool
	finalSize       int64
	compressedSHA   string
	compressedBytes int64
	durable         bool
}

// CaptureSHA256 is the envelope's frame_sha256: the SHA-256 over the encoded metadata
// followed by the request and response bytes. It identifies the exact captured content and
// is what the reconcile scan validates; without a process boundary it no longer has to match
// any wire frame, so the definition is simply "what was written".
func CaptureSHA256(capture *contentbackup.Capture) (string, error) {
	encoded, err := contentbackup.EncodeMetadata(capture.Meta)
	if err != nil {
		return "", fmt.Errorf("content backup writer: encode metadata: %w", err)
	}
	hasher := sha256.New()
	hasher.Write(encoded)
	for _, chunk := range capture.RequestChunks {
		hasher.Write(chunk)
	}
	for _, chunk := range capture.ResponseChunks {
		hasher.Write(chunk)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func buildJob(meta contentbackup.Metadata, frameSHA, compressedSHA string, compressedBytes int64, localPath string) (model.ContentBackupJob, error) {
	remotePath, err := meta.RemotePath()
	if err != nil {
		return model.ContentBackupJob{}, err
	}
	return model.ContentBackupJob{
		JobID:                 meta.JobID,
		RequestID:             meta.RequestID,
		UserID:                meta.UserID,
		TokenID:               meta.TokenID,
		ChannelID:             meta.ChannelID,
		ChannelName:           meta.ChannelName,
		ChannelType:           meta.ChannelType,
		Model:                 meta.Model,
		Endpoint:              meta.Endpoint,
		SessionSource:         common.DerefStringOr(meta.SessionSource, ""),
		SessionHash:           meta.SessionKey(),
		SessionHint:           sessionHint(meta.SessionValue),
		UpstreamRequestID:     common.DerefStringOr(meta.UpstreamRequestID, ""),
		CreatedAt:             meta.RequestStartedAt.Unix(),
		StorageNodeID:         meta.StorageNodeID,
		LocalPath:             localPath,
		TargetID:              meta.TargetID,
		ConfigVersion:         meta.ConfigVersion,
		RemotePath:            remotePath,
		FrameSHA256:           frameSHA,
		CompressedSHA256:      compressedSHA,
		CompressedBytes:       compressedBytes,
		Stream:                meta.Stream,
		HTTPStatus:            meta.HTTPStatus,
		TerminalReason:        meta.TerminalReason,
		RequestContentType:    meta.Request.ContentType,
		RequestCapturedBytes:  meta.Request.CapturedBytes,
		RequestObservedBytes:  meta.Request.ObservedBytes,
		RequestTruncated:      meta.Request.Truncated,
		RequestComplete:       meta.Request.Complete,
		ResponseContentType:   meta.Response.ContentType,
		ResponseCapturedBytes: meta.Response.CapturedBytes,
		ResponseObservedBytes: meta.Response.ObservedBytes,
		ResponseTruncated:     meta.Response.Truncated,
		ResponseComplete:      meta.Response.Complete,
		Status:                model.ContentBackupStatusPending,
		CleanupState:          model.ContentBackupCleanupNotApplicable,
	}, nil
}

// sessionHint keeps a short de-identified prefix for operators; the raw value stays only in
// the file and the DB matches on the full sha256 (design doc 5.1).
func sessionHint(value *string) string {
	if value == nil || *value == "" {
		return ""
	}
	runes := []rune(*value)
	const visible = 4
	if len(runes) <= visible {
		return string(runes)
	}
	return string(runes[:visible]) + "…"
}

type countingWriter struct{ n int64 }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}
