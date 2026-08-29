package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEffectiveAdminPermsDefaultsKeepLegacyAdminPowers(t *testing.T) {
	// 存量管理员（admin_perms 为空）不能因为上线掉权限
	perms := EffectiveAdminPerms(common.RoleAdminUser, "")
	assert.ElementsMatch(t, []string{
		AdminPermChannelView,
		AdminPermLogView,
		AdminPermQuotaGrant,
		AdminPermUserManage,
	}, perms)
	assert.NotContains(t, perms, AdminPermQuotaDeductSelf)
}

func TestChannelEditDefaultsOffForAdminsButOnForRoot(t *testing.T) {
	// 唯一一处默认值不等于上线前行为：建/改渠道必须超管逐个开
	assert.False(t, HasAdminPermFor(common.RoleAdminUser, "", AdminPermChannelEdit),
		"未配置的管理员不能默认拿到建/改渠道权限")
	assert.True(t, HasAdminPermFor(common.RoleAdminUser, "", AdminPermChannelView),
		"看渠道仍然默认开，不然存量管理员会掉权限")
	assert.True(t, HasAdminPermFor(common.RoleAdminUser, "channel.view,channel.edit", AdminPermChannelEdit))
	// root 恒有
	assert.True(t, HasAdminPermFor(common.RoleRootUser, AdminPermsNone, AdminPermChannelEdit))
	// 普通用户恒无
	assert.False(t, HasAdminPermFor(common.RoleCommonUser, "channel.edit", AdminPermChannelEdit))
}

func TestEffectiveAdminPermsExplicitNoneRevokesEverything(t *testing.T) {
	assert.Empty(t, EffectiveAdminPerms(common.RoleAdminUser, AdminPermsNone))
	assert.False(t, HasAdminPermFor(common.RoleAdminUser, AdminPermsNone, AdminPermChannelView))
}

func TestEffectiveAdminPermsSubsetAndUnknownKeys(t *testing.T) {
	perms := EffectiveAdminPerms(common.RoleAdminUser, "channel.view, bogus.perm ,quota.grant")
	assert.Equal(t, []string{AdminPermChannelView, AdminPermQuotaGrant}, perms)
	assert.True(t, HasAdminPermFor(common.RoleAdminUser, "channel.view,quota.grant", AdminPermQuotaGrant))
	assert.False(t, HasAdminPermFor(common.RoleAdminUser, "channel.view,quota.grant", AdminPermUserManage))
}

func TestEffectiveAdminPermsRootAlwaysFullNeverDeductSelf(t *testing.T) {
	// root 恒有全部模块权限，且充值永远不扣自己额度
	assert.True(t, HasAdminPermFor(common.RoleRootUser, AdminPermsNone, AdminPermChannelView))
	assert.True(t, HasAdminPermFor(common.RoleRootUser, AdminPermsNone, AdminPermUserManage))
	assert.False(t, HasAdminPermFor(common.RoleRootUser, "quota.deduct_self", AdminPermQuotaDeductSelf))
}

func TestEffectiveAdminPermsCommonUserHasNothing(t *testing.T) {
	assert.Empty(t, EffectiveAdminPerms(common.RoleCommonUser, "channel.view,user.manage"))
	assert.False(t, HasAdminPermFor(common.RoleCommonUser, "channel.view", AdminPermChannelView))
}

func TestNormalizeAdminPerms(t *testing.T) {
	raw, err := NormalizeAdminPerms([]string{AdminPermUserManage, AdminPermChannelView, AdminPermChannelView, AdminPermChannelEdit})
	require.NoError(t, err)
	// 固定按 AllAdminPerms 顺序输出且去重
	assert.Equal(t, "channel.view,channel.edit,user.manage", raw)

	raw, err = NormalizeAdminPerms(nil)
	require.NoError(t, err)
	assert.Equal(t, AdminPermsNone, raw, "空列表要落成显式 none，否则会被当成未配置而全开")

	_, err = NormalizeAdminPerms([]string{"channel.write"})
	assert.Error(t, err)
}

func TestTransferUserQuotaMovesQuotaAtomically(t *testing.T) {
	setupUserUpdateTestState(t)

	admin := &User{Username: "perm-admin", Password: "pw", AffCode: "aff-perm-admin", Role: common.RoleAdminUser, Quota: 1000, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(admin).Error)
	target := &User{Username: "perm-target", Password: "pw", AffCode: "aff-perm-target", Role: common.RoleCommonUser, Quota: 5, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(target).Error)

	require.NoError(t, TransferUserQuota(admin.Id, target.Id, 300))

	var reloadedAdmin, reloadedTarget User
	require.NoError(t, DB.First(&reloadedAdmin, admin.Id).Error)
	require.NoError(t, DB.First(&reloadedTarget, target.Id).Error)
	assert.Equal(t, 700, reloadedAdmin.Quota)
	assert.Equal(t, 305, reloadedTarget.Quota)
}

func TestTransferUserQuotaRejectsInsufficientAndSelf(t *testing.T) {
	setupUserUpdateTestState(t)

	admin := &User{Username: "poor-admin", Password: "pw", AffCode: "aff-poor-admin", Role: common.RoleAdminUser, Quota: 100, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(admin).Error)
	target := &User{Username: "poor-target", Password: "pw", AffCode: "aff-poor-target", Role: common.RoleCommonUser, Quota: 0, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(target).Error)

	require.ErrorIs(t, TransferUserQuota(admin.Id, target.Id, 101), ErrInsufficientUserQuota)
	assert.Error(t, TransferUserQuota(admin.Id, admin.Id, 10))
	assert.Error(t, TransferUserQuota(admin.Id, target.Id, 0))

	// 失败必须整笔回滚，收款方一分不能多
	var reloadedAdmin, reloadedTarget User
	require.NoError(t, DB.First(&reloadedAdmin, admin.Id).Error)
	require.NoError(t, DB.First(&reloadedTarget, target.Id).Error)
	assert.Equal(t, 100, reloadedAdmin.Quota)
	assert.Equal(t, 0, reloadedTarget.Quota)
}

func TestGetUserAdminPermsRawReadsFreshValue(t *testing.T) {
	setupUserUpdateTestState(t)

	admin := &User{Username: "raw-admin", Password: "pw", AffCode: "aff-raw-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(admin).Error)

	raw, err := GetUserAdminPermsRaw(admin.Id)
	require.NoError(t, err)
	assert.Equal(t, "", raw)

	require.NoError(t, UpdateUserAdminPerms(admin.Id, AdminPermsNone))
	raw, err = GetUserAdminPermsRaw(admin.Id)
	require.NoError(t, err)
	assert.Equal(t, AdminPermsNone, raw)

	allowed, err := UserHasAnyAdminPerm(admin.Id, common.RoleAdminUser, AdminPermUserManage, AdminPermQuotaGrant)
	require.NoError(t, err)
	assert.False(t, allowed)
}
