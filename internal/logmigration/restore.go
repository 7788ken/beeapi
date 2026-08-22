package logmigration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const restoreLedgerTable = "log_clickhouse_restore_events"

type EventCursor struct {
	CreatedAt int64  `json:"created_at"`
	EventID   string `json:"event_id"`
}

func compareEventCursor(left, right EventCursor) int {
	if left.CreatedAt < right.CreatedAt {
		return -1
	}
	if left.CreatedAt > right.CreatedAt {
		return 1
	}
	return strings.Compare(left.EventID, right.EventID)
}

type RestoreState struct {
	Version               int         `json:"version"`
	ConnectionFingerprint string      `json:"connection_fingerprint"`
	From                  int64       `json:"from"`
	HighWater             EventCursor `json:"high_water"`
	Cursor                EventCursor `json:"cursor"`
	RowsRestored          int64       `json:"rows_restored"`
}

func (state RestoreState) Complete() bool {
	return compareEventCursor(state.Cursor, state.HighWater) >= 0
}

func LoadRestoreState(path string) (RestoreState, error) {
	data, err := readStateFile(path)
	if errors.Is(err, ErrStateNotFound) {
		return RestoreState{}, nil
	}
	if err != nil {
		return RestoreState{}, err
	}
	var state RestoreState
	if err := json.Unmarshal(data, &state); err != nil {
		return RestoreState{}, fmt.Errorf("decode restore state: %w", err)
	}
	if state.Version != StateVersion {
		return RestoreState{}, fmt.Errorf("unsupported restore state version %d", state.Version)
	}
	return state, nil
}

func SaveRestoreState(path string, state RestoreState) error {
	state.Version = StateVersion
	return writeStateFile(path, state)
}

func CaptureClickHouseHighWater(ctx context.Context, clickHouse *gorm.DB, _ int64) (EventCursor, error) {
	var row LogRow
	result := clickHouse.WithContext(ctx).Table(clickHouseLogTable).
		Select("created_at, event_id").
		Where("event_id NOT LIKE 'legacy-%'").
		Order("created_at DESC, event_id DESC").
		Limit(1).
		Scan(&row)
	if result.Error != nil {
		return EventCursor{}, result.Error
	}
	if result.RowsAffected == 0 {
		return EventCursor{}, nil
	}
	return EventCursor{CreatedAt: row.CreatedAt, EventID: row.EventID}, nil
}

func clickHouseEventWindow(tx *gorm.DB, _ int64, after, highWater EventCursor) *gorm.DB {
	return tx.
		Where("event_id NOT LIKE 'legacy-%'").
		Where("created_at > ? OR (created_at = ? AND event_id > ?)", after.CreatedAt, after.CreatedAt, after.EventID).
		Where("created_at < ? OR (created_at = ? AND event_id <= ?)", highWater.CreatedAt, highWater.CreatedAt, highWater.EventID)
}

