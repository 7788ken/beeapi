package contentbackupworker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
)

func reapSeedRow(t *testing.T, f *t05Fixture, jobID string, created time.Time) {
	t.Helper()
	job := model.ContentBackupJob{
		SiteID:           t05SiteID,
		JobID:            jobID,
		StorageNodeID:    t05NodeID,
		CreatedAt:        created.Unix(),
		TargetID:         t05TargetID,
		RemotePath:       "/" + t05SiteID + "/2026-09-15/" + jobID[:2] + "/" + jobID + ".json.gz",
		CompressedSHA256: strings.Repeat("c", 64),
		CompressedBytes:  1,
	}
	if err := f.store.EnsurePending(context.Background(), job); err != nil {
		t.Fatalf("EnsurePending %s: %v", jobID, err)
	}
}

func reapClaim(t *testing.T, f *t05Fixture, jobID string, at, until time.Time) model.ContentBackupLease {
	t.Helper()
	lease, ok, err := f.store.Claim(context.Background(), jobID, t05NodeID, "uploader-x", at, until)
	if err != nil || !ok {
		t.Fatalf("Claim %s = (%v, %v)", jobID, ok, err)
	}
	return lease
}

func (h *t05RemoteHub) putTemp(temp IncomingTemp) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.temps == nil {
		h.temps = map[string]IncomingTemp{}
	}
	h.temps[temp.JobID+"."+temp.Token] = temp
}

func (h *t05RemoteHub) removedKeys() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]string(nil), h.removed...)
	sort.Strings(out)
	return out
}

// Only temps that no worker can still rename into the archive go: old enough, and not the
// token of their job's current lease. Everything else in the shard stays.
func TestContentBackupReapIncomingRemovesOnlyTempsWithoutACurrentLease(t *testing.T) {
	f := t05NewFixture(t)
	now := time.Date(2026, 9, 17, 5, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	young := now.Add(-time.Hour)

	const (
		liveJob     = "ab000000-0000-4000-8000-000000000001"
		expiredJob  = "ab000000-0000-4000-8000-000000000002"
		pendingJob  = "ab000000-0000-4000-8000-000000000003"
		uploadedJob = "ab000000-0000-4000-8000-000000000004"
		missingJob  = "ab000000-0000-4000-8000-000000000005"
		youngJob    = "ab000000-0000-4000-8000-000000000006"
		otherShard  = "cd000000-0000-4000-8000-000000000007"
	)
	for _, id := range []string{liveJob, expiredJob, pendingJob, uploadedJob, otherShard} {
		reapSeedRow(t, f, id, old)
	}
	live := reapClaim(t, f, liveJob, now.Add(-time.Minute), now.Add(time.Minute))
	expired := reapClaim(t, f, expiredJob, now.Add(-10*time.Minute), now.Add(-9*time.Minute))
	uploaded := reapClaim(t, f, uploadedJob, old, old.Add(time.Minute))
	if err := f.store.MarkUploaded(context.Background(), uploaded, old.Add(time.Second)); err != nil {
		t.Fatalf("MarkUploaded: %v", err)
	}

	temps := []IncomingTemp{
		{JobID: liveJob, Token: live.Token, ModTime: old},                         // the live lease: keep
		{JobID: liveJob, Token: "olderlease0000000000000000000001", ModTime: old}, // same job, earlier lease
		{JobID: expiredJob, Token: expired.Token, ModTime: old},                   // expired but not re-claimed: keep
		{JobID: pendingJob, Token: "retriedlease00000000000000000001", ModTime: old},
		{JobID: uploadedJob, Token: "firstlease0000000000000000000001", ModTime: old},
		{JobID: missingJob, Token: "expiredindex0000000000000000001", ModTime: old}, // row expired
		{JobID: youngJob, Token: "younglease0000000000000000000001", ModTime: young},
		{JobID: missingJob, Token: "notimestamp0000000000000000001"}, // no mtime: keep
		{JobID: otherShard, Token: "othershard0000000000000000000001", ModTime: old},
	}
	for _, temp := range temps {
		f.hub.putTemp(temp)
	}

	u := f.uploader(now, 0, nil)
	u.lastPutOK.Store(now.Unix())
	result, err := u.ReapIncomingShard(context.Background(), "ab")
	if err != nil {
		t.Fatalf("ReapIncomingShard: %v", err)
	}
	want := []string{
		liveJob + ".olderlease0000000000000000000001",
		missingJob + ".expiredindex0000000000000000001",
		pendingJob + ".retriedlease00000000000000000001",
		uploadedJob + ".firstlease0000000000000000000001",
	}
	sort.Strings(want)
	got := f.hub.removedKeys()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("removed temps:\n got %v\nwant %v", got, want)
	}
	if result.Listed != 8 || result.Removed != len(want) {
		t.Fatalf("result = %+v, want 8 listed in shard ab and %d removed", result, len(want))
	}
}

func TestContentBackupReapIncomingLeavesServerAloneWhenUploadsArePaused(t *testing.T) {
	f := t05NewFixture(t)
	now := time.Date(2026, 9, 17, 5, 0, 0, 0, time.UTC)
	f.hub.putTemp(IncomingTemp{JobID: "ab000000-0000-4000-8000-000000000009", Token: "abandoned000000000000000000000001", ModTime: now.Add(-72 * time.Hour)})
	f.cfg.UploadPaused = true
	u := f.uploader(now, 0, nil)
	u.lastPutOK.Store(now.Unix())
	result, err := u.ReapIncomingShard(context.Background(), "ab")
	if err != nil || result.Listed != 0 || len(f.hub.removedKeys()) != 0 {
		t.Fatalf("paused reap = %+v, %v, removed %v; want the server untouched", result, err, f.hub.removedKeys())
	}
	if created, _, _ := f.hub.counts(); created != 0 {
		t.Fatalf("a paused reap opened %d remote stores, want 0", created)
	}
}

