package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 测试基础设施
// ---------------------------------------------------------------------------

func resetSubscriptionTables(t *testing.T) {
	t.Helper()
	// TestMain 不会 migrate SubscriptionPreConsumeRecord，先按需补建；GORM AutoMigrate 幂等。
	require.NoError(t, DB.AutoMigrate(&SubscriptionPreConsumeRecord{}))
	require.NoError(t, DB.Exec("DELETE FROM subscription_pre_consume_records").Error)
	require.NoError(t, DB.Exec("DELETE FROM user_subscriptions").Error)
	require.NoError(t, DB.Exec("DELETE FROM subscription_plans").Error)
	getSubscriptionPlanCache().Purge()
}

func insertPlanForGroupTest(t *testing.T, id int, boundGroup string, totalAmount int64) {
	t.Helper()
	plan := &SubscriptionPlan{
		Id:            id,
		Title:         "plan-" + boundGroup,
		BoundGroup:    boundGroup,
		PriceAmount:   1,
		Currency:      "USD",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   totalAmount,
	}
	require.NoError(t, DB.Create(plan).Error)
}

func insertUserSubForGroupTest(t *testing.T, id, userId, planId int, amountUsed int64) {
	t.Helper()
	sub := &UserSubscription{
		Id:          id,
		UserId:      userId,
		PlanId:      planId,
		AmountTotal: 1000,
		AmountUsed:  amountUsed,
		EndTime:     GetDBTimestamp() + 86400, // 明天
		Status:      "active",
		Source:      "order",
	}
	require.NoError(t, DB.Create(sub).Error)
}

func loadUserSub(t *testing.T, id int) UserSubscription {
	t.Helper()
	var sub UserSubscription
	require.NoError(t, DB.Where("id = ?", id).First(&sub).Error)
	return sub
}

func withStrictMode(t *testing.T, strict bool) {
	t.Helper()
	prev := setting.SubscriptionStrictGroupIsolation
	setting.SubscriptionStrictGroupIsolation = strict
	t.Cleanup(func() { setting.SubscriptionStrictGroupIsolation = prev })
}

// ---------------------------------------------------------------------------
// matchBoundGroup 纯逻辑测试（无 DB 依赖）
// ---------------------------------------------------------------------------

func TestMatchBoundGroup_StrictMode(t *testing.T) {
	withStrictMode(t, true)

	assert.True(t, matchBoundGroup("codex", "codex"), "严格模式下相同 group 应匹配")
	assert.False(t, matchBoundGroup("codex", "claude"), "严格模式下不同 group 不匹配")
	assert.False(t, matchBoundGroup("", "claude"), `严格模式下 BoundGroup="" 不匹配任何 group`)
	assert.False(t, matchBoundGroup("codex", ""), `UsingGroup="" 永远不匹配`)
	assert.False(t, matchBoundGroup("", ""), "双空在严格模式下不匹配")
}

func TestMatchBoundGroup_LenientMode(t *testing.T) {
	withStrictMode(t, false)

	assert.True(t, matchBoundGroup("codex", "codex"))
	assert.False(t, matchBoundGroup("codex", "claude"))
	assert.True(t, matchBoundGroup("", "claude"), `兼容模式下 BoundGroup="" 视为通用，可被任意 group 消费`)
	assert.False(t, matchBoundGroup("codex", ""), `UsingGroup="" 在任何模式下都拒绝`)
}

// ---------------------------------------------------------------------------
// PreConsumeUserSubscription 集成测试
// ---------------------------------------------------------------------------

func TestPreConsumeUserSubscription_StrictPicksMatchingGroup_Codex(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertPlanForGroupTest(t, 2, "claude", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0)
	insertUserSubForGroupTest(t, 20, 100, 2, 0)

	res, err := PreConsumeUserSubscription("req-codex-1", 100, "gpt-4", 0, 50, "codex")
	require.NoError(t, err)
	assert.Equal(t, 10, res.UserSubscriptionId, "应扣 codex 订阅")
	assert.EqualValues(t, 50, res.PreConsumed)

	codexSub := loadUserSub(t, 10)
	claudeSub := loadUserSub(t, 20)
	assert.EqualValues(t, 50, codexSub.AmountUsed)
	assert.EqualValues(t, 0, claudeSub.AmountUsed, "claude 订阅不能被 codex 请求扣到")
}

func TestPreConsumeUserSubscription_StrictPicksMatchingGroup_Claude(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertPlanForGroupTest(t, 2, "claude", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0)
	insertUserSubForGroupTest(t, 20, 100, 2, 0)

	res, err := PreConsumeUserSubscription("req-claude-1", 100, "claude-sonnet", 0, 30, "claude")
	require.NoError(t, err)
	assert.Equal(t, 20, res.UserSubscriptionId, "应扣 claude 订阅")

	codexSub := loadUserSub(t, 10)
	claudeSub := loadUserSub(t, 20)
	assert.EqualValues(t, 0, codexSub.AmountUsed, "codex 订阅不能被 claude 请求扣到")
	assert.EqualValues(t, 30, claudeSub.AmountUsed)
}

