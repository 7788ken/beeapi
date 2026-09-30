package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 重试接口的装配测试（设计文档 9.3：failed 可选中重试，processing 不重复启动，
// 部分失败有逐条反馈）。一批里混着能重试的和不能重试的，接口必须逐条给出
// queued/skipped/failed 与原因，而不是整批成功或整批报错 —— 整批口径会让
// 运维以为点了重试就都排上了队。
func seedRetryJob(t *testing.T, db *gorm.DB, jobID, status, lastErrorCode string) {
	t.Helper()
	job := model.ContentBackupJob{
		JobID:         jobID,
		RequestID:     "req-" + jobID,
		UserID:        203,
		ChannelID:     12,
		Model:         "mock-model",
		Endpoint:      "/v1/chat/completions",
		CreatedAt:     1789600000,
		Status:        status,
		LastErrorCode: lastErrorCode,
	}
	require.NoError(t, db.Create(&job).Error)
}

func callContentBackupRetry(t *testing.T, jobIDs []string) *httptest.ResponseRecorder {
	t.Helper()
	blob, err := common.Marshal(dto.ContentBackupRetryRequest{JobIDs: jobIDs})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/content_backup/jobs/retry", bytes.NewReader(blob))
	c.Request.Header.Set("Content-Type", "application/json")
	ContentBackupRetry(c)
	return rec
}

func retryResultsOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]map[string]any {
	t.Helper()
	data := contentBackupJobDataOf(t, rec.Body.String())
	rows, ok := data["results"].([]any)
	require.True(t, ok, "响应结构异常: %s", rec.Body.String())
	out := map[string]map[string]any{}
	for _, row := range rows {
		item := row.(map[string]any)
		out[item["job_id"].(string)] = item
	}
	return out
}

func TestContentBackupRetryReportsEachJobSeparately(t *testing.T) {
	db := setupContentBackupJobDTOTestDB(t)

	seedRetryJob(t, db, "job-failed", model.ContentBackupStatusFailed, "ftps_timeout")
	seedRetryJob(t, db, "job-processing", model.ContentBackupStatusProcessing, "")
	seedRetryJob(t, db, "job-pending", model.ContentBackupStatusPending, "")
	seedRetryJob(t, db, "job-uploaded", model.ContentBackupStatusUploaded, "")

	ids := []string{"job-failed", "job-processing", "job-pending", "job-uploaded", "job-missing"}
	rec := callContentBackupRetry(t, ids)
	require.Equal(t, http.StatusOK, rec.Code, "响应体: %s", rec.Body.String())

	results := retryResultsOf(t, rec)
	require.Len(t, results, len(ids), "逐条反馈缺条目: %s", rec.Body.String())

	require.Equal(t, model.ContentBackupRetryQueued, results["job-failed"]["result"], "failed 应可重试")
	// processing 正在被守护进程占用，重排会造成同一份正文被重复上传。
	require.Equal(t, model.ContentBackupRetrySkipped, results["job-processing"]["result"])
	require.Equal(t, "processing", results["job-processing"]["reason"])
	require.Equal(t, model.ContentBackupRetrySkipped, results["job-pending"]["result"])
	require.Equal(t, "already_queued", results["job-pending"]["reason"])
	require.Equal(t, model.ContentBackupRetrySkipped, results["job-uploaded"]["result"])
	require.Equal(t, "already_uploaded", results["job-uploaded"]["reason"])
	require.Equal(t, model.ContentBackupRetryFailed, results["job-missing"]["result"])
	require.Equal(t, "not_found", results["job-missing"]["reason"])

	// 真的排进队才算重试成功：状态必须落库为 pending。
	var requeued model.ContentBackupJob
	require.NoError(t, db.Where("job_id = ?", "job-failed").First(&requeued).Error)
	require.Equal(t, model.ContentBackupStatusPending, requeued.Status, "重试没有真的把任务排回队列")

	// 被跳过的不能被顺手改状态。
	var untouched model.ContentBackupJob
	require.NoError(t, db.Where("job_id = ?", "job-processing").First(&untouched).Error)
	require.Equal(t, model.ContentBackupStatusProcessing, untouched.Status, "processing 被重试改写了状态")
}

// 空批和超限批直接拒，不能返回"成功但一条没处理"。
func TestContentBackupRetryRejectsEmptyAndOversizedBatch(t *testing.T) {
	setupContentBackupJobDTOTestDB(t)

	rec := callContentBackupRetry(t, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, "空批应被拒绝: %s", rec.Body.String())

	oversized := make([]string, 101)
	for i := range oversized {
		oversized[i] = "job-x"
	}
	rec = callContentBackupRetry(t, oversized)
	require.Equal(t, http.StatusBadRequest, rec.Code, "超过 100 条应被拒绝: %s", rec.Body.String())
}
