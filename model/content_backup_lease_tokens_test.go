package model

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
)

// The incoming reaper keeps a remote temp exactly when its job is still processing under
// that token. An expired lease still counts (only a new claim or a state write ends it);
// released leases and other sites do not; and the query must hold past one IN chunk.
func TestContentBackupCurrentLeaseTokens(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		now := contentBackupTestNow
		siteA := NewContentBackupStore(db, "site-a")
		siteB := NewContentBackupStore(db, "site-b")

		seed := func(store *ContentBackupStore, jobID string) {
			t.Helper()
			if err := store.EnsurePending(ctx, contentBackupTestJob(store.SiteID(), jobID, "node-a", now.Add(-time.Hour))); err != nil {
				t.Fatalf("EnsurePending %s: %v", jobID, err)
			}
		}
		claim := func(store *ContentBackupStore, jobID string, at, until time.Time) ContentBackupLease {
			t.Helper()
			lease, ok, err := store.Claim(ctx, jobID, "node-a", "owner-a", at, until)
			if err != nil || !ok {
				t.Fatalf("Claim %s = (%v, %v)", jobID, ok, err)
			}
			return lease
		}

		const (
			live     = "ffffffff-0000-4000-8000-000000000001"
			expired  = "ffffffff-0000-4000-8000-000000000002"
			released = "ffffffff-0000-4000-8000-000000000003"
			foreign  = "ffffffff-0000-4000-8000-000000000004"
		)
		for _, id := range []string{live, expired, released} {
			seed(siteA, id)
		}
		seed(siteB, foreign)
		liveLease := claim(siteA, live, now.Add(-time.Minute), now.Add(time.Minute))
		expiredLease := claim(siteA, expired, now.Add(-10*time.Minute), now.Add(-9*time.Minute))
		releasedLease := claim(siteA, released, now.Add(-time.Minute), now.Add(time.Minute))
		if err := siteA.ScheduleRetry(ctx, releasedLease, "network", "boom", now, now.Add(time.Minute)); err != nil {
			t.Fatalf("ScheduleRetry: %v", err)
		}
		claim(siteB, foreign, now.Add(-time.Minute), now.Add(time.Minute))

		// Past one 500-id chunk, with the live job in the second chunk.
		ids := make([]string, 0, contentBackupIDChunk+120)
		for i := 0; i < contentBackupIDChunk+100; i++ {
			ids = append(ids, fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
		}
		ids = append(ids, expired, released, foreign, live)

		got, err := siteA.CurrentLeaseTokens(ctx, ids)
		if err != nil {
			t.Fatalf("CurrentLeaseTokens: %v", err)
		}
		if len(got) != 2 || got[live] != liveLease.Token || got[expired] != expiredLease.Token {
			t.Fatalf("current leases = %v, want %s -> %s and %s -> %s", got, live, liveLease.Token, expired, expiredLease.Token)
		}
	})
}
