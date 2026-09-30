package controller

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/stretchr/testify/require"
)

func TestIsUpstreamModelMissingError(t *testing.T) {
	tests := []struct {
		name        string
		err         *types.NewAPIError
		originModel string
		want        bool
	}{
		{
			name:        "404且回显模型名",
			err:         types.NewErrorWithStatusCode(errors.New("The model `gpt-5x` does not exist"), types.ErrorCodeBadResponseStatusCode, 404),
			originModel: "gpt-5x",
			want:        true,
		},
		{
			name:        "404关键词命中但未回显模型名",
			err:         types.NewErrorWithStatusCode(errors.New("model not found"), types.ErrorCodeBadResponseStatusCode, 404),
			originModel: "gpt-5x",
			want:        true,
		},
		{
			name:        "非404但回显模型名且关键词命中",
			err:         types.NewErrorWithStatusCode(errors.New("invalid model: claude-x"), types.ErrorCodeBadResponseStatusCode, 400),
			originModel: "claude-x",
			want:        true,
		},
		{
			name:        "非404无关键词仅回显模型名",
			err:         types.NewErrorWithStatusCode(errors.New("bad request for claude-x"), types.ErrorCodeBadResponseStatusCode, 400),
			originModel: "claude-x",
			want:        false,
		},
		{
			name:        "404无关键词且未回显模型名",
			err:         types.NewErrorWithStatusCode(errors.New("endpoint not found"), types.ErrorCodeBadResponseStatusCode, 404),
			originModel: "gpt-5x",
			want:        false,
		},
		{
			name:        "5xx带模型名不算模型缺失",
			err:         types.NewErrorWithStatusCode(errors.New("internal error while serving gpt-5x"), types.ErrorCodeBadResponseStatusCode, 500),
			originModel: "gpt-5x",
			want:        false,
		},
		{
			name:        "channel前缀错误不算",
			err:         types.NewErrorWithStatusCode(errors.New("channel: model `gpt-5x` does not exist"), types.ErrorCode("channel:test"), 404),
			originModel: "gpt-5x",
			want:        false,
		},
		{
			name:        "网关侧本站未配置模型不算",
			err:         types.NewErrorWithStatusCode(errors.New("no model gpt-5x"), types.ErrorCodeModelNotFound, 404),
			originModel: "gpt-5x",
			want:        false,
		},
		{
			name:        "skipRetry错误不算",
			err:         types.NewErrorWithStatusCode(errors.New("model `gpt-5x` does not exist"), types.ErrorCodeBadResponseStatusCode, 404, types.ErrOptionWithSkipRetry()),
			originModel: "gpt-5x",
			want:        false,
		},
		{
			name:        "空模型名不算",
			err:         types.NewErrorWithStatusCode(errors.New("model not found"), types.ErrorCodeBadResponseStatusCode, 404),
			originModel: "",
			want:        false,
		},
		{
			name:        "nil错误不算",
			err:         nil,
			originModel: "gpt-5x",
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isUpstreamModelMissingError(tt.err, tt.originModel))
		})
	}
}

func TestModelMissingRemovalCooldown(t *testing.T) {
	key := "9999:cooldown-model"
	modelMissingRemovalTriggerState.Lock()
	delete(modelMissingRemovalTriggerState.lastTrigger, key)
	modelMissingRemovalTriggerState.Unlock()

	require.True(t, modelMissingRemovalCooldownOk(9999, "cooldown-model"))
	// 冷却窗口内同渠道同模型不再触发
	require.False(t, modelMissingRemovalCooldownOk(9999, "cooldown-model"))
	// 同渠道不同模型不受影响
	require.True(t, modelMissingRemovalCooldownOk(9999, "another-model"))
}

