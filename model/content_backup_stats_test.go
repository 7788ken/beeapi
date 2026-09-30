package model

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestContentBackupDailyStatsAccumulatePerNodeAndDay(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		base := contentBackupTestNow

		upload := func(jobID, node string, at time.Time, bytes int64) {
			job := contentBackupTestJob("site-a", jobID, node, at)
			job.CompressedBytes = bytes
			if err := store.EnsurePending(ctx, job); err != nil {
				t.Fatalf("EnsurePending %s: %v", jobID, err)
			}
			lease, ok, err := store.Claim(ctx, jobID, node, "owner-a", at, at.Add(180*time.Second))
			if err != nil || !ok {
				t.Fatalf("Claim %s = (%v, %v)", jobID, ok, err)
			}
			if err := store.MarkUploaded(ctx, lease, at.Add(time.Second)); err != nil {
				t.Fatalf("MarkUploaded %s: %v", jobID, err)
			}
		}

		upload("dddddddd-0000-4000-8000-000000000001", "node-a", base, 100)
		upload("dddddddd-0000-4000-8000-000000000002", "node-a", base.Add(time.Minute), 250)
		upload("dddddddd-0000-4000-8000-000000000003", "node-b", base.Add(2*time.Minute), 400)
		upload("dddddddd-0000-4000-8000-000000000004", "node-a", base.Add(25*time.Hour), 800)

		if _, err := store.GetDailyStats(ctx, "2026-09-15", ""); err == nil {
			t.Fatal("uploads must reach the daily stats only through the fold")
		}
		// A small batch makes node-a need two folds and proves node-b's delta is untouched.
		if n, err := store.FoldStatDeltas(ctx, "node-a", 2, base); err != nil || n != 2 {
			t.Fatalf("first node-a fold = (%d, %v), want 2 folded", n, err)
		}
		if n := contentBackupStatDeltaCount(t, db, "site-a", "node-b"); n != 1 {
			t.Fatalf("node-b deltas after folding node-a = %d, want 1", n)
		}
		contentBackupFold(t, store, "node-a", base)
		contentBackupFold(t, store, "node-b", base)
		if n := contentBackupFold(t, store, "node-a", base); n != 0 {
			t.Fatalf("a drained node folded %d more deltas", n)
		}

		first := contentBackupDailyStat(t, db, "site-a", "2026-09-15", "node-a")
		if first.UploadedCount != 2 || first.UploadedBytes != 350 {
			t.Fatalf("node-a day one = %+v, want count 2 bytes 350", first)
		}
		second := contentBackupDailyStat(t, db, "site-a", "2026-09-15", "node-b")
		if second.UploadedCount != 1 || second.UploadedBytes != 400 {
			t.Fatalf("node-b day one = %+v, want count 1 bytes 400", second)
		}
		third := contentBackupDailyStat(t, db, "site-a", "2026-09-16", "node-a")
		if third.UploadedCount != 1 || third.UploadedBytes != 800 {
			t.Fatalf("node-a day two = %+v, want count 1 bytes 800", third)
		}

		stats, err := store.ListDailyStats(ctx, "2026-09-15", "2026-09-16", 50)
		if err != nil {
			t.Fatalf("ListDailyStats: %v", err)
		}
		if len(stats) != 3 {
			t.Fatalf("ListDailyStats returned %d rows, want 3", len(stats))
		}
		today, err := store.GetDailyStats(ctx, "2026-09-15", "")
		if err != nil {
			t.Fatalf("GetDailyStats site wide: %v", err)
		}
		if today.UploadedCount != 3 || today.UploadedBytes != 750 {
			t.Fatalf("site wide day one = %+v, want count 3 bytes 750", today)
		}

		pruned, err := store.PruneDailyStats(ctx, "2026-09-16", 10)
		if err != nil {
			t.Fatalf("PruneDailyStats: %v", err)
		}
		if pruned != 2 {
			t.Fatalf("pruned = %d, want 2", pruned)
		}
		if _, err := store.GetDailyStats(ctx, "2026-09-16", "node-a"); err != nil {
			t.Fatalf("the newest day must survive a prune bounded by stat_date < before: %v", err)
		}
		if _, err := store.GetDailyStats(ctx, "2026-09-15", "node-a"); err == nil {
			t.Fatal("the pruned day must be gone")
		}
	})
}

