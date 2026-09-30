package contentbackupworker

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

const (
	// incomingOrphanAge is how long a temp must sit untouched before it can be reaped. A live
	// transfer keeps rewriting its temp and ends within upload_timeout (at most 1h), so a day
	// also absorbs clock skew and the server-local times an FTP LIST without MLSD reports.
	incomingOrphanAge = 24 * time.Hour
	// incomingReapEvery paces the walk: one shard per tick, all 256 in about an hour.
	incomingReapEvery  = 15 * time.Second
	incomingShardCount = 256
	// incomingReapAfterUpload keeps the reaper to servers that are taking uploads right now:
	// an idle node holds no session open for it, and a rejected password stops uploads and
	// the reaper alike instead of adding a failed login every tick.
	incomingReapAfterUpload = 10 * time.Minute
)

// IncomingReapResult reports one shard pass.
type IncomingReapResult struct {
	Listed  int
	Removed int
}

// reapIncomingLoop walks the incoming shards so temps left by crashed, cancelled or finally
// failed transfers stop piling up on the backup server. PutVerified only sweeps the same
// job's older temps when that job is retried, which never happens for a failed or orphaned one.
func (u *Uploader) reapIncomingLoop(ctx context.Context) {
	shard := rand.IntN(incomingShardCount)
	ticker := time.NewTicker(u.cfg.IncomingReapEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		name := fmt.Sprintf("%02x", shard)
		result, err := u.ReapIncomingShard(ctx, name)
		if result.Removed > 0 {
			u.cfg.Logf("content backup incoming reap %s removed %d orphaned temps", name, result.Removed)
		}
		if err != nil {
			if errors.Is(err, ErrUploadStopped) || ctx.Err() != nil {
				return
			}
			u.cfg.Logf("content backup incoming reap %s: %v", name, err)
		}
		shard = (shard + 1) % incomingShardCount
	}
}

// ReapIncomingShard deletes the temps in one shard that no worker can still rename into the
// archive: untouched for incomingOrphanAge and not named by the current lease of a job that is
// still processing. The shard is listed before the DB is asked, and a temp only appears after
// its lease was committed, so a lease taken in between is always seen and its temp kept.
func (u *Uploader) ReapIncomingShard(ctx context.Context, shard string) (IncomingReapResult, error) {
	var result IncomingReapResult
	u.mu.Lock()
	stopped := u.stopped
	u.mu.Unlock()
	if stopped {
		return result, ErrUploadStopped
	}
	cfg := u.cfg.Config()
	if cfg.UploadPaused || cfg.TargetID == "" {
		return result, nil
	}
	now := u.cfg.Now()
	if last := u.lastPutOK.Load(); last == 0 || now.Sub(time.Unix(last, 0)) > incomingReapAfterUpload {
		return result, nil
	}
	remote, err := u.remoteFor(cfg.TargetID, u.uploadTimeout(cfg))
	if err != nil {
		return result, err
	}
	temps, err := remote.ListIncoming(ctx, shard)
	if err != nil {
		return result, err
	}
	result.Listed = len(temps)

	var stale []IncomingTemp
	var jobIDs []string
	for _, temp := range temps {
		// No timestamp means no proof of age; such a temp is left alone.
		if temp.ModTime.IsZero() || now.Sub(temp.ModTime) < incomingOrphanAge {
			continue
		}
		stale = append(stale, temp)
		jobIDs = append(jobIDs, temp.JobID)
	}
	if len(stale) == 0 {
		return result, nil
	}
	current, err := u.cfg.Store.CurrentLeaseTokens(ctx, jobIDs)
	if err != nil {
		return result, err
	}
	// A temp that cannot be deleted must not keep the ones listed after it from ever going.
	rand.Shuffle(len(stale), func(i, j int) { stale[i], stale[j] = stale[j], stale[i] })
	for _, temp := range stale {
		if current[temp.JobID] == temp.Token {
			continue
		}
		if err := remote.RemoveIncoming(ctx, temp); err != nil {
			return result, err
		}
		result.Removed++
	}
	return result, nil
}
