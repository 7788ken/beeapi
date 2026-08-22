package model

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 订阅额度耗尽处理（ProcessExhaustedSubscriptions）
// 多订阅并存语义：降级/分组耗尽判定均按分组维度，异组订阅互不影响。
// ---------------------------------------------------------------------------

// setupExhaustTest 补齐 TestMain 未做的列名初始化（initCol 幂等），并重置订阅表。
func setupExhaustTest(t *testing.T) {
	t.Helper()
	initCol()
	resetSubscriptionTables(t)
	withStrictMode(t, true)
}

func insertExhaustUser(t *testing.T, id int, group, pref string) {
	t.Helper()
	setting := ""
	if pref != "" {
		setting = fmt.Sprintf(`{"billing_preference":%q}`, pref)
	}
	u := &User{
		Id:       id,
		Username: fmt.Sprintf("exhaust-u%d", id),
		Password: "testpass123",
		Email:    fmt.Sprintf("u%d@test.local", id),
		Group:    group,
		AffCode:  fmt.Sprintf("EX%d", id),
		Setting:  setting,
	}
	require.NoError(t, DB.Create(u).Error)
	t.Cleanup(func() { DB.Exec("DELETE FROM users WHERE id = ?", id) })
}

func insertExhaustSub(t *testing.T, id, userId, planId int, upgradeGroup string, total, used int64) {
	t.Helper()
	sub := &UserSubscription{
		Id:           id,
		UserId:       userId,
		PlanId:       planId,
		AmountTotal:  total,
		AmountUsed:   used,
		EndTime:      GetDBTimestamp() + 86400,
		Status:       "active",
		Source:       "order",
		UpgradeGroup: upgradeGroup,
	}
	require.NoError(t, DB.Create(sub).Error)
}

func loadUserGroup(t *testing.T, userId int) string {
	t.Helper()
	var g string
	require.NoError(t, DB.Model(&User{}).Where("id = ?", userId).Select(commonGroupCol).Find(&g).Error)
	return g
}

// 同组还有可用订阅：仅当前订阅转 exhausted，不降级、不算分组耗尽（不发通知）。
func TestProcessExhausted_SameGroupRemaining_NoDowngrade(t *testing.T) {
	setupExhaustTest(t)
	insertExhaustUser(t, 900, "a", "subscription_then_auto")
	insertPlanForGroupTest(t, 1, "a", 1000)
	insertExhaustSub(t, 10, 900, 1, "a", 1000, 1000) // 耗尽
	insertExhaustSub(t, 11, 900, 1, "a", 1000, 100)  // 同组仍有量

	n, events, err := ProcessExhaustedSubscriptions(100)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.Len(t, events, 1)
	assert.False(t, events[0].DowngradedToAuto, "同组仍有可用订阅，不应降级")
	assert.False(t, events[0].GroupExhausted, "同组仍有可用订阅，不算分组耗尽")

	assert.Equal(t, "a", loadUserGroup(t, 900), "用户分组不应变化")
	assert.Equal(t, "exhausted", loadUserSub(t, 10).Status)
	assert.Equal(t, "active", loadUserSub(t, 11).Status, "有量订阅不受影响")
}

// 用户场景：a 组订阅×2 全部耗尽、b 组订阅有量。
// b 组订阅不阻止 a 组降级；分组耗尽通知恰好触发一次（挂在 a 组最后一个被处理的订阅上）。
func TestProcessExhausted_CrossGroupNotBlockingDowngrade(t *testing.T) {
	setupExhaustTest(t)
	insertExhaustUser(t, 901, "a", "subscription_then_auto")
	insertPlanForGroupTest(t, 1, "a", 1000)
	insertPlanForGroupTest(t, 2, "b", 1000)
	insertExhaustSub(t, 20, 901, 1, "a", 1000, 1000)
	insertExhaustSub(t, 21, 901, 1, "a", 1000, 1000)
	insertExhaustSub(t, 22, 901, 2, "b", 1000, 50)

	n, events, err := ProcessExhaustedSubscriptions(100)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	require.Len(t, events, 2)

	groupExhaustedCount := 0
	downgradedCount := 0
	for _, e := range events {
		if e.GroupExhausted {
			groupExhaustedCount++
		}
		if e.DowngradedToAuto {
			downgradedCount++
		}
	}
	assert.Equal(t, 1, groupExhaustedCount, "分组耗尽通知应恰好触发一次")
	assert.Equal(t, 1, downgradedCount, "降级应恰好发生一次")

	assert.Equal(t, "auto", loadUserGroup(t, 901), "b 组生效订阅不应阻止 a 组降级到 auto")
	assert.Equal(t, "exhausted", loadUserSub(t, 20).Status)
	assert.Equal(t, "exhausted", loadUserSub(t, 21).Status)
	assert.Equal(t, "active", loadUserSub(t, 22).Status, "b 组订阅不受影响")
	assert.EqualValues(t, 0, loadUserSub(t, 22).ExhaustNotifiedAt)
}

// 默认偏好（subscription_first）：仅落标记，status 保持 active（请求路径继续 403），不降级。
func TestProcessExhausted_DefaultPref_MarkOnly(t *testing.T) {
	setupExhaustTest(t)
	insertExhaustUser(t, 902, "a", "")
	insertPlanForGroupTest(t, 1, "a", 1000)
	insertExhaustSub(t, 30, 902, 1, "a", 1000, 1000)

	n, events, err := ProcessExhaustedSubscriptions(100)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.Len(t, events, 1)
	assert.False(t, events[0].DowngradedToAuto)
	assert.True(t, events[0].GroupExhausted, "单订阅分组耗尽应触发通知")

	sub := loadUserSub(t, 30)
	assert.Equal(t, "active", sub.Status, "默认偏好不改状态，请求路径靠 403 停止")
	assert.Greater(t, sub.ExhaustNotifiedAt, int64(0))
	assert.Equal(t, "a", loadUserGroup(t, 902))

	// 幂等：已标记的订阅不再被扫描
	n2, events2, err := ProcessExhaustedSubscriptions(100)
	require.NoError(t, err)
	assert.Equal(t, 0, n2)
	assert.Empty(t, events2)
}

// 计费入口：分组内订阅全部 exhausted 时返回 hasExhausted=true（调用方拒绝而非回退钱包）。
func TestGetEligibleActiveSubscription_HasExhausted(t *testing.T) {
	setupExhaustTest(t)
	insertPlanForGroupTest(t, 1, "a", 1000)
	insertExhaustSub(t, 40, 903, 1, "a", 1000, 1000)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", 40).Update("status", "exhausted").Error)

	sub, _, hasExhausted, err := GetEligibleActiveSubscription(903, "a")
	require.NoError(t, err)
	assert.Nil(t, sub)
	assert.True(t, hasExhausted, "组内存在 exhausted 订阅时必须返回标记，禁止回退钱包")

	// 组内补一条 active：应优先返回 active，hasExhausted 仍为 true 但不影响扣订阅
	insertExhaustSub(t, 41, 903, 1, "a", 1000, 0)
	sub, _, hasExhausted, err = GetEligibleActiveSubscription(903, "a")
	require.NoError(t, err)
	require.NotNil(t, sub)
	assert.Equal(t, 41, sub.Id)
	assert.True(t, hasExhausted)
}
