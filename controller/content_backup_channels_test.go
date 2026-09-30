package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 批量翻转渠道内容备份开关的装配测试。router/content_backup_route_test.go 只证明
// 路由挂得上、鉴权拦得住，从没让 handler 碰过真数据库 —— 于是读-改-写事务里
// 「取出旧 setting」那一步用错 GORM API 也照样全绿。这里直接打 handler，
// 断言落库结果。
func setupContentBackupChannelTestDB(t *testing.T) *gorm.DB {
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
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))

	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func seedContentBackupChannel(t *testing.T, db *gorm.DB, name, setting string) *model.Channel {
	t.Helper()
	ch := &model.Channel{
		Type:    1,
		Key:     "sk-" + name,
		Status:  common.ChannelStatusEnabled,
		Name:    name,
		Models:  "mock-model",
		Group:   "default",
		BaseURL: strPtr("http://127.0.0.1:1"),
	}
	if setting != "" {
		ch.Setting = &setting
	}
	require.NoError(t, db.Create(ch).Error)
	return ch
}

func strPtr(s string) *string { return &s }

func callContentBackupUpdateChannels(t *testing.T, ids []int, enabled bool) (*httptest.ResponseRecorder, dto.ContentBackupChannelBackupRequest) {
	t.Helper()
	body := dto.ContentBackupChannelBackupRequest{ChannelIDs: ids, Enabled: enabled}
	blob, err := common.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/content_backup/channels/backup", bytes.NewReader(blob))
	c.Request.Header.Set("Content-Type", "application/json")
	ContentBackupUpdateChannels(c)
	return rec, body
}

func channelSettingsOf(t *testing.T, db *gorm.DB, id int) dto.ChannelSettings {
	t.Helper()
	var ch model.Channel
	require.NoError(t, db.First(&ch, id).Error)
	out := dto.ChannelSettings{}
	if ch.Setting != nil && *ch.Setting != "" {
		require.NoError(t, common.Unmarshal([]byte(*ch.Setting), &out))
	}
	return out
}

// 一批渠道一次切换必须真的落库，且不覆盖同一份 setting 里的其它字段
// （design doc 9.3）。此前 handler 恒返回 500 "no channel was updated"。
func TestContentBackupUpdateChannelsFlipsOnlyBackupFlag(t *testing.T) {
	db := setupContentBackupChannelTestDB(t)

	// 带其它设置的渠道：翻转后 proxy / system_prompt 必须原样保留。
	withSettings := seedContentBackupChannel(t, db, "with-settings",
		`{"proxy":"http://proxy.invalid:8080","system_prompt":"keep-me","force_format":true}`)
	// setting 为 NULL 的渠道：真实库里大量渠道从没写过 setting，
	// 读-改-写必须能处理 NULL，不能整批失败。
	nullSettings := seedContentBackupChannel(t, db, "null-settings", "")

	rec, _ := callContentBackupUpdateChannels(t, []int{withSettings.Id, nullSettings.Id}, true)
	require.Equal(t, http.StatusOK, rec.Code, "响应体: %s", rec.Body.String())

	got := channelSettingsOf(t, db, withSettings.Id)
	require.True(t, got.ContentBackupEnabled, "内容备份开关没有落库")
	require.Equal(t, "http://proxy.invalid:8080", got.Proxy, "其它 setting 字段被覆盖")
	require.Equal(t, "keep-me", got.SystemPrompt, "其它 setting 字段被覆盖")
	require.True(t, got.ForceFormat, "其它 setting 字段被覆盖")

	gotNull := channelSettingsOf(t, db, nullSettings.Id)
	require.True(t, gotNull.ContentBackupEnabled, "setting 为 NULL 的渠道没有被更新")

	// 再关回去，确认是双向的，不是只写得进 true。
	rec, _ = callContentBackupUpdateChannels(t, []int{withSettings.Id}, false)
	require.Equal(t, http.StatusOK, rec.Code, "响应体: %s", rec.Body.String())
	off := channelSettingsOf(t, db, withSettings.Id)
	require.False(t, off.ContentBackupEnabled, "关闭没有落库")
	require.Equal(t, "keep-me", off.SystemPrompt, "关闭时其它 setting 字段被覆盖")
}

// 不存在的渠道 ID 不能被算成"已更新"；整批都不存在时必须报错而不是假成功。
func TestContentBackupUpdateChannelsMissingChannelNotCounted(t *testing.T) {
	setupContentBackupChannelTestDB(t)

	rec, _ := callContentBackupUpdateChannels(t, []int{999001, 999002}, true)
	require.Equal(t, http.StatusInternalServerError, rec.Code,
		"全部 ID 都不存在时不能返回成功，响应体: %s", rec.Body.String())
}