func TestPreConsumeUserSubscription_StrictRejectsEmptyBoundGroup(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "", 1000) // 通用订阅
	insertUserSubForGroupTest(t, 10, 100, 1, 0)

	_, err := PreConsumeUserSubscription("req-empty-bound", 100, "gpt-4", 0, 50, "claude")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoEligibleSubscription),
		`严格模式下 BoundGroup="" 的订阅不能被任何 group 消费, got=%v`, err)
}

func TestPreConsumeUserSubscription_RejectsEmptyUsingGroup(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0)

	_, err := PreConsumeUserSubscription("req-empty-using", 100, "gpt-4", 0, 50, "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoEligibleSubscription),
		`UsingGroup="" 在严格模式下不能消费订阅, got=%v`, err)
}

func TestPreConsumeUserSubscription_LenientAllowsEmptyBoundGroup(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, false)

	insertPlanForGroupTest(t, 1, "", 1000) // 通用订阅
	insertUserSubForGroupTest(t, 10, 100, 1, 0)

	res, err := PreConsumeUserSubscription("req-lenient", 100, "gpt-4", 0, 50, "claude")
	require.NoError(t, err)
	assert.Equal(t, 10, res.UserSubscriptionId, "兼容模式下通用订阅应可被任意 group 消费")
}

func TestPreConsumeUserSubscription_NoMatchingGroupReturnsError(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0)

	_, err := PreConsumeUserSubscription("req-no-match", 100, "gpt-4", 0, 50, "claude")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoEligibleSubscription))
}

func TestPreConsumeUserSubscription_InsufficientFallsThroughSameGroup(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	// 两个同组订阅：sub 10 几乎用完，sub 11 余额充足
	insertUserSubForGroupTest(t, 10, 100, 1, 995) // 余 5
	insertUserSubForGroupTest(t, 11, 100, 1, 0)   // 余 1000

	res, err := PreConsumeUserSubscription("req-fallthrough", 100, "gpt-4", 0, 50, "codex")
	require.NoError(t, err)
	assert.Equal(t, 11, res.UserSubscriptionId, "余额不足时应跳到同组下一条订阅")
}

func TestPreConsumeUserSubscription_NoCrossGroupBorrow(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertPlanForGroupTest(t, 2, "claude", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0)    // codex 充足
	insertUserSubForGroupTest(t, 20, 100, 2, 1000) // claude 全部用完

	_, err := PreConsumeUserSubscription("req-no-borrow", 100, "claude-sonnet", 0, 50, "claude")
	require.Error(t, err, "claude 余额不足不能借 codex")
	assert.False(t, errors.Is(err, ErrNoEligibleSubscription),
		"有匹配的 group 订阅，错误应是余额不足而非 ErrNoEligibleSubscription, got=%v", err)

	codexSub := loadUserSub(t, 10)
	assert.EqualValues(t, 0, codexSub.AmountUsed, "codex 订阅不能被 claude 借扣")
}

func TestPreConsumeUserSubscription_IdempotentReplay(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "claude", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0)

	res1, err := PreConsumeUserSubscription("req-idem", 100, "claude-sonnet", 0, 50, "claude")
	require.NoError(t, err)
	assert.EqualValues(t, 50, res1.PreConsumed)

	// 重放同 requestId
	res2, err := PreConsumeUserSubscription("req-idem", 100, "claude-sonnet", 0, 50, "claude")
	require.NoError(t, err)
	assert.Equal(t, res1.UserSubscriptionId, res2.UserSubscriptionId)
	assert.EqualValues(t, 50, res2.PreConsumed, "重放应返回首次结果")

	sub := loadUserSub(t, 10)
	assert.EqualValues(t, 50, sub.AmountUsed, "重放不能重复扣费")
}

// NOTE: 退款链路（RefundSubscriptionPreConsume → PostConsumeUserSubscriptionDelta）涉及嵌套事务，
// 在 SQLite + SetMaxOpenConns(1) 的测试环境会自死锁，无法用本套测试覆盖；
// 退款行为未在本次改动中触及（仅按 requestId 定位 record 与 sub），由生产 MySQL/PG 环境保证。
// 本地 OrbStack 集成验证将在第 6.2 节手工跑通退款场景。

// ---------------------------------------------------------------------------
// GetEligibleActiveSubscription（入口判定）
// ---------------------------------------------------------------------------