func TestClassifyModelMissingWatchedModels(t *testing.T) {
	upstreamSet := map[string]struct{}{
		"gpt-5":       {},
		"target-b":    {},
		"other-model": {},
	}
	mapping := map[string]string{"alias-a": "target-b"}

	restored, stillMissing := classifyModelMissingWatchedModels(
		[]string{"gpt-5", "gone-model", "alias-a"},
		upstreamSet,
		mapping,
	)
	require.ElementsMatch(t, []string{"gpt-5", "alias-a"}, restored)
	require.ElementsMatch(t, []string{"gone-model"}, stillMissing)
}

func TestIsUpstreamRateLimitError(t *testing.T) {
	tests := []struct {
		name string
		err  *types.NewAPIError
		want bool
	}{
		{
			name: "上游429限流",
			err:  types.NewErrorWithStatusCode(errors.New("rate limit exceeded"), types.ErrorCodeBadResponseStatusCode, 429),
			want: true,
		},
		{
			name: "429不看文案，无关键词也算",
			err:  types.NewErrorWithStatusCode(errors.New("slow down"), types.ErrorCodeBadResponseStatusCode, 429),
			want: true,
		},
		{
			name: "channel前缀错误不算（渠道自身问题）",
			err:  types.NewErrorWithStatusCode(errors.New("rate limit"), types.ErrorCode("channel:test"), 429),
			want: false,
		},
		{
			name: "skipRetry错误不算",
			err:  types.NewErrorWithStatusCode(errors.New("rate limit"), types.ErrorCodeBadResponseStatusCode, 429, types.ErrOptionWithSkipRetry()),
			want: false,
		},
		{
			name: "503不算",
			err:  types.NewErrorWithStatusCode(errors.New("service unavailable"), types.ErrorCodeBadResponseStatusCode, 503),
			want: false,
		},
		{
			name: "nil不算",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isUpstreamRateLimitError(tt.err))
		})
	}
}

func TestClassifyModelRemovalReason(t *testing.T) {
	originMissing := operation_setting.ModelMissingRemovalEnabled
	originRateLimit := operation_setting.ModelRateLimitRemovalEnabled
	t.Cleanup(func() {
		operation_setting.ModelMissingRemovalEnabled = originMissing
		operation_setting.ModelRateLimitRemovalEnabled = originRateLimit
	})

	missingErr := types.NewErrorWithStatusCode(errors.New("The model `gpt-5x` does not exist"), types.ErrorCodeBadResponseStatusCode, 404)
	rateLimitErr := types.NewErrorWithStatusCode(errors.New("rate limit exceeded"), types.ErrorCodeBadResponseStatusCode, 429)
	// 部分上游用 429 表达「该模型未开通」：命中缺失关键词时应走缺失路径（判定更严，带存在性核实）
	rateLimitMissingErr := types.NewErrorWithStatusCode(errors.New("model `gpt-5x` does not exist"), types.ErrorCodeBadResponseStatusCode, 429)

	t.Run("限流开关关闭时429不触发", func(t *testing.T) {
		operation_setting.ModelMissingRemovalEnabled = true
		operation_setting.ModelRateLimitRemovalEnabled = false
		_, matched := classifyModelRemovalReason(rateLimitErr, "gpt-5x")
		require.False(t, matched)
	})

	t.Run("限流开关开启时429走限流路径", func(t *testing.T) {
		operation_setting.ModelMissingRemovalEnabled = true
		operation_setting.ModelRateLimitRemovalEnabled = true
		reason, matched := classifyModelRemovalReason(rateLimitErr, "gpt-5x")
		require.True(t, matched)
		require.Equal(t, modelRemovalReasonRateLimit, reason)
	})

	t.Run("429命中缺失关键词优先走缺失路径", func(t *testing.T) {
		operation_setting.ModelMissingRemovalEnabled = true
		operation_setting.ModelRateLimitRemovalEnabled = true
		reason, matched := classifyModelRemovalReason(rateLimitMissingErr, "gpt-5x")
		require.True(t, matched)
		require.Equal(t, modelRemovalReasonMissing, reason)
	})

	t.Run("缺失开关关闭但限流开启时404不误入限流路径", func(t *testing.T) {
		operation_setting.ModelMissingRemovalEnabled = false
		operation_setting.ModelRateLimitRemovalEnabled = true
		_, matched := classifyModelRemovalReason(missingErr, "gpt-5x")
		require.False(t, matched)
	})

	t.Run("两个开关都关时不接管", func(t *testing.T) {
		operation_setting.ModelMissingRemovalEnabled = false
		operation_setting.ModelRateLimitRemovalEnabled = false
		_, matched := classifyModelRemovalReason(missingErr, "gpt-5x")
		require.False(t, matched)
		_, matched = classifyModelRemovalReason(rateLimitErr, "gpt-5x")
		require.False(t, matched)
	})
}

