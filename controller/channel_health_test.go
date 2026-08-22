package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// FU-7-2 controller 层单测：API endpoint + manual takeover hook + 反弹保护
//
// 用 gin httptest + in-memory SQLite，关闭 Redis 走 sync.Map fallback。
// 每个测试独立 DB（基于 t.Name），自动 cleanup。

func setupChannelHealthControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:ctrl_%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)

	model.DB = db
	model.LOG_DB = db

	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelHealthEvent{}))

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func makeCtrlTestChannel(t *testing.T, id int, priority int64, weight uint, isMultiKey bool, status int) *model.Channel {
	t.Helper()
	autoBan := 1
	ch := &model.Channel{
		Id:       id,
		Type:     1,
		Name:     fmt.Sprintf("ctrl-test-ch-%d", id),
		Status:   status,
		Priority: &priority,
		Weight:   &weight,
		AutoBan:  &autoBan,
		Models:   "gpt-4",
		Group:    "default",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: isMultiKey,
		},
	}
	require.NoError(t, model.DB.Create(ch).Error)
	return ch
}

// newCtrlContext 构造一个带 :id param + admin user_id=1 的 gin.Context。
func newCtrlContext(t *testing.T, method, target string, paramID string, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		buf, err := common.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(buf)
	} else {
		reader = bytes.NewReader(nil)
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, reader)
	if paramID != "" {
		c.Params = []gin.Param{{Key: "id", Value: paramID}}
	}
	c.Set("id", 1) // admin
	return c, rec
}

// ─────────────────────────────────────────────────────────────────────────────
// TestRecoverChannelHealth: 单 key + multi-key 两个分支
// ─────────────────────────────────────────────────────────────────────────────

func TestRecoverChannelHealth_SingleKey_RestoresAndEnables(t *testing.T) {
	setupChannelHealthControllerTestDB(t)

	// 单 key 渠道，处于 L2 + AutoDisabled
	ch := makeCtrlTestChannel(t, 100, 8, 4, false, common.ChannelStatusAutoDisabled)
	// 模拟降级快照：original=10/20，degrade_level=2
	level := 2
	op := int64(10)
	ow := uint(20)
	ch.DegradeLevel = &level
	ch.OriginalPriority = &op
	ch.OriginalWeight = &ow
	require.NoError(t, model.DB.Save(ch).Error)

	c, rec := newCtrlContext(t, http.MethodPost, "/api/channel/100/health/recover", "100", nil)
	RecoverChannelHealth(c)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	got := reloadCtrlChannel(t, 100)
	require.Equal(t, 0, common.DerefIntOr(got.DegradeLevel, -1), "degrade_level reset to 0")
	require.Equal(t, common.ChannelStatusEnabled, got.Status, "status restored to Enabled")
	require.Equal(t, int64(10), common.DerefInt64Or(got.Priority, 0), "priority restored to original=10")
	require.Equal(t, uint(20), common.DerefUintOr(got.Weight, 0), "weight restored to original=20")
	require.Equal(t, int64(0), common.DerefInt64Or(got.OriginalPriority, -1), "original_priority cleared")
	require.Equal(t, 0, common.DerefIntOr(got.PermanentDisabled, -1), "permanent_disabled cleared")
	require.Equal(t, 0, common.DerefIntOr(got.RebounceCount, -1), "rebounce_count cleared")
}

