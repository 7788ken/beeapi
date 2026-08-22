package logmigration

import (
	"fmt"

	"gorm.io/gorm"
)

// Cache token totals used to be summed in Go by streaming every row of the
// table and decoding `other` per row. At us1 scale (59.1M rows / 114.8GB) that
// pulled ~49GB over the wire and could not finish inside the maintenance
// window, while reconciliation is the gate that lets traffic resume.
//
// Pushing the sum into SQL keeps memory flat and merges into the core aggregate
// scan, but every engine disagrees on how to coerce a JSON value to an integer.
// The Go reference (json.Number.Int64) fails on anything that is not an integer
// and yields 0, so the only portable contract is: count integers, treat
// everything else -- floats, numeric strings, booleans, null, missing keys,
// malformed documents -- as 0.
//
// Verified against MySQL 8.4, ClickHouse 25.3 and SQLite; see
// docs/2026-07-29-be17-reconcile-aggregation-options.md for the measurements.

// supportsCacheTokenPushDown reports whether the dialect can sum cache tokens
// in SQL with the exact Go semantics.
//
// Postgres is deliberately excluded. Guarding a malformed document needs
// `IS JSON`, which is Postgres 16+, while this project pins postgres:15 in
// compose. A structural regex guard is not a substitute: `{"cache_tokens":}`
// matches it and still aborts the query on cast. Postgres therefore keeps the
// row-streaming Go path, which is correct everywhere and only slow at a scale
// no Postgres deployment here runs at.
func supportsCacheTokenPushDown(dialect string) bool {
	switch dialect {
	case "mysql", "clickhouse", "sqlite":
		return true
	default:
		return false
	}
}

// cacheTokenSumExpression builds a SUM(...) over one integer field inside the
// JSON `other` column, matching the Go semantics above for the given dialect.
//
// The JSON_VALID guard on MySQL and SQLite is load-bearing, not defensive:
// JSON_EXTRACT/json_extract raise a hard error on an empty or malformed
// document instead of returning NULL, which would abort the whole
// reconciliation query. ClickHouse returns 0 and needs no guard.
func cacheTokenSumExpression(dialect, column, field string) (string, error) {
	switch dialect {
	case "mysql":
		// MySQL types any integer above the signed range as UNSIGNED INTEGER,
		// which includes int64-max; omitting that name silently scores those
		// values as 0.
		return fmt.Sprintf(
			`COALESCE(SUM(CASE WHEN JSON_VALID(%[1]s)`+
				` AND JSON_TYPE(JSON_EXTRACT(%[1]s, '$.%[2]s')) IN ('INTEGER', 'UNSIGNED INTEGER')`+
				` THEN JSON_VALUE(%[1]s, '$.%[2]s' RETURNING SIGNED) ELSE 0 END), 0)`,
			column, field,
		), nil
	case "clickhouse":
		// Int64 alone is the right whitelist: ClickHouse types every integer
		// that fits in int64 -- including int64-max -- as Int64, and reserves
		// UInt64 for values beyond that range, where JSONExtractInt returns 0
		// anyway. See the out-of-range note on the MySQL branch.
		return fmt.Sprintf(
			`COALESCE(SUM(CASE WHEN JSONType(%[1]s, '%[2]s') = 'Int64'`+
				` THEN JSONExtractInt(%[1]s, '%[2]s') ELSE 0 END), 0)`,
			column, field,
		), nil
	case "sqlite":
		// json_type returns 'integer' for integers; floats report 'real' and
		// numeric strings 'text', so both fall through to 0 as Go does.
		return fmt.Sprintf(
			`COALESCE(SUM(CASE WHEN json_valid(%[1]s)`+
				` AND json_type(%[1]s, '$.%[2]s') = 'integer'`+
				` THEN CAST(json_extract(%[1]s, '$.%[2]s') AS INTEGER) ELSE 0 END), 0)`,
			column, field,
		), nil
	default:
		return "", fmt.Errorf("cache token aggregation not supported for dialect %q", dialect)
	}
}

// cacheTokenSelectFragment returns the two aliased SUM columns that feed
// DaySummary.CacheReadTokens and DaySummary.CacheCreationToken, or an empty
// string when the dialect must fall back to the Go path.
func cacheTokenSelectFragment(db *gorm.DB, column string) (string, error) {
	dialect := db.Dialector.Name()
	if !supportsCacheTokenPushDown(dialect) {
		return "", nil
	}
	read, err := cacheTokenSumExpression(dialect, column, "cache_tokens")
	if err != nil {
		return "", err
	}
	creation, err := cacheTokenSumExpression(dialect, column, "cache_creation_tokens")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"%s AS cache_read_tokens,\n%s AS cache_creation_tokens",
		read, creation,
	), nil
}
