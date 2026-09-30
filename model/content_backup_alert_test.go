package model

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestContentBackupAlertDedupLeaseAndRecovery(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		base := contentBackupTestNow
		dedup := 30 * time.Minute

		alert := ContentBackupAlert{StorageNodeID: "node-a", Reason: ContentBackupAlertOldestPending, Message: "backlog 20m"}
		if err := store.UpsertAlert(ctx, alert, base); err != nil {
			t.Fatalf("UpsertAlert: %v", err)
		}
		alert.Message = "backlog 21m"
		if err := store.UpsertAlert(ctx, alert, base.Add(time.Minute)); err != nil {
			t.Fatalf("UpsertAlert second trigger: %v", err)
		}
		var count int64
		if err := db.Model(&ContentBackupAlert{}).Count(&count).Error; err != nil {
			t.Fatalf("count alerts: %v", err)
		}
		if count != 1 {
			t.Fatalf("alert rows = %d, want 1", count)
		}
		stored, err := store.GetAlert(ctx, "node-a", ContentBackupAlertOldestPending)
		if err != nil {
			t.Fatalf("GetAlert: %v", err)
		}
		if stored.TriggerCount != 2 {
			t.Fatalf("trigger_count = %d, want 2", stored.TriggerCount)
		}
		if stored.State != ContentBackupAlertFiring {
			t.Fatalf("state = %q, want %q", stored.State, ContentBackupAlertFiring)
		}
		if stored.SiteID != "site-a" {
			t.Fatalf("UpsertAlert trusted the caller site_id: %q", stored.SiteID)
		}

		won, err := store.ClaimAlertSend(ctx, "node-a", ContentBackupAlertOldestPending, "sender-1", base.Add(2*time.Minute), base.Add(3*time.Minute), dedup)
		if err != nil {
			t.Fatalf("ClaimAlertSend: %v", err)
		}
		if !won {
			t.Fatal("the first send claim must win")
		}
		blocked, err := store.ClaimAlertSend(ctx, "node-a", ContentBackupAlertOldestPending, "sender-2", base.Add(2*time.Minute+30*time.Second), base.Add(3*time.Minute+30*time.Second), dedup)
		if err != nil {
			t.Fatalf("ClaimAlertSend while leased: %v", err)
		}
		if blocked {
			t.Fatal("a second sender must not win while the send lease is still valid")
		}
		if err := store.MarkAlertSent(ctx, "node-a", ContentBackupAlertOldestPending, "sender-1", base.Add(2*time.Minute+45*time.Second)); err != nil {
			t.Fatalf("MarkAlertSent: %v", err)
		}
		if err := store.MarkAlertSent(ctx, "node-a", ContentBackupAlertOldestPending, "sender-9", base.Add(2*time.Minute+50*time.Second)); err == nil {
			t.Fatal("MarkAlertSent accepted a sender that does not hold the lease")
		}
		insideWindow, err := store.ClaimAlertSend(ctx, "node-a", ContentBackupAlertOldestPending, "sender-3", base.Add(10*time.Minute), base.Add(11*time.Minute), dedup)
		if err != nil {
			t.Fatalf("ClaimAlertSend inside the dedup window: %v", err)
		}
		if insideWindow {
			t.Fatal("the dedup window must suppress a repeat notification")
		}
		afterWindow, err := store.ClaimAlertSend(ctx, "node-a", ContentBackupAlertOldestPending, "sender-4", base.Add(33*time.Minute), base.Add(34*time.Minute), dedup)
		if err != nil {
			t.Fatalf("ClaimAlertSend after the dedup window: %v", err)
		}
		if !afterWindow {
			t.Fatal("a still-firing alert must be claimable once the dedup window lapses")
		}

		resolved, err := store.ResolveAlert(ctx, "node-a", ContentBackupAlertOldestPending, base.Add(40*time.Minute))
		if err != nil {
			t.Fatalf("ResolveAlert: %v", err)
		}
		if !resolved {
			t.Fatal("ResolveAlert did not flip a firing alert")
		}
		again, err := store.ResolveAlert(ctx, "node-a", ContentBackupAlertOldestPending, base.Add(41*time.Minute))
		if err != nil {
			t.Fatalf("ResolveAlert twice: %v", err)
		}
		if again {
			t.Fatal("recovery must be reported only once")
		}
		resolvedRow, err := store.GetAlert(ctx, "node-a", ContentBackupAlertOldestPending)
		if err != nil {
			t.Fatalf("GetAlert after recovery: %v", err)
		}
		if resolvedRow.State != ContentBackupAlertResolved || resolvedRow.ResolvedAt != base.Add(40*time.Minute).Unix() {
			t.Fatalf("recovered alert = %+v", resolvedRow)
		}

		alerts, err := store.ListAlerts(ctx, "node-a", 10)
		if err != nil {
			t.Fatalf("ListAlerts: %v", err)
		}
		if len(alerts) != 1 || alerts[0].Reason != ContentBackupAlertOldestPending {
			t.Fatalf("ListAlerts = %+v", alerts)
		}
		if len(strings.TrimSpace(alerts[0].Message)) == 0 {
			t.Fatal("the alert message was lost")
		}
	})
}

func TestContentBackupAlertsAreIsolatedPerNodeAndReason(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		other := NewContentBackupStore(db, "site-b")
		base := contentBackupTestNow

		if err := store.UpsertAlert(ctx, ContentBackupAlert{StorageNodeID: "node-a", Reason: ContentBackupAlertSpoolHigh}, base); err != nil {
			t.Fatalf("UpsertAlert node-a: %v", err)
		}
		if err := store.UpsertAlert(ctx, ContentBackupAlert{StorageNodeID: "node-b", Reason: ContentBackupAlertSpoolHigh}, base); err != nil {
			t.Fatalf("UpsertAlert node-b: %v", err)
		}
		if err := store.UpsertAlert(ctx, ContentBackupAlert{StorageNodeID: "node-a", Reason: ContentBackupAlertNodeOffline}, base); err != nil {
			t.Fatalf("UpsertAlert second reason: %v", err)
		}
		if err := other.UpsertAlert(ctx, ContentBackupAlert{StorageNodeID: "node-a", Reason: ContentBackupAlertSpoolHigh}, base); err != nil {
			t.Fatalf("UpsertAlert on site-b: %v", err)
		}

		var count int64
		if err := db.Model(&ContentBackupAlert{}).Count(&count).Error; err != nil {
			t.Fatalf("count alerts: %v", err)
		}
		if count != 4 {
			t.Fatalf("alert rows = %d, want 4 (site+node+reason is the identity)", count)
		}
		nodeA, err := store.ListAlerts(ctx, "node-a", 10)
		if err != nil {
			t.Fatalf("ListAlerts node-a: %v", err)
		}
		if len(nodeA) != 2 {
			t.Fatalf("node-a alerts = %d, want 2", len(nodeA))
		}
		siteWide, err := store.ListAlerts(ctx, "", 10)
		if err != nil {
			t.Fatalf("ListAlerts site wide: %v", err)
		}
		if len(siteWide) != 3 {
			t.Fatalf("site wide alerts = %d, want 3", len(siteWide))
		}
	})
}
