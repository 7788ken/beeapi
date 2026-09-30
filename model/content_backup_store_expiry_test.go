package model

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
)

func contentBackupSeedJob(t *testing.T, db *gorm.DB, siteID, nodeID, status, cleanup string, uploadedAt time.Time) string {
	t.Helper()
	jobID := fmt.Sprintf("00000000-0000-4000-8000-%012d", time.Now().UnixNano()%1_000_000_000_000)
	job := contentBackupTestJob(siteID, jobID, nodeID, contentBackupTestNow.AddDate(0, 0, -60))
	job.Status = status
	job.CleanupState = cleanup
	if !uploadedAt.IsZero() {
		job.UploadedAt = uploadedAt.Unix()
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed %s/%s job: %v", status, cleanup, err)
	}
	time.Sleep(time.Microsecond) // 保证 job_id 各不相同
	return jobID
}

func contentBackupJobExists(t *testing.T, db *gorm.DB, siteID, jobID string) bool {
	t.Helper()
	var n int64
	if err := db.Model(&ContentBackupJob{}).Where("site_id = ? AND job_id = ?", siteID, jobID).Count(&n).Error; err != nil {
		t.Fatalf("count job %s: %v", jobID, err)
	}
	return n == 1
}

// 过期只能碰"已上传、本地已清理、且早于保留期、属于本节点"的终态行。其余任何一类被删，
// 都等于把还没备份完或别人的记录抹掉。
func TestContentBackupExpireUploadedIndexOnlyTouchesExpiredTerminalRows(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		store := NewContentBackupStore(db, "site-a")
		cutoff := contentBackupTestNow.AddDate(0, 0, -30)
		old := cutoff.Add(-time.Hour)
		fresh := cutoff.Add(time.Hour)

		expired := contentBackupSeedJob(t, db, "site-a", "node-a", ContentBackupStatusUploaded, ContentBackupCleanupDone, old)
		keep := map[string]string{
			"recent uploaded":          contentBackupSeedJob(t, db, "site-a", "node-a", ContentBackupStatusUploaded, ContentBackupCleanupDone, fresh),
			"uploaded not yet cleaned": contentBackupSeedJob(t, db, "site-a", "node-a", ContentBackupStatusUploaded, ContentBackupCleanupPending, old),
			"pending":                  contentBackupSeedJob(t, db, "site-a", "node-a", ContentBackupStatusPending, ContentBackupCleanupNotApplicable, time.Time{}),
			"failed":                   contentBackupSeedJob(t, db, "site-a", "node-a", ContentBackupStatusFailed, ContentBackupCleanupNotApplicable, time.Time{}),
			"other node":               contentBackupSeedJob(t, db, "site-a", "node-b", ContentBackupStatusUploaded, ContentBackupCleanupDone, old),
		}
		otherSite := contentBackupSeedJob(t, db, "site-b", "node-a", ContentBackupStatusUploaded, ContentBackupCleanupDone, old)

		n, err := store.ExpireUploadedIndex(context.Background(), "node-a", cutoff, 100)
		if err != nil {
			t.Fatalf("ExpireUploadedIndex: %v", err)
		}
		if n != 1 {
			t.Fatalf("removed %d rows, want exactly the one expired terminal row", n)
		}
		if contentBackupJobExists(t, db, "site-a", expired) {
			t.Fatal("the expired uploaded+cleaned row must be removed")
		}
		for name, jobID := range keep {
			if !contentBackupJobExists(t, db, "site-a", jobID) {
				t.Fatalf("%s row must never expire by age", name)
			}
		}
		if !contentBackupJobExists(t, db, "site-b", otherSite) {
			t.Fatal("another site's row must not be touched")
		}
		if n, err := store.ExpireUploadedIndex(context.Background(), "", cutoff, 100); err != nil || n != 0 {
			t.Fatalf("an empty node id must delete nothing, got %d, %v", n, err)
		}
	})
}