func TestRecheckIntervalSecondsFor(t *testing.T) {
	originMissing := operation_setting.ModelMissingRecheckIntervalSeconds
	originRateLimit := operation_setting.ModelRateLimitRecheckIntervalSeconds
	originForbidden := operation_setting.ModelForbiddenRecheckIntervalSeconds
	t.Cleanup(func() {
		operation_setting.ModelMissingRecheckIntervalSeconds = originMissing
		operation_setting.ModelRateLimitRecheckIntervalSeconds = originRateLimit
		operation_setting.ModelForbiddenRecheckIntervalSeconds = originForbidden
	})

	operation_setting.ModelMissingRecheckIntervalSeconds = 600
	operation_setting.ModelRateLimitRecheckIntervalSeconds = 180
	operation_setting.ModelForbiddenRecheckIntervalSeconds = 1800
	require.Equal(t, int64(600), recheckIntervalSecondsFor(modelRemovalReasonMissing))
	require.Equal(t, int64(180), recheckIntervalSecondsFor(modelRemovalReasonRateLimit))
	// 权限变更由人工在上游控制台操作，复核间隔必须显著长于限流，不能落回 600/180
	require.Equal(t, int64(1800), recheckIntervalSecondsFor(modelRemovalReasonForbidden))

	// 低于下限时回落到各自默认值，不接受会把复核打成高频的配置
	operation_setting.ModelRateLimitRecheckIntervalSeconds = 5
	require.Equal(t, int64(180), recheckIntervalSecondsFor(modelRemovalReasonRateLimit))
	operation_setting.ModelForbiddenRecheckIntervalSeconds = 5
	require.Equal(t, int64(1800), recheckIntervalSecondsFor(modelRemovalReasonForbidden))
}

func TestIsUpstreamForbiddenError(t *testing.T) {
	tests := []struct {
		name string
		err  *types.NewAPIError
		want bool
	}{
		{
			name: "403权限拒绝命中关键词",
			err:  types.NewErrorWithStatusCode(errors.New("Permission denied for this deployment"), types.ErrorCodeBadResponseStatusCode, 403),
			want: true,
		},
		{
			name: "400模型不支持命中关键词",
			err:  types.NewErrorWithStatusCode(errors.New("The requested model is not supported in this region: model not supported"), types.ErrorCodeBadResponseStatusCode, 400),
			want: true,
		},
		{
			name: "403无权访问模型命中关键词",
			err:  types.NewErrorWithStatusCode(errors.New("your account does not have access to model claude-x"), types.ErrorCodeBadResponseStatusCode, 403),
			want: true,
		},
		{
			name: "关键词大小写不敏感",
			err:  types.NewErrorWithStatusCode(errors.New("PERMISSION DENIED"), types.ErrorCodeBadResponseStatusCode, 403),
			want: true,
		},
		{
			name: "403但无关键词不算（403 是宽口径，密钥整体失效也用它）",
			err:  types.NewErrorWithStatusCode(errors.New("forbidden"), types.ErrorCodeBadResponseStatusCode, 403),
			want: false,
		},
		{
			name: "命中关键词但状态码不在触发列表不算",
			err:  types.NewErrorWithStatusCode(errors.New("Permission denied"), types.ErrorCodeBadResponseStatusCode, 500),
			want: false,
		},
		{
			name: "默认触发列表只含400/403，429不算",
			err:  types.NewErrorWithStatusCode(errors.New("Permission denied"), types.ErrorCodeBadResponseStatusCode, 429),
			want: false,
		},
		{
			name: "channel前缀错误不算（渠道自身问题）",
			err:  types.NewErrorWithStatusCode(errors.New("Permission denied"), types.ErrorCode("channel:test"), 403),
			want: false,
		},
		{
			name: "skipRetry错误不算",
			err:  types.NewErrorWithStatusCode(errors.New("Permission denied"), types.ErrorCodeBadResponseStatusCode, 403, types.ErrOptionWithSkipRetry()),
			want: false,
		},
		{
			name: "网关侧本站未配置模型不算",
			err:  types.NewErrorWithStatusCode(errors.New("Permission denied"), types.ErrorCodeModelNotFound, 403),
			want: false,
		},
		{
			name: "nil不算",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isUpstreamForbiddenError(tt.err))
		})
	}
}