func TestRecoverChannelHealth_MultiKey_PreservesKeyStatusList(t *testing.T) {
	setupChannelHealthControllerTestDB(t)

	// multi-key 渠道，AutoDisabled 状态，已知 3 把 key 单独失效
	ch := makeCtrlTestChannel(t, 101, 5, 10, true, common.ChannelStatusAutoDisabled)
	ch.ChannelInfo.MultiKeySize = 5
	ch.ChannelInfo.MultiKeyStatusList = map[int]int{
		0: common.ChannelStatusAutoDisabled,
		1: common.ChannelStatusAutoDisabled,
		2: common.ChannelStatusAutoDisabled,
	}
	require.NoError(t, model.DB.Save(ch).Error)

	c, rec := newCtrlContext(t, http.MethodPost, "/api/channel/101/health/recover", "101", nil)
	RecoverChannelHealth(c)
	require.Equal(t, http.StatusOK, rec.Code)

	got := reloadCtrlChannel(t, 101)
	require.Equal(t, common.ChannelStatusEnabled, got.Status, "channel-level status enabled")
	// 关键断言：multi_key_status_list 保留——已知坏 key 不被错误"恢复"
	require.Equal(t, 3, len(got.ChannelInfo.MultiKeyStatusList),
		"multi-key status list MUST be preserved (don't unmark known-bad keys)")
}

// ─────────────────────────────────────────────────────────────────────────────
// TestGetChannelHealthEvents: snapshot + 事件流格式
// ─────────────────────────────────────────────────────────────────────────────

func TestGetChannelHealthEvents_ReturnsSnapshotAndEvents(t *testing.T) {
	setupChannelHealthControllerTestDB(t)

	ch := makeCtrlTestChannel(t, 102, 9, 10, false, common.ChannelStatusEnabled)
	level := 1
	op := int64(10)
	ow := uint(20)
	ts := int64(1750000000)
	reason := "test demote reason"
	ch.DegradeLevel = &level
	ch.OriginalPriority = &op
	ch.OriginalWeight = &ow
	ch.LastDemoteAt = &ts
	ch.LastDemoteReason = &reason
	require.NoError(t, model.DB.Save(ch).Error)

	// 写两条 audit event
	require.NoError(t, model.RecordChannelHealthEvent(102, model.HealthEventDemote, 0, 1, "first demote", "auto"))
	require.NoError(t, model.RecordChannelHealthEvent(102, model.HealthEventDemote, 1, 2, "second demote", "auto"))

	c, rec := newCtrlContext(t, http.MethodGet, "/api/channel/102/health/events?days=30&limit=100", "102", nil)
	GetChannelHealthEvents(c)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	body := rec.Body.String()
	require.Contains(t, body, `"degrade_level":1`)
	require.Contains(t, body, `"original_priority":10`)
	require.Contains(t, body, `"original_weight":20`)
	require.Contains(t, body, `"last_demote_at":1750000000`)
	require.Contains(t, body, `"last_demote_reason":"test demote reason"`)
	require.Contains(t, body, `"event_type":"demote"`)
	require.Contains(t, body, `"second demote"`)
}

