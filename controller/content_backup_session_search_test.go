package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 会话搜索的装配测试（设计文档 9.3：相同会话值不同用户不混档，跨日期可连续查询）。
// 会话哈希只对 (source, value) 取摘要，不掺用户身份 —— 两个用户用同一个会话值
// 落库的 session_hash 完全相同。不混档全靠接口强制按 user_id 收窄，
// 所以这道约束必须在 HTTP 层锁住，store 层的用例锁不住"handler 有没有把 user_id 传下去"。
func seedSessionJob(t *testing.T, db *gorm.DB, jobID string, userID int, source, sessionValue string, createdAt int64) {
	t.Helper()
	job := model.ContentBackupJob{
		JobID:          jobID,
		RequestID:      "req-" + jobID,
		UserID:         userID,
		ChannelID:      12,
		Model:          "mock-model",
		Endpoint:       "/v1/chat/completions",
		CreatedAt:      createdAt,
		Status:         "uploaded",
		HTTPStatus:     200,
		TerminalReason: "complete",
		SessionSource:  source,
		SessionHash:    contentbackup.SessionKey(source, sessionValue),
		SessionHint:    "hint-" + jobID,
	}
	require.NoError(t, db.Create(&job).Error)
}

func callSessionSearch(t *testing.T, req dto.ContentBackupSessionSearchRequest) *httptest.ResponseRecorder {
	t.Helper()
	blob, err := common.Marshal(req)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/content_backup/jobs/search-session", bytes.NewReader(blob))
	c.Request.Header.Set("Content-Type", "application/json")
	ContentBackupSearchSession(c)
	return rec
}

func sessionSearchJobIDs(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	data := contentBackupJobDataOf(t, rec.Body.String())
	items, ok := data["items"].([]any)
	require.True(t, ok, "响应结构异常: %s", rec.Body.String())
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.(map[string]any)["job_id"].(string))
	}
	return ids
}

func TestContentBackupSessionSearchDoesNotMixUsers(t *testing.T) {
	db := setupContentBackupJobDTOTestDB(t)

	const shared = "shared-session-value"
	// 两个用户用同一个会话值：落库的 session_hash 是同一个。
	seedSessionJob(t, db, "job-user-a", 203, contentbackup.SessionSourceUser, shared, 1789600000)
	seedSessionJob(t, db, "job-user-b", 777, contentbackup.SessionSourceUser, shared, 1789600001)
	require.Equal(t,
		contentbackup.SessionKey(contentbackup.SessionSourceUser, shared),
		contentbackup.SessionKey(contentbackup.SessionSourceUser, shared),
		"前提：会话哈希不含用户身份")

	rec := callSessionSearch(t, dto.ContentBackupSessionSearchRequest{UserID: 203, SessionValue: shared})
	require.Equal(t, http.StatusOK, rec.Code, "响应体: %s", rec.Body.String())
	require.Equal(t, []string{"job-user-a"}, sessionSearchJobIDs(t, rec), "查到了别的用户的归档")
}

// 不带 user_id 必须直接拒绝，否则等于开放跨用户的会话值反查。
func TestContentBackupSessionSearchRequiresUserID(t *testing.T) {
	db := setupContentBackupJobDTOTestDB(t)
	seedSessionJob(t, db, "job-user-a", 203, contentbackup.SessionSourceUser, "shared-session-value", 1789600000)

	rec := callSessionSearch(t, dto.ContentBackupSessionSearchRequest{SessionValue: "shared-session-value"})
	require.Equal(t, http.StatusBadRequest, rec.Code, "响应体: %s", rec.Body.String())
}

// 跨午夜：同一用户同一会话值的两条记录分别落在前一天和次日，
// 一次查询必须连续返回，不能被任何隐式的"当天"窗口切断。
func TestContentBackupSessionSearchSpansMidnight(t *testing.T) {
	db := setupContentBackupJobDTOTestDB(t)

	const value = "cross-midnight-session"
	// 2026-09-16 23:59:30 UTC 与 2026-09-17 00:00:30 UTC
	seedSessionJob(t, db, "job-before-midnight", 203, contentbackup.SessionSourceUser, value, 1789775970)
	seedSessionJob(t, db, "job-after-midnight", 203, contentbackup.SessionSourceUser, value, 1789776030)

	rec := callSessionSearch(t, dto.ContentBackupSessionSearchRequest{UserID: 203, SessionValue: value})
	require.Equal(t, http.StatusOK, rec.Code, "响应体: %s", rec.Body.String())
	require.ElementsMatch(t,
		[]string{"job-before-midnight", "job-after-midnight"},
		sessionSearchJobIDs(t, rec),
		"跨午夜的两条记录没有被一次查全")
}

// 只给会话来源时，跨来源的同值记录不能被混进来。
func TestContentBackupSessionSearchScopedBySource(t *testing.T) {
	db := setupContentBackupJobDTOTestDB(t)

	const value = "same-value-two-sources"
	seedSessionJob(t, db, "job-src-user", 203, contentbackup.SessionSourceUser, value, 1789600000)
	seedSessionJob(t, db, "job-src-cache", 203, contentbackup.SessionSourcePromptCacheKey, value, 1789600001)

	source := contentbackup.SessionSourceUser
	rec := callSessionSearch(t, dto.ContentBackupSessionSearchRequest{
		UserID: 203, SessionValue: value, SessionSource: &source,
	})
	require.Equal(t, http.StatusOK, rec.Code, "响应体: %s", rec.Body.String())
	require.Equal(t, []string{"job-src-user"}, sessionSearchJobIDs(t, rec), "指定来源后混入了其它来源")
}