func TestIsUpstreamForbiddenErrorRespectsConfiguredStatusCodes(t *testing.T) {
	origin := operation_setting.ModelForbiddenStatusCodesToString()
	t.Cleanup(func() {
		require.NoError(t, operation_setting.ModelForbiddenStatusCodesFromString(origin))
	})

	// 状态码列表可配：改成只认 429 后，默认的 403 必须失效、429 必须生效
	require.NoError(t, operation_setting.ModelForbiddenStatusCodesFromString("429"))
	require.True(t, isUpstreamForbiddenError(
		types.NewErrorWithStatusCode(errors.New("Permission denied"), types.ErrorCodeBadResponseStatusCode, 429)))
	require.False(t, isUpstreamForbiddenError(
		types.NewErrorWithStatusCode(errors.New("Permission denied"), types.ErrorCodeBadResponseStatusCode, 403)))
}

func TestClassifyModelRemovalReasonForbidden(t *testing.T) {
	originMissing := operation_setting.ModelMissingRemovalEnabled
	originRateLimit := operation_setting.ModelRateLimitRemovalEnabled
	originForbidden := operation_setting.ModelForbiddenRemovalEnabled
	originCodes := operation_setting.ModelForbiddenStatusCodesToString()
	t.Cleanup(func() {
		operation_setting.ModelMissingRemovalEnabled = originMissing
		operation_setting.ModelRateLimitRemovalEnabled = originRateLimit
		operation_setting.ModelForbiddenRemovalEnabled = originForbidden
		require.NoError(t, operation_setting.ModelForbiddenStatusCodesFromString(originCodes))
	})

	forbiddenErr := types.NewErrorWithStatusCode(errors.New("Permission denied for deployment gpt-5x"), types.ErrorCodeBadResponseStatusCode, 403)

	t.Run("权限开关关闭时403不被接管（零行为变更）", func(t *testing.T) {
		operation_setting.ModelMissingRemovalEnabled = true
		operation_setting.ModelRateLimitRemovalEnabled = true
		operation_setting.ModelForbiddenRemovalEnabled = false
		_, matched := classifyModelRemovalReason(forbiddenErr, "gpt-5x")
		require.False(t, matched, "开关关闭时必须放回原有停渠道判定")
	})

	t.Run("权限开关开启时403走权限路径", func(t *testing.T) {
		operation_setting.ModelMissingRemovalEnabled = true
		operation_setting.ModelRateLimitRemovalEnabled = true
		operation_setting.ModelForbiddenRemovalEnabled = true
		reason, matched := classifyModelRemovalReason(forbiddenErr, "gpt-5x")
		require.True(t, matched)
		require.Equal(t, modelRemovalReasonForbidden, reason)
	})

	t.Run("缺失关键词与权限关键词同时命中时缺失优先", func(t *testing.T) {
		operation_setting.ModelMissingRemovalEnabled = true
		operation_setting.ModelForbiddenRemovalEnabled = true
		// 缺失路径有存在性核实作硬证据，判定更强，必须优先
		bothErr := types.NewErrorWithStatusCode(
			errors.New("model `gpt-5x` does not exist: Permission denied"), types.ErrorCodeBadResponseStatusCode, 403)
		reason, matched := classifyModelRemovalReason(bothErr, "gpt-5x")
		require.True(t, matched)
		require.Equal(t, modelRemovalReasonMissing, reason)
	})

	t.Run("权限状态码含429时权限优先于限流", func(t *testing.T) {
		operation_setting.ModelMissingRemovalEnabled = true
		operation_setting.ModelRateLimitRemovalEnabled = true
		operation_setting.ModelForbiddenRemovalEnabled = true
		require.NoError(t, operation_setting.ModelForbiddenStatusCodesFromString("400,403,429"))
		// 限流只看状态码，权限要状态码+关键词双命中，判定更严者优先
		bothErr := types.NewErrorWithStatusCode(errors.New("Operation not allowed"), types.ErrorCodeBadResponseStatusCode, 429)
		reason, matched := classifyModelRemovalReason(bothErr, "gpt-5x")
		require.True(t, matched)
		require.Equal(t, modelRemovalReasonForbidden, reason)
	})

	t.Run("权限状态码含429但文案不命中时仍走限流", func(t *testing.T) {
		operation_setting.ModelMissingRemovalEnabled = true
		operation_setting.ModelRateLimitRemovalEnabled = true
		operation_setting.ModelForbiddenRemovalEnabled = true
		require.NoError(t, operation_setting.ModelForbiddenStatusCodesFromString("400,403,429"))
		reason, matched := classifyModelRemovalReason(
			types.NewErrorWithStatusCode(errors.New("slow down"), types.ErrorCodeBadResponseStatusCode, 429), "gpt-5x")
		require.True(t, matched)
		require.Equal(t, modelRemovalReasonRateLimit, reason)
	})
}

