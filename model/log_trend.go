package model

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/logmigration"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

const (
	defaultLogTrendBucketSeconds = int64(3600)
	maxLogTrendPoints            = int64(2000)
	logTrendRecentCacheTTL       = 20 * time.Second
	logTrendHistoryCacheTTL      = 5 * time.Minute
	logTrendQueryTimeout         = 30 * time.Second
)

type logTrendCacheEntry struct {
	data      LogTrendData
	expiresAt time.Time
}

var logTrendCache = struct {
	sync.RWMutex
	items map[string]logTrendCacheEntry
}{items: make(map[string]logTrendCacheEntry)}

var logTrendFlight singleflight.Group

type LogTrendPoint struct {
	BucketStart         int64 `json:"bucket_start" gorm:"column:bucket_start"`
	InputTokens         int64 `json:"input_tokens" gorm:"column:input_tokens"`
	OutputTokens        int64 `json:"output_tokens" gorm:"column:output_tokens"`
	CacheWriteTokens    int64 `json:"cache_write_tokens" gorm:"column:cache_write_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens" gorm:"column:cache_read_tokens"`
	CacheHitTokens      int64 `json:"cache_hit_tokens" gorm:"column:cache_hit_tokens"`
	CacheHitDenominator int64 `json:"cache_hit_denominator" gorm:"column:cache_hit_denominator"`
	RequestCount        int64 `json:"request_count" gorm:"column:request_count"`
}

type LogTrendData struct {
	Points        []LogTrendPoint `json:"points"`
	BucketSeconds int64           `json:"bucket_seconds"`
	StartTs       int64           `json:"start_ts"`
	EndTs         int64           `json:"end_ts"`
	TotalRows     int64           `json:"total_rows"`
	Partial       bool            `json:"partial"`
	GeneratedAt   int64           `json:"generated_at"`
}

func normalizeLogTrendBucket(startTs, endTs, requested int64) (int64, error) {
	if startTs <= 0 || endTs <= startTs {
		return 0, fmt.Errorf("start_timestamp and end_timestamp must define a positive range")
	}
	if requested == 0 {
		// Pick the finest allowed bucket that keeps the response bounded. This
		// lets the default chart cover long historical windows without ever
		// returning an unrenderable number of points.
		for _, candidate := range []int64{300, 900, 3600, 21600, 86400} {
			if logTrendBucketCount(startTs, endTs, candidate) <= maxLogTrendPoints {
				return candidate, nil
			}
		}
		return 0, fmt.Errorf("time range is too large for the trend chart")
	}
	// Callers may request any whole-minute bucket (>= 1 minute) so the chart can
	// switch between minute / hour / day granularity. The point-count cap below
	// still bounds the aggregate query, so a fine bucket must be paired with a
	// narrow time range.
	if requested < 60 || requested%60 != 0 {
		return 0, fmt.Errorf("bucket_seconds must be a positive multiple of 60 (>= 1 minute)")
	}
	if logTrendBucketCount(startTs, endTs, requested) > maxLogTrendPoints {
		return 0, fmt.Errorf("bucket_seconds produces too many points; narrow the time range or choose a larger bucket")
	}
	return requested, nil
}

func logTrendBucketCount(startTs, endTs, bucket int64) int64 {
	span := endTs - startTs
	count := span/bucket + 1
	if span%bucket != 0 {
		count++
	}
	return count
}

func logTrendBucketExpr(dialect string, tzOffset, bucket int64) string {
	if dialect == common.DatabaseTypeClickHouse {
		return fmt.Sprintf("toInt64(floor((created_at + %d) / %d) * %d - %d)", tzOffset, bucket, bucket, tzOffset)
	}
	return fmt.Sprintf("((created_at + %d) - ((created_at + %d) %% %d)) - %d", tzOffset, tzOffset, bucket, tzOffset)
}

