package logmigration

import (
	"context"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// Every other migration test runs against SQLite, which accepts identifiers
// that MySQL 8 reserves. Production log sources are all MySQL, so the
// reconciliation SQL must be exercised against a real MySQL server before it
// can be trusted. Set LOG_MIGRATION_MYSQL_TEST_DSN to a scratch database whose
// name contains "test"; the test drops and recreates its tables.
func openMySQLTestSource(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LOG_MIGRATION_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("LOG_MIGRATION_MYSQL_TEST_DSN not set")
	}
	database := dsn
	if separator := strings.LastIndex(database, "/"); separator >= 0 {
		database = database[separator+1:]
	}
	if cut := strings.Index(database, "?"); cut >= 0 {
		database = database[:cut]
	}
	if !strings.Contains(strings.ToLower(database), "test") {
		t.Fatalf("refusing to run destructive test against database %q", database)
	}
	db, err := OpenSource(dsn, "")
	if err != nil {
		t.Fatalf("open MySQL source: %v", err)
	}
	return db
}

func resetMySQLLogTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		"DROP TABLE IF EXISTS logs",
		"DROP TABLE IF EXISTS " + restoreLedgerTable,
		"CREATE TABLE logs (" +
			"id BIGINT AUTO_INCREMENT PRIMARY KEY," +
			"user_id BIGINT NOT NULL DEFAULT 0," +
			"created_at BIGINT NOT NULL DEFAULT 0," +
			"type INT NOT NULL DEFAULT 0," +
			"content TEXT," +
			"username VARCHAR(64) NOT NULL DEFAULT ''," +
			"token_name VARCHAR(64) NOT NULL DEFAULT ''," +
			"model_name VARCHAR(128) NOT NULL DEFAULT ''," +
			"quota BIGINT NOT NULL DEFAULT 0," +
			"prompt_tokens BIGINT NOT NULL DEFAULT 0," +
			"completion_tokens BIGINT NOT NULL DEFAULT 0," +
			"use_time BIGINT NOT NULL DEFAULT 0," +
			"is_stream TINYINT(1) NOT NULL DEFAULT 0," +
			"channel_id BIGINT NOT NULL DEFAULT 0," +
			"token_id BIGINT NOT NULL DEFAULT 0," +
			"`group` VARCHAR(64) NOT NULL DEFAULT ''," +
			"ip VARCHAR(64) NOT NULL DEFAULT ''," +
			"request_id VARCHAR(64) NOT NULL DEFAULT ''," +
			"upstream_request_id VARCHAR(64) NOT NULL DEFAULT ''," +
			"other TEXT)",
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("reset MySQL log tables (%s): %v", statement, err)
		}
	}
}

func seedMySQLLogRows(t *testing.T, db *gorm.DB) {
	t.Helper()
	rows := []relationalLogRow{
		{
			UserID: 11, CreatedAt: 86400, Type: 2, Content: "consume",
			Username: "alice", TokenName: "token-a", ModelName: "gpt-5.6-sol",
			Quota: 100, PromptTokens: 10, CompletionTokens: 3, UseTime: 120,
			IsStream: true, ChannelID: 21, TokenID: 31, Group: "vip",
			IP: "127.0.0.1", RequestID: "req-shared",
			Other: `{"cache_tokens":3,"cache_creation_tokens":4}`,
		},
		{
			UserID: 11, CreatedAt: 86401, Type: 6, Content: "refund",
			Username: "alice", TokenName: "token-a", ModelName: "gpt-5.6-sol",
			Quota: -20, ChannelID: 21, TokenID: 31, Group: "vip",
			IP: "127.0.0.1", RequestID: "req-shared", Other: "{}",
		},
		{
			UserID: 12, CreatedAt: 172800, Type: 5, Content: "error",
			Username: "bob", TokenName: "token-b", ModelName: "claude-sonnet",
			PromptTokens: 5, CompletionTokens: 7, ChannelID: 22, TokenID: 32,
			Group: "default", IP: "127.0.0.2", Other: "{}",
		},
	}
	for index := range rows {
		if err := db.Create(&rows[index]).Error; err != nil {
			t.Fatalf("seed MySQL log row: %v", err)
		}
	}
}