func TestGetEligibleActiveSubscription_PicksRightGroup(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertPlanForGroupTest(t, 2, "claude", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0)
	insertUserSubForGroupTest(t, 20, 100, 2, 0)

	sub, plan, _, err := GetEligibleActiveSubscription(100, "claude")
	require.NoError(t, err)
	require.NotNil(t, sub)
	require.NotNil(t, plan)
	assert.Equal(t, 20, sub.Id)
	assert.Equal(t, "claude", plan.BoundGroup)
}

func TestGetEligibleActiveSubscription_EmptyUsingGroupReturnsNil(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0)

	sub, _, _, err := GetEligibleActiveSubscription(100, "")
	require.NoError(t, err)
	assert.Nil(t, sub, `UsingGroup="" 不应返回任何订阅`)
}

// ---------------------------------------------------------------------------
// is_hidden 软删除 + 限购计数（用户端 my-subscription 删除按钮）
// ---------------------------------------------------------------------------

func TestHideUserSubscription_RejectsActive(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0) // 默认 status=active

	err := HideUserSubscription(100, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "active")

	sub := loadUserSub(t, 10)
	assert.False(t, sub.IsHidden, "active 订阅不应被隐藏")
}

// 边界场景：status=active 但 end_time 已过期。
// 这是 ExpireDueSubscriptions cron 滞后窗口（1 分钟 tick 周期内）的典型状态。
// 修复前后端会割裂：前端按 end_time<now 显示为"已过期"+可删，后端只看 status 拒绝。
// 修复后允许隐藏。
func TestHideUserSubscription_AllowsActiveButTimeExpired(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0) // status=active, end_time = now+86400
	// 把 end_time 改成过去时间，模拟 cron 还没跑的窗口
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id=?", 10).
		Update("end_time", GetDBTimestamp()-3600).Error)

	require.NoError(t, HideUserSubscription(100, 10))

	sub := loadUserSub(t, 10)
	assert.True(t, sub.IsHidden, "status=active 但 end_time 已过应允许隐藏")
}

func TestHideUserSubscription_HidesExpired(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id=?", 10).Update("status", "expired").Error)

	require.NoError(t, HideUserSubscription(100, 10))

	sub := loadUserSub(t, 10)
	assert.True(t, sub.IsHidden, "expired 订阅应被软删除")
}

func TestHideUserSubscription_RejectsOtherUser(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id=?", 10).Update("status", "expired").Error)

	// 用户 200 尝试删用户 100 的订阅
	err := HideUserSubscription(200, 10)
	require.Error(t, err, "禁止跨用户软删除")
	assert.Contains(t, err.Error(), "not found")
}

func TestHideUserSubscription_Idempotent(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id=?", 10).Update("status", "cancelled").Error)

	require.NoError(t, HideUserSubscription(100, 10))
	require.NoError(t, HideUserSubscription(100, 10), "幂等：重复隐藏不报错")
}

func TestCountUserSubscriptionsByPlan_ExcludesHidden(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0) // active
	insertUserSubForGroupTest(t, 11, 100, 1, 0) // 将设为 expired+hidden
	insertUserSubForGroupTest(t, 12, 100, 1, 0) // 将设为 expired 不 hidden

	require.NoError(t, DB.Model(&UserSubscription{}).Where("id IN ?", []int{11, 12}).Update("status", "expired").Error)
	require.NoError(t, HideUserSubscription(100, 11))

	count, err := CountUserSubscriptionsByPlan(100, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, count, "限购计数应排除 is_hidden=true（共 3 条订阅，其中 1 条 hidden → 2）")
}

func TestGetEligibleActiveSubscription_ExcludesHidden(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0)
	// 防御性：即便 active 被强制 hidden（理论上不会发生），扣费查询也应排除
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id=?", 10).Update("is_hidden", true).Error)

	sub, _, _, err := GetEligibleActiveSubscription(100, "codex")
	require.NoError(t, err)
	assert.Nil(t, sub, "is_hidden=true 的订阅不应被扣费选中")
}

func TestGetAllUserSubscriptions_ExcludesHidden(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0)
	insertUserSubForGroupTest(t, 11, 100, 1, 0)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id=?", 11).Update("status", "expired").Error)
	require.NoError(t, HideUserSubscription(100, 11))

	list, err := GetAllUserSubscriptions(100)
	require.NoError(t, err)
	assert.Len(t, list, 1, "我的订阅列表应排除 hidden")
	if len(list) > 0 {
		assert.Equal(t, 10, list[0].Subscription.Id)
	}
}

func TestGetEligibleActiveSubscription_NoMatchReturnsNil(t *testing.T) {
	resetSubscriptionTables(t)
	withStrictMode(t, true)

	insertPlanForGroupTest(t, 1, "codex", 1000)
	insertUserSubForGroupTest(t, 10, 100, 1, 0)

	sub, _, _, err := GetEligibleActiveSubscription(100, "claude")
	require.NoError(t, err)
	assert.Nil(t, sub, "无匹配 group 应返回 nil 不报错")
}
