package logmigration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// cacheTokenBoundaryInputs covers every shape the `other` column can take that
// changes how an engine coerces JSON to an integer. The Go reference path is
// authoritative: json.Number.Int64 fails on anything that is not an integer, so
// floats, numeric strings, booleans, null, missing keys and malformed documents
// must all score 0. Any SQL push-down that disagrees on one of these rows would
// silently produce a reconciliation mismatch during the cutover window.
var cacheTokenBoundaryInputs = []string{
	`{"cache_tokens":3,"cache_creation_tokens":4}`,
	`{"cache_tokens":-3}`,
	`{"cache_tokens":3.7}`,
	`{"cache_tokens":-3.7}`,
	`{"cache_tokens":1e3}`,
	`{"cache_tokens":"5"}`,
	`{"cache_tokens":"abc"}`,
	`{"cache_tokens":true}`,
	`{"cache_tokens":false}`,
	`{"cache_tokens":null}`,
	`{"cache_tokens":[1,2]}`,
	`{"cache_tokens":{"n":1}}`,
	`{"cache_creation_tokens":7}`,
	`{}`,
	``,
	`not-json`,
	`{"broken":`,
}

// goCacheTotals mirrors addCacheTotals for a single bucket, giving the expected
// values that every dialect must reproduce.
func goCacheTotals(payloads []string) (int64, int64) {
	var read, creation int64
	for _, payload := range payloads {
		if payload == "" {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(payload))
		decoder.UseNumber()
		var other map[string]any
		if decoder.Decode(&other) != nil {
			continue
		}
		read += numericJSONValue(other["cache_tokens"])
		creation += numericJSONValue(other["cache_creation_tokens"])
	}
	return read, creation
}

func TestGoCacheTotalsRejectNonIntegers(t *testing.T) {
	// Guards the contract the SQL expressions are written against: only the
	// two integer payloads and the creation-only payload contribute.
	read, creation := goCacheTotals(cacheTokenBoundaryInputs)
	if read != 0 {
		t.Fatalf("cache read total = %d, want 0 (3 + -3 cancel, rest are non-integers)", read)
	}
	if creation != 11 {
		t.Fatalf("cache creation total = %d, want 11 (4 + 7)", creation)
	}
}

func TestCacheTokenPushDownSupport(t *testing.T) {
	for _, dialect := range []string{"mysql", "clickhouse", "sqlite"} {
		if !supportsCacheTokenPushDown(dialect) {
			t.Fatalf("dialect %q must support cache token push-down", dialect)
		}
		if _, err := cacheTokenSumExpression(dialect, "logs.other", "cache_tokens"); err != nil {
			t.Fatalf("build %q expression: %v", dialect, err)
		}
	}
	// Postgres must keep the Go path: guarding a malformed document needs
	// `IS JSON` (Postgres 16+) while compose pins postgres:15.
	if supportsCacheTokenPushDown("postgres") {
		t.Fatal("postgres must fall back to the Go row-streaming path")
	}
	if _, err := cacheTokenSumExpression("postgres", "logs.other", "cache_tokens"); err == nil {
		t.Fatal("postgres expression must report an error instead of emitting unsafe SQL")
	}
}

func TestCacheTokenSelectFragmentFallsBackForUnsupportedDialect(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "host=127.0.0.1 user=test password=test dbname=test sslmode=disable",
	}), &gorm.Config{
		DryRun:               true,
		DisableAutomaticPing: true,
	})
	if err != nil {
		t.Fatalf("open dry-run postgres: %v", err)
	}
	fragment, err := cacheTokenSelectFragment(db, "logs.other")
	if err != nil {
		t.Fatalf("select fragment for postgres: %v", err)
	}
	if fragment != "" {
		t.Fatalf("postgres fragment = %q, want empty so the Go path runs", fragment)
	}
}

// TestSQLiteCacheTokenPushDownMatchesGo pins the SQLite expression against the
// Go reference. MySQL and ClickHouse are covered by the dialect-specific tests
// that run against real servers.
func TestSQLiteCacheTokenPushDownMatchesGo(t *testing.T) {
	ctx := context.Background()
	db := openTestLogDB(t, "cache-token-boundary")
	rows := make([]LogRow, 0, len(cacheTokenBoundaryInputs))
	for index, payload := range cacheTokenBoundaryInputs {
		rows = append(rows, LogRow{
			ID:        int64(index + 1),
			CreatedAt: 86400,
			Type:      2,
			RequestID: "req-boundary",
			Other:     payload,
		})
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed boundary rows: %v", err)
	}

	highWater, err := CaptureHighWater(ctx, db)
	if err != nil {
		t.Fatalf("capture high-water: %v", err)
	}
	summaries, err := Aggregate(ctx, db, highWater, true)
	if err != nil {
		t.Fatalf("aggregate SQLite source: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("daily buckets = %d, want 1", len(summaries))
	}
	wantRead, wantCreation := goCacheTotals(cacheTokenBoundaryInputs)
	if summaries[0].CacheReadTokens != wantRead {
		t.Fatalf("SQLite cache read = %d, Go reference = %d",
			summaries[0].CacheReadTokens, wantRead)
	}
	if summaries[0].CacheCreationToken != wantCreation {
		t.Fatalf("SQLite cache creation = %d, Go reference = %d",
			summaries[0].CacheCreationToken, wantCreation)
	}
	if summaries[0].Rows != int64(len(cacheTokenBoundaryInputs)) {
		t.Fatalf("rows = %d, want %d", summaries[0].Rows, len(cacheTokenBoundaryInputs))
	}
}

// TestGoFallbackMatchesPushDownOnSQLite runs both code paths over the same rows
// so a future change to either one cannot drift unnoticed.
func TestGoFallbackMatchesPushDownOnSQLite(t *testing.T) {
	ctx := context.Background()
	db := openTestLogDB(t, "cache-token-parity")
	rows := make([]LogRow, 0, len(cacheTokenBoundaryInputs))
	for index, payload := range cacheTokenBoundaryInputs {
		rows = append(rows, LogRow{
			ID:        int64(index + 1),
			CreatedAt: 86400,
			Type:      2,
			RequestID: "req-parity",
			Other:     payload,
		})
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed parity rows: %v", err)
	}
	highWater, err := CaptureHighWater(ctx, db)
	if err != nil {
		t.Fatalf("capture high-water: %v", err)
	}

	pushed, err := Aggregate(ctx, db, highWater, true)
	if err != nil {
		t.Fatalf("push-down aggregate: %v", err)
	}

	streamed, err := aggregateCore(ctx, db, highWater, true)
	if err != nil {
		t.Fatalf("core aggregate: %v", err)
	}
	for index := range streamed {
		streamed[index].CacheReadTokens = 0
		streamed[index].CacheCreationToken = 0
	}
	byDay := make(map[int64]*DaySummary, len(streamed))
	for index := range streamed {
		byDay[streamed[index].DayStart] = &streamed[index]
	}
	if err := aggregateCacheTokens(ctx, db, highWater, true, byDay); err != nil {
		t.Fatalf("row-streaming aggregate: %v", err)
	}

	if len(pushed) != len(streamed) {
		t.Fatalf("bucket count push-down=%d streamed=%d", len(pushed), len(streamed))
	}
	for index := range pushed {
		if pushed[index] != streamed[index] {
			pushedJSON, _ := json.Marshal(pushed[index])
			streamedJSON, _ := json.Marshal(streamed[index])
			t.Fatalf("push-down and Go path disagree:\n push-down=%s\n streamed =%s",
				pushedJSON, streamedJSON)
		}
	}
}
