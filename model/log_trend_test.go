package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestGetLogTrendAggregatesTokensAndCacheRateInOneBucket(t *testing.T) {
	setupLogQuotaTestDB(t)
	previousLogType := common.LogSqlType
	previousSQLite := common.UsingSQLite
	common.LogSqlType = common.DatabaseTypeSQLite
	common.UsingSQLite = true
	initCol()
	t.Cleanup(func() {
		common.LogSqlType = previousLogType
		common.UsingSQLite = previousSQLite
		initCol()
	})

	rows := []Log{
		{
			UserId: 1, Type: LogTypeConsume, CreatedAt: 3600,
			PromptTokens: 10, CompletionTokens: 4,
			Other: `{"input_tokens_total":13,"cache_tokens":3,"cache_creation_tokens":5}`,
		},
		{
			UserId: 1, Type: LogTypeConsume, CreatedAt: 3601,
			PromptTokens: 10, CompletionTokens: 2,
			Other: `{"claude":true,"cache_tokens":2}`,
		},
		{
			UserId: 2, Type: LogTypeConsume, CreatedAt: 3602,
			PromptTokens: 6, CompletionTokens: 1,
			Other: `{}`,
		},
	}
	require.NoError(t, LOG_DB.Create(&rows).Error)

	data, err := GetLogTrend(context.Background(), nil, LogTypeConsume, 3600, 7199, "", "", "", 0, "", "", 3600, 0)
	require.NoError(t, err)
	require.Equal(t, int64(3), data.TotalRows)
	require.Equal(t, int64(3600), data.BucketSeconds)
	require.Len(t, data.Points, 1)
	point := data.Points[0]
	require.Equal(t, int64(29), point.InputTokens)
	require.Equal(t, int64(7), point.OutputTokens)
	require.Equal(t, int64(5), point.CacheWriteTokens)
	require.Equal(t, int64(5), point.CacheReadTokens)
	require.Equal(t, int64(5), point.CacheHitTokens)
	// New normalized rows use input_tokens_total; old Claude rows use prompt+cache.
	require.Equal(t, int64(31), point.CacheHitDenominator)
}

func TestLogTrendCacheKeySeparatesPermissionScopes(t *testing.T) {
	params := `{"start":1,"end":2}`
	adminKey := LogTrendCacheKey("admin", 0, params)
	userOneKey := LogTrendCacheKey("self", 1, params)
	userTwoKey := LogTrendCacheKey("self", 2, params)
	if adminKey == userOneKey || userOneKey == userTwoKey {
		t.Fatalf("trend cache keys must be permission-scoped: %q %q %q", adminKey, userOneKey, userTwoKey)
	}
}

func TestLogTrendBucketRejectsUnboundedPointCount(t *testing.T) {
	_, err := normalizeLogTrendBucket(1, 86400*365, 300)
	if err == nil {
		t.Fatal("expected bucket count validation error")
	}
}

func TestNormalizeLogTrendBucketWholeMinuteGranularity(t *testing.T) {
	// 1-minute buckets over a 1-hour window (~61 points) are accepted.
	bucket, err := normalizeLogTrendBucket(60, 3660, 60)
	require.NoError(t, err)
	require.Equal(t, int64(60), bucket)

	// Day granularity over a month (~31 points) is accepted.
	bucket, err = normalizeLogTrendBucket(60, 60+86400*30, 86400)
	require.NoError(t, err)
	require.Equal(t, int64(86400), bucket)

	// Non-multiple-of-60 buckets are rejected.
	_, err = normalizeLogTrendBucket(60, 3660, 61)
	require.Error(t, err)

	// Sub-minute buckets are rejected.
	_, err = normalizeLogTrendBucket(60, 3660, 30)
	require.Error(t, err)

	// 1-minute buckets over a year exceed the point cap and are rejected.
	_, err = normalizeLogTrendBucket(60, 60+86400*365, 60)
	require.Error(t, err)
}

