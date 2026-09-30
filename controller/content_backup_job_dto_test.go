package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 归档任务对外字段的装配测试。库里存了 observed_bytes / content_type，
// 前端抽屉也留了「请求字节 captured / observed」「内容类型」四个格子，
// 但接口层的字段映射漏掉了这四个 —— 前端于是永远显示 "-"。
// 客查判"正文是不是被截断"靠的正是 captured 与 observed 的差值：
// 恒定的 "-" 属于设计文档 9.3 明令禁止的"假 0/假成功"。
func setupContentBackupJobDTOTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(model.ContentBackupModels()...))

	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// 一条被截断的归档：captured 远小于 observed，两侧内容类型也不同。
func seedTruncatedContentBackupJob(t *testing.T, db *gorm.DB) model.ContentBackupJob {
	t.Helper()
	job := model.ContentBackupJob{
		JobID:                 "job-truncated-1",
		RequestID:             "req-truncated-1",
		UserID:                10086,
		ChannelID:             12,
		Model:                 "mock-model",
		Endpoint:              "/v1/chat/completions",
		CreatedAt:             1789600000,
		Status:                "uploaded",
		Stream:                true,
		HTTPStatus:            200,
		TerminalReason:        "complete",
		RequestContentType:    "application/json",
		RequestCapturedBytes:  1024,
		RequestObservedBytes:  4096,
		RequestTruncated:      true,
		ResponseContentType:   "text/event-stream",
		ResponseCapturedBytes: 2048,
		ResponseObservedBytes: 9000,
		ResponseTruncated:     true,
	}
	require.NoError(t, db.Create(&job).Error)
	return job
}

func contentBackupJobDataOf(t *testing.T, body string) map[string]any {
	t.Helper()
	var payload struct {
		Success bool           `json:"success"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, common.UnmarshalJsonStr(body, &payload), "响应体: %s", body)
	require.True(t, payload.Success, "响应体: %s", body)
	return payload.Data
}

func requireObservedFields(t *testing.T, job map[string]any, where string) {
	t.Helper()
	require.EqualValues(t, 4096, job["request_observed_bytes"],
		"%s 缺少请求实际字节数，前端只能显示 '-'", where)
	require.EqualValues(t, 9000, job["response_observed_bytes"],
		"%s 缺少响应实际字节数，截断证据不可见", where)
	require.Equal(t, "application/json", job["request_content_type"],
		"%s 缺少请求内容类型", where)
	require.Equal(t, "text/event-stream", job["response_content_type"],
		"%s 缺少响应内容类型", where)
	// captured 必须同时在场，否则 captured/observed 的对比无从谈起。
	require.EqualValues(t, 1024, job["request_captured_bytes"], "%s 缺少请求采集字节数", where)
	require.EqualValues(t, 2048, job["response_captured_bytes"], "%s 缺少响应采集字节数", where)
}

// 详情接口（抽屉数据源）必须给出 observed 与 content_type。
func TestContentBackupJobDetailExposesObservedBytesAndContentType(t *testing.T) {
	db := setupContentBackupJobDTOTestDB(t)
	seeded := seedTruncatedContentBackupJob(t, db)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/content_backup/jobs/"+seeded.JobID, nil)
	c.Params = gin.Params{{Key: "job_id", Value: seeded.JobID}}
	ContentBackupJobDetail(c)

	require.Equal(t, http.StatusOK, rec.Code, "响应体: %s", rec.Body.String())
	requireObservedFields(t, contentBackupJobDataOf(t, rec.Body.String()), "详情接口")
}

// 列表接口与详情共用同一份字段映射，一起锁住，避免只补详情。
func TestContentBackupJobsListExposesObservedBytesAndContentType(t *testing.T) {
	db := setupContentBackupJobDTOTestDB(t)
	seedTruncatedContentBackupJob(t, db)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/content_backup/jobs", nil)
	ContentBackupJobs(c)

	require.Equal(t, http.StatusOK, rec.Code, "响应体: %s", rec.Body.String())
	data := contentBackupJobDataOf(t, rec.Body.String())
	items, ok := data["items"].([]any)
	require.True(t, ok, "列表结构异常: %s", rec.Body.String())
	require.Len(t, items, 1)
	requireObservedFields(t, items[0].(map[string]any), "列表接口")
}
