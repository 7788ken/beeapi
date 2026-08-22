package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertUserForPaymentGuardTest(t *testing.T, id int, quota int) {
	t.Helper()
	user := &User{
		Id:       id,
		Username: "payment_guard_user",
		Status:   common.UserStatusEnabled,
		Quota:    quota,
	}
	require.NoError(t, DB.Create(user).Error)
}

func insertSubscriptionPlanForPaymentGuardTest(t *testing.T, id int) *SubscriptionPlan {
	t.Helper()
	plan := &SubscriptionPlan{
		Id:            id,
		Title:         "Guard Plan",
		PriceAmount:   9.99,
		Currency:      "USD",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   1000,
	}
	require.NoError(t, DB.Create(plan).Error)
	return plan
}

func insertSubscriptionOrderForPaymentGuardTest(t *testing.T, tradeNo string, userID int, planID int, paymentProvider string) {
	t.Helper()
	order := &SubscriptionOrder{
		UserId:          userID,
		PlanId:          planID,
		Money:           9.99,
		TradeNo:         tradeNo,
		PaymentMethod:   paymentProvider,
		PaymentProvider: paymentProvider,
		Status:          common.TopUpStatusPending,
		CreateTime:      time.Now().Unix(),
	}
	require.NoError(t, order.Insert())
}

func insertTopUpForPaymentGuardTest(t *testing.T, tradeNo string, userID int, paymentProvider string) {
	t.Helper()
	topUp := &TopUp{
		UserId:          userID,
		Amount:          2,
		Money:           9.99,
		TradeNo:         tradeNo,
		PaymentMethod:   paymentProvider,
		PaymentProvider: paymentProvider,
		Status:          common.TopUpStatusPending,
		CreateTime:      time.Now().Unix(),
	}
	require.NoError(t, topUp.Insert())
}

func getTopUpStatusForPaymentGuardTest(t *testing.T, tradeNo string) string {
	t.Helper()
	topUp := GetTopUpByTradeNo(tradeNo)
	require.NotNil(t, topUp)
	return topUp.Status
}

func countUserSubscriptionsForPaymentGuardTest(t *testing.T, userID int) int64 {
	t.Helper()
	var count int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", userID).Count(&count).Error)
	return count
}

func getUserQuotaForPaymentGuardTest(t *testing.T, userID int) int {
	t.Helper()
	var user User
	require.NoError(t, DB.Select("quota").Where("id = ?", userID).First(&user).Error)
	return user.Quota
}

func TestRechargeWaffoPancake_RejectsMismatchedPaymentMethod(t *testing.T) {
	truncateTables(t)

	insertUserForPaymentGuardTest(t, 101, 0)
	insertTopUpForPaymentGuardTest(t, "waffo-pancake-guard", 101, PaymentProviderStripe)

	err := RechargeWaffoPancake("waffo-pancake-guard")
	require.Error(t, err)

	topUp := GetTopUpByTradeNo("waffo-pancake-guard")
	require.NotNil(t, topUp)
	assert.Equal(t, common.TopUpStatusPending, topUp.Status)
	assert.Equal(t, 0, getUserQuotaForPaymentGuardTest(t, 101))
}

func TestUpdatePendingTopUpStatus_RejectsMismatchedPaymentProvider(t *testing.T) {
	testCases := []struct {
		name                    string
		tradeNo                 string
		storedPaymentProvider   string
		expectedPaymentProvider string
		targetStatus            string
	}{
		{
			name:                    "stripe expire",
			tradeNo:                 "stripe-expire-guard",
			storedPaymentProvider:   PaymentProviderCreem,
			expectedPaymentProvider: PaymentProviderStripe,
			targetStatus:            common.TopUpStatusExpired,
		},
		{
			name:                    "waffo failed",
			tradeNo:                 "waffo-failed-guard",
			storedPaymentProvider:   PaymentProviderStripe,
			expectedPaymentProvider: PaymentProviderWaffo,
			targetStatus:            common.TopUpStatusFailed,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			insertUserForPaymentGuardTest(t, 150, 0)
			insertTopUpForPaymentGuardTest(t, tc.tradeNo, 150, tc.storedPaymentProvider)

			err := UpdatePendingTopUpStatus(tc.tradeNo, tc.expectedPaymentProvider, tc.targetStatus)
			require.ErrorIs(t, err, ErrPaymentMethodMismatch)
			assert.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, tc.tradeNo))
		})
	}
}

