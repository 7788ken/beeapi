package logmigration

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"gorm.io/driver/clickhouse"
	"gorm.io/gorm"
)

const clickHouseLogTable = "logs"

var requiredLogColumns = map[string]string{
	"id":                  "Int64",
	"user_id":             "Int64",
	"created_at":          "Int64",
	"type":                "Int32",
	"content":             "String",
	"username":            "String",
	"token_name":          "String",
	"model_name":          "String",
	"quota":               "Int64",
	"prompt_tokens":       "Int64",
	"completion_tokens":   "Int64",
	"use_time":            "Int64",
	"is_stream":           "UInt8",
	"channel_id":          "Int64",
	"token_id":            "Int64",
	"group":               "String",
	"ip":                  "String",
	"request_id":          "String",
	"event_id":            "String",
	"upstream_request_id": "String",
	"other":               "String",
}

func IsClickHouseDSN(dsn string) bool {
	return strings.HasPrefix(dsn, "clickhouse://") ||
		strings.HasPrefix(dsn, "tcp://") ||
		strings.HasPrefix(dsn, "http://") ||
		strings.HasPrefix(dsn, "https://")
}

func NormalizeClickHouseDSN(dsn string) string {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "https" {
		return dsn
	}
	query := parsed.Query()
	if _, exists := query["secure"]; !exists {
		query.Set("secure", "true")
		parsed.RawQuery = query.Encode()
	}
	return parsed.String()
}

func OpenClickHouse(dsn string) (*gorm.DB, error) {
	if !IsClickHouseDSN(dsn) {
		return nil, fmt.Errorf("LOG_SQL_DSN is not a ClickHouse DSN")
	}
	return gorm.Open(clickhouse.Open(NormalizeClickHouseDSN(dsn)), &gorm.Config{
		PrepareStmt: false,
	})
}

func ClickHouseLogTTLExpression(ttlDays int) string {
	if ttlDays <= 0 {
		return ""
	}
	return fmt.Sprintf("toDateTime(created_at) + INTERVAL %d DAY DELETE", ttlDays)
}

func ClickHouseLogCreateTableSQL(ttlDays int) string {
	ttlClause := ""
	if expression := ClickHouseLogTTLExpression(ttlDays); expression != "" {
		ttlClause = "\nTTL " + expression
	}
	return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS logs (
	id Int64 DEFAULT 0,
	user_id Int64 DEFAULT 0,
	created_at Int64,
	type Int32 DEFAULT 0,
	content String DEFAULT '',
	username String DEFAULT '',
	token_name String DEFAULT '',
	model_name String DEFAULT '',
	quota Int64 DEFAULT 0,
	prompt_tokens Int64 DEFAULT 0,
	completion_tokens Int64 DEFAULT 0,
	use_time Int64 DEFAULT 0,
	is_stream UInt8 DEFAULT 0,
	channel_id Int64 DEFAULT 0,
	token_id Int64 DEFAULT 0,
	`+"`group`"+` String DEFAULT '',
	ip String DEFAULT '',
	request_id String DEFAULT '',
	event_id String DEFAULT toString(generateUUIDv4()),
	upstream_request_id String DEFAULT '',
	other String DEFAULT ''
)
ENGINE = MergeTree()
PARTITION BY toYYYYMM(toDateTime(created_at))
ORDER BY (created_at, event_id)%s`, ttlClause)
}

type clickHouseTableMetadata struct {
	Engine       string `gorm:"column:engine"`
	SortingKey   string `gorm:"column:sorting_key"`
	PartitionKey string `gorm:"column:partition_key"`
}

type clickHouseColumnMetadata struct {
	Name              string `gorm:"column:name"`
	Type              string `gorm:"column:type"`
	DefaultKind       string `gorm:"column:default_kind"`
	DefaultExpression string `gorm:"column:default_expression"`
}

func normalizeExpression(value string) string {
	value = strings.ReplaceAll(value, "`", "")
	return strings.Join(strings.Fields(value), "")
}

func clickHouseSortingKeyMatches(value string) bool {
	switch normalizeExpression(value) {
	case "(created_at,event_id)", "tuple(created_at,event_id)", "created_at,event_id":
		return true
	default:
		return false
	}
}