// TestContentBackupStatDayUsesBeijingDayBoundary 钉住 stat_date 的时区口径。
// 上面那个累加测试的基准时刻 04:34:56Z 在北京是同日 12:34，对时区不敏感，
// 所以期望值在这里全部写成字面量，避免用被测函数自己推导而变成同义反复。
func TestContentBackupStatDayUsesBeijingDayBoundary(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{"北京午夜前一秒仍属当天", time.Date(2026, 9, 15, 15, 59, 59, 0, time.UTC), "2026-09-15"},
		{"北京午夜整点进入次日", time.Date(2026, 9, 15, 16, 0, 0, 0, time.UTC), "2026-09-16"},
		{"北京次日凌晨", time.Date(2026, 9, 15, 20, 30, 0, 0, time.UTC), "2026-09-16"},
		{"跨年边界", time.Date(2026, 12, 31, 16, 30, 0, 0, time.UTC), "2027-01-01"},
		{"跨年边界前一秒", time.Date(2026, 12, 31, 15, 59, 59, 0, time.UTC), "2026-12-31"},
		{"UTC 与北京同日的普通时刻", time.Date(2026, 9, 15, 4, 34, 56, 0, time.UTC), "2026-09-15"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ContentBackupStatDay(test.at.Unix()); got != test.want {
				t.Fatalf("ContentBackupStatDay(%s) = %q, want %q", test.at.Format(time.RFC3339), got, test.want)
			}
		})
	}

	// 真正区分北京口径与 UTC 口径的断言：同一时刻两者必须不同。
	crossing := time.Date(2026, 9, 15, 20, 30, 0, 0, time.UTC)
	if got, utc := ContentBackupStatDay(crossing.Unix()), crossing.UTC().Format("2006-01-02"); got == utc {
		t.Fatalf("stat_date %q must not equal the UTC day %q, otherwise the day key is not Beijing-aligned", got, utc)
	}

	// 日键必须与 remote_path 的目录日期同源，否则客查会与统计日错开 8 小时。
	if got := ContentBackupStatDay(crossing.Unix()); got != "2026-09-16" {
		t.Fatalf("stat_date must match the remote directory date, got %q", got)
	}

	if got := ContentBackupStatDay(0); got != "" {
		t.Fatalf("a non-positive timestamp has no day key, got %q", got)
	}
	if got := ContentBackupStatDay(-1); got != "" {
		t.Fatalf("a negative timestamp has no day key, got %q", got)
	}
}

// Two slots fold the same node at once during a blue-green switch. Whatever interleaving
// the database picks, every upload lands in the daily row exactly once.
func TestContentBackupFoldStatDeltasCountsEachUploadOnceUnderConcurrentFolds(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		const uploads = 120
		at := contentBackupTestNow
		for i := 0; i < uploads; i++ {
			jobID := fmt.Sprintf("eeeeeeee-0000-4000-8000-%012d", i)
			job := contentBackupTestJob("site-a", jobID, "node-a", at)
			job.CompressedBytes = 10
			if err := store.EnsurePending(ctx, job); err != nil {
				t.Fatalf("EnsurePending: %v", err)
			}
			lease, ok, err := store.Claim(ctx, jobID, "node-a", "owner-a", at, at.Add(time.Minute))
			if err != nil || !ok {
				t.Fatalf("Claim = (%v, %v)", ok, err)
			}
			if err := store.MarkUploaded(ctx, lease, at.Add(time.Second)); err != nil {
				t.Fatalf("MarkUploaded: %v", err)
			}
		}

		var wg sync.WaitGroup
		var folded atomic.Int64
		for slot := 0; slot < 6; slot++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				slotStore := NewContentBackupStore(db, "site-a")
				for idle := 0; idle < 3; {
					n, err := slotStore.FoldStatDeltas(ctx, "node-a", 7, at)
					if err != nil {
						t.Errorf("FoldStatDeltas: %v", err)
						return
					}
					folded.Add(int64(n))
					if n == 0 {
						idle++
					}
				}
			}()
		}
		wg.Wait()
		for contentBackupFold(t, store, "node-a", at) > 0 {
		}

		stat := contentBackupDailyStat(t, db, "site-a", "2026-09-15", "node-a")
		if stat.UploadedCount != uploads || stat.UploadedBytes != uploads*10 {
			t.Fatalf("daily row = %d uploads / %d bytes, want %d / %d", stat.UploadedCount, stat.UploadedBytes, uploads, uploads*10)
		}
		if n := contentBackupStatDeltaCount(t, db, "site-a", "node-a"); n != 0 {
			t.Fatalf("%d deltas left after folding", n)
		}
	})
}