func TestContentBackupReapIncomingReturnsListingError(t *testing.T) {
	f := t05NewFixture(t)
	now := time.Date(2026, 9, 17, 5, 0, 0, 0, time.UTC)
	f.hub.putTemp(IncomingTemp{JobID: "ab000000-0000-4000-8000-000000000009", Token: "abandoned000000000000000000000001", ModTime: now.Add(-72 * time.Hour)})
	f.hub.mu.Lock()
	f.hub.listErr = ErrFTPSNetwork
	f.hub.mu.Unlock()
	u := f.uploader(now, 0, nil)
	u.lastPutOK.Store(now.Unix())
	if _, err := u.ReapIncomingShard(context.Background(), "ab"); !errors.Is(err, ErrFTPSNetwork) {
		t.Fatalf("ReapIncomingShard error = %v, want the listing error", err)
	}
	if removed := f.hub.removedKeys(); len(removed) != 0 {
		t.Fatalf("removed %v after a failed listing", removed)
	}
}

// The reaper only talks to a server that is taking uploads right now: with no verified
// transfer in the last ten minutes it neither lists nor opens a remote store.
func TestContentBackupReapIncomingWaitsForARecentUpload(t *testing.T) {
	f := t05NewFixture(t)
	now := time.Date(2026, 9, 17, 5, 0, 0, 0, time.UTC)
	f.hub.putTemp(IncomingTemp{JobID: "ab000000-0000-4000-8000-000000000009", Token: "abandoned000000000000000000000001", ModTime: now.Add(-72 * time.Hour)})
	u := f.uploader(now, 0, nil)

	for _, last := range []time.Time{{}, now.Add(-11 * time.Minute)} {
		if !last.IsZero() {
			u.lastPutOK.Store(last.Unix())
		}
		result, err := u.ReapIncomingShard(context.Background(), "ab")
		if err != nil || result.Listed != 0 {
			t.Fatalf("reap with last upload %v = %+v, %v; want it skipped", last, result, err)
		}
	}
	if created, _, _ := f.hub.counts(); created != 0 {
		t.Fatalf("an idle reaper opened %d remote stores, want 0", created)
	}

	u.lastPutOK.Store(now.Add(-time.Minute).Unix())
	if result, err := u.ReapIncomingShard(context.Background(), "ab"); err != nil || result.Removed != 1 {
		t.Fatalf("reap after a recent upload = %+v, %v; want the orphan removed", result, err)
	}
}

// One temp that the server refuses to delete must not keep the others in its shard forever.
func TestContentBackupReapIncomingGetsPastATempItCannotDelete(t *testing.T) {
	f := t05NewFixture(t)
	now := time.Date(2026, 9, 17, 5, 0, 0, 0, time.UTC)
	// Listed first, so without a shuffle every pass would stop on it.
	stuck := IncomingTemp{JobID: "ab000000-0000-4000-8000-000000000000", Token: "undeletable0000000000000000000001", ModTime: now.Add(-72 * time.Hour)}
	f.hub.putTemp(stuck)
	f.hub.failRemove = stuck.JobID + "." + stuck.Token
	for i := 1; i <= 6; i++ {
		f.hub.putTemp(IncomingTemp{
			JobID:   fmt.Sprintf("ab000000-0000-4000-8000-%012d", i),
			Token:   "abandoned000000000000000000000001",
			ModTime: now.Add(-72 * time.Hour),
		})
	}
	u := f.uploader(now, 0, nil)
	u.lastPutOK.Store(now.Unix())
	for pass := 0; pass < 64 && len(f.hub.removedKeys()) < 6; pass++ {
		_, _ = u.ReapIncomingShard(context.Background(), "ab")
	}
	if removed := f.hub.removedKeys(); len(removed) != 6 {
		t.Fatalf("removed %d of the 6 deletable temps, want all of them: %v", len(removed), removed)
	}
}

// The reaper is only worth something if Start runs it: Renew and the spool lock both sat
// fully written and tested with no caller. A running uploader verifies one real upload and
// then reaps the orphan next to it.
func TestContentBackupUploaderStartRunsIncomingReaper(t *testing.T) {
	f := t05NewFixture(t)
	now := time.Date(2026, 9, 17, 5, 0, 0, 0, time.UTC)
	job := f.seedJob(t, "uploaded-before-reaping")
	orphan := IncomingTemp{JobID: "ab000000-0000-4000-8000-00000000000a", Token: "abandoned000000000000000000000001", ModTime: now.Add(-72 * time.Hour)}
	f.hub.putTemp(orphan)
	u := f.uploader(now, 0, func(cfg *UploadConfig) { cfg.IncomingReapEvery = time.Millisecond })
	ctx, cancel := context.WithCancel(context.Background())
	u.Start(ctx)
	defer func() {
		cancel()
		u.Wait()
	}()
	waitFor(t, "the running uploader to reap the orphaned temp", func() bool {
		return len(f.hub.removedKeys()) == 1
	})
	if got := f.job(t, job.JobID).Status; got != model.ContentBackupStatusUploaded {
		t.Fatalf("seeded job status = %q, want uploaded before the reaper ran", got)
	}
}
