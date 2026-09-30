package service

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

// qualityErrorFromFilter 走真实的 ApplyResponseQualityFilter 造拦截错误，保证 skipRetry 等构造细节与生产一致。
func qualityErrorFromFilter(t *testing.T, code types.ErrorCode, statusCode int) *types.NewAPIError {
	t.Helper()
	cfg := operation_setting.GetResponseQualitySetting()
	prev := *cfg
	t.Cleanup(func() { *cfg = prev })
	cfg.ApplyAllChannels = true
	cfg.BlockApologyEnabled = code == types.ErrorCodeResponseQualityApology
	cfg.BlockLowTokenEnabled = code == types.ErrorCodeResponseQualityLowToken
	cfg.ApologyStatusCode = statusCode
	cfg.LowTokenStatusCode = statusCode
	cfg.LowTokenThreshold = 300

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.QualityInspect.Text = "I'm sorry, I cannot assist with that request."
	if code == types.ErrorCodeResponseQualityLowToken {
		info.QualityInspect.Text = "A normal answer that is simply too short."
	}
	err := ApplyResponseQualityFilter(nil, info, &dto.Usage{CompletionTokens: 12})
	require.NotNil(t, err)
	require.Equal(t, code, err.GetErrorCode())
	require.Equal(t, statusCode, err.StatusCode)
	require.True(t, types.IsSkipRetryError(err), "前置事实：拦截错误带 skipRetry（不换渠道）")
	return err
}

func withCountableStatusCodes(t *testing.T, raw string) {
	t.Helper()
	cfg := operation_setting.GetChannelHealthConfig()
	prev := cfg.CountableStatusCodes
	cfg.CountableStatusCodes = raw
	t.Cleanup(func() { cfg.CountableStatusCodes = prev })
}

// 质量闸门拦截按它返回的状态码计入渠道 streak，不因为带 skipRetry 被排除；普通 skipRetry 错误仍不计入。
func TestIsCountableError_ResponseQualityByStatusCode(t *testing.T) {
	cfg := &operation_setting.ChannelHealthConfig{Count429AsError: false}
	cfg429 := &operation_setting.ChannelHealthConfig{Count429AsError: true}

	t.Run("兜底模式：5xx 计入，4xx 不计入，429 看开关", func(t *testing.T) {
		withCountableStatusCodes(t, "")
		require.True(t, isCountableError(qualityErrorFromFilter(t, types.ErrorCodeResponseQualityApology, 503), cfg))
		require.True(t, isCountableError(qualityErrorFromFilter(t, types.ErrorCodeResponseQualityLowToken, 503), cfg))
		require.False(t, isCountableError(qualityErrorFromFilter(t, types.ErrorCodeResponseQualityApology, 400), cfg))
		require.False(t, isCountableError(qualityErrorFromFilter(t, types.ErrorCodeResponseQualityApology, 429), cfg))
		require.True(t, isCountableError(qualityErrorFromFilter(t, types.ErrorCodeResponseQualityApology, 429), cfg429))
	})

	t.Run("白名单模式：只认列表内状态码", func(t *testing.T) {
		withCountableStatusCodes(t, "500-599")
		require.True(t, isCountableError(qualityErrorFromFilter(t, types.ErrorCodeResponseQualityLowToken, 503), cfg))
		require.False(t, isCountableError(qualityErrorFromFilter(t, types.ErrorCodeResponseQualityApology, 403), cfg))
	})

	t.Run("对照组：普通 skipRetry 错误仍不计入", func(t *testing.T) {
		withCountableStatusCodes(t, "")
		plain := types.NewErrorWithStatusCode(errors.New("oversized"), types.ErrorCodeBadResponseStatusCode, 503, types.ErrOptionWithSkipRetry())
		require.False(t, isCountableError(plain, cfg))
	})
}

// 连续两次道歉拦截 → L1，优先级 110 → 109。修复前拦截错误被 skipRetry 排除，渠道永远停在 L0。
func TestStateMachine_ResponseQualityErrorDemotes(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.DegradeThreshold = 2
		cfg.BaseDegradeThreshold = 2
		cfg.LevelStepThreshold = 3
		cfg.L2Threshold = 5
		cfg.DisableThreshold = 0
		cfg.UpgradeThreshold = 20
		cfg.CountableStatusCodes = ""
	})
	makeTestChannel(t, model.DB, 41, 110, 20)
	qerr := qualityErrorFromFilter(t, types.ErrorCodeResponseQualityApology, 503)

	for i := 0; i < 2; i++ {
		RecordChannelResult(41, "sk-test", qerr, -1)
	}

	got := reloadChannel(t, 41)
	require.Equal(t, 1, common.DerefIntOr(got.DegradeLevel, 0))
	require.Equal(t, int64(109), common.DerefInt64Or(got.Priority, 0))
	require.Equal(t, common.ChannelStatusEnabled, got.Status)
}

// 闸门状态码可配成 401/403，但上游其实回了 200，不算 key 失效：
// 走 key 失效分支的话单 key 渠道会被直接停用、绕过降级。要求它只推降级连击。
func TestStateMachine_ResponseQuality403NotKeyFatal(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.DegradeThreshold = 2
		cfg.BaseDegradeThreshold = 2
		cfg.LevelStepThreshold = 3
		cfg.L2Threshold = 5
		cfg.DisableThreshold = 0
		cfg.UpgradeThreshold = 20
		cfg.CountableStatusCodes = "401-407,409-599"
	})
	makeTestChannel(t, model.DB, 42, 110, 20)
	qerr := qualityErrorFromFilter(t, types.ErrorCodeResponseQualityApology, 403)

	for i := 0; i < 2; i++ {
		RecordChannelResult(42, "sk-test", qerr, -1)
	}

	got := reloadChannel(t, 42)
	require.Equal(t, common.ChannelStatusEnabled, got.Status, "不应被当成 key 失效停掉渠道")
	require.Equal(t, 1, common.DerefIntOr(got.DegradeLevel, 0))
}
