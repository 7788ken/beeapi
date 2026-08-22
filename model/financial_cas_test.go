package model

import (
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupFinancialCASTestDB(t *testing.T) {
	t.Helper()
	previousDB := DB
	previousLogDB := LOG_DB
	previousSQLite := common.UsingSQLite
	previousQuotaPerUnit := common.QuotaPerUnit
	previousStrictGroupIsolation := setting.SubscriptionStrictGroupIsolation

	dsn := fmt.Sprintf("file:%s?_busy_timeout=10000&_journal_mode=WAL", filepath.Join(t.TempDir(), "financial-cas.db"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	require.NoError(t, db.AutoMigrate(
		&User{},
		&Token{},
		&TopUp{},
		&Redemption{},
		&SubscriptionPlan{},
		&SubscriptionOrder{},
		&UserSubscription{},
		&SubscriptionPreConsumeRecord{},
		&Log{},
	))

	DB = db
	LOG_DB = db
	common.UsingSQLite = true
	common.QuotaPerUnit = 100
	setting.SubscriptionStrictGroupIsolation = true
	getSubscriptionPlanCache().Purge()
	t.Cleanup(func() {
		DB = previousDB
		LOG_DB = previousLogDB
		common.UsingSQLite = previousSQLite
		common.QuotaPerUnit = previousQuotaPerUnit
		setting.SubscriptionStrictGroupIsolation = previousStrictGroupIsolation
		getSubscriptionPlanCache().Purge()
		_ = sqlDB.Close()
	})
}

func runConcurrentFinancialCalls(t *testing.T, count int, call func() error) []error {
	t.Helper()
	start := make(chan struct{})
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			errs[index] = call()
		}(i)
	}
	close(start)
	wg.Wait()
	return errs
}

func TestSQLiteTopUpCASHasSingleWinner(t *testing.T) {
	setupFinancialCASTestDB(t)
	topUp := TopUp{
		UserId:          1,
		TradeNo:         "sqlite-topup-cas",
		PaymentProvider: PaymentProviderWaffo,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, DB.Create(&topUp).Error)

	var wins atomic.Int32
	errs := runConcurrentFinancialCalls(t, 8, func() error {
		err := DB.Transaction(func(tx *gorm.DB) error {
			return transitionPendingTopUpTx(tx, &TopUp{Id: topUp.Id}, map[string]interface{}{
				"status":        common.TopUpStatusSuccess,
				"complete_time": int64(1),
			})
		})
		if err == nil {
			wins.Add(1)
		}
		return err
	})
	require.EqualValues(t, 1, wins.Load(), "exactly one pending -> success CAS may win")
	for _, err := range errs {
		if err != nil {
			require.ErrorIs(t, err, ErrTopUpStatusInvalid)
		}
	}
}

func TestSQLiteConcurrentTopUpCreditsQuotaOnce(t *testing.T) {
	setupFinancialCASTestDB(t)
	user := User{Username: "sqlite-topup-user", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	topUp := TopUp{
		UserId:          user.Id,
		Amount:          2,
		Money:           2,
		TradeNo:         "sqlite-topup-credit",
		PaymentMethod:   PaymentMethodWaffoPancake,
		PaymentProvider: PaymentProviderWaffoPancake,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, DB.Create(&topUp).Error)

	runConcurrentFinancialCalls(t, 2, func() error {
		return RechargeWaffoPancake(topUp.TradeNo)
	})

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	require.Equal(t, 200, storedUser.Quota)
	var storedTopUp TopUp
	require.NoError(t, DB.First(&storedTopUp, topUp.Id).Error)
	require.Equal(t, common.TopUpStatusSuccess, storedTopUp.Status)
}

// 易支付回调曾经只判断内存里的 Status 再无条件 DB.Save，多节点同时收到同一笔回调时
// 两侧都会通过 pending 判断并各加一次额度。改为事务内 pending -> success 条件更新后，
// 只有一方能成功，另一方必须拿到 ErrTopUpStatusInvalid 且不再入账。
func TestSQLiteConcurrentEpayCreditsQuotaOnce(t *testing.T) {
	setupFinancialCASTestDB(t)
	user := User{Username: "sqlite-epay-user", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	topUp := TopUp{
		UserId:          user.Id,
		Amount:          3,
		Money:           3,
		TradeNo:         "sqlite-epay-credit",
		PaymentMethod:   "alipay",
		PaymentProvider: PaymentProviderEpay,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, DB.Create(&topUp).Error)

	errs := runConcurrentFinancialCalls(t, 4, func() error {
		_, _, err := RechargeEpay(topUp.TradeNo, "wxpay")
		return err
	})
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
			continue
		}
		require.ErrorIs(t, err, ErrTopUpStatusInvalid)
	}
	require.Equal(t, 1, successes, "同一笔易支付回调只允许结算一次")

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	require.Equal(t, 300, storedUser.Quota, "重复回调不得重复加额度")
	var storedTopUp TopUp
	require.NoError(t, DB.First(&storedTopUp, topUp.Id).Error)
	require.Equal(t, common.TopUpStatusSuccess, storedTopUp.Status)
	require.Equal(t, "wxpay", storedTopUp.PaymentMethod, "回调侧实际支付方式应落库")
	require.NotZero(t, storedTopUp.CompleteTime, "结算时间必须落库，否则对账缺少完成时间")
}

// 多节点同时收到同一笔回调时，两侧都会在对方落库前读到 pending。用 barrier 固定这个
// 交错：旧写法（内存判断 + 无条件 Save）在此处会各加一次额度，CAS 版本必须只入账一次。
func TestSQLiteEpayCreditsOnceUnderConcurrentReadInterleave(t *testing.T) {
	setupFinancialCASTestDB(t)
	user := User{Username: "sqlite-epay-interleave-user", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	topUp := TopUp{
		UserId:          user.Id,
		Amount:          3,
		Money:           3,
		TradeNo:         "sqlite-epay-interleave",
		PaymentMethod:   "alipay",
		PaymentProvider: PaymentProviderEpay,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, DB.Create(&topUp).Error)

	var afterRead sync.WaitGroup
	afterRead.Add(2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 先各自读到 pending，再同时进入结算。
			_ = GetTopUpByTradeNo(topUp.TradeNo)
			afterRead.Done()
			afterRead.Wait()
			_, _, _ = RechargeEpay(topUp.TradeNo, "wxpay")
		}()
	}
	wg.Wait()

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	require.Equal(t, 300, storedUser.Quota, "并发回调不得重复加额度")
}

func TestSQLiteEpayRejectsWrongProvider(t *testing.T) {
	setupFinancialCASTestDB(t)
	user := User{Username: "sqlite-epay-provider-user", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	topUp := TopUp{
		UserId:          user.Id,
		Amount:          3,
		Money:           3,
		TradeNo:         "sqlite-epay-provider",
		PaymentProvider: PaymentProviderStripe,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, DB.Create(&topUp).Error)

	_, _, err := RechargeEpay(topUp.TradeNo, "alipay")
	require.ErrorIs(t, err, ErrPaymentMethodMismatch)

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	require.Equal(t, 0, storedUser.Quota)
}

func TestSQLiteConcurrentRedeemCreditsQuotaOnce(t *testing.T) {
	setupFinancialCASTestDB(t)
	user := User{Username: "sqlite-redeem-user", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	redemption := Redemption{
		Key:    "sqlite-redeem-cas",
		Status: common.RedemptionCodeStatusEnabled,
		Quota:  300,
	}
	require.NoError(t, DB.Create(&redemption).Error)

	errs := runConcurrentFinancialCalls(t, 2, func() error {
		_, err := Redeem(redemption.Key, user.Id)
		return err
	})
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	require.Equal(t, redemption.Quota, storedUser.Quota)
}

func TestSQLiteConcurrentAffiliateTransferPreservesBalance(t *testing.T) {
	setupFinancialCASTestDB(t)
	user := User{
		Username: "sqlite-affiliate-user",
		Status:   common.UserStatusEnabled,
		AffQuota: 150,
	}
	require.NoError(t, DB.Create(&user).Error)

	errs := runConcurrentFinancialCalls(t, 2, func() error {
		candidate := User{Id: user.Id}
		return candidate.TransferAffQuotaToQuota(100, "127.0.0.1")
	})
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	require.Equal(t, 50, storedUser.AffQuota)
	require.Equal(t, 100, storedUser.Quota)
}

func TestSQLiteConcurrentSubscriptionPurchaseCommitsOnce(t *testing.T) {
	setupFinancialCASTestDB(t)
	user := User{Username: "sqlite-subscription-user", Status: common.UserStatusEnabled, Quota: 2000}
	require.NoError(t, DB.Create(&user).Error)
	plan := SubscriptionPlan{
		Title:         "SQLite CAS Plan",
		PriceAmount:   10,
		Currency:      "USD",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   1000,
		StockTotal:    10,
	}
	require.NoError(t, DB.Create(&plan).Error)
	order := SubscriptionOrder{
		UserId:          user.Id,
		PlanId:          plan.Id,
		Money:           plan.PriceAmount,
		TradeNo:         "sqlite-subscription-cas",
		PaymentMethod:   PaymentMethodBalance,
		PaymentProvider: PaymentProviderBalance,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, DB.Create(&order).Error)

	runConcurrentFinancialCalls(t, 2, func() error {
		return CompleteSubscriptionOrderByBalance(order.TradeNo)
	})

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	require.Equal(t, 1000, storedUser.Quota)
	var storedPlan SubscriptionPlan
	require.NoError(t, DB.First(&storedPlan, plan.Id).Error)
	require.Equal(t, 1, storedPlan.StockSold)
	var subscriptionCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", user.Id).Count(&subscriptionCount).Error)
	require.EqualValues(t, 1, subscriptionCount)
}

func TestSQLiteConcurrentSubscriptionPreConsumeUsesQuotaOnce(t *testing.T) {
	setupFinancialCASTestDB(t)
	user := User{Username: "sqlite-preconsume-user", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	token := Token{
		UserId:      user.Id,
		Key:         "sqlite-preconsume-token",
		Status:      common.TokenStatusEnabled,
		RemainQuota: 1000,
	}
	require.NoError(t, DB.Create(&token).Error)
	plan := SubscriptionPlan{
		Title:         "SQLite PreConsume Plan",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   100,
		BoundGroup:    "sqlite-group",
	}
	require.NoError(t, DB.Create(&plan).Error)
	subscription := UserSubscription{
		UserId:      user.Id,
		PlanId:      plan.Id,
		AmountTotal: 100,
		Status:      "active",
		EndTime:     common.GetTimestamp() + 3600,
	}
	require.NoError(t, DB.Create(&subscription).Error)

	var callIndex atomic.Int32
	runConcurrentFinancialCalls(t, 2, func() error {
		index := callIndex.Add(1)
		_, err := PreConsumeUserSubscriptionToken(
			fmt.Sprintf("sqlite-preconsume-%d", index),
			user.Id,
			token.Id,
			"test-model",
			0,
			60,
			"sqlite-group",
		)
		return err
	})

	var storedSubscription UserSubscription
	require.NoError(t, DB.First(&storedSubscription, subscription.Id).Error)
	require.EqualValues(t, 60, storedSubscription.AmountUsed)
	var storedToken Token
	require.NoError(t, DB.First(&storedToken, token.Id).Error)
	require.Equal(t, 940, storedToken.RemainQuota)
	require.Equal(t, 60, storedToken.UsedQuota)
	var recordCount int64
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Count(&recordCount).Error)
	require.EqualValues(t, 1, recordCount)
}

func TestSQLiteConcurrentSubscriptionRefundAppliesOnce(t *testing.T) {
	setupFinancialCASTestDB(t)
	user := User{Username: "sqlite-refund-user", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	token := Token{
		UserId:      user.Id,
		Key:         "sqlite-refund-token",
		Status:      common.TokenStatusEnabled,
		RemainQuota: 50,
		UsedQuota:   50,
	}
	require.NoError(t, DB.Create(&token).Error)
	subscription := UserSubscription{
		UserId:      user.Id,
		PlanId:      1,
		AmountTotal: 100,
		AmountUsed:  50,
		Status:      "active",
		EndTime:     common.GetTimestamp() + 3600,
	}
	require.NoError(t, DB.Create(&subscription).Error)
	record := SubscriptionPreConsumeRecord{
		RequestId:          "sqlite-refund-cas",
		UserId:             user.Id,
		TokenId:            token.Id,
		UserSubscriptionId: subscription.Id,
		PreConsumed:        50,
		Status:             "consumed",
	}
	require.NoError(t, DB.Create(&record).Error)

	runConcurrentFinancialCalls(t, 2, func() error {
		return RefundSubscriptionPreConsume(record.RequestId)
	})

	var storedSubscription UserSubscription
	require.NoError(t, DB.First(&storedSubscription, subscription.Id).Error)
	require.Zero(t, storedSubscription.AmountUsed)
	var storedToken Token
	require.NoError(t, DB.First(&storedToken, token.Id).Error)
	require.Equal(t, 100, storedToken.RemainQuota)
	require.Zero(t, storedToken.UsedQuota)
	var storedRecord SubscriptionPreConsumeRecord
	require.NoError(t, DB.First(&storedRecord, record.Id).Error)
	require.Equal(t, "refunded", storedRecord.Status)
}
