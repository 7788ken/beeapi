package model

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func insertLog(t *testing.T, log *Log) {
	t.Helper()
	if log.CreatedAt == 0 {
		log.CreatedAt = common.GetTimestamp()
	}
	require.NoError(t, LOG_DB.Create(log).Error)
}

func TestGetAllLogsModelNameExactUnlessWildcardExplicit(t *testing.T) {
	truncateTables(t)

	insertLog(t, &Log{ModelName: "gpt-4", Type: LogTypeConsume, Username: "admin"})
	insertLog(t, &Log{ModelName: "gpt-4o", Type: LogTypeConsume, Username: "admin"})
	insertLog(t, &Log{ModelName: "gpt-4.1", Type: LogTypeConsume, Username: "admin"})

	logs, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "gpt-4", "", "", 0, 20, 0, "", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
	require.Equal(t, "gpt-4", logs[0].ModelName)

	logs, total, err = GetAllLogs(LogTypeUnknown, 0, 0, "gpt-4%", "", "", 0, 20, 0, "", "")
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Len(t, logs, 3)
}

func TestRelayLogsPersistUpstreamRequestId(t *testing.T) {
	setupLogQuotaTestDB(t)
	require.NoError(t, DB.Exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			setting TEXT,
			deleted_at DATETIME
		)`).Error)
	require.NoError(t, DB.Exec(`INSERT INTO users (id, setting) VALUES (1, '{}')`).Error)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("username", "alice")
	c.Set(common.RequestIdKey, "local-request-id")
	c.Set(common.UpstreamRequestIdKey, "upstream-request-id")

	RecordConsumeLog(c, 1, RecordConsumeLogParams{
		ChannelId: 1,
		ModelName: "gpt-4",
		Other:     map[string]interface{}{},
	})
	RecordErrorLog(c, 1, 1, "gpt-4", "token", "upstream error", 1, 1, false, "default", map[string]interface{}{})

	var logs []Log
	require.NoError(t, LOG_DB.Order("id").Find(&logs).Error)
	require.Len(t, logs, 2)
	require.Equal(t, LogTypeConsume, logs[0].Type)
	require.Equal(t, "upstream-request-id", logs[0].UpstreamRequestId)
	require.Equal(t, LogTypeError, logs[1].Type)
	require.Equal(t, "upstream-request-id", logs[1].UpstreamRequestId)
}

func TestCountLogsUpToAppliesLimitBeforeCount(t *testing.T) {
	truncateTables(t)

	for i := 0; i < 4; i++ {
		insertLog(t, &Log{UserId: 7, Type: LogTypeConsume})
	}
	insertLog(t, &Log{UserId: 8, Type: LogTypeConsume})

	query, err := BuildUserLogsQuery(7, LogTypeUnknown, 0, 0, "", "", "", "")
	require.NoError(t, err)

	total, err := countLogsUpTo(query, 3)
	require.NoError(t, err)
	require.Equal(t, int64(3), total)

	total, err = countLogsUpTo(query, 5)
	require.NoError(t, err)
	require.Equal(t, int64(4), total)
}

func TestGetUserLogsCapsTotalAtSearchLimit(t *testing.T) {
	truncateTables(t)

	rows := make([]*Log, 0, logSearchCountLimit+1)
	for i := 0; i < logSearchCountLimit+1; i++ {
		rows = append(rows, &Log{
			UserId:    7,
			Type:      LogTypeConsume,
			CreatedAt: int64(i + 1),
		})
	}
	require.NoError(t, LOG_DB.CreateInBatches(rows, 1000).Error)

	logs, total, totalIsCapped, err := GetUserLogs(
		7,
		LogTypeUnknown,
		0,
		0,
		"",
		"",
		0,
		20,
		"",
		"",
	)
	require.NoError(t, err)
	require.Len(t, logs, 20)
	require.Equal(t, int64(logSearchCountLimit), total)
	require.True(t, totalIsCapped)
}

func TestGetUserLogsReturnsExactTotalForShortFirstPage(t *testing.T) {
	truncateTables(t)

	insertLog(t, &Log{UserId: 7, Type: LogTypeConsume})
	insertLog(t, &Log{UserId: 7, Type: LogTypeConsume})
	insertLog(t, &Log{UserId: 8, Type: LogTypeConsume})

	logs, total, totalIsCapped, err := GetUserLogs(
		7,
		LogTypeUnknown,
		0,
		0,
		"",
		"",
		0,
		20,
		"",
		"",
	)
	require.NoError(t, err)
	require.Len(t, logs, 2)
	require.Equal(t, int64(2), total)
	require.False(t, totalIsCapped)
}
