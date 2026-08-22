package logmigration

import (
	"context"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// The ClickHouse cache token push-down must reproduce the Go reference exactly.
// Unit tests cannot cover this: JSONType/JSONExtractInt only exist on a real
// server. Set LOG_MIGRATION_CLICKHOUSE_TEST_DSN to a scratch database; the test
// drops and recreates the logs table, so the DSN database name must contain
// "test" or "it".
func openClickHouseTestTarget(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LOG_MIGRATION_CLICKHOUSE_TEST_DSN")
	if dsn == "" {
		t.Skip("LOG_MIGRATION_CLICKHOUSE_TEST_DSN not set")
	}
	database := dsn
	if separator := strings.LastIndex(database, "/"); separator >= 0 {
		database = database[separator+1:]
	}
	if cut := strings.Index(database, "?"); cut >= 0 {
		database = database[:cut]
	}
	lower := strings.ToLower(database)
	if !strings.Contains(lower, "test") && !strings.Contains(lower, "it") {
		t.Fatalf("refusing to run destructive test against database %q", database)
	}
	db, err := OpenClickHouse(dsn)
	if err != nil {
		t.Fatalf("open ClickHouse target: %v", err)
	}
	return db
}

func resetClickHouseLogTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	if err := db.WithContext(ctx).Exec("DROP TABLE IF EXISTS logs").Error; err != nil {
		t.Fatalf("drop ClickHouse logs: %v", err)
	}
	if err := EnsureClickHouseLogSchema(ctx, db, 0); err != nil {
		t.Fatalf("create ClickHouse logs: %v", err)
	}
}

func TestCacheTokenPushDownMatchesGoOnClickHouse(t *testing.T) {
	db := openClickHouseTestTarget(t)
	resetClickHouseLogTable(t, db)

	ctx := context.Background()
	rows := make([]LogRow, 0, len(cacheTokenBoundaryInputs))
	for index, payload := range cacheTokenBoundaryInputs {
		rows = append(rows, LogRow{
			ID:        int64(index + 1),
			CreatedAt: 86400,
			Type:      2,
			RequestID: "req-boundary",
			// Aggregate(source=false) only counts backfilled rows.
			EventID: legacyEventID(86400, int64(index+1)),
			Other:   payload,
		})
	}
	if err := db.WithContext(ctx).Table(clickHouseLogTable).
		CreateInBatches(&rows, len(rows)).Error; err != nil {
		t.Fatalf("seed ClickHouse boundary rows: %v", err)
	}

	highWater := Cursor{CreatedAt: 86400, ID: int64(len(cacheTokenBoundaryInputs))}
	summaries, err := Aggregate(ctx, db, highWater, false)
	if err != nil {
		t.Fatalf("aggregate ClickHouse boundary rows: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("daily buckets = %d, want 1", len(summaries))
	}
	wantRead, wantCreation := goCacheTotals(cacheTokenBoundaryInputs)
	if summaries[0].CacheReadTokens != wantRead {
		t.Fatalf("ClickHouse cache read = %d, Go reference = %d",
			summaries[0].CacheReadTokens, wantRead)
	}
	if summaries[0].CacheCreationToken != wantCreation {
		t.Fatalf("ClickHouse cache creation = %d, Go reference = %d",
			summaries[0].CacheCreationToken, wantCreation)
	}
}

// int64-max survives the round trip: ClickHouse types it as Int64 (UInt64 is
// reserved for values beyond the signed range, where JSONExtractInt returns 0),
// so the total must match Go and MySQL.
func TestCacheTokenPushDownKeepsLargeIntegersOnClickHouse(t *testing.T) {
	db := openClickHouseTestTarget(t)
	resetClickHouseLogTable(t, db)

	ctx := context.Background()
	rows := []LogRow{{
		ID:        1,
		CreatedAt: 86400,
		Type:      2,
		RequestID: "req-max",
		EventID:   legacyEventID(86400, 1),
		Other:     `{"cache_tokens":9223372036854775807}`,
	}}
	if err := db.WithContext(ctx).Table(clickHouseLogTable).
		CreateInBatches(&rows, len(rows)).Error; err != nil {
		t.Fatalf("seed int64-max row: %v", err)
	}

	summaries, err := Aggregate(ctx, db, Cursor{CreatedAt: 86400, ID: 1}, false)
	if err != nil {
		t.Fatalf("aggregate int64-max row: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("daily buckets = %d, want 1", len(summaries))
	}
	const wantMax = int64(9223372036854775807)
	if summaries[0].CacheReadTokens != wantMax {
		t.Fatalf("ClickHouse cache read = %d, want %d (UInt64 must stay in the whitelist)",
			summaries[0].CacheReadTokens, wantMax)
	}
}
