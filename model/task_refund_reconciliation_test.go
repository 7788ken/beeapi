package model

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAsyncRefundReconciliation_ClassifiesDurableHistoricalAndMissingEvidence(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM midjourneys").Error)

	tasks := []Task{
		{
			ID:         801,
			TaskID:     "durable-task",
			UserId:     81,
			Quota:      100,
			Status:     TaskStatusFailure,
			FinishTime: 120,
			FailReason: "durable failure",
			PrivateData: TaskPrivateData{
				BillingSource: "wallet",
			},
			Data: json.RawMessage(`{}`),
		},
		{
			ID:         802,
			TaskID:     "historical-log-task",
			UserId:     82,
			Quota:      200,
			Status:     TaskStatusFailure,
			FinishTime: 130,
			FailReason: "historical failure",
			PrivateData: TaskPrivateData{
				BillingSource: "wallet",
			},
			Data: json.RawMessage(`{}`),
		},
		{
			ID:         803,
			TaskID:     "ambiguous-log-task",
			UserId:     83,
			Quota:      300,
			Status:     TaskStatusFailure,
			FinishTime: 140,
			FailReason: "ambiguous failure",
			PrivateData: TaskPrivateData{
				BillingSource: "wallet",
			},
			Data: json.RawMessage(`{}`),
		},
		{
			ID:         805,
			TaskID:     "unknown-source-task",
			UserId:     85,
			Quota:      500,
			Status:     TaskStatusInProgress,
			SubmitTime: 160,
			FailReason: "refund safely blocked",
			Data:       json.RawMessage(`{}`),
		},
	}
	require.NoError(t, DB.Create(&tasks).Error)
	require.NoError(t, DB.Create(&Midjourney{
		Id:            804,
		MjId:          "missing-midjourney-task",
		UserId:        84,
		Quota:         400,
		Status:        string(TaskStatusFailure),
		BillingSource: "wallet",
		FinishTime:    150,
		FailReason:    "missing failure",
	}).Error)
	require.NoError(t, DB.Create(&AsyncTaskRefundRecord{
		TaskKind:      AsyncTaskRefundKindTask,
		TaskDbId:      801,
		TaskId:        "durable-task",
		UserId:        81,
		BillingSource: "wallet",
		Quota:         100,
		Status:        AsyncTaskRefundStatusRefunded,
		CreatedAt:     121,
	}).Error)
	logs := []Log{
		{
			UserId:    82,
			CreatedAt: 131,
			Type:      LogTypeRefund,
			Quota:     200,
			Other:     `{"task_id":"historical-log-task"}`,
		},
		{
			UserId:    83,
			CreatedAt: 141,
			Type:      LogTypeRefund,
			Quota:     300,
			Other:     `{"task_id":"ambiguous-log-task"}`,
		},
		{
			UserId:    83,
			CreatedAt: 142,
			Type:      LogTypeRefund,
			Quota:     300,
			Other:     `{"task_id":"ambiguous-log-task"}`,
		},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	report, err := BuildAsyncRefundReconciliation(100, 200, 20)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.True(t, report.ReadOnly)
	assert.False(t, report.AutomaticProcessingAllowed)
	assert.False(t, report.Truncated)
	require.Len(t, report.Items, 5)
	assert.Equal(t, AsyncRefundReconciliationSummary{
		Total:             5,
		DurableRecords:    1,
		HistoricalLogOnly: 1,
		MissingEvidence:   1,
		AmbiguousLogs:     1,
		UnknownSource:     1,
		ManualReview:      3,
	}, report.Summary)

	byTaskId := make(map[string]AsyncRefundReconciliationItem, len(report.Items))
	for _, item := range report.Items {
		byTaskId[item.TaskId] = item
		assert.False(t, item.AutomaticAction)
	}
	assert.Equal(t, AsyncRefundEvidenceDurableRecord, byTaskId["durable-task"].Evidence)
	assert.NotZero(t, byTaskId["durable-task"].RefundRecordId)
	assert.Equal(t, AsyncRefundEvidenceHistoricalLog, byTaskId["historical-log-task"].Evidence)
	assert.Len(t, byTaskId["historical-log-task"].RefundLogIds, 1)
	assert.Equal(t, AsyncRefundEvidenceAmbiguous, byTaskId["ambiguous-log-task"].Evidence)
	assert.True(t, byTaskId["ambiguous-log-task"].ManualReview)
	assert.Len(t, byTaskId["ambiguous-log-task"].RefundLogIds, 2)
	assert.Equal(t, AsyncRefundEvidenceMissing, byTaskId["missing-midjourney-task"].Evidence)
	assert.True(t, byTaskId["missing-midjourney-task"].ManualReview)
	assert.Equal(t, AsyncRefundEvidenceUnknownSource, byTaskId["unknown-source-task"].Evidence)
	assert.Equal(t, string(TaskStatusInProgress), byTaskId["unknown-source-task"].TaskStatus)
	assert.True(t, byTaskId["unknown-source-task"].ManualReview)
}

func TestBuildAsyncRefundReconciliation_RejectsUnboundedInputs(t *testing.T) {
	_, err := BuildAsyncRefundReconciliation(0, 100, 20)
	require.Error(t, err)
	_, err = BuildAsyncRefundReconciliation(100, 200, 501)
	require.Error(t, err)
}

func TestLoadAsyncRefundLogsUsesClickHouseEventEvidence(t *testing.T) {
	db := openLogMigrationTestDB(t)
	require.NoError(t, db.Exec(`
		CREATE TABLE logs (
			id INTEGER,
			event_id TEXT,
			user_id INTEGER,
			created_at INTEGER,
			type INTEGER,
			quota INTEGER,
			other TEXT
		)`).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO logs (id, event_id, user_id, created_at, type, quota, other)
		VALUES
			(0, 'event-a', 82, 131, 6, 200, '{"task_id":"same-task"}'),
			(0, 'event-b', 82, 132, 6, 200, '{"task_id":"same-task"}')`).Error)

	previousDB := LOG_DB
	previousType := common.LogSqlType
	LOG_DB = db
	common.LogSqlType = common.DatabaseTypeClickHouse
	t.Cleanup(func() {
		LOG_DB = previousDB
		common.LogSqlType = previousType
		initCol()
	})
	initCol()

	evidenceByMatch, truncated, err := loadAsyncRefundLogs(100, 200)
	require.NoError(t, err)
	assert.False(t, truncated)
	evidence := evidenceByMatch[asyncRefundLogMatchKey("same-task", 82, 200)]
	assert.Empty(t, evidence.Ids)
	assert.Equal(t, []string{"event-a", "event-b"}, evidence.EventIds)
	assert.Equal(t, 2, evidence.Count())
}
