package logmigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const StateVersion = 1

var ErrStateNotFound = errors.New("migration state not found")

type LogRow struct {
	ID                int64  `gorm:"column:id"`
	UserID            int64  `gorm:"column:user_id"`
	CreatedAt         int64  `gorm:"column:created_at"`
	Type              int32  `gorm:"column:type"`
	Content           string `gorm:"column:content"`
	Username          string `gorm:"column:username"`
	TokenName         string `gorm:"column:token_name"`
	ModelName         string `gorm:"column:model_name"`
	Quota             int64  `gorm:"column:quota"`
	PromptTokens      int64  `gorm:"column:prompt_tokens"`
	CompletionTokens  int64  `gorm:"column:completion_tokens"`
	UseTime           int64  `gorm:"column:use_time"`
	IsStream          bool   `gorm:"column:is_stream"`
	ChannelID         int64  `gorm:"column:channel_id"`
	TokenID           int64  `gorm:"column:token_id"`
	Group             string `gorm:"column:group"`
	IP                string `gorm:"column:ip"`
	RequestID         string `gorm:"column:request_id"`
	EventID           string `gorm:"column:event_id"`
	UpstreamRequestID string `gorm:"column:upstream_request_id"`
	Other             string `gorm:"column:other"`
}

func (LogRow) TableName() string {
	return clickHouseLogTable
}

type Cursor struct {
	CreatedAt int64 `json:"created_at"`
	ID        int64 `json:"id"`
}

func compareCursor(left, right Cursor) int {
	if left.ID < right.ID {
		return -1
	}
	if left.ID > right.ID {
		return 1
	}
	return 0
}

type State struct {
	Version               int    `json:"version"`
	ConnectionFingerprint string `json:"connection_fingerprint"`
	HighWater             Cursor `json:"high_water"`
	Cursor                Cursor `json:"cursor"`
	RowsCopied            int64  `json:"rows_copied"`
}

func (state State) Complete() bool {
	return compareCursor(state.Cursor, state.HighWater) >= 0
}

func connectionIdentity(value string) string {
	if parsed, err := url.Parse(value); err == nil {
		switch parsed.Scheme {
		case "postgres", "postgresql", "clickhouse", "tcp", "http", "https":
			if parsed.User != nil {
				parsed.User = url.User(parsed.User.Username())
			}
			query := parsed.Query()
			for key := range query {
				lowerKey := strings.ToLower(key)
				if strings.Contains(lowerKey, "password") ||
					strings.Contains(lowerKey, "token") ||
					strings.Contains(lowerKey, "secret") {
					query.Set(key, "")
				}
			}
			parsed.RawQuery = query.Encode()
			return parsed.String()
		}
	}
	if separator := strings.LastIndex(value, "@"); separator >= 0 {
		credentials := value[:separator]
		if password := strings.Index(credentials, ":"); password >= 0 {
			return credentials[:password] + value[separator:]
		}
	}
	return value
}

func ConnectionFingerprint(values ...string) string {
	identities := make([]string, len(values))
	for index, value := range values {
		identities[index] = connectionIdentity(value)
	}
	sum := sha256.Sum256([]byte(strings.Join(identities, "\x00")))
	return hex.EncodeToString(sum[:])
}

func readStateFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrStateNotFound
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

func LoadState(path string) (State, error) {
	data, err := readStateFile(path)
	if errors.Is(err, ErrStateNotFound) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("decode migration state: %w", err)
	}
	if state.Version != StateVersion {
		return State{}, fmt.Errorf("unsupported migration state version %d", state.Version)
	}
	return state, nil
}

func writeStateFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(parent, ".log-migration-state-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

func SaveState(path string, state State) error {
	state.Version = StateVersion
	return writeStateFile(path, state)
}

func OpenSource(dsn, sqlitePath string) (*gorm.DB, error) {
	switch {
	case dsn == "" || strings.HasPrefix(dsn, "local"):
		if sqlitePath == "" {
			sqlitePath = "one-api.db?_busy_timeout=30000"
		}
		return gorm.Open(sqlite.Open(sqlitePath), &gorm.Config{PrepareStmt: true})
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		return gorm.Open(postgres.New(postgres.Config{
			DSN:                  dsn,
			PreferSimpleProtocol: true,
		}), &gorm.Config{PrepareStmt: true})
	case IsClickHouseDSN(dsn):
		return nil, errors.New("source SQL_DSN cannot be ClickHouse")
	default:
		if !strings.Contains(dsn, "parseTime") {
			if strings.Contains(dsn, "?") {
				dsn += "&parseTime=true"
			} else {
				dsn += "?parseTime=true"
			}
		}
		return gorm.Open(mysql.Open(dsn), &gorm.Config{PrepareStmt: true})
	}
}

func CaptureHighWater(ctx context.Context, source *gorm.DB) (Cursor, error) {
	var row LogRow
	result := source.WithContext(ctx).Table(clickHouseLogTable).
		Select("id, created_at").
		Order("id DESC").
		Limit(1).
		Scan(&row)
	if result.Error != nil {
		return Cursor{}, result.Error
	}
	if result.RowsAffected == 0 {
		return Cursor{}, nil
	}
	return Cursor{CreatedAt: row.CreatedAt, ID: row.ID}, nil
}

func sourceWindow(tx *gorm.DB, after, highWater Cursor) *gorm.DB {
	return tx.
		Where("id > ?", after.ID).
		Where("id <= ?", highWater.ID)
}

// legacyEventID derives the deterministic event key for a historical row.
// Backfill and reconciliation both depend on this exact format, so it must have
// a single definition.
func legacyEventID(createdAt, id int64) string {
	return fmt.Sprintf("legacy-%020d-%020d", createdAt, id)
}

func normalizeMigrationRow(row LogRow) LogRow {
	row.EventID = legacyEventID(row.CreatedAt, row.ID)
	if row.RequestID == "" {
		row.RequestID = "legacy-request-" + row.EventID
	}
	return row
}

func loadSourceBatch(ctx context.Context, source *gorm.DB, after, highWater Cursor, limit int) ([]LogRow, error) {
	var rows []LogRow
	err := sourceWindow(
		source.WithContext(ctx).Table(clickHouseLogTable),
		after,
		highWater,
	).Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func filterExistingTargetRows(ctx context.Context, target *gorm.DB, rows []LogRow) ([]LogRow, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	eventIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		eventIDs = append(eventIDs, row.EventID)
	}
	var existing []LogRow
	if err := target.WithContext(ctx).Table(clickHouseLogTable).
		Where("event_id IN ?", eventIDs).Find(&existing).Error; err != nil {
		return nil, err
	}
	existingByEventID := make(map[string]LogRow, len(existing))
	for _, row := range existing {
		if _, duplicate := existingByEventID[row.EventID]; duplicate {
			return nil, fmt.Errorf("target contains duplicate event_id %q", row.EventID)
		}
		existingByEventID[row.EventID] = row
	}
	pending := make([]LogRow, 0, len(rows))
	for _, row := range rows {
		if current, exists := existingByEventID[row.EventID]; exists {
			if current != row {
				return nil, fmt.Errorf("target event_id %q has different payload", row.EventID)
			}
			continue
		}
		pending = append(pending, row)
	}
	return pending, nil
}

type BackfillOptions struct {
	BatchSize int
	OnCommit  func(State) error
}

func Backfill(ctx context.Context, source, target *gorm.DB, state State, options BackfillOptions) (State, error) {
	if options.BatchSize <= 0 {
		return state, errors.New("batch size must be positive")
	}
	if compareCursor(state.Cursor, state.HighWater) > 0 {
		return state, errors.New("migration cursor is beyond high-water mark")
	}
	if state.Complete() {
		return state, nil
	}
	for !state.Complete() {
		if err := ctx.Err(); err != nil {
			return state, err
		}
		sourceRows, err := loadSourceBatch(ctx, source, state.Cursor, state.HighWater, options.BatchSize)
		if err != nil {
			return state, err
		}
		if len(sourceRows) == 0 {
			return state, errors.New("source ended before the captured high-water mark")
		}
		targetRows := make([]LogRow, len(sourceRows))
		for index, row := range sourceRows {
			targetRows[index] = normalizeMigrationRow(row)
		}
		pending, err := filterExistingTargetRows(ctx, target, targetRows)
		if err != nil {
			return state, err
		}
		if len(pending) > 0 {
			if err := target.WithContext(ctx).Table(clickHouseLogTable).
				CreateInBatches(&pending, len(pending)).Error; err != nil {
				return state, err
			}
		}
		last := sourceRows[len(sourceRows)-1]
		state.Cursor = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
		state.RowsCopied += int64(len(pending))
		if options.OnCommit != nil {
			if err := options.OnCommit(state); err != nil {
				return state, err
			}
		}
	}
	return state, nil
}

func AdvanceHighWater(ctx context.Context, source *gorm.DB, state State) (State, error) {
	if !state.Complete() {
		return state, errors.New("cannot advance an incomplete migration window")
	}
	highWater, err := CaptureHighWater(ctx, source)
	if err != nil {
		return state, err
	}
	if compareCursor(highWater, state.HighWater) < 0 {
		return state, errors.New("source high-water mark moved backwards")
	}
	state.HighWater = highWater
	return state, nil
}

type DaySummary struct {
	DayStart int64 `json:"day_start" gorm:"column:day_start"`
	// rows is a reserved word on MySQL 8, so the aggregate alias must stay
	// total_rows; only the report key keeps the shorter name.
	Rows               int64 `json:"rows" gorm:"column:total_rows"`
	ConsumeRows        int64 `json:"consume_rows" gorm:"column:consume_rows"`
	ErrorRows          int64 `json:"error_rows" gorm:"column:error_rows"`
	RefundRows         int64 `json:"refund_rows" gorm:"column:refund_rows"`
	Quota              int64 `json:"quota" gorm:"column:quota"`
	RefundQuota        int64 `json:"refund_quota" gorm:"column:refund_quota"`
	PromptTokens       int64 `json:"prompt_tokens" gorm:"column:prompt_tokens"`
	CompletionTokens   int64 `json:"completion_tokens" gorm:"column:completion_tokens"`
	CacheReadTokens    int64 `json:"cache_read_tokens" gorm:"column:cache_read_tokens"`
	CacheCreationToken int64 `json:"cache_creation_tokens" gorm:"column:cache_creation_tokens"`
	DistinctRequestIDs int64 `json:"distinct_request_ids" gorm:"column:distinct_request_ids"`
}

func aggregateCore(ctx context.Context, db *gorm.DB, highWater Cursor, source bool) ([]DaySummary, error) {
	selectClause := `
			(logs.created_at - (logs.created_at % 86400)) AS day_start,
			COUNT(*) AS total_rows,
			SUM(CASE WHEN logs.type = 2 THEN 1 ELSE 0 END) AS consume_rows,
			SUM(CASE WHEN logs.type = 5 THEN 1 ELSE 0 END) AS error_rows,
			SUM(CASE WHEN logs.type = 6 THEN 1 ELSE 0 END) AS refund_rows,
			COALESCE(SUM(logs.quota), 0) AS quota,
			COALESCE(SUM(CASE WHEN logs.type = 6 THEN logs.quota ELSE 0 END), 0) AS refund_quota,
			COALESCE(SUM(logs.prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(logs.completion_tokens), 0) AS completion_tokens,
			COUNT(DISTINCT NULLIF(logs.request_id, ''))
			  + SUM(CASE WHEN logs.request_id = '' THEN 1 ELSE 0 END) AS distinct_request_ids`
	cacheClause, err := cacheTokenSelectFragment(db, "logs.other")
	if err != nil {
		return nil, err
	}
	if cacheClause != "" {
		selectClause += ",\n" + cacheClause
	}
	query := db.WithContext(ctx).Table(clickHouseLogTable).Select(selectClause)
	if source {
		query = query.Where("id <= ?", highWater.ID)
	} else {
		query = query.
			Where("id <= ?", highWater.ID).
			Where("event_id LIKE 'legacy-%'")
	}
	var rows []DaySummary
	if err := query.Group("day_start").Order("day_start ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func numericJSONValue(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case json.Number:
		result, _ := typed.Int64()
		return result
	default:
		return 0
	}
}

func addCacheTotals(summary map[int64]*DaySummary, rows []LogRow) {
	for _, row := range rows {
		if row.Other == "" {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(row.Other))
		decoder.UseNumber()
		var other map[string]any
		if decoder.Decode(&other) != nil {
			continue
		}
		dayStart := row.CreatedAt - row.CreatedAt%86400
		current := summary[dayStart]
		if current == nil {
			continue
		}
		current.CacheReadTokens += numericJSONValue(other["cache_tokens"])
		current.CacheCreationToken += numericJSONValue(other["cache_creation_tokens"])
	}
}

func aggregateCacheTokens(ctx context.Context, db *gorm.DB, highWater Cursor, source bool, summary map[int64]*DaySummary) error {
	const batchSize = 5000
	if source {
		cursor := Cursor{}
		for compareCursor(cursor, highWater) < 0 {
			rows, err := loadSourceBatch(ctx, db, cursor, highWater, batchSize)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				break
			}
			addCacheTotals(summary, rows)
			last := rows[len(rows)-1]
			cursor = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
		}
		return nil
	}

	var createdAt int64
	var eventID string
	for {
		var rows []LogRow
		err := db.WithContext(ctx).Table(clickHouseLogTable).
			Where("id <= ?", highWater.ID).
			Where("event_id LIKE 'legacy-%'").
			Where("created_at > ? OR (created_at = ? AND event_id > ?)", createdAt, createdAt, eventID).
			Order("created_at ASC, event_id ASC").
			Limit(batchSize).
			Find(&rows).Error
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		addCacheTotals(summary, rows)
		last := rows[len(rows)-1]
		createdAt, eventID = last.CreatedAt, last.EventID
	}
	return nil
}

