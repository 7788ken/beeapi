package model

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type quotaIntegrationUser struct {
	ID        int `gorm:"primaryKey"`
	Quota     int
	DeletedAt gorm.DeletedAt
}

func (quotaIntegrationUser) TableName() string {
	return "users"
}

type quotaIntegrationToken struct {
	ID             int `gorm:"primaryKey"`
	UserID         int
	RemainQuota    int
	UsedQuota      int
	UnlimitedQuota bool
	AccessedTime   int64
	DeletedAt      gorm.DeletedAt
}

func (quotaIntegrationToken) TableName() string {
	return "tokens"
}

func TestQuotaAuthorityMySQL(t *testing.T) {
	dsn := os.Getenv("QUOTA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("QUOTA_TEST_MYSQL_DSN is not set")
	}
	runQuotaAuthorityDatabaseTest(t, mysql.Open(dsn))
}

func TestQuotaAuthorityPostgreSQL(t *testing.T) {
	dsn := os.Getenv("QUOTA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("QUOTA_TEST_POSTGRES_DSN is not set")
	}
	runQuotaAuthorityDatabaseTest(t, postgres.Open(dsn))
}

func runQuotaAuthorityDatabaseTest(t *testing.T, dialector gorm.Dialector) {
	t.Helper()
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		t.Fatalf("open quota authority database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get quota authority sql database: %v", err)
	}
	sqlDB.SetMaxOpenConns(8)
	if err := db.AutoMigrate(
		&quotaIntegrationUser{},
		&quotaIntegrationToken{},
		&SubscriptionPlan{},
		&UserSubscription{},
		&SubscriptionPreConsumeRecord{},
		&Midjourney{},
		&AsyncTaskRefundRecord{},
	); err != nil {
		t.Fatalf("migrate quota authority database: %v", err)
	}

	previousDB := DB
	previousStrict := setting.SubscriptionStrictGroupIsolation
	previousSQLite := common.UsingSQLite
	previousMySQL := common.UsingMySQL
	previousPostgreSQL := common.UsingPostgreSQL
	DB = db
	setting.SubscriptionStrictGroupIsolation = true
	common.UsingSQLite = false
	common.UsingMySQL = db.Dialector.Name() == "mysql"
	common.UsingPostgreSQL = db.Dialector.Name() == "postgres"
	getSubscriptionPlanCache().Purge()
	t.Cleanup(func() {
		DB = previousDB
		setting.SubscriptionStrictGroupIsolation = previousStrict
		common.UsingSQLite = previousSQLite
		common.UsingMySQL = previousMySQL
		common.UsingPostgreSQL = previousPostgreSQL
		getSubscriptionPlanCache().Purge()
		_ = sqlDB.Close()
	})

	user := quotaIntegrationUser{Quota: 100}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create rollback user: %v", err)
	}
	token := quotaIntegrationToken{UserID: user.ID, RemainQuota: 40}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create rollback token: %v", err)
	}
	if err := ReserveUserTokenQuota(user.ID, token.ID, 60); !errors.Is(err, ErrInsufficientTokenQuota) {
		t.Fatalf("rollback reservation error = %v, want ErrInsufficientTokenQuota", err)
	}
	assertQuotaIntegrationState(t, user.ID, token.ID, 100, 40, 0)
	if err := ReserveUserTokenQuota(user.ID, token.ID, 20); err != nil {
		t.Fatalf("reserve before soft delete: %v", err)
	}
	if err := DB.Delete(&token).Error; err != nil {
		t.Fatalf("soft delete integration token: %v", err)
	}
	if err := DB.Delete(&user).Error; err != nil {
		t.Fatalf("soft delete integration user: %v", err)
	}
	if err := AdjustUserTokenQuota(user.ID, token.ID, -20); err != nil {
		t.Fatalf("refund after integration soft delete: %v", err)
	}
	assertQuotaIntegrationState(t, user.ID, token.ID, 100, 40, 0)

	concurrentUser := quotaIntegrationUser{Quota: 100}
	if err := DB.Create(&concurrentUser).Error; err != nil {
		t.Fatalf("create concurrent user: %v", err)
	}
	concurrentToken := quotaIntegrationToken{UserID: concurrentUser.ID, RemainQuota: 100}
	if err := DB.Create(&concurrentToken).Error; err != nil {
		t.Fatalf("create concurrent token: %v", err)
	}

	var successes atomic.Int32
	var unexpected atomic.Value
	start := make(chan struct{})
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			err := ReserveUserTokenQuota(concurrentUser.ID, concurrentToken.ID, 80)
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrInsufficientUserQuota):
			default:
				unexpected.Store(err)
			}
		}()
	}
	close(start)
	wait.Wait()

	if value := unexpected.Load(); value != nil {
		t.Fatalf("unexpected concurrent reservation error: %v", value)
	}
	if successes.Load() != 1 {
		t.Fatalf("successful concurrent reservations = %d, want 1", successes.Load())
	}
	assertQuotaIntegrationState(t, concurrentUser.ID, concurrentToken.ID, 20, 20, 80)

	runUnlimitedTokenZeroChangedRowsIntegrationTest(t)
	runSubscriptionTokenIntegrationTest(t)
	runMidjourneyQuotaIntegrationTest(t)
}