func TestGetLogTrendFillsEmptyBucketsWithZeroes(t *testing.T) {
	setupLogQuotaTestDB(t)
	previousLogType := common.LogSqlType
	previousSQLite := common.UsingSQLite
	common.LogSqlType = common.DatabaseTypeSQLite
	common.UsingSQLite = true
	initCol()
	t.Cleanup(func() {
		common.LogSqlType = previousLogType
		common.UsingSQLite = previousSQLite
		initCol()
	})

	// Four 3600s buckets (3600, 7200, 10800, 14400) with data only in the
	// first and last; the middle two must come back as zero-filled points.
	rows := []Log{
		{
			UserId: 1, Type: LogTypeConsume, CreatedAt: 3600,
			PromptTokens: 10, CompletionTokens: 4,
			Other: `{"input_tokens_total":13,"cache_tokens":3}`,
		},
		{
			UserId: 1, Type: LogTypeConsume, CreatedAt: 14400,
			PromptTokens: 8, CompletionTokens: 2,
			Other: `{"input_tokens_total":8}`,
		},
	}
	require.NoError(t, LOG_DB.Create(&rows).Error)

	data, err := GetLogTrend(context.Background(), nil, LogTypeConsume, 3600, 4*3600, "", "", "", 0, "", "", 3600, 0)
	require.NoError(t, err)
	require.Len(t, data.Points, 4)
	for i, wantStart := range []int64{3600, 7200, 10800, 14400} {
		require.Equal(t, wantStart, data.Points[i].BucketStart, "bucket %d", i)
	}
	// Buckets without rows report zero usage and a zero hit-rate denominator
	// so the frontend renders them as 0 tokens and a broken rate line.
	for _, i := range []int{1, 2} {
		empty := data.Points[i]
		require.Equal(t, int64(0), empty.InputTokens)
		require.Equal(t, int64(0), empty.OutputTokens)
		require.Equal(t, int64(0), empty.CacheWriteTokens)
		require.Equal(t, int64(0), empty.CacheReadTokens)
		require.Equal(t, int64(0), empty.CacheHitTokens)
		require.Equal(t, int64(0), empty.CacheHitDenominator)
		require.Equal(t, int64(0), empty.RequestCount)
	}
	require.Equal(t, int64(13), data.Points[0].InputTokens)
	require.Equal(t, int64(8), data.Points[3].InputTokens)
	// Zero-filled buckets must not inflate the row total.
	require.Equal(t, int64(2), data.TotalRows)
}

func TestGetLogTrendKeepsEmptyPointsWhenNoRows(t *testing.T) {
	setupLogQuotaTestDB(t)
	previousLogType := common.LogSqlType
	previousSQLite := common.UsingSQLite
	common.LogSqlType = common.DatabaseTypeSQLite
	common.UsingSQLite = true
	initCol()
	t.Cleanup(func() {
		common.LogSqlType = previousLogType
		common.UsingSQLite = previousSQLite
		initCol()
	})

	// A range with no rows at all stays empty so the chart keeps its
	// "no data" state instead of a misleading zero line.
	data, err := GetLogTrend(context.Background(), nil, LogTypeConsume, 3600, 4*3600-1, "", "", "", 0, "", "", 3600, 0)
	require.NoError(t, err)
	require.Empty(t, data.Points)
	require.Equal(t, int64(0), data.TotalRows)
}

func TestFillLogTrendBucketsAlignsGridToTimeZone(t *testing.T) {
	// tzOffset=28800 (+08:00), bucket=3600, start=1000 (off-boundary):
	// the grid must start at the bucket containing startTs in that zone (0),
	// run to the bucket containing endTs (7200), and keep the real point.
	filled := fillLogTrendBuckets(
		[]LogTrendPoint{{BucketStart: 3600, InputTokens: 5, CacheHitDenominator: 5}},
		1000, 7200, 3600, 28800,
	)
	require.Len(t, filled, 3)
	require.Equal(t, int64(0), filled[0].BucketStart)
	require.Equal(t, int64(0), filled[0].InputTokens)
	require.Equal(t, int64(3600), filled[1].BucketStart)
	require.Equal(t, int64(5), filled[1].InputTokens)
	require.Equal(t, int64(7200), filled[2].BucketStart)
	require.Equal(t, int64(0), filled[2].CacheHitDenominator)
}

func TestFillLogTrendBucketsStaysWithinPointBudget(t *testing.T) {
	// Zero-filling must never exceed the cap normalizeLogTrendBucket enforces,
	// including when startTs/endTs sit off bucket boundaries.
	startTs, endTs, bucket := int64(59), int64(59+86400), int64(300)
	points := []LogTrendPoint{{BucketStart: 59 - 59%300, RequestCount: 1}}
	filled := fillLogTrendBuckets(points, startTs, endTs, bucket, 0)
	require.NotEmpty(t, filled)
	require.LessOrEqual(t, int64(len(filled)), logTrendBucketCount(startTs, endTs, bucket))

	// Empty input stays empty (the caller keeps its no-data state).
	require.Empty(t, fillLogTrendBuckets(nil, startTs, endTs, bucket, 0))
}