// fillLogTrendBuckets returns points with every bucket in [startTs, endTs]
// present, inserting zero-value points where the aggregate query returned no
// rows. This keeps the chart's time axis continuous; an empty input stays
// empty so the frontend can distinguish "no data" from "zero usage". The grid
// uses the same alignment math as logTrendBucketExpr, so it stays within the
// point cap normalizeLogTrendBucket already enforces.
func fillLogTrendBuckets(points []LogTrendPoint, startTs, endTs, bucket, tzOffset int64) []LogTrendPoint {
	if len(points) == 0 || bucket <= 0 {
		return points
	}
	bucketStartOf := func(ts int64) int64 {
		shifted := ts + tzOffset
		return shifted - shifted%bucket - tzOffset
	}
	first := bucketStartOf(startTs)
	last := bucketStartOf(endTs)
	if points[0].BucketStart < first {
		first = points[0].BucketStart
	}
	if points[len(points)-1].BucketStart > last {
		last = points[len(points)-1].BucketStart
	}
	filled := make([]LogTrendPoint, 0, len(points)+int((last-first)/bucket))
	next := 0
	for gridStart := first; gridStart <= last; gridStart += bucket {
		if next < len(points) && points[next].BucketStart == gridStart {
			filled = append(filled, points[next])
			next++
		} else {
			filled = append(filled, LogTrendPoint{BucketStart: gridStart})
		}
	}
	// Defensive: never drop real aggregates even if they fall off the grid.
	filled = append(filled, points[next:]...)
	return filled
}

func logTrendCacheWriteExpr(dialect string) (string, error) {
	read, err := logmigration.CacheTokenValueExpression(dialect, "logs.other", "cache_write_tokens")
	if err != nil {
		return "", err
	}
	legacy, err := logmigration.CacheTokenValueExpression(dialect, "logs.other", "cache_creation_tokens")
	if err != nil {
		return "", err
	}
	five, err := logmigration.CacheTokenValueExpression(dialect, "logs.other", "cache_creation_tokens_5m")
	if err != nil {
		return "", err
	}
	one, err := logmigration.CacheTokenValueExpression(dialect, "logs.other", "cache_creation_tokens_1h")
	if err != nil {
		return "", err
	}
	if dialect == common.DatabaseTypeSQLite {
		return fmt.Sprintf("MAX(%s, %s, (%s + %s))", read, legacy, five, one), nil
	}
	return fmt.Sprintf("GREATEST(%s, %s, (%s + %s))", read, legacy, five, one), nil
}

func logTrendNumericExpr(dialect, field string) (string, error) {
	return logmigration.CacheTokenValueExpression(dialect, "logs.other", field)
}

func applyLogTrendFilters(query *gorm.DB, userID *int, typeValue int, startTs, endTs int64, modelName, username, tokenName string, channel int, group, requestID string) (*gorm.DB, error) {
	if userID != nil {
		query = query.Where("logs.user_id = ?", *userID)
	}
	query = query.Where("logs.type = ? AND logs.created_at >= ? AND logs.created_at <= ?", typeValue, startTs, endTs)
	var err error
	if query, err = applyExplicitLogTextFilter(query, "logs.model_name", modelName); err != nil {
		return nil, err
	}
	if username != "" {
		query = query.Where("logs.username = ?", username)
	}
	if tokenName != "" {
		query = query.Where("logs.token_name = ?", tokenName)
	}
	if channel != 0 {
		query = query.Where("logs.channel_id = ?", channel)
	}
	if group != "" {
		query = query.Where("logs."+logGroupCol+" = ?", group)
	}
	if requestID != "" {
		query = query.Where("logs.request_id = ?", requestID)
	}
	return query, nil
}