func loadClickHouseEventBatch(
	ctx context.Context,
	clickHouse *gorm.DB,
	from int64,
	after EventCursor,
	highWater EventCursor,
	limit int,
) ([]LogRow, error) {
	var rows []LogRow
	err := clickHouseEventWindow(
		clickHouse.WithContext(ctx).Table(clickHouseLogTable),
		from,
		after,
		highWater,
	).Order("created_at ASC, event_id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func EnsureRestoreLedger(ctx context.Context, relational *gorm.DB) error {
	var ddl string
	switch relational.Dialector.Name() {
	case "mysql":
		ddl = `CREATE TABLE IF NOT EXISTS log_clickhouse_restore_events (
			event_id VARCHAR(64) PRIMARY KEY,
			log_id BIGINT NOT NULL DEFAULT 0,
			source_created_at BIGINT NOT NULL,
			restored_at BIGINT NOT NULL
		)`
	case "postgres":
		ddl = `CREATE TABLE IF NOT EXISTS log_clickhouse_restore_events (
			event_id VARCHAR(64) PRIMARY KEY,
			log_id BIGINT NOT NULL DEFAULT 0,
			source_created_at BIGINT NOT NULL,
			restored_at BIGINT NOT NULL
		)`
	case "sqlite":
		ddl = `CREATE TABLE IF NOT EXISTS log_clickhouse_restore_events (
			event_id TEXT PRIMARY KEY,
			log_id INTEGER NOT NULL DEFAULT 0,
			source_created_at INTEGER NOT NULL,
			restored_at INTEGER NOT NULL
		)`
	default:
		return fmt.Errorf("unsupported relational restore database %q", relational.Dialector.Name())
	}
	return relational.WithContext(ctx).Exec(ddl).Error
}

type relationalLogRow struct {
	ID                int64  `gorm:"column:id;primaryKey;autoIncrement"`
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
	UpstreamRequestID string `gorm:"column:upstream_request_id"`
	Other             string `gorm:"column:other"`
}

func (relationalLogRow) TableName() string {
	return clickHouseLogTable
}

func toRelationalLogRow(row LogRow) relationalLogRow {
	return relationalLogRow{
		UserID:            row.UserID,
		CreatedAt:         row.CreatedAt,
		Type:              row.Type,
		Content:           row.Content,
		Username:          row.Username,
		TokenName:         row.TokenName,
		ModelName:         row.ModelName,
		Quota:             row.Quota,
		PromptTokens:      row.PromptTokens,
		CompletionTokens:  row.CompletionTokens,
		UseTime:           row.UseTime,
		IsStream:          row.IsStream,
		ChannelID:         row.ChannelID,
		TokenID:           row.TokenID,
		Group:             row.Group,
		IP:                row.IP,
		RequestID:         row.RequestID,
		UpstreamRequestID: row.UpstreamRequestID,
		Other:             row.Other,
	}
}

func reserveRestoreEvent(ctx context.Context, tx *gorm.DB, row LogRow) (bool, error) {
	var result *gorm.DB
	switch tx.Dialector.Name() {
	case "mysql":
		result = tx.WithContext(ctx).Exec(
			`INSERT IGNORE INTO log_clickhouse_restore_events
				(event_id, source_created_at, restored_at) VALUES (?, ?, ?)`,
			row.EventID, row.CreatedAt, 0,
		)
	case "postgres", "sqlite":
		result = tx.WithContext(ctx).Exec(
			`INSERT INTO log_clickhouse_restore_events
				(event_id, source_created_at, restored_at) VALUES (?, ?, ?)
			 ON CONFLICT(event_id) DO NOTHING`,
			row.EventID, row.CreatedAt, 0,
		)
	default:
		return false, fmt.Errorf("unsupported relational restore database %q", tx.Dialector.Name())
	}
	return result.RowsAffected == 1, result.Error
}

func restoreBatch(ctx context.Context, relational *gorm.DB, rows []LogRow) (int64, error) {
	var restored int64
	err := relational.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, row := range rows {
			if row.EventID == "" {
				return errors.New("ClickHouse row has empty event_id")
			}
			reserved, err := reserveRestoreEvent(ctx, tx, row)
			if err != nil {
				return err
			}
			if !reserved {
				continue
			}
			logRow := toRelationalLogRow(row)
			if err := tx.WithContext(ctx).Create(&logRow).Error; err != nil {
				return err
			}
			if err := tx.WithContext(ctx).Table(restoreLedgerTable).
				Where("event_id = ?", row.EventID).
				Updates(map[string]any{
					"log_id":      logRow.ID,
					"restored_at": row.CreatedAt,
				}).Error; err != nil {
				return err
			}
			restored++
		}
		return nil
	})
	return restored, err
}

type RestoreOptions struct {
	BatchSize int
	OnCommit  func(RestoreState) error
}

