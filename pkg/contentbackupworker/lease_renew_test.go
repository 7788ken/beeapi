package contentbackupworker

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// leaseClock runs at wall-clock speed from a chosen instant and can be jumped forward: the
// transfer hangs in a hook while the test moves time past the lease, which is what a slow
// large upload looks like to the DB. It keeps ticking so a bounded renewal call times out
// in real time exactly when the lease says it should.
type leaseClock struct{ offset atomic.Int64 }

func newLeaseClock(start time.Time) *leaseClock {
	c := &leaseClock{}
	c.set(start)
	return c
}

func (c *leaseClock) now() time.Time          { return time.Now().Add(time.Duration(c.offset.Load())).UTC() }
func (c *leaseClock) set(t time.Time)         { c.offset.Store(int64(time.Until(t))) }
func (c *leaseClock) advance(d time.Duration) { c.offset.Add(int64(d)) }

func leaseRenewFixture(t *testing.T, clock *leaseClock, renewEvery time.Duration) (*t05Fixture, func(string) *Uploader) {
	t.Helper()
	f := t05NewFixture(t)
	f.cfg.LeaseSeconds = 60
	// 0 falls back to UploadConfig.LeaseRenewEvery, so the test renews in milliseconds
	// instead of the 5 second config minimum.
	f.cfg.LeaseRenewSeconds = 0
	newUploader := func(processID string) *Uploader {
		return f.uploader(time.Time{}, 0, func(cfg *UploadConfig) {
			cfg.ProcessID = processID
			cfg.Now = clock.now
			cfg.LeaseRenewEvery = renewEvery
		})
	}
	return f, newUploader
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// A transfer that outlives lease_seconds keeps its lease, so the second slot that scans
// expired leases finds nothing and the file goes to the remote exactly once.
func TestContentBackupUploadRenewsLeaseSoSlowTransferIsNotUploadedTwice(t *testing.T) {
	start := time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC)
	clock := newLeaseClock(start)
	f, newUploader := leaseRenewFixture(t, clock, 5*time.Millisecond)
	job := f.seedJob(t, "slow-large-upload")

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var puts atomic.Int32
	f.hub.setHook(func(ctx context.Context, _ model.ContentBackupJob, _ model.ContentBackupLease, src io.Reader) error {
		if puts.Add(1) > 1 {
			// A duplicate transfer must not hang the test; the assertions below catch it.
			_, err := io.Copy(io.Discard, src)
			return err
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		_, err := io.Copy(io.Discard, src)
		return err
	})

	first := newUploader(t05Process)
	done := make(chan CycleResult, 1)
	go func() {
		result, err := first.RunCycle(context.Background())
		if err != nil {
			t.Errorf("first RunCycle: %v", err)
		}
		done <- result
	}()
	<-started
	if got := f.job(t, job.JobID).LeaseUntil; got < start.Add(60*time.Second).Unix() || got > start.Add(62*time.Second).Unix() {
		t.Fatalf("claimed lease_until = %d, want the claim instant + 60s", got)
	}

	// Walk the clock 90 seconds past the claim in 10 second steps, as a real slow transfer
	// would see it; the claimed lease (start+60s) alone would have lapsed half way.
	for step := 1; step <= 9; step++ {
		clock.advance(10 * time.Second)
		wantUntil := clock.now().Add(60 * time.Second).Unix()
		waitFor(t, "lease renewal to reach the DB", func() bool {
			return f.job(t, job.JobID).LeaseUntil >= wantUntil
		})
	}

	second := newUploader("00000000-0000-4000-8000-0000000000bb")
	result, err := second.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("second RunCycle: %v", err)
	}
	if result.Claimed != 0 {
		t.Fatalf("second slot claimed %d jobs while the first still renews, want 0", result.Claimed)
	}

	close(release)
	firstResult := <-done
	if firstResult.Uploaded != 1 {
		t.Fatalf("first cycle = %+v, want the renewed transfer to commit", firstResult)
	}
	if n := puts.Load(); n != 1 {
		t.Fatalf("PutVerified ran %d times, want exactly 1", n)
	}
	if after := f.job(t, job.JobID); after.Status != model.ContentBackupStatusUploaded {
		t.Fatalf("status = %q, want uploaded", after.Status)
	}
}