func TestRemovalCapApplies(t *testing.T) {
	origin := operation_setting.ModelMissingRemovalCapEnabled
	t.Cleanup(func() { operation_setting.ModelMissingRemovalCapEnabled = origin })

	operation_setting.ModelMissingRemovalCapEnabled = false
	// 依据是推断的两条路径必须受上限约束
	require.True(t, removalCapApplies(modelRemovalReasonRateLimit))
	require.True(t, removalCapApplies(modelRemovalReasonForbidden))
	// 缺失路径有存在性核实兜底，默认不受约束（= 改造前行为）
	require.False(t, removalCapApplies(modelRemovalReasonMissing))

	operation_setting.ModelMissingRemovalCapEnabled = true
	require.True(t, removalCapApplies(modelRemovalReasonMissing))
}

func TestModelRemovalMaxRemovedPerChannel(t *testing.T) {
	origin := operation_setting.ModelRemovalMaxRemovedPerChannel
	t.Cleanup(func() { operation_setting.ModelRemovalMaxRemovedPerChannel = origin })

	// 默认值必须是 5，与改造前硬编码常量一致
	require.Equal(t, 5, origin)

	operation_setting.ModelRemovalMaxRemovedPerChannel = 3
	require.Equal(t, 3, modelRemovalMaxRemovedPerChannel())

	// 非法值（0 / 负数）不得让上限失效变成"无限摘"
	operation_setting.ModelRemovalMaxRemovedPerChannel = 0
	require.Equal(t, 5, modelRemovalMaxRemovedPerChannel())
	operation_setting.ModelRemovalMaxRemovedPerChannel = -1
	require.Equal(t, 5, modelRemovalMaxRemovedPerChannel())
}

