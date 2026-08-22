package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGuardChannelMetricsQueryAddsMySQLServerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	query := guardChannelMetricsQuery(ctx, "mysql", channelMetricsAggregateQuery)
	require.Contains(t, query, "SELECT /*+ MAX_EXECUTION_TIME(")
	require.Contains(t, query, "FROM logs")

	var milliseconds int64
	_, err := fmt.Sscanf(
		strings.SplitN(query, "\n", 2)[0],
		"SELECT /*+ MAX_EXECUTION_TIME(%d) */ type, COUNT(*) AS count",
		&milliseconds,
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, milliseconds, int64(24_000))
	require.LessOrEqual(t, milliseconds, int64(25_000))
}

func TestProcessSampleRowTracksRecentUseTime(t *testing.T) {
	var sample channelSample

	processSampleRow(&sample, model.LogTypeConsume, "", `{"frt":1200}`, 3, true)
	processSampleRow(&sample, model.LogTypeConsume, "", `{"frt":800}`, 5, false)
	processSampleRow(&sample, model.LogTypeError, "status_code=503, upstream unavailable", "", 99, false)

	require.Equal(t, float64(8), sample.UseTimeSum)
	require.Equal(t, 2, sample.UseTimeCount)
	require.Equal(t, 1, sample.StreamCount)
	require.Equal(t, 1, sample.NonStreamCnt)
	require.Equal(t, 1, sample.E503)
}

func TestRecomputeChannelMetricsUsesExactCountsAndSampledUseTime(t *testing.T) {
	dsn := fmt.Sprintf("file:channel_metrics_recompute_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.Exec(`
		CREATE TABLE channels (
			id INTEGER PRIMARY KEY,
			quality_score INTEGER,
			quality_updated_at INTEGER,
			quality_detail TEXT
		)`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id INTEGER,
			type INTEGER,
			created_at INTEGER,
			use_time INTEGER,
			content TEXT,
			other TEXT,
			is_stream INTEGER
		)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channels (id) VALUES (7)`).Error)

	now := common.GetTimestamp()
	require.NoError(t, db.Exec(`
		INSERT INTO logs (channel_id, type, created_at, use_time, content, other, is_stream)
		VALUES
			(7, 2, ?, 2, '', '{"frt":500}', 1),
			(7, 2, ?, 4, '', '{"frt":1500}', 1),
			(7, 5, ?, 99, 'status_code=503, unavailable', '', 0)`,
		now, now, now).Error)

	previousDB, previousLogDB := model.DB, model.LOG_DB
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, RecomputeChannelMetricsOnce(ctx))

	var row struct {
		QualityDetail string `gorm:"column:quality_detail"`
	}
	require.NoError(t, db.Table("channels").Where("id = ?", 7).Scan(&row).Error)

	var detail qualityDetail
	require.NoError(t, json.Unmarshal([]byte(row.QualityDetail), &detail))
	require.Equal(t, int64(2), detail.SuccessCnt)
	require.Equal(t, int64(1), detail.ErrorCnt)
	require.Equal(t, int64(3000), detail.AvgUseTimeMs)
	require.Equal(t, int64(1000), detail.AvgFrtMs)
}

func TestGuardChannelMetricsQueryUsesMinimumDeadlineWhenContextIsExpiring(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	query := guardChannelMetricsQuery(ctx, "mysql", channelMetricsSampleQuery)
	require.Contains(t, query, "MAX_EXECUTION_TIME(1)")
}

func TestGuardChannelMetricsQueryLeavesOtherDialectsUnchanged(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			require.Equal(
				t,
				channelMetricsAggregateQuery,
				guardChannelMetricsQuery(context.Background(), dialect, channelMetricsAggregateQuery),
			)
		})
	}
}
