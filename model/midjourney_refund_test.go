package model

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupMidjourneyRefundTestDB(t *testing.T) {
	t.Helper()
	previousDB := DB
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", filepath.Join(t.TempDir(), "midjourney-refund.db"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &UserSubscription{}, &Midjourney{}, &AsyncTaskRefundRecord{}))
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		_ = sqlDB.Close()
	})
}

func seedMidjourneyRefundTask(t *testing.T, tokenUsed int) (*User, *Token, *Midjourney) {
	t.Helper()
	user := &User{Username: "midjourney-refund-user", Status: common.UserStatusEnabled, Quota: 40}
	require.NoError(t, DB.Create(user).Error)
	token := &Token{
		UserId:      user.Id,
		Key:         "midjourney-refund-token",
		Name:        "midjourney-refund",
		Status:      common.TokenStatusEnabled,
		RemainQuota: 100 - tokenUsed,
		UsedQuota:   tokenUsed,
	}
	require.NoError(t, DB.Create(token).Error)
	task := &Midjourney{
		UserId:        user.Id,
		TokenId:       token.Id,
		MjId:          "midjourney-refund-task",
		Status:        "IN_PROGRESS",
		Progress:      "50%",
		Quota:         tokenUsed,
		BillingSource: "wallet",
		ChannelId:     1,
	}
	require.NoError(t, DB.Create(task).Error)
	return user, token, task
}

func TestMidjourneyFailureTransitionRefundsUserAndTokenOnce(t *testing.T) {
	setupMidjourneyRefundTestDB(t)
	user, token, task := seedMidjourneyRefundTask(t, 60)
	task.Status = "FAILURE"
	task.Progress = "100%"
	task.FailReason = "upstream failed"

	won, err := task.UpdateWithStatusAndRefund("IN_PROGRESS")
	require.NoError(t, err)
	require.True(t, won)
	assertMidjourneyRefundState(t, user.Id, token.Id, task.Id, 100, 100, 0, "FAILURE")
	var refundCount int64
	require.NoError(t, DB.Model(&AsyncTaskRefundRecord{}).
		Where("task_kind = ? AND task_db_id = ?", AsyncTaskRefundKindMidjourney, task.Id).
		Count(&refundCount).Error)
	require.EqualValues(t, 1, refundCount)

	won, err = task.UpdateWithStatusAndRefund("IN_PROGRESS")
	require.NoError(t, err)
	require.False(t, won)
	assertMidjourneyRefundState(t, user.Id, token.Id, task.Id, 100, 100, 0, "FAILURE")
}

func TestMidjourneyConsumeAndFailureRefundShareTaskChargeState(t *testing.T) {
	setupMidjourneyRefundTestDB(t)
	user := &User{Username: "midjourney-consume-user", Status: common.UserStatusEnabled, Quota: 100}
	require.NoError(t, DB.Create(user).Error)
	token := &Token{
		UserId:      user.Id,
		Key:         "midjourney-consume-token",
		Name:        "midjourney-consume",
		Status:      common.TokenStatusEnabled,
		RemainQuota: 100,
	}
	require.NoError(t, DB.Create(token).Error)
	task := &Midjourney{
		UserId:   user.Id,
		TokenId:  token.Id,
		MjId:     "midjourney-consume-task",
		Status:   "IN_PROGRESS",
		Progress: "0%",
		Quota:    0,
	}
	require.NoError(t, DB.Create(task).Error)

	require.NoError(t, task.ConsumeQuota(user.Id, token.Id, 0, 60))
	assertMidjourneyRefundState(t, user.Id, token.Id, task.Id, 40, 40, 60, "IN_PROGRESS")

	task.Status = "FAILURE"
	task.Progress = "100%"
	won, err := task.UpdateWithStatusAndRefund("IN_PROGRESS")
	require.NoError(t, err)
	require.True(t, won)
	assertMidjourneyRefundState(t, user.Id, token.Id, task.Id, 100, 100, 0, "FAILURE")
}

func TestMidjourneySubscriptionFailureRefundsOriginalSubscriptionAndToken(t *testing.T) {
	setupMidjourneyRefundTestDB(t)
	user := &User{Username: "midjourney-subscription-user", Status: common.UserStatusEnabled, Quota: 25}
	require.NoError(t, DB.Create(user).Error)
	token := &Token{
		UserId:      user.Id,
		Key:         "midjourney-subscription-token",
		Name:        "midjourney-subscription",
		Status:      common.TokenStatusEnabled,
		RemainQuota: 100,
	}
	require.NoError(t, DB.Create(token).Error)
	subscription := &UserSubscription{
		UserId:      user.Id,
		AmountTotal: 1000,
		AmountUsed:  100,
		Status:      "active",
		EndTime:     GetDBTimestamp() + 3600,
	}
	require.NoError(t, DB.Create(subscription).Error)
	task := &Midjourney{
		UserId:   user.Id,
		MjId:     "midjourney-subscription-task",
		Status:   "IN_PROGRESS",
		Progress: "0%",
	}
	require.NoError(t, DB.Create(task).Error)

	require.NoError(t, task.ConsumeQuota(user.Id, token.Id, subscription.Id, 60))
	var chargedSubscription UserSubscription
	require.NoError(t, DB.First(&chargedSubscription, subscription.Id).Error)
	require.EqualValues(t, 160, chargedSubscription.AmountUsed)
	assertMidjourneyRefundState(t, user.Id, token.Id, task.Id, 25, 40, 60, "IN_PROGRESS")

	task.Status = "FAILURE"
	task.Progress = "100%"
	task.FailReason = "subscription upstream failure"
	won, err := task.UpdateWithStatusAndRefund("IN_PROGRESS")
	require.NoError(t, err)
	require.True(t, won)

	var refundedSubscription UserSubscription
	require.NoError(t, DB.First(&refundedSubscription, subscription.Id).Error)
	require.EqualValues(t, 100, refundedSubscription.AmountUsed)
	assertMidjourneyRefundState(t, user.Id, token.Id, task.Id, 25, 100, 0, "FAILURE")

	var record AsyncTaskRefundRecord
	require.NoError(t, DB.Where(
		"task_kind = ? AND task_db_id = ?",
		AsyncTaskRefundKindMidjourney,
		task.Id,
	).First(&record).Error)
	require.Equal(t, "subscription", record.BillingSource)
	require.Equal(t, subscription.Id, record.UserSubscriptionId)
}