func TestGetChannelHealthEvents_NotFound(t *testing.T) {
	setupChannelHealthControllerTestDB(t)
	c, rec := newCtrlContext(t, http.MethodGet, "/api/channel/9999/health/events", "9999", nil)
	GetChannelHealthEvents(c)
	// 项目用 common.ApiError 统一返回 200 + success:false（不是 4xx）
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"success":false`)
	require.Contains(t, rec.Body.String(), "record not found")
}

// ─────────────────────────────────────────────────────────────────────────────
// TestMaybeResetChannelHealthOnManualEdit: 接管 hook 行为
// ─────────────────────────────────────────────────────────────────────────────

func TestMaybeResetChannelHealthOnManualEdit_ClearsSnapshotWhenPriorityChanged(t *testing.T) {
	setupChannelHealthControllerTestDB(t)
	enableHealthForTest(t)

	// 渠道当前在 L1
	level := 1
	op := int64(10)
	ow := uint(20)
	ts := int64(1750000000)
	rebounce := 1
	ch := makeCtrlTestChannel(t, 103, 9, 10, false, common.ChannelStatusEnabled)
	ch.DegradeLevel = &level
	ch.OriginalPriority = &op
	ch.OriginalWeight = &ow
	ch.LastDisabledAt = &ts
	ch.RebounceCount = &rebounce
	require.NoError(t, model.DB.Save(ch).Error)

	// 用户改了 priority（9 → 7）
	origin, err := model.GetChannelById(103, false)
	require.NoError(t, err)
	newPriority := int64(7)
	updated := *origin
	updated.Priority = &newPriority

	c, _ := newCtrlContext(t, http.MethodPut, "/api/channel/", "", nil)
	maybeResetChannelHealthOnManualEdit(c, origin, &updated)

	got := reloadCtrlChannel(t, 103)
	require.Equal(t, 0, common.DerefIntOr(got.DegradeLevel, -1), "degrade_level cleared")
	require.Equal(t, int64(0), common.DerefInt64Or(got.OriginalPriority, -1), "original_priority cleared")
	require.Equal(t, int64(0), common.DerefInt64Or(got.LastDisabledAt, -1), "last_disabled_at cleared (FU-1)")
	require.Equal(t, 0, common.DerefIntOr(got.RebounceCount, -1), "rebounce_count cleared")
}

func TestMaybeResetChannelHealthOnManualEdit_NoOpWhenPriorityUnchanged(t *testing.T) {
	setupChannelHealthControllerTestDB(t)
	enableHealthForTest(t)

	level := 1
	op := int64(10)
	ow := uint(20)
	ch := makeCtrlTestChannel(t, 104, 9, 10, false, common.ChannelStatusEnabled)
	ch.DegradeLevel = &level
	ch.OriginalPriority = &op
	ch.OriginalWeight = &ow
	require.NoError(t, model.DB.Save(ch).Error)

	// 用户没改 priority/weight，只改了 name 之类
	origin, err := model.GetChannelById(104, false)
	require.NoError(t, err)
	updated := *origin // priority/weight 不变

	c, _ := newCtrlContext(t, http.MethodPut, "/api/channel/", "", nil)
	maybeResetChannelHealthOnManualEdit(c, origin, &updated)

	got := reloadCtrlChannel(t, 104)
	require.Equal(t, 1, common.DerefIntOr(got.DegradeLevel, -1), "degrade_level NOT cleared (priority unchanged)")
	require.Equal(t, int64(10), common.DerefInt64Or(got.OriginalPriority, -1), "original_priority preserved")
}

func TestMaybeResetChannelHealthOnManualEdit_NoOpWhenChannelHealthy(t *testing.T) {
	setupChannelHealthControllerTestDB(t)
	enableHealthForTest(t)

	// 健康渠道（L0 + 没锁死 + 没禁用历史）
	ch := makeCtrlTestChannel(t, 105, 9, 10, false, common.ChannelStatusEnabled)
	require.NoError(t, model.DB.Save(ch).Error)

	origin, err := model.GetChannelById(105, false)
	require.NoError(t, err)
	newPriority := int64(15)
	updated := *origin
	updated.Priority = &newPriority

	c, _ := newCtrlContext(t, http.MethodPut, "/api/channel/", "", nil)
	maybeResetChannelHealthOnManualEdit(c, origin, &updated)

	got := reloadCtrlChannel(t, 105)
	require.Equal(t, 0, common.DerefIntOr(got.DegradeLevel, -1))
	// priority/weight 不被 hook 改（hook 不动这两个字段，调用方 channel.Update 才改）
}

// ─────────────────────────────────────────────────────────────────────────────
// 工具函数
// ─────────────────────────────────────────────────────────────────────────────

func reloadCtrlChannel(t *testing.T, id int) *model.Channel {
	t.Helper()
	var ch model.Channel
	require.NoError(t, model.DB.First(&ch, id).Error)
	return &ch
}

func enableHealthForTest(t *testing.T) {
	t.Helper()
	cfg := operation_setting.GetChannelHealthConfig()
	original := *cfg
	cfg.Enabled = true
	t.Cleanup(func() { *cfg = original })
}