func Aggregate(ctx context.Context, db *gorm.DB, highWater Cursor, source bool) ([]DaySummary, error) {
	rows, err := aggregateCore(ctx, db, highWater, source)
	if err != nil {
		return nil, err
	}
	// Dialects that can sum cache tokens in SQL already did so inside
	// aggregateCore's single scan; only the fallback needs a second pass.
	if supportsCacheTokenPushDown(db.Dialector.Name()) {
		return rows, nil
	}
	byDay := make(map[int64]*DaySummary, len(rows))
	for index := range rows {
		byDay[rows[index].DayStart] = &rows[index]
	}
	if err := aggregateCacheTokens(ctx, db, highWater, source, byDay); err != nil {
		return nil, err
	}
	return rows, nil
}

func Reconcile(ctx context.Context, source, target *gorm.DB, highWater Cursor) ([]DaySummary, error) {
	sourceRows, err := Aggregate(ctx, source, highWater, true)
	if err != nil {
		return nil, fmt.Errorf("aggregate source: %w", err)
	}
	targetRows, err := Aggregate(ctx, target, highWater, false)
	if err != nil {
		return nil, fmt.Errorf("aggregate target: %w", err)
	}
	if len(sourceRows) != len(targetRows) {
		return nil, fmt.Errorf("daily bucket count mismatch: source=%d target=%d", len(sourceRows), len(targetRows))
	}
	for index := range sourceRows {
		if sourceRows[index] != targetRows[index] {
			sourceJSON, _ := json.Marshal(sourceRows[index])
			targetJSON, _ := json.Marshal(targetRows[index])
			return nil, fmt.Errorf("daily reconciliation mismatch: source=%s target=%s", sourceJSON, targetJSON)
		}
	}
	return sourceRows, nil
}
