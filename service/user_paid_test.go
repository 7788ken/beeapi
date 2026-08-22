package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func setupPaidUserTest(t *testing.T) {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Redemption{}, &model.SubscriptionOrder{}))
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM users")
		model.DB.Exec("DELETE FROM top_ups")
		model.DB.Exec("DELETE FROM redemptions")
		model.DB.Exec("DELETE FROM subscription_orders")
		paidUserCache.Range(func(k, _ any) bool { paidUserCache.Delete(k); return true })
	})
}

func seedPaidUser(t *testing.T, id int, usedQuota int) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.User{Id: id, Username: "u", UsedQuota: usedQuota}).Error)
}

// 白嫖号：赠送/签到范围内的消费，且无任何充值记录 —— 不该再发余额提醒。
func TestIsPaidUserFreeAccountFiltered(t *testing.T) {
	setupPaidUserTest(t)
	seedPaidUser(t, 8001, 5247801) // ≈$10，站内典型签到党量级

	require.False(t, IsPaidUser(8001))
}

func TestIsPaidUserWithTopUp(t *testing.T) {
	setupPaidUserTest(t)
	seedPaidUser(t, 8002, 100)
	require.NoError(t, model.DB.Create(&model.TopUp{UserId: 8002, TradeNo: "t8002", Status: common.TopUpStatusSuccess}).Error)

	require.True(t, IsPaidUser(8002))
}

func TestIsPaidUserWithRedemption(t *testing.T) {
	setupPaidUserTest(t)
	seedPaidUser(t, 8003, 100)
	require.NoError(t, model.DB.Create(&model.Redemption{Key: "k8003", UsedUserId: 8003}).Error)

	require.True(t, IsPaidUser(8003))
}

// 额度由管理员直充的老客户在充值表里查无记录，只能靠历史消费兜底识别。
// 站内这类账号（余额已见底、消费上千美元）正是最需要提醒的人。
func TestIsPaidUserAdminGrantedFallback(t *testing.T) {
	setupPaidUserTest(t)
	seedPaidUser(t, 8004, paidUserUsedQuotaFallback)

	require.True(t, IsPaidUser(8004))
}

func TestIsPaidUserUnknownUser(t *testing.T) {
	setupPaidUserTest(t)

	require.False(t, IsPaidUser(8005))
	require.False(t, IsPaidUser(0))
}

// 判定结果进缓存，低余额用户的每次请求不应打一次库。
func TestIsPaidUserCached(t *testing.T) {
	setupPaidUserTest(t)
	seedPaidUser(t, 8006, 100)
	require.False(t, IsPaidUser(8006))

	require.NoError(t, model.DB.Create(&model.TopUp{UserId: 8006, TradeNo: "t8006", Status: common.TopUpStatusSuccess}).Error)
	require.False(t, IsPaidUser(8006), "缓存期内不应重查")

	paidUserCache.Delete(8006)
	require.True(t, IsPaidUser(8006))
}