func GetLogTrend(ctx context.Context, userID *int, typeValue int, startTs, endTs int64, modelName, username, tokenName string, channel int, group, requestID string, bucket, tzOffset int64) (LogTrendData, error) {
	if typeValue != 0 && typeValue != LogTypeConsume {
		return LogTrendData{}, fmt.Errorf("trend only supports consume logs")
	}
	bucket, err := normalizeLogTrendBucket(startTs, endTs, bucket)
	if err != nil {
		return LogTrendData{}, err
	}
	if tzOffset < -50400 || tzOffset > 50400 {
		return LogTrendData{}, fmt.Errorf("tz_offset_sec out of range")
	}
	query, err := applyLogTrendFilters(LOG_DB.WithContext(ctx).Table("logs"), userID, LogTypeConsume, startTs, endTs, modelName, username, tokenName, channel, group, requestID)
	if err != nil {
		return LogTrendData{}, err
	}
	dialect := LOG_DB.Dialector.Name()
	writeExpr, err := logTrendCacheWriteExpr(dialect)
	if err != nil {
		return LogTrendData{}, err
	}
	readExpr, err := logTrendNumericExpr(dialect, "cache_tokens")
	if err != nil {
		return LogTrendData{}, err
	}
	inputExpr, err := logTrendNumericExpr(dialect, "input_tokens_total")
	if err != nil {
		return LogTrendData{}, err
	}
	bucketExpr := logTrendBucketExpr(dialect, tzOffset, bucket)
	claudeExpr, err := logTrendNumericExpr(dialect, "cache_tokens")
	if err != nil {
		return LogTrendData{}, err
	}
	// input_tokens_total is authoritative when present; old Claude rows need
	// prompt + cache as denominator, while other legacy rows use prompt.
	denominatorExpr := fmt.Sprintf("CASE WHEN %s > 0 THEN %s ELSE logs.prompt_tokens + CASE WHEN logs.other LIKE '%%\"claude\":true%%' THEN %s ELSE 0 END END", inputExpr, inputExpr, claudeExpr)
	selectPrefix := ""
	if dialect == common.DatabaseTypeMySQL {
		selectPrefix = fmt.Sprintf("/*+ MAX_EXECUTION_TIME(%d) */ ", (logTrendQueryTimeout - time.Second).Milliseconds())
	}
	selectSQL := fmt.Sprintf(`%s%s AS bucket_start,
COALESCE(SUM(CASE WHEN %s > 0 THEN %s ELSE logs.prompt_tokens END), 0) AS input_tokens,
COALESCE(SUM(logs.completion_tokens), 0) AS output_tokens,
COALESCE(SUM(%s), 0) AS cache_write_tokens,
COALESCE(SUM(%s), 0) AS cache_read_tokens,
COALESCE(SUM(%s), 0) AS cache_hit_tokens,
COALESCE(SUM(%s), 0) AS cache_hit_denominator,
COUNT(*) AS request_count`, selectPrefix, bucketExpr, inputExpr, inputExpr, writeExpr, readExpr, readExpr, denominatorExpr)
	var points []LogTrendPoint
	if err := query.Select(selectSQL).Group("bucket_start").Order("bucket_start ASC").Scan(&points).Error; err != nil {
		return LogTrendData{}, err
	}
	points = fillLogTrendBuckets(points, startTs, endTs, bucket, tzOffset)
	var totalRows int64
	for _, point := range points {
		totalRows += point.RequestCount
	}
	return LogTrendData{Points: points, BucketSeconds: bucket, StartTs: startTs, EndTs: endTs, TotalRows: totalRows, GeneratedAt: time.Now().Unix()}, nil
}

// LogTrendCacheKey creates a permission-scoped key. The caller must include all
// filters and the bucket/time-zone parameters in params.
func LogTrendCacheKey(scope string, userID int, params string) string {
	return "usage-log-trend:v2:" + strings.Join([]string{scope, fmt.Sprint(userID), common.Sha256([]byte(params))}, ":")
}

func logTrendTTL(endTs, bucket int64) time.Duration {
	if endTs >= time.Now().Unix()-bucket*2 {
		return logTrendRecentCacheTTL
	}
	return logTrendHistoryCacheTTL
}

func getLogTrendMemory(key string) (LogTrendData, bool) {
	logTrendCache.RLock()
	entry, ok := logTrendCache.items[key]
	logTrendCache.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			logTrendCache.Lock()
			delete(logTrendCache.items, key)
			logTrendCache.Unlock()
		}
		return LogTrendData{}, false
	}
	return entry.data, true
}

