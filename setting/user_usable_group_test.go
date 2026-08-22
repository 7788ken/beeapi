package setting

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func resetUserUsableGroups(t *testing.T, initial map[string]UserUsableGroupEntry) {
	t.Helper()
	userUsableGroupsMutex.Lock()
	defer userUsableGroupsMutex.Unlock()
	userUsableGroups = make(map[string]UserUsableGroupEntry, len(initial))
	for k, v := range initial {
		userUsableGroups[k] = v
	}
}

func TestUpdateUserUsableGroupsByJSONString_LegacyStringFormat(t *testing.T) {
	resetUserUsableGroups(t, nil)
	// 旧格式 {"default":"默认分组","vip":"vip分组"}
	err := UpdateUserUsableGroupsByJSONString(`{"default":"默认分组","vip":"vip分组"}`)
	require.NoError(t, err)

	all := GetAllUserUsableGroupsCopy()
	require.Equal(t, "默认分组", all["default"].Description)
	require.True(t, all["default"].UserSelectable, "旧格式默认应该 UserSelectable=true")
	require.Equal(t, "vip分组", all["vip"].Description)
	require.True(t, all["vip"].UserSelectable)
}

func TestUpdateUserUsableGroupsByJSONString_NewObjectFormat(t *testing.T) {
	resetUserUsableGroups(t, nil)
	err := UpdateUserUsableGroupsByJSONString(`{
		"default":{"description":"默认分组","user_selectable":true},
		"admin_only":{"description":"管理员专用","user_selectable":false}
	}`)
	require.NoError(t, err)

	all := GetAllUserUsableGroupsCopy()
	require.True(t, all["default"].UserSelectable)
	require.False(t, all["admin_only"].UserSelectable)
	require.Equal(t, "管理员专用", all["admin_only"].Description)
}

func TestUpdateUserUsableGroupsByJSONString_MixedFormat(t *testing.T) {
	resetUserUsableGroups(t, nil)
	// 用户数据迁移过程中两种格式混存
	err := UpdateUserUsableGroupsByJSONString(`{
		"default":"默认分组",
		"vip":{"description":"vip分组","user_selectable":true},
		"hidden":{"description":"","user_selectable":false}
	}`)
	require.NoError(t, err)

	all := GetAllUserUsableGroupsCopy()
	require.True(t, all["default"].UserSelectable, "旧字符串格式默认可选")
	require.True(t, all["vip"].UserSelectable)
	require.False(t, all["hidden"].UserSelectable)
	require.Equal(t, "", all["hidden"].Description)
}

func TestGetUserUsableGroupsCopy_FiltersUnselectable(t *testing.T) {
	resetUserUsableGroups(t, map[string]UserUsableGroupEntry{
		"default": {Description: "默认", UserSelectable: true},
		"vip":     {Description: "VIP", UserSelectable: true},
		"hidden":  {Description: "藏起来", UserSelectable: false},
	})

	visible := GetUserUsableGroupsCopy()
	require.Len(t, visible, 2)
	require.Contains(t, visible, "default")
	require.Contains(t, visible, "vip")
	require.NotContains(t, visible, "hidden", "user_selectable=false 必须被过滤")
}

func TestUserUsableGroups2JSONString_RoundTrip(t *testing.T) {
	resetUserUsableGroups(t, map[string]UserUsableGroupEntry{
		"default":   {Description: "默认", UserSelectable: true},
		"admin":     {Description: "管理员", UserSelectable: false},
		"emptydesc": {Description: "", UserSelectable: false},
	})

	js := UserUsableGroups2JSONString()
	// 序列化后应该是新对象格式
	var parsed map[string]UserUsableGroupEntry
	require.NoError(t, json.Unmarshal([]byte(js), &parsed))
	require.Equal(t, "默认", parsed["default"].Description)
	require.True(t, parsed["default"].UserSelectable)
	require.False(t, parsed["admin"].UserSelectable)

	// roundtrip：再写回去要等价
	resetUserUsableGroups(t, nil)
	require.NoError(t, UpdateUserUsableGroupsByJSONString(js))
	all := GetAllUserUsableGroupsCopy()
	require.Equal(t, parsed["default"], all["default"])
	require.Equal(t, parsed["admin"], all["admin"])
	require.Equal(t, parsed["emptydesc"], all["emptydesc"])
}

func TestUpdateUserUsableGroupsByJSONString_EmptyAndInvalid(t *testing.T) {
	resetUserUsableGroups(t, map[string]UserUsableGroupEntry{
		"stale": {Description: "旧值", UserSelectable: true},
	})

	// 空字符串：清空
	require.NoError(t, UpdateUserUsableGroupsByJSONString(""))
	require.Empty(t, GetAllUserUsableGroupsCopy())

	// 非法 JSON：返回 error，不修改内存状态
	resetUserUsableGroups(t, map[string]UserUsableGroupEntry{
		"keep": {Description: "保留", UserSelectable: true},
	})
	err := UpdateUserUsableGroupsByJSONString(`{not json`)
	require.Error(t, err)
	all := GetAllUserUsableGroupsCopy()
	require.Equal(t, "保留", all["keep"].Description)
}

func TestIsGroupUserSelectable(t *testing.T) {
	resetUserUsableGroups(t, map[string]UserUsableGroupEntry{
		"default": {Description: "默认", UserSelectable: true},
		"hidden":  {Description: "藏起来", UserSelectable: false},
	})

	require.True(t, IsGroupUserSelectable("default"))
	require.False(t, IsGroupUserSelectable("hidden"))
	// 未配置的分组按 false 处理
	require.False(t, IsGroupUserSelectable("unknown"))
}

func TestIsGroupExplicitlyHidden(t *testing.T) {
	resetUserUsableGroups(t, map[string]UserUsableGroupEntry{
		"default": {Description: "默认", UserSelectable: true},
		"hidden":  {Description: "管理员专用", UserSelectable: false},
	})

	require.False(t, IsGroupExplicitlyHidden("default"))
	require.True(t, IsGroupExplicitlyHidden("hidden"))
	// 与 IsGroupUserSelectable 的关键差异：未注册分组不算"显式隐藏"，
	// 用户专属分组/特殊规则分组不应被误伤
	require.False(t, IsGroupExplicitlyHidden("exclusive_unregistered"))
}

func TestGetUsableGroupDescription_IgnoresSelectableFlag(t *testing.T) {
	resetUserUsableGroups(t, map[string]UserUsableGroupEntry{
		"default": {Description: "默认", UserSelectable: true},
		"hidden":  {Description: "管理员专用", UserSelectable: false},
	})

	require.Equal(t, "默认", GetUsableGroupDescription("default"))
	require.Equal(t, "管理员专用", GetUsableGroupDescription("hidden"),
		"description 查询不应该被 UserSelectable 影响——任何代码都该拿到友好名")
	require.Equal(t, "unknown", GetUsableGroupDescription("unknown"))
}