func TestMidjourneyConsumeRollsBackChargeMarkerWhenTokenIsInsufficient(t *testing.T) {
	setupMidjourneyRefundTestDB(t)
	user := &User{Username: "midjourney-consume-rollback", Status: common.UserStatusEnabled, Quota: 100}
	require.NoError(t, DB.Create(user).Error)
	token := &Token{
		UserId:      user.Id,
		Key:         "midjourney-consume-rollback-token",
		Name:        "midjourney-consume-rollback",
		Status:      common.TokenStatusEnabled,
		RemainQuota: 10,
	}
	require.NoError(t, DB.Create(token).Error)
	task := &Midjourney{
		UserId:   user.Id,
		TokenId:  token.Id,
		MjId:     "midjourney-consume-rollback-task",
		Status:   "IN_PROGRESS",
		Progress: "0%",
		Quota:    0,
	}
	require.NoError(t, DB.Create(task).Error)

	require.ErrorIs(t, task.ConsumeQuota(user.Id, token.Id, 0, 60), ErrInsufficientTokenQuota)
	assertMidjourneyRefundState(t, user.Id, token.Id, task.Id, 100, 10, 0, "IN_PROGRESS")
	var persistedTask Midjourney
	require.NoError(t, DB.First(&persistedTask, task.Id).Error)
	require.Zero(t, persistedTask.Quota)
}

func TestMidjourneyFailureRefundRollsBackTerminalStateWhenTokenCannotRefund(t *testing.T) {
	setupMidjourneyRefundTestDB(t)
	user, token, task := seedMidjourneyRefundTask(t, 10)
	task.Quota = 60
	task.Status = "FAILURE"
	task.Progress = "100%"

	won, err := task.UpdateWithStatusAndRefund("IN_PROGRESS")
	require.ErrorIs(t, err, ErrInsufficientTokenQuota)
	require.False(t, won)
	assertMidjourneyRefundState(t, user.Id, token.Id, task.Id, 40, 90, 10, "IN_PROGRESS")
	var refundCount int64
	require.NoError(t, DB.Model(&AsyncTaskRefundRecord{}).Count(&refundCount).Error)
	require.EqualValues(t, 0, refundCount)
}

func TestMidjourneyHistoricalUnknownBillingSourceRequiresManualReconciliation(t *testing.T) {
	setupMidjourneyRefundTestDB(t)
	user, token, task := seedMidjourneyRefundTask(t, 10)
	task.BillingSource = ""
	require.NoError(t, DB.Model(&Midjourney{}).
		Where("id = ?", task.Id).
		Update("billing_source", "").Error)
	task.Status = "FAILURE"
	task.Progress = "100%"

	won, err := task.UpdateWithStatusAndRefund("IN_PROGRESS")
	require.ErrorContains(t, err, "billing source is unknown")
	require.False(t, won)
	assertMidjourneyRefundState(t, user.Id, token.Id, task.Id, 40, 90, 10, "IN_PROGRESS")
	var refundCount int64
	require.NoError(t, DB.Model(&AsyncTaskRefundRecord{}).Count(&refundCount).Error)
	require.EqualValues(t, 0, refundCount)
}

func TestMidjourneyUnchargedFailureDoesNotRefundOtherUsage(t *testing.T) {
	setupMidjourneyRefundTestDB(t)
	user, token, task := seedMidjourneyRefundTask(t, 60)
	task.Quota = 0
	require.NoError(t, DB.Model(&Midjourney{}).Where("id = ?", task.Id).Update("quota", 0).Error)
	task.Status = "FAILURE"
	task.Progress = "100%"

	won, err := task.UpdateWithStatusAndRefund("IN_PROGRESS")
	require.NoError(t, err)
	require.True(t, won)
	assertMidjourneyRefundState(t, user.Id, token.Id, task.Id, 40, 40, 60, "FAILURE")
}

func assertMidjourneyRefundState(t *testing.T, userID int, tokenID int, taskID int, userQuota int, tokenRemain int, tokenUsed int, taskStatus string) {
	t.Helper()
	var user User
	require.NoError(t, DB.First(&user, userID).Error)
	require.Equal(t, userQuota, user.Quota)
	var token Token
	require.NoError(t, DB.First(&token, tokenID).Error)
	require.Equal(t, tokenRemain, token.RemainQuota)
	require.Equal(t, tokenUsed, token.UsedQuota)
	var task Midjourney
	require.NoError(t, DB.First(&task, taskID).Error)
	require.Equal(t, taskStatus, task.Status)
}