func TestModelRemovalConsecutiveThreshold(t *testing.T) {
	origin := operation_setting.ModelRemovalConsecutiveThreshold
	t.Cleanup(func() { operation_setting.ModelRemovalConsecutiveThreshold = origin })

	// 默认值必须是 10
	require.Equal(t, 10, origin)
	require.Equal(t, int64(10), modelRemovalConsecutiveThreshold())

	operation_setting.ModelRemovalConsecutiveThreshold = 3
	require.Equal(t, int64(3), modelRemovalConsecutiveThreshold())

	// 设 1 = 命中即摘的旧行为，必须允许
	operation_setting.ModelRemovalConsecutiveThreshold = 1
	require.Equal(t, int64(1), modelRemovalConsecutiveThreshold())

	// 非法值（0 / 负数）回落默认 10，不得让门槛失效
	operation_setting.ModelRemovalConsecutiveThreshold = 0
	require.Equal(t, int64(10), modelRemovalConsecutiveThreshold())
	operation_setting.ModelRemovalConsecutiveThreshold = -1
	require.Equal(t, int64(10), modelRemovalConsecutiveThreshold())
}

func TestRemovalStreakGateApplies(t *testing.T) {
	// 推断依据的两条路径受连击门槛约束
	require.True(t, removalStreakGateApplies(modelRemovalReasonRateLimit))
	require.True(t, removalStreakGateApplies(modelRemovalReasonForbidden))
	// 缺失路径有存在性核实作硬证据，不叠加连击门槛
	require.False(t, removalStreakGateApplies(modelRemovalReasonMissing))
}

func TestModelRemovalStreakReached(t *testing.T) {
	origin := operation_setting.ModelRemovalConsecutiveThreshold
	t.Cleanup(func() { operation_setting.ModelRemovalConsecutiveThreshold = origin })
	operation_setting.ModelRemovalConsecutiveThreshold = 3

	t.Run("限流：连续 N 次才达门槛，达到后清零重新起算", func(t *testing.T) {
		const chID = 900001
		model := "claude-opus-4-6"
		service.ResetModelRemovalStreak(chID, model)
		t.Cleanup(func() { service.ResetModelRemovalStreak(chID, model) })

		require.False(t, modelRemovalStreakReached(chID, "c", model, modelRemovalReasonRateLimit), "第1次<3，暂缓")
		require.False(t, modelRemovalStreakReached(chID, "c", model, modelRemovalReasonRateLimit), "第2次<3，暂缓")
		require.True(t, modelRemovalStreakReached(chID, "c", model, modelRemovalReasonRateLimit), "第3次达门槛")
		// 达门槛后已清零：下一轮重新从 1 起算
		require.False(t, modelRemovalStreakReached(chID, "c", model, modelRemovalReasonRateLimit), "清零后重新起算")
	})

	t.Run("权限路径同样受连击门槛约束", func(t *testing.T) {
		const chID = 900002
		model := "claude-sonnet-4-6"
		service.ResetModelRemovalStreak(chID, model)
		t.Cleanup(func() { service.ResetModelRemovalStreak(chID, model) })

		require.False(t, modelRemovalStreakReached(chID, "c", model, modelRemovalReasonForbidden))
		require.False(t, modelRemovalStreakReached(chID, "c", model, modelRemovalReasonForbidden))
		require.True(t, modelRemovalStreakReached(chID, "c", model, modelRemovalReasonForbidden))
	})

	t.Run("中途成功清零打断连击：永远到不了门槛", func(t *testing.T) {
		const chID = 900003
		model := "claude-haiku-4-5"
		service.ResetModelRemovalStreak(chID, model)
		t.Cleanup(func() { service.ResetModelRemovalStreak(chID, model) })

		require.False(t, modelRemovalStreakReached(chID, "c", model, modelRemovalReasonRateLimit)) // streak 1
		require.False(t, modelRemovalStreakReached(chID, "c", model, modelRemovalReasonRateLimit)) // streak 2
		// 一次成功请求（relay 成功路径调用）清零
		service.ResetModelRemovalStreak(chID, model)
		require.False(t, modelRemovalStreakReached(chID, "c", model, modelRemovalReasonRateLimit), "清零后重新从1起算，仍<3")
		require.False(t, modelRemovalStreakReached(chID, "c", model, modelRemovalReasonRateLimit), "第2次，仍未达门槛")
	})

	t.Run("缺失路径恒达门槛（不受连击约束）", func(t *testing.T) {
		const chID = 900004
		model := "gpt-5x"
		service.ResetModelRemovalStreak(chID, model)
		require.True(t, modelRemovalStreakReached(chID, "c", model, modelRemovalReasonMissing), "缺失单次核实即可信")
		require.True(t, modelRemovalStreakReached(chID, "c", model, modelRemovalReasonMissing))
	})

	t.Run("不同渠道的计数互相隔离", func(t *testing.T) {
		const chA, chB = 900005, 900006
		model := "claude-opus-4-6"
		service.ResetModelRemovalStreak(chA, model)
		service.ResetModelRemovalStreak(chB, model)
		t.Cleanup(func() {
			service.ResetModelRemovalStreak(chA, model)
			service.ResetModelRemovalStreak(chB, model)
		})

		require.False(t, modelRemovalStreakReached(chA, "a", model, modelRemovalReasonRateLimit)) // A:1
		require.False(t, modelRemovalStreakReached(chA, "a", model, modelRemovalReasonRateLimit)) // A:2
		require.False(t, modelRemovalStreakReached(chB, "b", model, modelRemovalReasonRateLimit), "B 独立从1起算，不受 A 影响")
	})
}