func RestoreClickHouseRows(
	ctx context.Context,
	clickHouse *gorm.DB,
	relational *gorm.DB,
	state RestoreState,
	options RestoreOptions,
) (RestoreState, error) {
	if options.BatchSize <= 0 {
		return state, errors.New("batch size must be positive")
	}
	if compareEventCursor(state.Cursor, state.HighWater) > 0 {
		return state, errors.New("restore cursor is beyond high-water mark")
	}
	if state.Complete() {
		return state, nil
	}

	for !state.Complete() {
		if err := ctx.Err(); err != nil {
			return state, err
		}
		rows, err := loadClickHouseEventBatch(
			ctx,
			clickHouse,
			state.From,
			state.Cursor,
			state.HighWater,
			options.BatchSize,
		)
		if err != nil {
			return state, err
		}
		if len(rows) == 0 {
			return state, errors.New("ClickHouse source ended before the captured restore high-water mark")
		}
		restored, err := restoreBatch(ctx, relational, rows)
		if err != nil {
			return state, err
		}
		last := rows[len(rows)-1]
		state.Cursor = EventCursor{CreatedAt: last.CreatedAt, EventID: last.EventID}
		state.RowsRestored += restored
		if options.OnCommit != nil {
			if err := options.OnCommit(state); err != nil {
				return state, err
			}
		}
	}
	return state, nil
}

func aggregateRestoredCore(
	ctx context.Context,
	relational *gorm.DB,
	_ int64,
	highWater EventCursor,
) ([]DaySummary, error) {
	selectClause := `
			(l.created_at - (l.created_at % 86400)) AS day_start,
			COUNT(*) AS total_rows,
			SUM(CASE WHEN l.type = 2 THEN 1 ELSE 0 END) AS consume_rows,
			SUM(CASE WHEN l.type = 5 THEN 1 ELSE 0 END) AS error_rows,
			SUM(CASE WHEN l.type = 6 THEN 1 ELSE 0 END) AS refund_rows,
			COALESCE(SUM(l.quota), 0) AS quota,
			COALESCE(SUM(CASE WHEN l.type = 6 THEN l.quota ELSE 0 END), 0) AS refund_quota,
			COALESCE(SUM(l.prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(l.completion_tokens), 0) AS completion_tokens,
			COUNT(DISTINCT NULLIF(l.request_id, ''))
			  + SUM(CASE WHEN l.request_id = '' THEN 1 ELSE 0 END) AS distinct_request_ids`
	cacheClause, err := cacheTokenSelectFragment(relational, "l.other")
	if err != nil {
		return nil, err
	}
	if cacheClause != "" {
		selectClause += ",\n" + cacheClause
	}
	var rows []DaySummary
	err = relational.WithContext(ctx).Table("logs AS l").
		Select(selectClause).
		Joins("JOIN "+restoreLedgerTable+" AS e ON e.log_id = l.id").
		Where(
			"e.source_created_at < ? OR (e.source_created_at = ? AND e.event_id <= ?)",
			highWater.CreatedAt, highWater.CreatedAt, highWater.EventID,
		).
		Group("day_start").Order("day_start ASC").Scan(&rows).Error
	return rows, err
}

func aggregateClickHouseRestoreCore(
	ctx context.Context,
	clickHouse *gorm.DB,
	_ int64,
	highWater EventCursor,
) ([]DaySummary, error) {
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
	cacheClause, err := cacheTokenSelectFragment(clickHouse, "logs.other")
	if err != nil {
		return nil, err
	}
	if cacheClause != "" {
		selectClause += ",\n" + cacheClause
	}
	var rows []DaySummary
	err = clickHouse.WithContext(ctx).Table(clickHouseLogTable).
		Select(selectClause).
		Where("event_id NOT LIKE 'legacy-%'").
		Where(
			"created_at < ? OR (created_at = ? AND event_id <= ?)",
			highWater.CreatedAt, highWater.CreatedAt, highWater.EventID,
		).
		Group("day_start").Order("day_start ASC").Scan(&rows).Error
	return rows, err
}

