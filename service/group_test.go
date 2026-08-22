package service

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

// setupGroupVisibilityFixture 配置三类分组并在测试结束后恢复：
//   - default        注册且可选
//   - hidden_admin   注册但显式隐藏（user_selectable=false）
//   - hidden_special 注册但显式隐藏，且被特殊规则加给 default 用户组
func setupGroupVisibilityFixture(t *testing.T) {
	t.Helper()

	prevGroups := setting.UserUsableGroups2JSONString()
	t.Cleanup(func() { _ = setting.UpdateUserUsableGroupsByJSONString(prevGroups) })
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{
		"default":{"description":"默认分组","user_selectable":true},
		"hidden_admin":{"description":"管理员专用","user_selectable":false},
		"hidden_special":{"description":"内部渠道","user_selectable":false}
	}`))

	special := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
	prevRule, hadRule := special.Get("default")
	t.Cleanup(func() {
		if hadRule {
			special.Set("default", prevRule)
		} else {
			special.Set("default", map[string]string{})
		}
	})
	special.Set("default", map[string]string{"+:hidden_special": "内部渠道"})
}

func TestGetUserUsableGroups_ReAddKeepsHiddenGroupsRoutable(t *testing.T) {
	setupGroupVisibilityFixture(t)

	// 准入口径：特殊规则 / 用户自身分组的 re-add 保留显式隐藏分组，
	// relay 鉴权（middleware/auth.go）依赖这一点，存量 token 才不会 403
	usable := GetUserUsableGroups("default")
	require.Contains(t, usable, "default")
	require.Contains(t, usable, "hidden_special", "特殊规则加回的隐藏分组必须保持可路由")
	require.NotContains(t, usable, "hidden_admin")

	usable = GetUserUsableGroups("hidden_admin")
	require.Contains(t, usable, "hidden_admin", "用户自身分组必须保持可路由")
}

func TestGetUserVisibleGroups_HidesExplicitlyHiddenGroups(t *testing.T) {
	setupGroupVisibilityFixture(t)

	// 特殊规则 re-add 的隐藏分组：展示口径必须剔除
	visible := GetUserVisibleGroups("default")
	require.Contains(t, visible, "default")
	require.NotContains(t, visible, "hidden_special", "特殊规则不能把显式隐藏分组带回前台")
	require.NotContains(t, visible, "hidden_admin")

	// 用户自身分组是显式隐藏分组：同样剔除（管理员意图优先，路由不受影响）
	visible = GetUserVisibleGroups("hidden_admin")
	require.NotContains(t, visible, "hidden_admin")
	require.Contains(t, visible, "default")
}

func TestGetUserVisibleGroups_KeepsUnregisteredExclusiveGroup(t *testing.T) {
	setupGroupVisibilityFixture(t)

	// 未在 UserUsableGroups 注册的专属分组不算"显式隐藏"，保持可见（不回归）
	visible := GetUserVisibleGroups("exclusive_vip")
	require.Contains(t, visible, "exclusive_vip")
	require.Contains(t, visible, "default")

	// 匿名口径：与准入口径一致（源头已过滤，无 re-add）
	require.Equal(t, GetUserUsableGroups(""), GetUserVisibleGroups(""))
}
