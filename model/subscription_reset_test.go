package model

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func insertSubscriptionResetUser(t *testing.T, id int, group string) {
	t.Helper()
	require.NoError(t, DB.Create(&User{
		Id:       id,
		Username: fmt.Sprintf("subscription-reset-%d", id),
		Password: "testpass123",
		Email:    fmt.Sprintf("subscription-reset-%d@test.local", id),
		Group:    group,
		AffCode:  fmt.Sprintf("SR%d", id),
	}).Error)
}

func insertSubscriptionResetSub(
	t *testing.T,
	id int,
	userId int,
	planId int,
	status string,
	used int64,
	endTime int64,
	hidden bool,
	upgradeGroup string,
	lastResetTime int64,
	nextResetTime int64,
) {
	t.Helper()
	require.NoError(t, DB.Create(&UserSubscription{
		Id:                id,
		UserId:            userId,
		PlanId:            planId,
		AmountTotal:       1000,
		AmountUsed:        used,
		StartTime:         endTime - 7200,
		EndTime:           endTime,
		Status:            status,
		Source:            "order",
		LastResetTime:     lastResetTime,
		NextResetTime:     nextResetTime,
		ExhaustNotifiedAt: 123,
		UpgradeGroup:      upgradeGroup,
		IsHidden:          hidden,
	}).Error)
}

func TestAdminResetPlanSubscriptions_ResetsEligibleAndRestoresOnlyAutoGroup(t *testing.T) {
	truncateTables(t)
	initCol()

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:                      610,
		Title:                   "reset-plan",
		PriceAmount:             1,
		Currency:                "USD",
		DurationUnit:            SubscriptionDurationMonth,
		DurationValue:           1,
		Enabled:                 true,
		TotalAmount:             1000,
		QuotaResetPeriod:        SubscriptionResetCustom,
		QuotaResetCustomSeconds: 3600,
	}
	require.NoError(t, DB.Create(plan).Error)

	insertSubscriptionResetUser(t, 611, "codex")
	insertSubscriptionResetUser(t, 612, "auto")
	insertSubscriptionResetUser(t, 613, "manual")
	insertSubscriptionResetUser(t, 614, "auto")
	insertSubscriptionResetUser(t, 615, "auto")
	insertSubscriptionResetUser(t, 616, "auto")

	insertSubscriptionResetSub(t, 621, 611, plan.Id, "active", 400, now+7200, false, "codex", now-3600, now+300)
	insertSubscriptionResetSub(t, 622, 612, plan.Id, "exhausted", 1000, now+7200, false, "codex", now-3600, now+300)
	insertSubscriptionResetSub(t, 623, 613, plan.Id, "exhausted", 1000, now+7200, false, "codex", now-3600, now+300)
	insertSubscriptionResetSub(t, 624, 614, plan.Id, "expired", 900, now+7200, false, "codex", now-3600, now+300)
	insertSubscriptionResetSub(t, 625, 615, plan.Id, "cancelled", 900, now+7200, false, "codex", now-3600, now+300)
	insertSubscriptionResetSub(t, 626, 616, plan.Id, "active", 900, now+7200, true, "codex", now-3600, now+300)

	result, err := AdminResetPlanSubscriptions(plan.Id, true)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 3, result.MatchedCount)
	assert.Equal(t, 3, result.ResetCount)
	assert.Equal(t, 3, result.UserCount)
	assert.True(t, result.AdvanceResetTime)
	require.Len(t, result.Items, 3)

	active := loadUserSub(t, 621)
	assert.EqualValues(t, 0, active.AmountUsed)
	assert.Equal(t, "active", active.Status)
	assert.EqualValues(t, 0, active.ExhaustNotifiedAt)
	assert.InDelta(t, now, active.LastResetTime, 2)
	assert.InDelta(t, now+3600, active.NextResetTime, 2)

	exhausted := loadUserSub(t, 622)
	assert.EqualValues(t, 0, exhausted.AmountUsed)
	assert.Equal(t, "active", exhausted.Status)
	assert.Equal(t, "codex", loadUserGroup(t, 612))

	manual := loadUserSub(t, 623)
	assert.EqualValues(t, 0, manual.AmountUsed)
	assert.Equal(t, "active", manual.Status)
	assert.Equal(t, "manual", loadUserGroup(t, 613), "管理员手工分组不得被订阅重置覆盖")

	assert.Equal(t, "expired", loadUserSub(t, 624).Status)
	assert.EqualValues(t, 900, loadUserSub(t, 624).AmountUsed)
	assert.Equal(t, "cancelled", loadUserSub(t, 625).Status)
	assert.EqualValues(t, 900, loadUserSub(t, 625).AmountUsed)
	assert.True(t, loadUserSub(t, 626).IsHidden)
	assert.EqualValues(t, 900, loadUserSub(t, 626).AmountUsed)
}

func TestAdminResetUserSubscriptionsByPlan_PreservesCycleUnlessRequested(t *testing.T) {
	truncateTables(t)
	initCol()

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:            630,
		Title:         "preserve-cycle-plan",
		PriceAmount:   1,
		Currency:      "USD",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   1000,
	}
	require.NoError(t, DB.Create(plan).Error)
	insertSubscriptionResetUser(t, 631, "codex")
	insertSubscriptionResetSub(t, 632, 631, plan.Id, "active", 700, now+7200, false, "codex", 111, 222)

	result, err := AdminResetUserSubscriptionsByPlan(631, plan.Id, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.ResetCount)
	assert.False(t, result.AdvanceResetTime)

	sub := loadUserSub(t, 632)
	assert.EqualValues(t, 0, sub.AmountUsed)
	assert.EqualValues(t, 111, sub.LastResetTime)
	assert.EqualValues(t, 222, sub.NextResetTime)
}

func TestAdminResetUserSubscriptionsByPlan_RejectsWhenNoEligibleSubscription(t *testing.T) {
	truncateTables(t)
	initCol()

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:            640,
		Title:         "no-match-plan",
		PriceAmount:   1,
		Currency:      "USD",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   1000,
	}
	require.NoError(t, DB.Create(plan).Error)
	insertSubscriptionResetUser(t, 641, "auto")
	insertSubscriptionResetSub(t, 642, 641, plan.Id, "expired", 800, now+7200, false, "codex", 111, 222)

	result, err := AdminResetUserSubscriptionsByPlan(641, plan.Id, true)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "没有有效")
	assert.EqualValues(t, 800, loadUserSub(t, 642).AmountUsed)
	assert.Equal(t, "expired", loadUserSub(t, 642).Status)
}

func TestResetUserSubscriptionTx_RejectsStaleSnapshotWithoutLosingConsumption(t *testing.T) {
	truncateTables(t)
	initCol()

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:            650,
		Title:         "reset-race-plan",
		PriceAmount:   1,
		Currency:      "USD",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   1000,
	}
	require.NoError(t, DB.Create(plan).Error)
	insertSubscriptionResetUser(t, 651, "codex")
	insertSubscriptionResetSub(t, 652, 651, plan.Id, "active", 500, now+7200, false, "codex", 111, 222)

	var stale UserSubscription
	require.NoError(t, DB.First(&stale, 652).Error)
	require.NoError(t, DB.Model(&UserSubscription{}).
		Where("id = ?", stale.Id).
		Update("amount_used", 750).Error)

	err := DB.Transaction(func(tx *gorm.DB) error {
		_, err := resetUserSubscriptionTx(tx, &stale, plan, now, false)
		return err
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, errFinancialStateConflict)
	assert.EqualValues(t, 750, loadUserSub(t, stale.Id).AmountUsed)
}