// 一轮最多删 limit 行，先删最旧的；剩下的下一轮再删，不会一次性扫穿整张表。
func TestContentBackupExpireUploadedIndexIsBoundedOldestFirst(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		store := NewContentBackupStore(db, "site-a")
		cutoff := contentBackupTestNow.AddDate(0, 0, -30)
		oldest := contentBackupSeedJob(t, db, "site-a", "node-a", ContentBackupStatusUploaded, ContentBackupCleanupDone, cutoff.Add(-3*time.Hour))
		middle := contentBackupSeedJob(t, db, "site-a", "node-a", ContentBackupStatusUploaded, ContentBackupCleanupDone, cutoff.Add(-2*time.Hour))
		newest := contentBackupSeedJob(t, db, "site-a", "node-a", ContentBackupStatusUploaded, ContentBackupCleanupDone, cutoff.Add(-time.Hour))

		n, err := store.ExpireUploadedIndex(context.Background(), "node-a", cutoff, 2)
		if err != nil || n != 2 {
			t.Fatalf("first pass removed %d (%v), want 2", n, err)
		}
		if contentBackupJobExists(t, db, "site-a", oldest) || contentBackupJobExists(t, db, "site-a", middle) {
			t.Fatal("the two oldest rows must go first")
		}
		if !contentBackupJobExists(t, db, "site-a", newest) {
			t.Fatal("the third row must wait for the next pass")
		}
		if n, err := store.ExpireUploadedIndex(context.Background(), "node-a", cutoff, 2); err != nil || n != 1 {
			t.Fatalf("second pass removed %d (%v), want 1", n, err)
		}
	})
}

// 统计只数工作队列和待清理集合；已上传归档再多，也不能改变这几个数。
func TestContentBackupCountJobsIgnoresUploadedArchive(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		store := NewContentBackupStore(db, "site-a")
		old := contentBackupTestNow.AddDate(0, 0, -1)
		for i := 0; i < 5; i++ {
			contentBackupSeedJob(t, db, "site-a", "node-a", ContentBackupStatusUploaded, ContentBackupCleanupDone, old)
		}
		contentBackupSeedJob(t, db, "site-a", "node-a", ContentBackupStatusPending, ContentBackupCleanupNotApplicable, time.Time{})
		contentBackupSeedJob(t, db, "site-a", "node-a", ContentBackupStatusPending, ContentBackupCleanupNotApplicable, time.Time{})
		contentBackupSeedJob(t, db, "site-a", "node-a", ContentBackupStatusProcessing, ContentBackupCleanupNotApplicable, time.Time{})
		contentBackupSeedJob(t, db, "site-a", "node-a", ContentBackupStatusFailed, ContentBackupCleanupNotApplicable, time.Time{})
		contentBackupSeedJob(t, db, "site-a", "node-a", ContentBackupStatusUploaded, ContentBackupCleanupPending, old)
		contentBackupSeedJob(t, db, "site-a", "node-b", ContentBackupStatusPending, ContentBackupCleanupNotApplicable, time.Time{})

		node, err := store.CountJobs(context.Background(), "node-a")
		if err != nil {
			t.Fatalf("CountJobs(node-a): %v", err)
		}
		if node.PendingCount != 2 || node.ProcessingCount != 1 || node.FailedCount != 1 ||
			node.CleanupPendingCount != 1 || node.CleanupPendingBytes != 2048 {
			t.Fatalf("node-a counts = %+v", node)
		}
		if node.OldestPendingAt != contentBackupTestNow.AddDate(0, 0, -60).Unix() {
			t.Fatalf("oldest pending = %d", node.OldestPendingAt)
		}
		site, err := store.CountJobs(context.Background(), "")
		if err != nil {
			t.Fatalf("CountJobs(site): %v", err)
		}
		if site.PendingCount != 3 {
			t.Fatalf("site-wide pending = %d, want 3 across both nodes", site.PendingCount)
		}
	})
}