func setLogTrendMemory(key string, data LogTrendData, ttl time.Duration) {
	logTrendCache.Lock()
	now := time.Now()
	for cachedKey, entry := range logTrendCache.items {
		if !now.Before(entry.expiresAt) {
			delete(logTrendCache.items, cachedKey)
		}
	}
	for len(logTrendCache.items) >= 256 {
		for cachedKey := range logTrendCache.items {
			delete(logTrendCache.items, cachedKey)
			break
		}
	}
	logTrendCache.items[key] = logTrendCacheEntry{data: data, expiresAt: now.Add(ttl)}
	logTrendCache.Unlock()
}

func getLogTrendRedis(ctx context.Context, key string) (string, error) {
	if !common.RedisEnabled || common.RDB == nil {
		return "", fmt.Errorf("redis is not enabled")
	}
	redisCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return common.RDB.Get(redisCtx, key).Result()
}

func setLogTrendRedis(ctx context.Context, key, value string, ttl time.Duration) error {
	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	redisCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return common.RDB.Set(redisCtx, key, value, ttl).Err()
}

func deleteLogTrendRedis(ctx context.Context, key string) error {
	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	redisCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return common.RDB.Del(redisCtx, key).Err()
}

func getLogTrendCached(ctx context.Context, key string, ttl time.Duration, load func(context.Context) (LogTrendData, error)) (LogTrendData, error) {
	if data, ok := getLogTrendMemory(key); ok {
		return data, nil
	}
	if common.RedisEnabled && common.RDB != nil {
		if raw, err := getLogTrendRedis(ctx, key); err == nil && raw != "" {
			var data LogTrendData
			if err := common.UnmarshalJsonStr(raw, &data); err == nil {
				setLogTrendMemory(key, data, ttl)
				return data, nil
			}
			// A schema/version mismatch should not poison future reads.
			_ = deleteLogTrendRedis(ctx, key)
		}
	}

	value, err, _ := logTrendFlight.Do(key, func() (any, error) {
		if data, ok := getLogTrendMemory(key); ok {
			return data, nil
		}
		data, err := load(ctx)
		if err != nil {
			return LogTrendData{}, err
		}
		setLogTrendMemory(key, data, ttl)
		if common.RedisEnabled && common.RDB != nil {
			if payload, marshalErr := common.Marshal(data); marshalErr == nil {
				_ = setLogTrendRedis(context.Background(), key, string(payload), ttl)
			}
		}
		return data, nil
	})
	if err != nil {
		return LogTrendData{}, err
	}
	return value.(LogTrendData), nil
}

// GetLogTrendCached executes one bounded aggregate query on a cache miss. The
// result is cached in Redis (shared) and a small process-local hot cache.
func GetLogTrendCached(ctx context.Context, key string, userID *int, typeValue int, startTs, endTs int64, modelName, username, tokenName string, channel int, group, requestID string, bucket, tzOffset int64) (LogTrendData, error) {
	if typeValue != 0 && typeValue != LogTypeConsume {
		return LogTrendData{}, fmt.Errorf("trend only supports consume logs")
	}
	bucket, err := normalizeLogTrendBucket(startTs, endTs, bucket)
	if err != nil {
		return LogTrendData{}, err
	}
	if tzOffset < -50400 || tzOffset > 50400 {
		return LogTrendData{}, fmt.Errorf("tz_offset_sec out of range")
	}
	if key == "" {
		key = LogTrendCacheKey("uncached", 0, fmt.Sprintf("%d|%d|%d|%d", startTs, endTs, bucket, tzOffset))
	}
	ttl := logTrendTTL(endTs, bucket)
	return getLogTrendCached(ctx, key, ttl, func(flightCtx context.Context) (LogTrendData, error) {
		queryCtx, cancel := context.WithTimeout(flightCtx, logTrendQueryTimeout)
		defer cancel()
		return GetLogTrend(queryCtx, userID, typeValue, startTs, endTs, modelName, username, tokenName, channel, group, requestID, bucket, tzOffset)
	})
}
