package contentbackupworker

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// CleanupResult reports one cleaner cycle for tests and runtime observability.
type CleanupResult struct {
	Considered int
	Cleaned    int
	Scheduled  int
	Conflicts  int
	Unknown    int
}

// Cleaner reclaims local disk space for jobs whose upload already committed
// (design doc 6.4): uploaded and space-reclaimed are two separate outcomes, so
// the cleaner only ever deletes the exact job file of this node's uploaded rows.
// It never touches pending/failed files and never re-uploads anything.
type Cleaner struct {
	store   *model.ContentBackupStore
	spool   *SpoolManager
	site    string
	node    string
	nowFn   func() time.Time
	logf    func(format string, args ...any)
	limit   int
	stopped bool
	mu      sync.Mutex
	wg      sync.WaitGroup
}

type CleanerConfig struct {
	SiteID        string
	StorageNodeID string
	Store         *model.ContentBackupStore
	Spool         *SpoolManager
	Now           func() time.Time
	Logf          func(format string, args ...any)
	ScanLimit     int
}

func NewCleaner(cfg CleanerConfig) (*Cleaner, error) {
	if cfg.SiteID == "" || cfg.StorageNodeID == "" {
		return nil, fmt.Errorf("content backup cleanup: site and storage node identity are required")
	}
	if cfg.Store == nil {
		return nil, fmt.Errorf("content backup cleanup: store is required")
	}
	if cfg.Spool == nil {
		return nil, fmt.Errorf("content backup cleanup: spool is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.ScanLimit <= 0 {
		cfg.ScanLimit = 100
	}
	return &Cleaner{
		store: cfg.Store,
		spool: cfg.Spool,
		site:  cfg.SiteID,
		node:  cfg.StorageNodeID,
		nowFn: cfg.Now,
		logf:  cfg.Logf,
		limit: cfg.ScanLimit,
	}, nil
}

// RunCycle deletes the local files of due uploaded+cleanup_pending rows, then
// marks them cleaned. Design doc 6.4 ordering: re-check persisted state, delete
// the exact job file, fsync the parent directory, only then MarkCleaned.
func (c *Cleaner) RunCycle(ctx context.Context) (CleanupResult, error) {
	result := CleanupResult{}
	now := c.nowFn()

	jobs, err := c.store.ListCleanupJobs(ctx, c.node, now, c.limit)
	if err != nil {
		return result, fmt.Errorf("list cleanup jobs: %w", err)
	}
	result.Considered = len(jobs)

	for _, job := range jobs {
		// The scan list can be stale by the time we act; only the persisted row decides.
		current, err := c.store.GetJob(ctx, job.JobID)
		if err != nil {
			c.logf("content backup cleanup %s: reload: %v", job.JobID, err)
			result.Unknown++
			continue
		}
		if current.Status != model.ContentBackupStatusUploaded || current.CleanupState != model.ContentBackupCleanupPending {
			// Not ours to delete anymore: keep the file, do not fight the upload state.
			result.Conflicts++
			continue
		}

		path, pathErr := c.spool.JobPath(current.JobID)
		if pathErr != nil {
			c.scheduleRetry(ctx, current, pathErr, &result)
			continue
		}

		info, statErr := os.Lstat(path)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				// File gone but the row says uploaded: idempotent completion, next pass
				// backs the "delete ok, DB write failed" recovery from design doc 6.4.
				if err := c.store.MarkCleaned(ctx, current.JobID, c.node, now); err != nil {
					c.handleMarkErr(ctx, current, err, &result)
				} else {
					result.Cleaned++
				}
				continue
			}
			c.scheduleRetry(ctx, current, statErr, &result)
			continue
		}
		// A symlink is never ours; refuse instead of following it out of the spool root.
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			c.scheduleRetry(ctx, current, fmt.Errorf("spool path %s is not a regular file", path), &result)
			continue
		}

		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				if err := c.store.MarkCleaned(ctx, current.JobID, c.node, now); err != nil {
					c.handleMarkErr(ctx, current, err, &result)
				} else {
					result.Cleaned++
				}
				continue
			}
			c.scheduleRetry(ctx, current, err, &result)
			continue
		}
		if err := fsyncDir(c.spool.BodiesDir()); err != nil {
			// File already gone and the directory is durable-enough for the OS; treat as
			// deleted but let the DB write decide the final state, same as Remove success.
			c.logf("content backup cleanup %s: fsync dir: %v", current.JobID, err)
		}
		if err := c.store.MarkCleaned(ctx, current.JobID, c.node, now); err != nil {
			c.handleMarkErr(ctx, current, err, &result)
			continue
		}
		c.spool.Release(current.CompressedBytes)
		result.Cleaned++
	}
	return result, nil
}

func (c *Cleaner) scheduleRetry(ctx context.Context, job model.ContentBackupJob, cause error, result *CleanupResult) {
	now := c.nowFn()
	next := now.Add(cleanupBackoff(job.CleanupAttempts))
	if err := c.store.ScheduleCleanup(ctx, job.JobID, c.node, cause.Error(), now, next); err != nil {
		c.logf("content backup cleanup %s: schedule: %v", job.JobID, err)
		result.Unknown++
		return
	}
	result.Scheduled++
}

func (c *Cleaner) handleMarkErr(ctx context.Context, job model.ContentBackupJob, err error, result *CleanupResult) {
	if err == nil {
		return
	}
	// The file is gone and MarkCleaned failed: the next cycle sees "missing + uploaded"
	// and completes the idempotent backfill, so this is not a re-upload trigger.
	c.logf("content backup cleanup %s: mark cleaned: %v", job.JobID, err)
	result.Unknown++
}

// cleanupBackoff is the design doc 6.4 ladder for cleanup retries: 1m/5m/30m/2h/6h
// capped at 6h, with no automatic give-up; the alert fires past 30 minutes.
func cleanupBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	steps := []time.Duration{
		time.Minute,
		5 * time.Minute,
		30 * time.Minute,
		2 * time.Hour,
		6 * time.Hour,
	}
	idx := attempts - 1
	if idx >= len(steps) {
		idx = len(steps) - 1
	}
	return steps[idx]
}
