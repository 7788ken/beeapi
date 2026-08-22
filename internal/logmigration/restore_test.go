package logmigration

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRestoreClickHouseRowsIsIdempotentAndReconciles(t *testing.T) {
	ctx := context.Background()
	clickHouse := openTestLogDB(t, "clickhouse")
	relational := openTestLogDB(t, "relational")
	if err := relational.Migrator().DropTable(&LogRow{}); err != nil {
		t.Fatalf("drop relational test logs: %v", err)
	}
	if err := relational.AutoMigrate(&relationalLogRow{}); err != nil {
		t.Fatalf("migrate relational logs: %v", err)
	}
	if err := EnsureRestoreLedger(ctx, relational); err != nil {
		t.Fatalf("ensure ledger: %v", err)
	}

	rows := []LogRow{
		{
			CreatedAt:    100,
			Type:         2,
			UserID:       7,
			Quota:        50,
			PromptTokens: 3,
			RequestID:    "same-request",
			EventID:      "event-a",
			Other:        `{"cache_tokens":2}`,
		},
		{
			CreatedAt:        100,
			Type:             5,
			UserID:           7,
			CompletionTokens: 4,
			RequestID:        "same-request",
			EventID:          "event-b",
		},
		{
			CreatedAt: 172900,
			Type:      6,
			UserID:    8,
			Quota:     -20,
			RequestID: "refund-request",
			EventID:   "event-c",
		},
		{
			ID:        99,
			CreatedAt: 200,
			Type:      2,
			RequestID: "historical-backfill",
			EventID:   "legacy-00000000000000000200-00000000000000000099",
		},
	}
	if err := clickHouse.Create(&rows).Error; err != nil {
		t.Fatalf("seed ClickHouse rows: %v", err)
	}
	highWater, err := CaptureClickHouseHighWater(ctx, clickHouse, 100)
	if err != nil {
		t.Fatalf("capture high-water: %v", err)
	}
	state := RestoreState{
		Version:   StateVersion,
		From:      100,
		HighWater: highWater,
	}
	state, err = RestoreClickHouseRows(ctx, clickHouse, relational, state, RestoreOptions{BatchSize: 1})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !state.Complete() || state.RowsRestored != 3 {
		t.Fatalf("unexpected restore state: %+v", state)
	}

	var targetCount int64
	if err := relational.Model(&relationalLogRow{}).Count(&targetCount).Error; err != nil {
		t.Fatalf("count restored logs: %v", err)
	}
	if targetCount != 3 {
		t.Fatalf("restored logs = %d, want 3", targetCount)
	}

	repeated, err := RestoreClickHouseRows(ctx, clickHouse, relational, RestoreState{
		Version:   StateVersion,
		From:      100,
		HighWater: highWater,
	}, RestoreOptions{BatchSize: 2})
	if err != nil {
		t.Fatalf("repeat restore: %v", err)
	}
	if repeated.RowsRestored != 0 {
		t.Fatalf("repeat restored %d rows", repeated.RowsRestored)
	}
	if err := relational.Model(&relationalLogRow{}).Count(&targetCount).Error; err != nil {
		t.Fatalf("count repeated restore: %v", err)
	}
	if targetCount != 3 {
		t.Fatalf("repeat produced %d logs, want 3", targetCount)
	}

	daily, err := ReconcileRestored(ctx, clickHouse, relational, 100, highWater)
	if err != nil {
		t.Fatalf("reconcile restored rows: %v", err)
	}
	if len(daily) != 2 {
		t.Fatalf("daily buckets = %d, want 2", len(daily))
	}
	if daily[0].Rows != 2 || daily[0].ConsumeRows != 1 || daily[0].ErrorRows != 1 ||
		daily[0].Quota != 50 || daily[0].PromptTokens != 3 ||
		daily[0].CompletionTokens != 4 || daily[0].CacheReadTokens != 2 ||
		daily[0].DistinctRequestIDs != 1 {
		t.Fatalf("unexpected first-day restore reconciliation: %+v", daily[0])
	}
}

func TestRestoreStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restore", "state.json")
	want := RestoreState{
		Version:               StateVersion,
		ConnectionFingerprint: "fingerprint",
		From:                  100,
		HighWater:             EventCursor{CreatedAt: 200, EventID: "event-z"},
		Cursor:                EventCursor{CreatedAt: 150, EventID: "event-m"},
		RowsRestored:          9,
	}
	if err := SaveRestoreState(path, want); err != nil {
		t.Fatalf("save restore state: %v", err)
	}
	got, err := LoadRestoreState(path)
	if err != nil {
		t.Fatalf("load restore state: %v", err)
	}
	if got != want {
		t.Fatalf("restore state mismatch: got %+v want %+v", got, want)
	}
}
