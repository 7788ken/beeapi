package controller

import (
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 渠道列表「可用性」列扫的是 logs 里 type=2/5 的记录。定时测试成功会写 consume log，
// 失败必须补一条 error log，否则长期挂掉又没有真实流量的渠道永远是空图，管理员看不出它已不可用。
// token_name 按被测那一刻的渠道状态打标：启用态「模型测试」，禁用态「模型测试-停用」
// （分组广场靠它永久排除禁用期探活，见 service/group_uptime.go）。
func TestRecordChannelTestFailureWritesErrorLog(t *testing.T) {
	restore := setupChannelTestLogDB(t)
	defer restore()

	c, _ := gin.CreateTestContext(nil)
	c.Set("group", "default")
	channel := &model.Channel{Id: 315, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled}

	recordChannelTestFailure(testResult{context: c, localErr: errors.New("connection refused")},
		channel, "claude-opus-5", false, 3)

	disabled := &model.Channel{Id: 316, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusAutoDisabled}
	recordChannelTestFailure(testResult{context: c, localErr: errors.New("still down")},
		disabled, "claude-opus-5", false, 3)

	var logs []model.Log
	require.NoError(t, model.LOG_DB.Order("channel_id asc").Find(&logs).Error)
	require.Len(t, logs, 2)
	require.Equal(t, model.LogTypeError, logs[0].Type)
	require.Equal(t, 315, logs[0].ChannelId)
	require.Equal(t, "claude-opus-5", logs[0].ModelName)
	require.Equal(t, model.ChannelTestTokenName, logs[0].TokenName)
	require.Contains(t, logs[0].Content, "connection refused")
	require.Equal(t, 316, logs[1].ChannelId)
	require.Equal(t, model.ChannelTestOfflineTokenName, logs[1].TokenName)
}

// 测活日志的 token_name 只看被测那一刻的渠道状态：只有启用态是「模型测试」，
// 手动禁用、自动禁用（恢复探活）一律「模型测试-停用」。
func TestChannelTestLogTokenName(t *testing.T) {
	require.Equal(t, model.ChannelTestTokenName,
		channelTestLogTokenName(&model.Channel{Status: common.ChannelStatusEnabled}))
	require.Equal(t, model.ChannelTestOfflineTokenName,
		channelTestLogTokenName(&model.Channel{Status: common.ChannelStatusManuallyDisabled}))
	require.Equal(t, model.ChannelTestOfflineTokenName,
		channelTestLogTokenName(&model.Channel{Status: common.ChannelStatusAutoDisabled}))
	require.Equal(t, model.ChannelTestOfflineTokenName, channelTestLogTokenName(nil))
}

// 渠道类型不支持测试 / 本地取数失败时 testChannel 返回 context=nil：这是「没测成」，
// 不是渠道不可用，计进去会凭空拉低可用率。
func TestRecordChannelTestFailureSkipsWhenNotActuallyTested(t *testing.T) {
	restore := setupChannelTestLogDB(t)
	defer restore()

	c, _ := gin.CreateTestContext(nil)
	channel := &model.Channel{Id: 316, Type: constant.ChannelTypeMidjourney}

	recordChannelTestFailure(testResult{localErr: errors.New("channel test is not supported")},
		channel, "midjourney", false, 0)
	recordChannelTestFailure(testResult{context: c}, channel, "midjourney", false, 0)

	var count int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestResolveChannelTestModel(t *testing.T) {
	explicit := "gpt-5.5"
	channelModel := "claude-sonnet-5"

	require.Equal(t, explicit, resolveChannelTestModel(&model.Channel{TestModel: &channelModel}, " "+explicit+" "))
	require.Equal(t, channelModel, resolveChannelTestModel(&model.Channel{TestModel: &channelModel}, ""))

	empty := ""
	withModels := &model.Channel{TestModel: &empty, Models: "gemini-3-pro,gpt-5.5"}
	require.Equal(t, "gemini-3-pro", resolveChannelTestModel(withModels, ""))
	require.Equal(t, "gpt-4o-mini", resolveChannelTestModel(&model.Channel{}, ""))
}

func setupChannelTestLogDB(t *testing.T) func() {
	t.Helper()
	previousLogDB := model.LOG_DB
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.User{}))
	model.LOG_DB = db
	model.DB = db
	common.RedisEnabled = false
	return func() {
		model.LOG_DB = previousLogDB
		model.DB = previousDB
		common.RedisEnabled = previousRedis
	}
}
