package ratio_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestUpdateGroupRatioByJSONStringRemovesKeys(t *testing.T) {
	prev := GroupRatio2JSONString()
	t.Cleanup(func() { _ = UpdateGroupRatioByJSONString(prev) })

	require.NoError(t, UpdateGroupRatioByJSONString(`{"keep":1,"drop":2}`))
	require.True(t, ContainsGroupRatio("drop"))
	require.True(t, ContainsGroupRatio("keep"))

	require.NoError(t, UpdateGroupRatioByJSONString(`{"keep":1}`))
	require.False(t, ContainsGroupRatio("drop"))
	require.True(t, ContainsGroupRatio("keep"))
	require.Equal(t, 1.0, GetGroupRatio("keep"))
}

func TestLoadFromDBStaleNestedGroupRatioDoesNotResurrect(t *testing.T) {
	prevRatio := GroupRatio2JSONString()
	prevGG := GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		_ = UpdateGroupRatioByJSONString(prevRatio)
		_ = UpdateGroupGroupRatioByJSONString(prevGG)
	})

	require.NoError(t, UpdateGroupRatioByJSONString(`{"keep":1}`))
	require.NoError(t, UpdateGroupGroupRatioByJSONString(`{"vip":{"keep":0.9}}`))

	// 模拟库里仍留着旧的 GlobalConfig 双写副本（含已删除的 drop）。
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"group_ratio_setting.group_ratio":       `{"keep":1,"drop":2}`,
		"group_ratio_setting.group_group_ratio": `{"vip":{"keep":0.9,"drop":0.5}}`,
	}))

	require.False(t, ContainsGroupRatio("drop"), "stale nested group_ratio must not resurrect deleted groups")
	require.True(t, ContainsGroupRatio("keep"))
	_, dropOverride := GetGroupGroupRatio("vip", "drop")
	require.False(t, dropOverride, "stale nested group_group_ratio must not resurrect deleted overrides")
	got, ok := GetGroupGroupRatio("vip", "keep")
	require.True(t, ok)
	require.Equal(t, 0.9, got)
}