// Once a renewal proves the job belongs to someone else, the transfer is cancelled with
// ErrLeaseLost as the cause and no state is written over the new owner's lease.
func TestContentBackupUploadStopsTransferWhenLeaseIsLost(t *testing.T) {
	clock := newLeaseClock(time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC))
	f, newUploader := leaseRenewFixture(t, clock, 5*time.Millisecond)
	job := f.seedJob(t, "stolen-lease")

	started := make(chan struct{})
	var cause atomic.Value
	f.hub.setHook(func(ctx context.Context, _ model.ContentBackupJob, _ model.ContentBackupLease, _ io.Reader) error {
		close(started)
		select {
		case <-ctx.Done():
			cause.Store(context.Cause(ctx))
			return ctx.Err()
		case <-time.After(3 * time.Second):
			return errors.New("transfer was never stopped")
		}
	})

	u := newUploader(t05Process)
	done := make(chan CycleResult, 1)
	go func() {
		result, _ := u.RunCycle(context.Background())
		done <- result
	}()
	<-started
	if err := f.db.Model(&model.ContentBackupJob{}).
		Where("job_id = ?", job.JobID).
		Update("lease_token", "stolen0000000000000000000000000000").Error; err != nil {
		t.Fatalf("steal lease: %v", err)
	}

	result := <-done
	if got, _ := cause.Load().(error); !errors.Is(got, model.ErrLeaseLost) {
		t.Fatalf("transfer stop cause = %v, want ErrLeaseLost", got)
	}
	if result.LeasesLost != 1 || result.Retried != 0 || result.Failed != 0 {
		t.Fatalf("cycle = %+v, want one lost lease and no state write", result)
	}
	after := f.job(t, job.JobID)
	if after.Status != model.ContentBackupStatusProcessing || after.LeaseToken != "stolen0000000000000000000000000000" {
		t.Fatalf("row = %s/%s, the new owner's lease must be left alone", after.Status, after.LeaseToken)
	}
}

// A renewal that cannot reach the DB (here: the only pooled connection is taken, like an
// exhausted gateway pool) must not let the transfer run on past the lease. The call is
// bounded by the lease it extends, and the transfer stops once the lease is spent, before
// another slot could claim the job and upload it again.
func TestContentBackupUploadStopsTransferWhenRenewalHangsPastTheLease(t *testing.T) {
	clock := newLeaseClock(time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC))
	// The first renewal fires a second into the transfer, leaving time to take the
	// connection and move the clock before it.
	f, newUploader := leaseRenewFixture(t, clock, time.Second)
	job := f.seedJob(t, "hung-renewal")

	started := make(chan struct{})
	stopped := make(chan error, 1)
	f.hub.setHook(func(ctx context.Context, _ model.ContentBackupJob, _ model.ContentBackupLease, _ io.Reader) error {
		close(started)
		select {
		case <-ctx.Done():
			stopped <- context.Cause(ctx)
			return ctx.Err()
		case <-time.After(5 * time.Second):
			stopped <- nil
			return errors.New("transfer was never stopped")
		}
	})
	u := newUploader(t05Process)
	done := make(chan CycleResult, 1)
	go func() {
		result, _ := u.RunCycle(context.Background())
		done <- result
	}()
	<-started

	tx := f.db.Begin()
	var until int64
	if err := tx.Model(&model.ContentBackupJob{}).Where("job_id = ?", job.JobID).
		Select("lease_until").Scan(&until).Error; err != nil {
		t.Fatalf("read lease_until: %v", err)
	}
	// When the first renewal fires the lease has about 300ms left, so the call starts in
	// time and then hangs on the pool; only its bound by the lease can end it.
	clock.set(time.Unix(until, 0).Add(-1300 * time.Millisecond))

	select {
	case cause := <-stopped:
		if !errors.Is(cause, model.ErrLeaseLost) {
			t.Fatalf("transfer stop cause = %v, want ErrLeaseLost", cause)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the transfer kept running past its lease while the renewal waited for the DB")
	}
	tx.Rollback()
	if result := <-done; result.LeasesLost != 1 || result.Uploaded != 0 {
		t.Fatalf("cycle = %+v, want one lost lease and no upload", result)
	}
}

// Validation accepts any renew interval shorter than the lease; one a second shorter would
// only renew after the lease had already expired.
func TestContentBackupLeaseRenewIntervalLeavesRoomForRetries(t *testing.T) {
	f := t05NewFixture(t)
	u := f.uploader(time.Now(), 0, nil)
	cfg := f.cfg
	cfg.LeaseSeconds, cfg.LeaseRenewSeconds = 180, 30
	if got := u.leaseRenewEvery(cfg); got != 30*time.Second {
		t.Fatalf("renew interval = %v, want the configured 30s", got)
	}
	cfg.LeaseSeconds, cfg.LeaseRenewSeconds = 60, 59
	if got := u.leaseRenewEvery(cfg); got != 20*time.Second {
		t.Fatalf("renew interval = %v, want it capped at a third of the 60s lease", got)
	}
}