func ValidateClickHouseLogSchema(ctx context.Context, db *gorm.DB) error {
	var metadata clickHouseTableMetadata
	if err := db.WithContext(ctx).Raw(`
SELECT engine, sorting_key, partition_key
FROM system.tables
WHERE database = currentDatabase() AND name = ?`, clickHouseLogTable).Scan(&metadata).Error; err != nil {
		return err
	}
	if metadata.Engine != "MergeTree" {
		return fmt.Errorf("logs must use MergeTree, got %q", metadata.Engine)
	}
	if !clickHouseSortingKeyMatches(metadata.SortingKey) {
		return fmt.Errorf("logs sorting key must be (created_at, event_id), got %q", metadata.SortingKey)
	}
	if normalizeExpression(metadata.PartitionKey) != "toYYYYMM(toDateTime(created_at))" {
		return fmt.Errorf("logs partition key mismatch: %q", metadata.PartitionKey)
	}

	var columns []clickHouseColumnMetadata
	if err := db.WithContext(ctx).Raw(`
SELECT name, type, default_kind, default_expression
FROM system.columns
WHERE database = currentDatabase() AND table = ?`, clickHouseLogTable).Scan(&columns).Error; err != nil {
		return err
	}
	sort.Slice(columns, func(i, j int) bool {
		return columns[i].Name < columns[j].Name
	})
	missing := make([]string, 0)
	for name, requiredType := range requiredLogColumns {
		index := sort.Search(len(columns), func(index int) bool {
			return columns[index].Name >= name
		})
		if index == len(columns) || columns[index].Name != name {
			missing = append(missing, name)
			continue
		}
		if columns[index].Type != requiredType {
			return fmt.Errorf("logs column %s must be %s, got %s", name, requiredType, columns[index].Type)
		}
		if name == "event_id" &&
			(columns[index].DefaultKind != "DEFAULT" ||
				normalizeExpression(columns[index].DefaultExpression) != "toString(generateUUIDv4())") {
			return fmt.Errorf(
				"logs event_id default must be toString(generateUUIDv4()), got kind=%q expression=%q",
				columns[index].DefaultKind,
				columns[index].DefaultExpression,
			)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("logs schema missing columns: %s", strings.Join(missing, ", "))
	}
	return nil
}

func clickHouseCreateTableHasTTL(createTableSQL string) bool {
	upperSQL := strings.ToUpper(createTableSQL)
	return strings.Contains(upperSQL, "\nTTL ") || strings.Contains(upperSQL, " TTL ")
}

func clickHouseCreateTableTTLMatches(createTableSQL string, ttlDays int) bool {
	normalizedSQL := normalizeExpression(createTableSQL)
	intervalExpression := fmt.Sprintf("toDateTime(created_at) + INTERVAL %d DAY", ttlDays)
	functionExpression := fmt.Sprintf("toDateTime(created_at) + toIntervalDay(%d)", ttlDays)
	for _, expression := range []string{intervalExpression, functionExpression} {
		for _, action := range []string{"", " DELETE"} {
			candidate := normalizeExpression("TTL " + expression + action)
			index := strings.Index(normalizedSQL, candidate)
			if index < 0 {
				continue
			}
			remainder := normalizedSQL[index+len(candidate):]
			if remainder == "" || strings.HasPrefix(remainder, "SETTINGS") {
				return true
			}
		}
	}
	return false
}

func ValidateClickHouseLogTTL(ctx context.Context, db *gorm.DB, ttlDays int) error {
	if ttlDays < 0 {
		return fmt.Errorf("ClickHouse log TTL days cannot be negative")
	}
	var createTableSQL string
	if err := db.WithContext(ctx).Raw("SHOW CREATE TABLE logs").Scan(&createTableSQL).Error; err != nil {
		return err
	}
	expected := ClickHouseLogTTLExpression(ttlDays)
	if expected == "" {
		if clickHouseCreateTableHasTTL(createTableSQL) {
			return fmt.Errorf("logs TTL is configured but expected to be disabled")
		}
		return nil
	}
	if !clickHouseCreateTableTTLMatches(createTableSQL, ttlDays) {
		return fmt.Errorf("logs TTL does not match %q", expected)
	}
	return nil
}

func syncClickHouseLogTTL(ctx context.Context, db *gorm.DB, ttlDays int) error {
	if expression := ClickHouseLogTTLExpression(ttlDays); expression != "" {
		return db.WithContext(ctx).Exec("ALTER TABLE logs MODIFY TTL " + expression).Error
	}

	var createTableSQL string
	if err := db.WithContext(ctx).Raw("SHOW CREATE TABLE logs").Scan(&createTableSQL).Error; err != nil {
		return err
	}
	if !clickHouseCreateTableHasTTL(createTableSQL) {
		return nil
	}
	return db.WithContext(ctx).Exec("ALTER TABLE logs REMOVE TTL").Error
}

func EnsureClickHouseLogSchema(ctx context.Context, db *gorm.DB, ttlDays int) error {
	if ttlDays < 0 {
		return fmt.Errorf("ClickHouse log TTL days cannot be negative")
	}
	if err := db.WithContext(ctx).Exec(ClickHouseLogCreateTableSQL(ttlDays)).Error; err != nil {
		return err
	}
	if err := ValidateClickHouseLogSchema(ctx, db); err != nil {
		return err
	}
	if err := syncClickHouseLogTTL(ctx, db, ttlDays); err != nil {
		return err
	}
	return ValidateClickHouseLogTTL(ctx, db, ttlDays)
}
