package model

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestContentBackupNodeStatusUpsertKeepsSingleNodeRow(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")

		snapshot := ContentBackupNodeStatus{
			SiteID:               "site-ignored",
			StorageNodeID:        "node-a",
			ProcessID:            "process-1",
			ConfigVersion:        7,
			AppliedConfigVersion: 7,
			LastSeenAt:           contentBackupTestNow.Unix(),
			SampledAt:            contentBackupTestNow.Unix(),
			SpoolBytes:           1024,
			SpoolLimitBytes:      2048,
			DiskTotalBytes:       1 << 30,
			FreeBytes:            1 << 29,
			InodeTotal:           1000,
			FreeInodes:           900,
			PendingCount:         3,
			FailedCount:          1,
			OldestPendingAt:      contentBackupTestNow.Add(-time.Minute).Unix(),
			CleanupPendingCount:  2,
			CleanupPendingBytes:  4096,
			OrphanCount:          1,
			HandoffRejectedCount: 5,
			HandoffUnknownCount:  2,
		}
		if err := store.SaveNodeStatus(ctx, snapshot); err != nil {
			t.Fatalf("SaveNodeStatus: %v", err)
		}
		// Identical values must not be mistaken for a missing row (MySQL changed rows).
		if err := store.SaveNodeStatus(ctx, snapshot); err != nil {
			t.Fatalf("SaveNodeStatus with identical values: %v", err)
		}

		snapshot.ProcessID = "process-2"
		snapshot.LastSeenAt = contentBackupTestNow.Add(15 * time.Second).Unix()
		snapshot.SpoolBytes = 2048
		snapshot.AppliedConfigVersion = 6
		if err := store.SaveNodeStatus(ctx, snapshot); err != nil {
			t.Fatalf("SaveNodeStatus heartbeat update: %v", err)
		}

		var count int64
		if err := db.Model(&ContentBackupNodeStatus{}).Where("site_id = ?", "site-a").Count(&count).Error; err != nil {
			t.Fatalf("count node status rows: %v", err)
		}
		if count != 1 {
			t.Fatalf("node status rows = %d, want 1", count)
		}

		stored, err := store.GetNodeStatus(ctx, "node-a")
		if err != nil {
			t.Fatalf("GetNodeStatus: %v", err)
		}
		if stored.SiteID != "site-a" {
			t.Fatalf("SaveNodeStatus trusted the caller site_id: %q", stored.SiteID)
		}
		if stored.ProcessID != "process-2" || stored.AppliedConfigVersion != 6 || stored.SpoolBytes != 2048 {
			t.Fatalf("heartbeat was not applied: %+v", stored)
		}
		if stored.CreatedAt == 0 || stored.CreatedAt > stored.UpdatedAt {
			t.Fatalf("created_at/updated_at = %d/%d, want a stable creation time", stored.CreatedAt, stored.UpdatedAt)
		}

		if _, err := store.GetNodeStatus(ctx, "node-missing"); err == nil {
			t.Fatal("GetNodeStatus returned a row for an unknown node")
		}
		other := NewContentBackupStore(db, "site-b")
		if _, err := other.GetNodeStatus(ctx, "node-a"); err == nil {
			t.Fatal("site-b must not read site-a node status")
		}
		all, err := store.ListNodeStatus(ctx)
		if err != nil {
			t.Fatalf("ListNodeStatus: %v", err)
		}
		if len(all) != 1 {
			t.Fatalf("ListNodeStatus returned %d rows, want 1", len(all))
		}
	})
}

func TestContentBackupCountJobsIsSiteAndNodeScoped(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		other := NewContentBackupStore(db, "site-b")
		base := contentBackupTestNow

		if err := store.EnsurePending(ctx, contentBackupTestJob("site-a", "eeeeeeee-0000-4000-8000-000000000001", "node-a", base)); err != nil {
			t.Fatalf("EnsurePending: %v", err)
		}
		if err := other.EnsurePending(ctx, contentBackupTestJob("site-b", "eeeeeeee-0000-4000-8000-000000000002", "node-a", base)); err != nil {
			t.Fatalf("EnsurePending on site-b: %v", err)
		}

		counts, err := store.CountJobs(ctx, "")
		if err != nil {
			t.Fatalf("CountJobs for the whole site: %v", err)
		}
		if counts.PendingCount != 1 {
			t.Fatalf("site wide pending = %d, want 1 (site-b must not leak in)", counts.PendingCount)
		}
		nodeCounts, err := store.CountJobs(ctx, "node-a")
		if err != nil {
			t.Fatalf("CountJobs for a node: %v", err)
		}
		if nodeCounts.PendingCount != 1 {
			t.Fatalf("node pending = %d, want 1", nodeCounts.PendingCount)
		}
		empty, err := store.CountJobs(ctx, "node-z")
		if err != nil {
			t.Fatalf("CountJobs for an unknown node: %v", err)
		}
		if empty.PendingCount != 0 || empty.CleanupPendingBytes != 0 || empty.OldestPendingAt != 0 {
			t.Fatalf("unknown node counts = %+v, want all zero", empty)
		}
	})
}

// TestContentBackupHandoffCountersSurviveDaemonHeartbeat 守的是一个跨进程所有权边界：
// 拒收与"结果未知"只有业务进程知道（socket 连不上时 daemon 根本没收到请求），
// 而节点行由 daemon 的心跳每 15 秒整行改写。只要这两列还在 daemon 的可变列里，
// 业务侧写多少都会在下一次心跳被清零，UI 和"交接拒收"告警就永远看到 0。
func TestContentBackupNodeStatusHeartbeatCarriesHandoffCounters(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		// 2026-09-18 起交接计数与心跳出自同一个业务进程：它们随心跳整行更新，不再有第二个写入者。
		if err := store.SaveNodeStatus(ctx, ContentBackupNodeStatus{
			StorageNodeID:        "node-a",
			ProcessID:            "beeapi-1",
			LastSeenAt:           contentBackupTestNow.Unix(),
			SpoolBytes:           1024,
			HandoffRejectedCount: 7,
			HandoffUnknownCount:  3,
		}); err != nil {
			t.Fatalf("SaveNodeStatus: %v", err)
		}
		if err := store.SaveNodeStatus(ctx, ContentBackupNodeStatus{
			StorageNodeID:        "node-a",
			ProcessID:            "beeapi-1",
			LastSeenAt:           contentBackupTestNow.Add(15 * time.Second).Unix(),
			SpoolBytes:           2048,
			HandoffRejectedCount: 9,
			HandoffUnknownCount:  3,
		}); err != nil {
			t.Fatalf("SaveNodeStatus heartbeat: %v", err)
		}
		status, err := store.GetNodeStatus(ctx, "node-a")
		if err != nil {
			t.Fatalf("GetNodeStatus: %v", err)
		}
		if status.HandoffRejectedCount != 9 || status.HandoffUnknownCount != 3 || status.SpoolBytes != 2048 {
			t.Fatalf("heartbeat must update the handoff counters with the rest of the row: %+v", status)
		}
	})
}
