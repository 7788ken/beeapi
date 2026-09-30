package controller

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 装配级：relay 失败路径把质量闸门拦截交给渠道健康状态机，且不走单次停渠道。
// 修复前 processChannelError 对拦截错误整体跳过 RecordChannelResult，渠道错误率再高也停在 L0。
func TestProcessChannelError_ResponseQualityFeedsDegradeStreak(t *testing.T) {
	db := setupModelRemovalTestDB(t)

	health := operation_setting.GetChannelHealthConfig()
	prevHealth := *health
	quality := operation_setting.GetResponseQualitySetting()
	prevQuality := *quality
	prevAutoDisable := common.AutomaticDisableChannelEnabled
	prevDisableCodes := operation_setting.AutomaticDisableStatusCodesToString()
	prevErrorLog := constant.ErrorLogEnabled
	t.Cleanup(func() {
		*health = prevHealth
		*quality = prevQuality
		common.AutomaticDisableChannelEnabled = prevAutoDisable
		_ = operation_setting.AutomaticDisableStatusCodesFromString(prevDisableCodes)
		constant.ErrorLogEnabled = prevErrorLog
	})

	health.Enabled = true
	health.BaseDegradeThreshold = 1 // 单次命中即降一级：只起一个后台任务，避免并发写 SQLite
	health.LevelStepThreshold = 5
	health.MaxDegradeLevel = 10
	health.DisableThreshold = 0
	health.DemoteCooldownSec = 0
	health.NotifyOnDegrade = false
	health.CountableStatusCodes = ""
	// 503 同时在停渠道状态码表里：拦截错误仍不得走单次停渠道
	common.AutomaticDisableChannelEnabled = true
	require.NoError(t, operation_setting.AutomaticDisableStatusCodesFromString("401,503"))
	constant.ErrorLogEnabled = false

	quality.ApplyAllChannels = true
	quality.BlockApologyEnabled = true
	quality.ApologyStatusCode = 503

	priority := int64(110)
	weight := uint(20)
	autoBan := 1
	ch := &model.Channel{
		Type:     1,
		Name:     "quality-health",
		Key:      "sk-test",
		Status:   common.ChannelStatusEnabled,
		Priority: &priority,
		Weight:   &weight,
		AutoBan:  &autoBan,
		Models:   "claude-sonnet-4-5",
		Group:    "default",
	}
	require.NoError(t, db.Create(ch).Error)
	service.ClearChannelHealthRuntime(ch.Id)
	t.Cleanup(func() { service.ClearChannelHealthRuntime(ch.Id) })

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.QualityInspect.Text = "I'm sorry, I cannot assist with that request."
	qerr := service.ApplyResponseQualityFilter(nil, info, &dto.Usage{CompletionTokens: 12})
	require.NotNil(t, qerr)
	require.Equal(t, types.ErrorCodeResponseQualityApology, qerr.GetErrorCode())
	require.False(t, service.ShouldDisableChannel(qerr), "拦截错误不走单次停渠道")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	channelError := types.ChannelError{ChannelId: ch.Id, ChannelType: ch.Type, ChannelName: ch.Name, UsingKey: "sk-test", AutoBan: true}
	processChannelError(c, channelError, qerr)

	require.Eventually(t, func() bool {
		var cur model.Channel
		if err := db.First(&cur, "id = ?", ch.Id).Error; err != nil {
			return false
		}
		return common.DerefIntOr(cur.DegradeLevel, 0) == 1
	}, 5*time.Second, 20*time.Millisecond, "拦截错误应计入降级连击")

	got := reloadChannel(t, db, ch.Id)
	require.Equal(t, int64(109), common.DerefInt64Or(got.Priority, 0))
	require.Equal(t, common.ChannelStatusEnabled, got.Status)
}
