package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/require"
)

// 装配级：option 登记有两处，漏任何一处都不会报错，只会静默失效——
//   - InitOptionMap 漏登记：后台读不到该键，前端拿到的是自己写死的默认值；
//   - updateOptionMap 漏 case：后台保存成功，但内存变量永不改变，配置形同虚设。
//
// 这里两头都验。

func snapshotRemovalOptions(t *testing.T) {
	t.Helper()
	forbiddenEnabled := operation_setting.ModelForbiddenRemovalEnabled
	forbiddenCodes := operation_setting.ModelForbiddenStatusCodesToString()
	forbiddenKeywords := operation_setting.ModelForbiddenKeywordsToString()
	forbiddenInterval := operation_setting.ModelForbiddenRecheckIntervalSeconds
	maxRemoved := operation_setting.ModelRemovalMaxRemovedPerChannel
	capAction := operation_setting.ModelRemovalCapAction
	capCountsMissing := operation_setting.ModelMissingRemovalCapEnabled
	consecutiveThreshold := operation_setting.ModelRemovalConsecutiveThreshold

	t.Cleanup(func() {
		operation_setting.ModelForbiddenRemovalEnabled = forbiddenEnabled
		_ = operation_setting.ModelForbiddenStatusCodesFromString(forbiddenCodes)
		operation_setting.ModelForbiddenKeywordsFromString(forbiddenKeywords)
		operation_setting.ModelForbiddenRecheckIntervalSeconds = forbiddenInterval
		operation_setting.ModelRemovalMaxRemovedPerChannel = maxRemoved
		operation_setting.ModelRemovalCapAction = capAction
		operation_setting.ModelMissingRemovalCapEnabled = capCountsMissing
		operation_setting.ModelRemovalConsecutiveThreshold = consecutiveThreshold
	})
}

// 默认值 = 上线前行为：这三个键的默认值一旦漂移，各站点不改配置就会被动改变行为。
func TestChannelRemovalOptionDefaultsMatchPreLaunchBehavior(t *testing.T) {
	require.False(t, operation_setting.ModelForbiddenRemovalEnabled,
		"权限摘除默认必须关：开着就会把原本停整渠道的 403 改成只摘模型")
	require.Equal(t, operation_setting.ModelRemovalCapActionAlertOnly, operation_setting.ModelRemovalCapAction,
		"触顶动作默认必须是仅告警：改造前触顶只记一行日志")
	require.False(t, operation_setting.ModelMissingRemovalCapEnabled,
		"缺失路径默认必须不受上限约束：改造前缺失路径完全不看上限")
	require.Equal(t, 5, operation_setting.ModelRemovalMaxRemovedPerChannel,
		"上限默认必须是 5，与改造前硬编码值一致")
	require.Equal(t, "400,403", operation_setting.ModelForbiddenStatusCodesToString())
	require.Equal(t, 1800, operation_setting.ModelForbiddenRecheckIntervalSeconds)
}

func TestChannelRemovalOptionsRegisteredInOptionMap(t *testing.T) {
	InitOptionMap()

	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	for _, key := range []string{
		"ModelForbiddenRemovalEnabled",
		"ModelForbiddenStatusCodes",
		"ModelForbiddenKeywords",
		"ModelForbiddenRecheckIntervalSeconds",
		"ModelRemovalMaxRemovedPerChannel",
		"ModelRemovalCapAction",
		"ModelMissingRemovalCapEnabled",
		"ModelRemovalConsecutiveThreshold",
	} {
		_, ok := common.OptionMap[key]
		require.Truef(t, ok, "option %s 未登记进 OptionMap，后台将读不到该配置", key)
	}
	require.Equal(t, "alert_only", common.OptionMap["ModelRemovalCapAction"])
	require.Equal(t, "400,403", common.OptionMap["ModelForbiddenStatusCodes"])
	require.Equal(t, "5", common.OptionMap["ModelRemovalMaxRemovedPerChannel"])
	require.Equal(t, "10", common.OptionMap["ModelRemovalConsecutiveThreshold"])
}

func TestUpdateOptionMapAppliesChannelRemovalOptions(t *testing.T) {
	snapshotRemovalOptions(t)

	require.NoError(t, updateOptionMap("ModelForbiddenRemovalEnabled", "true"))
	require.True(t, operation_setting.ModelForbiddenRemovalEnabled)

	require.NoError(t, updateOptionMap("ModelMissingRemovalCapEnabled", "true"))
	require.True(t, operation_setting.ModelMissingRemovalCapEnabled)

	// 与 AutomaticDisableStatusCodes 同一套 ranges 解析：空格容错、相邻区间会被合并
	require.NoError(t, updateOptionMap("ModelForbiddenStatusCodes", "403, 405-406"))
	require.Equal(t, "403,405-406", operation_setting.ModelForbiddenStatusCodesToString())

	require.NoError(t, updateOptionMap("ModelForbiddenKeywords", "Denied Here\nno access"))
	require.Equal(t, []string{"Denied Here", "no access"}, operation_setting.ModelForbiddenKeywords)

	require.NoError(t, updateOptionMap("ModelForbiddenRecheckIntervalSeconds", "3600"))
	require.Equal(t, 3600, operation_setting.ModelForbiddenRecheckIntervalSeconds)

	require.NoError(t, updateOptionMap("ModelRemovalMaxRemovedPerChannel", "8"))
	require.Equal(t, 8, operation_setting.ModelRemovalMaxRemovedPerChannel)

	require.NoError(t, updateOptionMap("ModelRemovalConsecutiveThreshold", "3"))
	require.Equal(t, 3, operation_setting.ModelRemovalConsecutiveThreshold)

	require.NoError(t, updateOptionMap("ModelRemovalCapAction", "disable_channel"))
	require.Equal(t, "disable_channel", operation_setting.ModelRemovalCapAction)
}

// 非法输入必须报错或被拒，不能静默写进内存变量。
func TestUpdateOptionMapRejectsInvalidChannelRemovalOptions(t *testing.T) {
	snapshotRemovalOptions(t)

	operation_setting.ModelRemovalCapAction = operation_setting.ModelRemovalCapActionAlertOnly
	err := updateOptionMap("ModelRemovalCapAction", "disable")
	require.Error(t, err, "枚举写错必须报错，否则管理员以为已开启升级而实际没生效")
	require.Equal(t, operation_setting.ModelRemovalCapActionAlertOnly, operation_setting.ModelRemovalCapAction)

	require.Error(t, updateOptionMap("ModelForbiddenStatusCodes", "abc"))

	// 上限 <1 会让安全阀变成"无限摘"，必须拒绝写入
	operation_setting.ModelRemovalMaxRemovedPerChannel = 5
	require.NoError(t, updateOptionMap("ModelRemovalMaxRemovedPerChannel", "0"))
	require.Equal(t, 5, operation_setting.ModelRemovalMaxRemovedPerChannel)

	// 连击门槛 <1 会让门槛失效，必须拒写
	operation_setting.ModelRemovalConsecutiveThreshold = 10
	require.NoError(t, updateOptionMap("ModelRemovalConsecutiveThreshold", "0"))
	require.Equal(t, 10, operation_setting.ModelRemovalConsecutiveThreshold)

	// 复核间隔 <60 会把复核打成高频，必须拒绝写入
	operation_setting.ModelForbiddenRecheckIntervalSeconds = 1800
	require.NoError(t, updateOptionMap("ModelForbiddenRecheckIntervalSeconds", "5"))
	require.Equal(t, 1800, operation_setting.ModelForbiddenRecheckIntervalSeconds)
}