func runUnlimitedTokenZeroChangedRowsIntegrationTest(t *testing.T) {
	t.Helper()
	user := quotaIntegrationUser{Quota: 800}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create unlimited integration user: %v", err)
	}
	token := quotaIntegrationToken{
		UserID:         user.ID,
		RemainQuota:    -500,
		UsedQuota:      500,
		UnlimitedQuota: true,
		AccessedTime:   common.GetTimestamp(),
	}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create unlimited integration token: %v", err)
	}

	runConcurrent := func(name string, adjust func() error) {
		t.Helper()
		start := make(chan struct{})
		errs := make(chan error, 8)
		var wait sync.WaitGroup
		for range 8 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				errs <- adjust()
			}()
		}
		close(start)
		wait.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("%s unlimited integration adjustment failed: %v", name, err)
			}
		}
	}

	runConcurrent("reserve", func() error {
		return ReserveUserTokenQuota(user.ID, token.ID, 25)
	})
	runConcurrent("settle-refund", func() error {
		return AdjustUserTokenQuota(user.ID, token.ID, -25)
	})
	assertQuotaIntegrationState(t, user.ID, token.ID, 800, -500, 500)
}

func runSubscriptionTokenIntegrationTest(t *testing.T) {
	t.Helper()
	user := quotaIntegrationUser{Quota: 0}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create subscription integration user: %v", err)
	}
	token := quotaIntegrationToken{UserID: user.ID, RemainQuota: 100}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create subscription integration token: %v", err)
	}
	plan := SubscriptionPlan{
		Title:            "quota-integration-plan",
		BoundGroup:       "codex",
		Enabled:          true,
		TotalAmount:      100,
		QuotaResetPeriod: SubscriptionResetNever,
	}
	if err := DB.Create(&plan).Error; err != nil {
		t.Fatalf("create subscription integration plan: %v", err)
	}
	subscription := UserSubscription{
		UserId:      user.ID,
		PlanId:      plan.Id,
		AmountTotal: 100,
		EndTime:     GetDBTimestamp() + 86400,
		Status:      "active",
		Source:      "order",
	}
	if err := DB.Create(&subscription).Error; err != nil {
		t.Fatalf("create subscription integration row: %v", err)
	}
	requestID := fmt.Sprintf("subscription-integration-%s-%d", DB.Dialector.Name(), user.ID)
	if _, err := PreConsumeUserSubscriptionToken(requestID, user.ID, token.ID, "gpt-4", 0, 30, "codex"); err != nil {
		t.Fatalf("reserve subscription/token integration quota: %v", err)
	}
	if err := ReserveSubscriptionPreConsumeTokenQuota(requestID, user.ID, subscription.Id, token.ID, 20); err != nil {
		t.Fatalf("extend subscription/token integration quota: %v", err)
	}
	assertSubscriptionTokenIntegrationState(t, subscription.Id, token.ID, 50, 50, 50)
	if err := RefundSubscriptionPreConsume(requestID); err != nil {
		t.Fatalf("refund subscription/token integration quota: %v", err)
	}
	assertSubscriptionTokenIntegrationState(t, subscription.Id, token.ID, 0, 100, 0)
}