func TestCompleteSubscriptionOrder_RejectsMismatchedPaymentProvider(t *testing.T) {
	truncateTables(t)

	insertUserForPaymentGuardTest(t, 202, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 301)
	insertSubscriptionOrderForPaymentGuardTest(t, "sub-guard-order", 202, plan.Id, PaymentProviderStripe)

	err := CompleteSubscriptionOrder("sub-guard-order", `{"provider":"epay"}`, PaymentProviderEpay, "alipay")
	require.ErrorIs(t, err, ErrPaymentMethodMismatch)

	order := GetSubscriptionOrderByTradeNo("sub-guard-order")
	require.NotNil(t, order)
	assert.Equal(t, common.TopUpStatusPending, order.Status)
	assert.Zero(t, countUserSubscriptionsForPaymentGuardTest(t, 202))

	topUp := GetTopUpByTradeNo("sub-guard-order")
	assert.Nil(t, topUp)
}

func TestCompleteSubscriptionOrderCompletesSharedTopUpOnce(t *testing.T) {
	truncateTables(t)

	insertUserForPaymentGuardTest(t, 203, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 302)
	require.NoError(t, DB.Model(plan).Updates(map[string]interface{}{
		"stock_total": 10,
		"stock_sold":  0,
	}).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	insertSubscriptionOrderForPaymentGuardTest(t, "sub-shared-topup", 203, plan.Id, PaymentProviderStripe)
	insertTopUpForPaymentGuardTest(t, "sub-shared-topup", 203, PaymentProviderStripe)

	require.NoError(t, CompleteSubscriptionOrder(
		"sub-shared-topup",
		`{"provider":"stripe"}`,
		PaymentProviderStripe,
		PaymentMethodStripe,
	))

	order := GetSubscriptionOrderByTradeNo("sub-shared-topup")
	require.NotNil(t, order)
	assert.Equal(t, common.TopUpStatusSuccess, order.Status)
	assert.Equal(t, common.TopUpStatusSuccess, getTopUpStatusForPaymentGuardTest(t, "sub-shared-topup"))
	assert.EqualValues(t, 1, countUserSubscriptionsForPaymentGuardTest(t, 203))
	var storedPlan SubscriptionPlan
	require.NoError(t, DB.Select("stock_sold").First(&storedPlan, plan.Id).Error)
	assert.Equal(t, 1, storedPlan.StockSold)

	require.NoError(t, CompleteSubscriptionOrder(
		"sub-shared-topup",
		`{"provider":"stripe"}`,
		PaymentProviderStripe,
		PaymentMethodStripe,
	))
	assert.EqualValues(t, 1, countUserSubscriptionsForPaymentGuardTest(t, 203))
	require.NoError(t, DB.Select("stock_sold").First(&storedPlan, plan.Id).Error)
	assert.Equal(t, 1, storedPlan.StockSold)
}

func TestCompleteSubscriptionOrderRollsBackEarlyTopUpWhenPlanSoldOut(t *testing.T) {
	truncateTables(t)

	insertUserForPaymentGuardTest(t, 204, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 303)
	require.NoError(t, DB.Model(plan).Updates(map[string]interface{}{
		"stock_total": 1,
		"stock_sold":  1,
	}).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	insertSubscriptionOrderForPaymentGuardTest(t, "sub-shared-topup-sold-out", 204, plan.Id, PaymentProviderStripe)
	insertTopUpForPaymentGuardTest(t, "sub-shared-topup-sold-out", 204, PaymentProviderStripe)

	err := CompleteSubscriptionOrder(
		"sub-shared-topup-sold-out",
		`{"provider":"stripe"}`,
		PaymentProviderStripe,
		PaymentMethodStripe,
	)
	require.ErrorIs(t, err, ErrSubscriptionPlanSoldOut)

	order := GetSubscriptionOrderByTradeNo("sub-shared-topup-sold-out")
	require.NotNil(t, order)
	assert.Equal(t, common.TopUpStatusPending, order.Status)
	assert.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, "sub-shared-topup-sold-out"))
	assert.Zero(t, countUserSubscriptionsForPaymentGuardTest(t, 204))
}

func TestCompleteSubscriptionOrderByBalanceIncrementsStockOnce(t *testing.T) {
	truncateTables(t)

	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1000
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
	})

	insertUserForPaymentGuardTest(t, 250, 5000)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 350)
	require.NoError(t, DB.Model(plan).Updates(map[string]interface{}{
		"price_amount": 1.0,
		"stock_total":  10,
		"stock_sold":   0,
	}).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	order := &SubscriptionOrder{
		UserId:          250,
		PlanId:          plan.Id,
		Money:           1.0,
		TradeNo:         "sub-balance-stock-once",
		PaymentMethod:   PaymentMethodBalance,
		PaymentProvider: PaymentProviderBalance,
		Status:          common.TopUpStatusPending,
		CreateTime:      time.Now().Unix(),
	}
	require.NoError(t, order.Insert())

	require.NoError(t, CompleteSubscriptionOrderByBalance("sub-balance-stock-once"))

	var storedPlan SubscriptionPlan
	require.NoError(t, DB.Select("stock_sold").First(&storedPlan, plan.Id).Error)
	assert.Equal(t, 1, storedPlan.StockSold)
	assert.Equal(t, 4000, getUserQuotaForPaymentGuardTest(t, 250))
	assert.EqualValues(t, 1, countUserSubscriptionsForPaymentGuardTest(t, 250))
	storedOrder := GetSubscriptionOrderByTradeNo("sub-balance-stock-once")
	require.NotNil(t, storedOrder)
	assert.Equal(t, common.TopUpStatusSuccess, storedOrder.Status)

	require.NoError(t, CompleteSubscriptionOrderByBalance("sub-balance-stock-once"))

	require.NoError(t, DB.Select("stock_sold").First(&storedPlan, plan.Id).Error)
	assert.Equal(t, 1, storedPlan.StockSold)
	assert.Equal(t, 4000, getUserQuotaForPaymentGuardTest(t, 250))
	assert.EqualValues(t, 1, countUserSubscriptionsForPaymentGuardTest(t, 250))
}