func addClickHouseRestoreCacheTotals(
	ctx context.Context,
	clickHouse *gorm.DB,
	from int64,
	highWater EventCursor,
	summary map[int64]*DaySummary,
) error {
	cursor := EventCursor{}
	for compareEventCursor(cursor, highWater) < 0 {
		rows, err := loadClickHouseEventBatch(ctx, clickHouse, from, cursor, highWater, 5000)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		addCacheTotals(summary, rows)
		last := rows[len(rows)-1]
		cursor = EventCursor{CreatedAt: last.CreatedAt, EventID: last.EventID}
	}
	return nil
}

func addRestoredCacheTotals(
	ctx context.Context,
	relational *gorm.DB,
	_ int64,
	highWater EventCursor,
	summary map[int64]*DaySummary,
) error {
	var cursor string
	for {
		var rows []LogRow
		err := relational.WithContext(ctx).Table("logs AS l").
			Select("l.*").
			Joins("JOIN "+restoreLedgerTable+" AS e ON e.log_id = l.id").
			Where(
				"e.source_created_at < ? OR (e.source_created_at = ? AND e.event_id <= ?)",
				highWater.CreatedAt, highWater.CreatedAt, highWater.EventID,
			).
			Where("e.event_id > ?", cursor).
			Order("e.event_id ASC").
			Limit(5000).
			Find(&rows).Error
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		addCacheTotals(summary, rows)
		var lastEventID string
		if err := relational.WithContext(ctx).Table(restoreLedgerTable).
			Select("event_id").
			Where("log_id = ?", rows[len(rows)-1].ID).
			Scan(&lastEventID).Error; err != nil {
			return err
		}
		if lastEventID == "" || lastEventID <= cursor {
			return errors.New("restore ledger event cursor did not advance")
		}
		cursor = lastEventID
	}
}

func ReconcileRestored(
	ctx context.Context,
	clickHouse *gorm.DB,
	relational *gorm.DB,
	from int64,
	highWater EventCursor,
) ([]DaySummary, error) {
	sourceRows, err := aggregateClickHouseRestoreCore(ctx, clickHouse, from, highWater)
	if err != nil {
		return nil, fmt.Errorf("aggregate ClickHouse restore source: %w", err)
	}
	targetRows, err := aggregateRestoredCore(ctx, relational, from, highWater)
	if err != nil {
		return nil, fmt.Errorf("aggregate relational restore target: %w", err)
	}
	// Dialects that can sum cache tokens in SQL already did so inside the
	// aggregate scans above; only the fallback needs a second row-streaming pass.
	if !supportsCacheTokenPushDown(clickHouse.Dialector.Name()) {
		sourceByDay := make(map[int64]*DaySummary, len(sourceRows))
		for index := range sourceRows {
			sourceByDay[sourceRows[index].DayStart] = &sourceRows[index]
		}
		if err := addClickHouseRestoreCacheTotals(ctx, clickHouse, from, highWater, sourceByDay); err != nil {
			return nil, err
		}
	}
	if !supportsCacheTokenPushDown(relational.Dialector.Name()) {
		targetByDay := make(map[int64]*DaySummary, len(targetRows))
		for index := range targetRows {
			targetByDay[targetRows[index].DayStart] = &targetRows[index]
		}
		if err := addRestoredCacheTotals(ctx, relational, from, highWater, targetByDay); err != nil {
			return nil, err
		}
	}
	if len(sourceRows) != len(targetRows) {
		return nil, fmt.Errorf("restore daily bucket count mismatch: source=%d target=%d", len(sourceRows), len(targetRows))
	}
	for index := range sourceRows {
		if sourceRows[index] != targetRows[index] {
			sourceJSON, _ := json.Marshal(sourceRows[index])
			targetJSON, _ := json.Marshal(targetRows[index])
			return nil, fmt.Errorf("restore daily reconciliation mismatch: source=%s target=%s", sourceJSON, targetJSON)
		}
	}
	return sourceRows, nil
}