func TestModelRemovalCapActionDefaults(t *testing.T) {
	// 默认动作必须是仅告警——这就是「默认值 = 上线前行为」的落点
	require.Equal(t, operation_setting.ModelRemovalCapActionAlertOnly, operation_setting.ModelRemovalCapAction)
	require.False(t, operation_setting.ShouldDisableChannelOnRemovalCap())

	origin := operation_setting.ModelRemovalCapAction
	t.Cleanup(func() { operation_setting.ModelRemovalCapAction = origin })
	operation_setting.ModelRemovalCapAction = operation_setting.ModelRemovalCapActionDisableChannel
	require.True(t, operation_setting.ShouldDisableChannelOnRemovalCap())

	require.True(t, operation_setting.IsValidModelRemovalCapAction("alert_only"))
	require.True(t, operation_setting.IsValidModelRemovalCapAction("disable_channel"))
	require.False(t, operation_setting.IsValidModelRemovalCapAction("disable"))
	require.False(t, operation_setting.IsValidModelRemovalCapAction(""))
}

func TestShouldSkipRemovalBecauseModelStillListed(t *testing.T) {
	upstreamSet := map[string]struct{}{"gpt-5": {}}

	// 缺失路径：模型还在上游 = 证伪「模型没了」，必须放弃移出
	require.True(t, shouldSkipRemovalBecauseModelStillListed(modelRemovalReasonMissing, upstreamSet, "gpt-5"))
	// 缺失路径：模型确实不在，放行移出
	require.False(t, shouldSkipRemovalBecauseModelStillListed(modelRemovalReasonMissing, upstreamSet, "gone-model"))

	// 限流路径核心断言：模型仍在上游是常态（限流不等于下线），不得因此放弃移出，
	// 否则 429 永远摘不掉——这正是「把 429 直接塞进缺失链路」会失效的原因。
	require.False(t, shouldSkipRemovalBecauseModelStillListed(modelRemovalReasonRateLimit, upstreamSet, "gpt-5"))
	require.False(t, shouldSkipRemovalBecauseModelStillListed(modelRemovalReasonRateLimit, upstreamSet, "gone-model"))

	// 权限路径核心断言：403 无权时模型必然仍被上游列出（/v1/models 只说上游有这个模型，
	// 不说本密钥能不能调），若走存在性核实则永远摘不掉，与 429 同一个坑。
	require.False(t, shouldSkipRemovalBecauseModelStillListed(modelRemovalReasonForbidden, upstreamSet, "gpt-5"))
	require.False(t, shouldSkipRemovalBecauseModelStillListed(modelRemovalReasonForbidden, upstreamSet, "gone-model"))
}
