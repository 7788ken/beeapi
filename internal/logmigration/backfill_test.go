package logmigration

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openTestLogDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.AutoMigrate(&LogRow{}); err != nil {
		t.Fatalf("migrate test logs: %v", err)
	}
	return db
}

func TestBackfillResumesAndReconciles(t *testing.T) {
	ctx := context.Background()
	source := openTestLogDB(t, "source")
	target := openTestLogDB(t, "target")
	sourceRows := []LogRow{
		{
			ID:           1,
			UserID:       11,
			CreatedAt:    86401,
			Type:         2,
			Quota:        100,
			PromptTokens: 10,
			Other:        `{"cache_tokens":3,"cache_creation_tokens":4}`,
		},
		{
			ID:               2,
			UserID:           12,
			CreatedAt:        86401,
			Type:             6,
			Quota:            -20,
			CompletionTokens: 7,
			RequestID:        "req-2",
		},
		{
			ID:               3,
			UserID:           13,
			CreatedAt:        172802,
			Type:             2,
			Quota:            250,
			PromptTokens:     20,
			CompletionTokens: 8,
			RequestID:        "req-3",
			Other:            `{"cache_tokens":5}`,
		},
	}
	if err := source.Create(&sourceRows).Error; err != nil {
		t.Fatalf("seed source: %v", err)
	}

	highWater, err := CaptureHighWater(ctx, source)
	if err != nil {
		t.Fatalf("capture high-water: %v", err)
	}
	state := State{
		Version:   StateVersion,
		HighWater: highWater,
	}
	var commits []State
	state, err = Backfill(ctx, source, target, state, BackfillOptions{
		BatchSize: 1,
		OnCommit: func(next State) error {
			commits = append(commits, next)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if !state.Complete() {
		t.Fatal("backfill state is incomplete")
	}
	if state.RowsCopied != int64(len(sourceRows)) {
		t.Fatalf("rows copied = %d, want %d", state.RowsCopied, len(sourceRows))
	}
	if len(commits) != len(sourceRows) {
		t.Fatalf("commit checkpoints = %d, want %d", len(commits), len(sourceRows))
	}

	var migrated []LogRow
	if err := target.Order("created_at, id").Find(&migrated).Error; err != nil {
		t.Fatalf("load target: %v", err)
	}
	if len(migrated) != len(sourceRows) {
		t.Fatalf("target rows = %d, want %d", len(migrated), len(sourceRows))
	}
	if migrated[0].EventID != "legacy-00000000000000086401-00000000000000000001" {
		t.Fatalf("unexpected legacy event ID %q", migrated[0].EventID)
	}
	if migrated[0].RequestID != "legacy-request-legacy-00000000000000086401-00000000000000000001" {
		t.Fatalf("unexpected legacy request ID %q", migrated[0].RequestID)
	}

	repeated, err := Backfill(ctx, source, target, state, BackfillOptions{BatchSize: 2})
	if err != nil {
		t.Fatalf("repeat backfill: %v", err)
	}
	if repeated.RowsCopied != state.RowsCopied {
		t.Fatalf("repeat changed rows copied: got %d want %d", repeated.RowsCopied, state.RowsCopied)
	}
	var targetCount int64
	if err := target.Model(&LogRow{}).Count(&targetCount).Error; err != nil {
		t.Fatalf("count target: %v", err)
	}
	if targetCount != int64(len(sourceRows)) {
		t.Fatalf("target rows after repeat = %d, want %d", targetCount, len(sourceRows))
	}

	daily, err := Reconcile(ctx, source, target, highWater)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(daily) != 2 {
		t.Fatalf("daily buckets = %d, want 2", len(daily))
	}
	if daily[0].Rows != 2 || daily[0].ConsumeRows != 1 || daily[0].ErrorRows != 0 ||
		daily[0].RefundRows != 1 || daily[0].Quota != 80 || daily[0].RefundQuota != -20 ||
		daily[0].PromptTokens != 10 || daily[0].CompletionTokens != 7 ||
		daily[0].CacheReadTokens != 3 || daily[0].CacheCreationToken != 4 ||
		daily[0].DistinctRequestIDs != 2 {
		t.Fatalf("unexpected first-day reconciliation: %+v", daily[0])
	}
}

func TestBackfillPreservesDuplicateSourceRequestIDs(t *testing.T) {
	ctx := context.Background()
	source := openTestLogDB(t, "source")
	target := openTestLogDB(t, "target")
	rows := []LogRow{
		{ID: 1, CreatedAt: 10, RequestID: "duplicate"},
		{ID: 2, CreatedAt: 11, RequestID: "duplicate"},
	}
	if err := source.Create(&rows).Error; err != nil {
		t.Fatalf("seed source: %v", err)
	}
	state, err := Backfill(ctx, source, target, State{
		Version:   StateVersion,
		HighWater: Cursor{CreatedAt: 11, ID: 2},
	}, BackfillOptions{BatchSize: 10})
	if err != nil {
		t.Fatalf("backfill duplicate request IDs: %v", err)
	}
	if !state.Complete() {
		t.Fatal("backfill state is incomplete")
	}
	var migrated []LogRow
	if err := target.Order("event_id").Find(&migrated).Error; err != nil {
		t.Fatalf("load target: %v", err)
	}
	if len(migrated) != 2 || migrated[0].RequestID != "duplicate" || migrated[1].RequestID != "duplicate" {
		t.Fatalf("duplicate request IDs were not preserved: %+v", migrated)
	}
	if migrated[0].EventID == migrated[1].EventID {
		t.Fatalf("rows share event ID %q", migrated[0].EventID)
	}
}

func TestBackfillUsesSourceIDAcrossClockSkew(t *testing.T) {
	ctx := context.Background()
	source := openTestLogDB(t, "source")
	target := openTestLogDB(t, "target")
	rows := []LogRow{
		{ID: 1, CreatedAt: 200, RequestID: "first"},
		{ID: 2, CreatedAt: 100, RequestID: "second"},
	}
	if err := source.Create(&rows).Error; err != nil {
		t.Fatalf("seed source: %v", err)
	}
	highWater, err := CaptureHighWater(ctx, source)
	if err != nil {
		t.Fatalf("capture high-water: %v", err)
	}
	if highWater.ID != 2 || highWater.CreatedAt != 100 {
		t.Fatalf("unexpected high-water: %+v", highWater)
	}
	state, err := Backfill(ctx, source, target, State{
		Version:   StateVersion,
		HighWater: highWater,
	}, BackfillOptions{BatchSize: 1})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if !state.Complete() || state.RowsCopied != 2 {
		t.Fatalf("clock-skewed row was missed: %+v", state)
	}
	var count int64
	if err := target.Model(&LogRow{}).Count(&count).Error; err != nil {
		t.Fatalf("count target: %v", err)
	}
	if count != 2 {
		t.Fatalf("target rows = %d, want 2", count)
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	want := State{
		Version:               StateVersion,
		ConnectionFingerprint: "fingerprint",
		HighWater:             Cursor{CreatedAt: 100, ID: 20},
		Cursor:                Cursor{CreatedAt: 90, ID: 10},
		RowsCopied:            42,
	}
	if err := SaveState(path, want); err != nil {
		t.Fatalf("save state: %v", err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if got != want {
		t.Fatalf("state mismatch: got %+v want %+v", got, want)
	}
}

func TestConnectionFingerprintExcludesPasswords(t *testing.T) {
	urlWithFirstPassword := ConnectionFingerprint(
		"postgres://operator:first@db.example/logs",
		"clickhouse://writer:first@clickhouse.example/default",
	)
	urlWithSecondPassword := ConnectionFingerprint(
		"postgres://operator:second@db.example/logs",
		"clickhouse://writer:second@clickhouse.example/default",
	)
	if urlWithFirstPassword != urlWithSecondPassword {
		t.Fatal("URL password changed connection identity")
	}
	queryWithFirstSecret := ConnectionFingerprint("https://clickhouse.example/default?password=first")
	queryWithSecondSecret := ConnectionFingerprint("https://clickhouse.example/default?password=second")
	if queryWithFirstSecret != queryWithSecondSecret {
		t.Fatal("query-string password changed connection identity")
	}

	mysqlWithFirstPassword := ConnectionFingerprint("operator:first@tcp(db.example:3306)/logs")
	mysqlWithSecondPassword := ConnectionFingerprint("operator:second@tcp(db.example:3306)/logs")
	if mysqlWithFirstPassword != mysqlWithSecondPassword {
		t.Fatal("MySQL password changed connection identity")
	}
	if mysqlWithFirstPassword == ConnectionFingerprint("other:first@tcp(db.example:3306)/logs") {
		t.Fatal("different database user produced same connection identity")
	}
}