func TestCompleteSubscriptionOrderByBalanceRollsBackStockWhenQuotaInsufficient(t *testing.T) {
	truncateTables(t)

	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1000
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
	})

	insertUserForPaymentGuardTest(t, 251, 999)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 351)
	require.NoError(t, DB.Model(plan).Updates(map[string]interface{}{
		"price_amount": 1.0,
		"stock_total":  10,
		"stock_sold":   0,
	}).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	order := &SubscriptionOrder{
		UserId:          251,
		PlanId:          plan.Id,
		Money:           1.0,
		TradeNo:         "sub-balance-insufficient-quota",
		PaymentMethod:   PaymentMethodBalance,
		PaymentProvider: PaymentProviderBalance,
		Status:          common.TopUpStatusPending,
		CreateTime:      time.Now().Unix(),
	}
	require.NoError(t, order.Insert())

	err := CompleteSubscriptionOrderByBalance("sub-balance-insufficient-quota")
	require.ErrorIs(t, err, ErrSubscriptionInsufficientQuota)

	var storedPlan SubscriptionPlan
	require.NoError(t, DB.Select("stock_sold").First(&storedPlan, plan.Id).Error)
	assert.Equal(t, 0, storedPlan.StockSold)
	assert.Equal(t, 999, getUserQuotaForPaymentGuardTest(t, 251))
	assert.Zero(t, countUserSubscriptionsForPaymentGuardTest(t, 251))
	storedOrder := GetSubscriptionOrderByTradeNo("sub-balance-insufficient-quota")
	require.NotNil(t, storedOrder)
	assert.Equal(t, common.TopUpStatusPending, storedOrder.Status)
}

func TestExpireSubscriptionOrder_RejectsMismatchedPaymentProvider(t *testing.T) {
	truncateTables(t)

	insertUserForPaymentGuardTest(t, 303, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 401)
	insertSubscriptionOrderForPaymentGuardTest(t, "sub-expire-guard", 303, plan.Id, PaymentProviderStripe)

	err := ExpireSubscriptionOrder("sub-expire-guard", PaymentProviderCreem)
	require.ErrorIs(t, err, ErrPaymentMethodMismatch)

	order := GetSubscriptionOrderByTradeNo("sub-expire-guard")
	require.NotNil(t, order)
	assert.Equal(t, common.TopUpStatusPending, order.Status)
}

func TestGetTopUpByProviderOrderID(t *testing.T) {
	truncateTables(t)

	insertUserForPaymentGuardTest(t, 501, 0)

	target := &TopUp{
		UserId:          501,
		Amount:          5,
		Money:           4.99,
		TradeNo:         "WAFFO_PANCAKE-501-1700000000000-target",
		PaymentMethod:   PaymentMethodWaffoPancake,
		PaymentProvider: PaymentProviderWaffoPancake,
		ProviderOrderID: "ORD_target",
		Status:          common.TopUpStatusPending,
		CreateTime:      time.Now().Unix(),
	}
	require.NoError(t, target.Insert())

	otherProvider := &TopUp{
		UserId:          501,
		Amount:          5,
		Money:           4.99,
		TradeNo:         "STRIPE-collision",
		PaymentMethod:   PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe,
		ProviderOrderID: "ORD_target", // 同 OrderID 但不同 provider
		Status:          common.TopUpStatusPending,
		CreateTime:      time.Now().Unix(),
	}
	require.NoError(t, otherProvider.Insert())

	got := GetTopUpByProviderOrderID(PaymentProviderWaffoPancake, "ORD_target")
	require.NotNil(t, got)
	assert.Equal(t, target.TradeNo, got.TradeNo)

	assert.Nil(t, GetTopUpByProviderOrderID(PaymentProviderWaffoPancake, "ORD_unknown"))
	assert.Nil(t, GetTopUpByProviderOrderID("", "ORD_target"))
	assert.Nil(t, GetTopUpByProviderOrderID(PaymentProviderWaffoPancake, ""))
}