func TestAggregateRunsOnMySQL(t *testing.T) {
	db := openMySQLTestSource(t)
	resetMySQLLogTables(t, db)
	seedMySQLLogRows(t, db)

	ctx := context.Background()
	highWater, err := CaptureHighWater(ctx, db)
	if err != nil {
		t.Fatalf("capture MySQL high-water: %v", err)
	}
	summaries, err := Aggregate(ctx, db, highWater, true)
	if err != nil {
		t.Fatalf("aggregate MySQL source: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("daily buckets = %d, want 2", len(summaries))
	}
	first := summaries[0]
	if first.Rows != 2 || first.ConsumeRows != 1 || first.RefundRows != 1 {
		t.Fatalf("first bucket = %+v, want 2 rows / 1 consume / 1 refund", first)
	}
	if first.Quota != 80 || first.RefundQuota != -20 {
		t.Fatalf("first bucket quota = %d / refund %d, want 80 / -20", first.Quota, first.RefundQuota)
	}
	if first.CacheReadTokens != 3 || first.CacheCreationToken != 4 {
		t.Fatalf("first bucket cache tokens = %d / %d, want 3 / 4",
			first.CacheReadTokens, first.CacheCreationToken)
	}
	if first.DistinctRequestIDs != 1 {
		t.Fatalf("first bucket distinct request ids = %d, want 1", first.DistinctRequestIDs)
	}
	if summaries[1].Rows != 1 || summaries[1].ErrorRows != 1 {
		t.Fatalf("second bucket = %+v, want 1 row / 1 error", summaries[1])
	}
	if summaries[1].DistinctRequestIDs != 1 {
		t.Fatalf("empty request id must still count as one event, got %d",
			summaries[1].DistinctRequestIDs)
	}
}

// The SQL push-down must reproduce the Go reference exactly, including MySQL's
// habit of typing large positive integers as UNSIGNED INTEGER and of hard-erroring
// on an empty or malformed JSON document.
func TestCacheTokenPushDownMatchesGoOnMySQL(t *testing.T) {
	db := openMySQLTestSource(t)
	resetMySQLLogTables(t, db)

	for index, payload := range cacheTokenBoundaryInputs {
		row := relationalLogRow{
			CreatedAt: 86400,
			Type:      2,
			RequestID: "req-boundary",
			Other:     payload,
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed boundary row %d: %v", index, err)
		}
	}

	ctx := context.Background()
	highWater, err := CaptureHighWater(ctx, db)
	if err != nil {
		t.Fatalf("capture MySQL high-water: %v", err)
	}
	summaries, err := Aggregate(ctx, db, highWater, true)
	if err != nil {
		t.Fatalf("aggregate MySQL boundary rows: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("daily buckets = %d, want 1", len(summaries))
	}
	wantRead, wantCreation := goCacheTotals(cacheTokenBoundaryInputs)
	if summaries[0].CacheReadTokens != wantRead {
		t.Fatalf("MySQL cache read = %d, Go reference = %d",
			summaries[0].CacheReadTokens, wantRead)
	}
	if summaries[0].CacheCreationToken != wantCreation {
		t.Fatalf("MySQL cache creation = %d, Go reference = %d",
			summaries[0].CacheCreationToken, wantCreation)
	}
}

// int64-max is typed UNSIGNED INTEGER by MySQL; a whitelist missing that name
// scores it as 0 while Go and ClickHouse both return the full value.
func TestCacheTokenPushDownKeepsLargeIntegersOnMySQL(t *testing.T) {
	db := openMySQLTestSource(t)
	resetMySQLLogTables(t, db)

	row := relationalLogRow{
		CreatedAt: 86400,
		Type:      2,
		RequestID: "req-max",
		Other:     `{"cache_tokens":9223372036854775807}`,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed int64-max row: %v", err)
	}

	ctx := context.Background()
	highWater, err := CaptureHighWater(ctx, db)
	if err != nil {
		t.Fatalf("capture MySQL high-water: %v", err)
	}
	summaries, err := Aggregate(ctx, db, highWater, true)
	if err != nil {
		t.Fatalf("aggregate int64-max row: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("daily buckets = %d, want 1", len(summaries))
	}
	const wantMax = int64(9223372036854775807)
	if summaries[0].CacheReadTokens != wantMax {
		t.Fatalf("MySQL cache read = %d, want %d (UNSIGNED INTEGER must stay in the whitelist)",
			summaries[0].CacheReadTokens, wantMax)
	}
}

func TestAggregateRestoredRunsOnMySQL(t *testing.T) {
	db := openMySQLTestSource(t)
	resetMySQLLogTables(t, db)
	seedMySQLLogRows(t, db)

	ctx := context.Background()
	if err := EnsureRestoreLedger(ctx, db); err != nil {
		t.Fatalf("ensure MySQL restore ledger: %v", err)
	}
	restored := []LogRow{{
		UserID: 14, CreatedAt: 259200, Type: 2, Content: "runtime",
		Username: "dave", TokenName: "token-d", ModelName: "gpt-5.6-luna",
		Quota: 55, PromptTokens: 6, CompletionTokens: 4, UseTime: 70,
		IsStream: true, ChannelID: 24, TokenID: 34, IP: "127.0.0.4",
		RequestID: "req-runtime", EventID: "evt-runtime-1",
		Other: `{"cache_creation_tokens":2}`,
	}}
	if _, err := restoreBatch(ctx, db, restored); err != nil {
		t.Fatalf("restore batch into MySQL: %v", err)
	}

	highWater := EventCursor{CreatedAt: 259200, EventID: "evt-runtime-1"}
	summaries, err := aggregateRestoredCore(ctx, db, 0, highWater)
	if err != nil {
		t.Fatalf("aggregate restored rows on MySQL: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("restored buckets = %d, want 1", len(summaries))
	}
	if summaries[0].Rows != 1 || summaries[0].Quota != 55 {
		t.Fatalf("restored bucket = %+v, want 1 row / quota 55", summaries[0])
	}

	// MySQL sums cache tokens inside the aggregate scan above, so the restored
	// bucket must already carry them; calling the Go fallback here as well
	// would double-count.
	if summaries[0].CacheCreationToken != 2 {
		t.Fatalf("restored cache creation tokens = %d, want 2", summaries[0].CacheCreationToken)
	}
	if summaries[0].CacheReadTokens != 0 {
		t.Fatalf("restored cache read tokens = %d, want 0", summaries[0].CacheReadTokens)
	}

	// The row-streaming fallback must agree with the push-down on the same rows.
	fallback, err := aggregateRestoredCore(ctx, db, 0, highWater)
	if err != nil {
		t.Fatalf("re-aggregate restored rows: %v", err)
	}
	fallback[0].CacheReadTokens = 0
	fallback[0].CacheCreationToken = 0
	byDay := map[int64]*DaySummary{fallback[0].DayStart: &fallback[0]}
	if err := addRestoredCacheTotals(ctx, db, 0, highWater, byDay); err != nil {
		t.Fatalf("aggregate restored cache tokens on MySQL: %v", err)
	}
	if fallback[0] != summaries[0] {
		t.Fatalf("push-down and Go path disagree:\n push-down=%+v\n streamed =%+v",
			summaries[0], fallback[0])
	}
}
