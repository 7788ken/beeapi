package model

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupSubscriptionTokenQuotaTestDB(t *testing.T) {
	t.Helper()
	previousDB := DB
	previousSQLite := common.UsingSQLite
	previousStrict := setting.SubscriptionStrictGroupIsolation
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", filepath.Join(t.TempDir(), "subscription-token.db"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	require.NoError(t, db.AutoMigrate(
		&User{},
		&Token{},
		&SubscriptionPlan{},
		&UserSubscription{},
		&SubscriptionPreConsumeRecord{},
	))
	DB = db
	common.UsingSQLite = true
	setting.SubscriptionStrictGroupIsolation = true
	getSubscriptionPlanCache().Purge()
	t.Cleanup(func() {
		DB = previousDB
		common.UsingSQLite = previousSQLite
		setting.SubscriptionStrictGroupIsolation = previousStrict
		getSubscriptionPlanCache().Purge()
		_ = sqlDB.Close()
	})
}

func seedSubscriptionTokenQuota(t *testing.T, tokenRemain int, subscriptionUsed int64) (int, int, int) {
	t.Helper()
	user := User{Username: "subscription-token-user", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	token := Token{
		UserId:      user.Id,
		Key:         "subscription-token-key",
		Name:        "subscription-token",
		Status:      common.TokenStatusEnabled,
		RemainQuota: tokenRemain,
	}
	require.NoError(t, DB.Create(&token).Error)
	plan := SubscriptionPlan{
		Title:         "subscription-token-plan",
		BoundGroup:    "codex",
		PriceAmount:   1,
		Currency:      "USD",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   100,
	}
	require.NoError(t, DB.Create(&plan).Error)
	subscription := UserSubscription{
		UserId:      user.Id,
		PlanId:      plan.Id,
		AmountTotal: 100,
		AmountUsed:  subscriptionUsed,
		EndTime:     GetDBTimestamp() + 86400,
		Status:      "active",
		Source:      "order",
	}
	require.NoError(t, DB.Create(&subscription).Error)
	return user.Id, token.Id, subscription.Id
}

func TestPreConsumeUserSubscriptionTokenRollsBackWhenTokenInsufficient(t *testing.T) {
	setupSubscriptionTokenQuotaTestDB(t)
	userID, tokenID, subscriptionID := seedSubscriptionTokenQuota(t, 10, 5)

	_, err := PreConsumeUserSubscriptionToken("subscription-token-rollback", userID, tokenID, "gpt-4", 0, 20, "codex")
	require.ErrorIs(t, err, ErrInsufficientTokenQuota)

	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, subscriptionID).Error)
	require.Equal(t, int64(5), subscription.AmountUsed)
	var token Token
	require.NoError(t, DB.First(&token, tokenID).Error)
	require.Equal(t, 10, token.RemainQuota)
	require.Equal(t, 0, token.UsedQuota)
	var recordCount int64
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).
		Where("request_id = ?", "subscription-token-rollback").
		Count(&recordCount).Error)
	require.Zero(t, recordCount)
}

func TestSubscriptionTokenReservationReplayAndRefundAreAtomic(t *testing.T) {
	setupSubscriptionTokenQuotaTestDB(t)
	userID, tokenID, subscriptionID := seedSubscriptionTokenQuota(t, 100, 0)
	const requestID = "subscription-token-conservation"

	first, err := PreConsumeUserSubscriptionToken(requestID, userID, tokenID, "gpt-4", 0, 30, "codex")
	require.NoError(t, err)
	require.Equal(t, subscriptionID, first.UserSubscriptionId)

	replayed, err := PreConsumeUserSubscriptionToken(requestID, userID, tokenID, "gpt-4", 0, 30, "codex")
	require.NoError(t, err)
	require.Equal(t, first.UserSubscriptionId, replayed.UserSubscriptionId)
	require.Equal(t, int64(30), replayed.PreConsumed)

	_, err = PreConsumeUserSubscriptionToken(requestID, userID, tokenID+1, "gpt-4", 0, 30, "codex")
	require.EqualError(t, err, "subscription pre-consume identity mismatch")

	require.NoError(t, ReserveSubscriptionPreConsumeTokenQuota(requestID, userID, subscriptionID, tokenID, 20))
	assertSubscriptionTokenQuota(t, subscriptionID, tokenID, 50, 50, 50)

	require.NoError(t, RefundSubscriptionPreConsume(requestID))
	assertSubscriptionTokenQuota(t, subscriptionID, tokenID, 0, 100, 0)

	require.NoError(t, RefundSubscriptionPreConsume(requestID))
	assertSubscriptionTokenQuota(t, subscriptionID, tokenID, 0, 100, 0)

	var record SubscriptionPreConsumeRecord
	require.NoError(t, DB.Where("request_id = ?", requestID).First(&record).Error)
	require.Equal(t, tokenID, record.TokenId)
	require.Equal(t, int64(50), record.PreConsumed)
	require.Equal(t, "refunded", record.Status)

	assertSubscriptionTokenQuota(t, subscriptionID, tokenID, 0, 100, 0)
}

func assertSubscriptionTokenQuota(t *testing.T, subscriptionID int, tokenID int, subscriptionUsed int64, tokenRemain int, tokenUsed int) {
	t.Helper()
	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, subscriptionID).Error)
	require.Equal(t, subscriptionUsed, subscription.AmountUsed)
	var token Token
	require.NoError(t, DB.First(&token, tokenID).Error)
	require.Equal(t, tokenRemain, token.RemainQuota)
	require.Equal(t, tokenUsed, token.UsedQuota)
}