func runMidjourneyQuotaIntegrationTest(t *testing.T) {
	t.Helper()
	user := quotaIntegrationUser{Quota: 100}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create midjourney integration user: %v", err)
	}
	token := quotaIntegrationToken{UserID: user.ID, RemainQuota: 100}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create midjourney integration token: %v", err)
	}
	task := Midjourney{
		UserId:   user.ID,
		TokenId:  token.ID,
		MjId:     fmt.Sprintf("midjourney-integration-%s-%d", DB.Dialector.Name(), user.ID),
		Status:   "IN_PROGRESS",
		Progress: "0%",
	}
	if err := DB.Create(&task).Error; err != nil {
		t.Fatalf("create midjourney integration task: %v", err)
	}
	if err := task.ConsumeQuota(user.ID, token.ID, 0, 60); err != nil {
		t.Fatalf("consume midjourney integration quota: %v", err)
	}
	assertQuotaIntegrationState(t, user.ID, token.ID, 40, 40, 60)
	task.Status = "FAILURE"
	task.Progress = "100%"
	won, err := task.UpdateWithStatusAndRefund("IN_PROGRESS")
	if err != nil {
		t.Fatalf("refund midjourney integration quota: %v", err)
	}
	if !won {
		t.Fatal("midjourney integration refund did not win terminal transition")
	}
	assertQuotaIntegrationState(t, user.ID, token.ID, 100, 100, 0)
}

func assertSubscriptionTokenIntegrationState(t *testing.T, subscriptionID int, tokenID int, subscriptionUsed int64, tokenRemain int, tokenUsed int) {
	t.Helper()
	var subscription UserSubscription
	if err := DB.First(&subscription, subscriptionID).Error; err != nil {
		t.Fatalf("read integration subscription: %v", err)
	}
	var token quotaIntegrationToken
	if err := DB.Unscoped().First(&token, tokenID).Error; err != nil {
		t.Fatalf("read integration subscription token: %v", err)
	}
	if subscription.AmountUsed != subscriptionUsed || token.RemainQuota != tokenRemain || token.UsedQuota != tokenUsed {
		t.Fatalf(
			"subscription quota state = subscription:%d token_remain:%d token_used:%d, want subscription:%d token_remain:%d token_used:%d",
			subscription.AmountUsed, token.RemainQuota, token.UsedQuota, subscriptionUsed, tokenRemain, tokenUsed,
		)
	}
}

func assertQuotaIntegrationState(t *testing.T, userID int, tokenID int, userQuota int, tokenQuota int, tokenUsed int) {
	t.Helper()
	var user quotaIntegrationUser
	if err := DB.Unscoped().First(&user, userID).Error; err != nil {
		t.Fatalf("read integration user: %v", err)
	}
	var token quotaIntegrationToken
	if err := DB.Unscoped().First(&token, tokenID).Error; err != nil {
		t.Fatalf("read integration token: %v", err)
	}
	if user.Quota != userQuota || token.RemainQuota != tokenQuota || token.UsedQuota != tokenUsed {
		t.Fatalf(
			"quota state = user:%d token_remain:%d token_used:%d, want user:%d token_remain:%d token_used:%d",
			user.Quota, token.RemainQuota, token.UsedQuota, userQuota, tokenQuota, tokenUsed,
		)
	}
}
