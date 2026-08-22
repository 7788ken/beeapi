package model

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupQuotaReservationTestDB(t *testing.T) {
	t.Helper()
	previousDB := DB
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", filepath.Join(t.TempDir(), "quota.db"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open quota reservation database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get quota reservation sql database: %v", err)
	}
	sqlDB.SetMaxOpenConns(8)
	if err := db.AutoMigrate(&User{}, &Token{}, &UserSubscription{}); err != nil {
		t.Fatalf("migrate quota reservation database: %v", err)
	}
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		_ = sqlDB.Close()
	})
}

func TestReserveUserTokenQuotaConcurrentInsufficientBalance(t *testing.T) {
	setupQuotaReservationTestDB(t)
	user := User{Username: "quota-concurrent", Quota: 100}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	token := Token{UserId: user.Id, Key: "quota-concurrent-token", RemainQuota: 100}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	var successes atomic.Int32
	var unexpected atomic.Value
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := ReserveUserTokenQuota(user.Id, token.Id, 80)
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
	wg.Wait()

	if value := unexpected.Load(); value != nil {
		t.Fatalf("unexpected reservation error: %v", value)
	}
	if got := successes.Load(); got != 1 {
		t.Fatalf("successful reservations = %d, want 1", got)
	}
	assertQuotaReservationState(t, user.Id, token.Id, 20, 20, 80)
}

func TestValidateUserTokenPersistsTerminalStatusWithRedisEnabled(t *testing.T) {
	setupQuotaReservationTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	previousCommonKeyCol := commonKeyCol
	common.RedisEnabled = true
	commonKeyCol = "`key`"
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		commonKeyCol = previousCommonKeyCol
	})

	user := User{Username: "token-terminal-status", Quota: 100}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create token terminal user: %v", err)
	}
	expired := Token{
		UserId:      user.Id,
		Key:         "token-terminal-expired",
		Status:      common.TokenStatusEnabled,
		RemainQuota: 100,
		ExpiredTime: common.GetTimestamp() - 1,
	}
	if err := DB.Create(&expired).Error; err != nil {
		t.Fatalf("create expired token: %v", err)
	}
	if _, err := ValidateUserToken(expired.Key); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("validate expired token error = %v, want ErrTokenInvalid", err)
	}
	var persisted Token
	if err := DB.First(&persisted, expired.Id).Error; err != nil {
		t.Fatalf("read expired token: %v", err)
	}
	if persisted.Status != common.TokenStatusExpired {
		t.Fatalf("expired token status = %d, want %d", persisted.Status, common.TokenStatusExpired)
	}

	exhausted := Token{
		UserId:      user.Id,
		Key:         "token-terminal-exhausted",
		Status:      common.TokenStatusEnabled,
		RemainQuota: 0,
		ExpiredTime: -1,
	}
	if err := DB.Create(&exhausted).Error; err != nil {
		t.Fatalf("create exhausted token: %v", err)
	}
	if _, err := ValidateUserToken(exhausted.Key); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("validate exhausted token error = %v, want ErrTokenInvalid", err)
	}
	persisted = Token{}
	if err := DB.First(&persisted, exhausted.Id).Error; err != nil {
		t.Fatalf("read exhausted token: %v", err)
	}
	if persisted.Status != common.TokenStatusExhausted {
		t.Fatalf("exhausted token status = %d, want %d", persisted.Status, common.TokenStatusExhausted)
	}
}

func TestReserveUserTokenQuotaRollsBackUserWhenTokenInsufficient(t *testing.T) {
	setupQuotaReservationTestDB(t)
	user := User{Username: "quota-rollback", Quota: 100}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	token := Token{UserId: user.Id, Key: "quota-rollback-token", RemainQuota: 40}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	err := ReserveUserTokenQuota(user.Id, token.Id, 60)
	if !errors.Is(err, ErrInsufficientTokenQuota) {
		t.Fatalf("reservation error = %v, want ErrInsufficientTokenQuota", err)
	}
	assertQuotaReservationState(t, user.Id, token.Id, 100, 40, 0)
}

func TestAdjustUserTokenQuotaPreservesReservationSettlementConservation(t *testing.T) {
	setupQuotaReservationTestDB(t)
	user := User{Username: "quota-settlement", Quota: 200}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	token := Token{UserId: user.Id, Key: "quota-settlement-token", RemainQuota: 200}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	if err := ReserveUserTokenQuota(user.Id, token.Id, 80); err != nil {
		t.Fatalf("reserve quota: %v", err)
	}
	if err := AdjustUserTokenQuota(user.Id, token.Id, -30); err != nil {
		t.Fatalf("settle reservation: %v", err)
	}
	assertQuotaReservationState(t, user.Id, token.Id, 150, 150, 50)
}

func TestAdjustUserTokenQuotaRollsBackUserReturnWhenTokenCannotReturn(t *testing.T) {
	setupQuotaReservationTestDB(t)
	user := User{Username: "quota-return-rollback", Quota: 120}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	token := Token{UserId: user.Id, Key: "quota-return-rollback-token", RemainQuota: 150, UsedQuota: 50}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	err := AdjustUserTokenQuota(user.Id, token.Id, -60)
	if !errors.Is(err, ErrInsufficientTokenQuota) {
		t.Fatalf("return error = %v, want ErrInsufficientTokenQuota", err)
	}
	assertQuotaReservationState(t, user.Id, token.Id, 120, 150, 50)
}

func TestAdjustUserTokenQuotaRefundsAfterTokenSoftDelete(t *testing.T) {
	setupQuotaReservationTestDB(t)
	user := User{Username: "quota-soft-delete-refund", Quota: 120}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	token := Token{UserId: user.Id, Key: "quota-soft-delete-refund-token", RemainQuota: 150, UsedQuota: 50}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	if err := DB.Delete(&token).Error; err != nil {
		t.Fatalf("soft delete token: %v", err)
	}
	if err := DB.Delete(&user).Error; err != nil {
		t.Fatalf("soft delete user: %v", err)
	}

	if err := AdjustUserTokenQuota(user.Id, token.Id, -30); err != nil {
		t.Fatalf("refund after token delete: %v", err)
	}

	var reloadedUser User
	if err := DB.Unscoped().First(&reloadedUser, user.Id).Error; err != nil {
		t.Fatalf("read soft-deleted user: %v", err)
	}
	var reloadedToken Token
	if err := DB.Unscoped().First(&reloadedToken, token.Id).Error; err != nil {
		t.Fatalf("read soft-deleted token: %v", err)
	}
	if reloadedUser.Quota != 150 || reloadedToken.RemainQuota != 180 || reloadedToken.UsedQuota != 20 {
		t.Fatalf(
			"refund state = user:%d token_remain:%d token_used:%d, want user:150 token_remain:180 token_used:20",
			reloadedUser.Quota,
			reloadedToken.RemainQuota,
			reloadedToken.UsedQuota,
		)
	}
}

func TestReserveUserTokenQuotaUsesDatabaseUnlimitedFlag(t *testing.T) {
	setupQuotaReservationTestDB(t)
	user := User{Username: "quota-unlimited", Quota: 100}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	token := Token{
		UserId:         user.Id,
		Key:            "quota-unlimited-token",
		RemainQuota:    7,
		UsedQuota:      9,
		UnlimitedQuota: true,
	}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	if err := ReserveUserTokenQuota(user.Id, token.Id, 40); err != nil {
		t.Fatalf("reserve unlimited token quota: %v", err)
	}
	assertQuotaReservationState(t, user.Id, token.Id, 60, 7, 9)

	if err := AdjustUserTokenQuota(user.Id, token.Id, -20); err != nil {
		t.Fatalf("refund unlimited token quota: %v", err)
	}
	assertQuotaReservationState(t, user.Id, token.Id, 80, 7, 9)
}

func TestResolveZeroAffectedTokenQuotaUpdateOnlyAcceptsMatchingUnlimitedToken(t *testing.T) {
	setupQuotaReservationTestDB(t)
	user := User{Username: "quota-zero-affected", Quota: 100}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create zero-affected user: %v", err)
	}
	token := Token{
		UserId:         user.Id,
		Key:            "quota-zero-affected-token",
		RemainQuota:    -100,
		UsedQuota:      100,
		UnlimitedQuota: true,
		AccessedTime:   common.GetTimestamp(),
	}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create zero-affected token: %v", err)
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		return resolveZeroAffectedTokenQuotaUpdate(tx, token.Id, user.Id, 10)
	}); err != nil {
		t.Fatalf("matching unlimited token was rejected: %v", err)
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return resolveZeroAffectedTokenQuotaUpdate(tx, token.Id, user.Id+1, 10)
	}); !errors.Is(err, ErrInsufficientTokenQuota) {
		t.Fatalf("mismatched token error = %v, want ErrInsufficientTokenQuota", err)
	}

	if err := DB.Model(&Token{}).Where("id = ?", token.Id).Update("unlimited_quota", false).Error; err != nil {
		t.Fatalf("make token finite: %v", err)
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return resolveZeroAffectedTokenQuotaUpdate(tx, token.Id, user.Id, 10)
	}); !errors.Is(err, ErrInsufficientTokenQuota) {
		t.Fatalf("finite token error = %v, want ErrInsufficientTokenQuota", err)
	}
}

func TestUnlimitedTokenQuotaConcurrentReserveSettleAndRefund(t *testing.T) {
	setupQuotaReservationTestDB(t)
	user := User{Username: "quota-unlimited-concurrent", Quota: 800}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create concurrent unlimited user: %v", err)
	}
	token := Token{
		UserId:         user.Id,
		Key:            "quota-unlimited-concurrent-token",
		RemainQuota:    -500,
		UsedQuota:      500,
		UnlimitedQuota: true,
		AccessedTime:   common.GetTimestamp(),
	}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create concurrent unlimited token: %v", err)
	}

	runConcurrentAdjustments := func(name string, adjust func() error) {
		t.Helper()
		var wait sync.WaitGroup
		errs := make(chan error, 8)
		start := make(chan struct{})
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
				t.Fatalf("%s adjustment failed: %v", name, err)
			}
		}
	}

	runConcurrentAdjustments("reserve", func() error {
		return ReserveUserTokenQuota(user.Id, token.Id, 25)
	})
	runConcurrentAdjustments("settle-refund", func() error {
		return AdjustUserTokenQuota(user.Id, token.Id, -25)
	})
	assertQuotaReservationState(t, user.Id, token.Id, 800, -500, 500)
}

func TestAdjustSubscriptionTokenQuotaRollsBackSubscriptionWhenTokenInsufficient(t *testing.T) {
	setupQuotaReservationTestDB(t)
	user := User{Username: "subscription-token-rollback"}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	subscription := UserSubscription{UserId: user.Id, AmountTotal: 100, AmountUsed: 50}
	if err := DB.Create(&subscription).Error; err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	token := Token{UserId: user.Id, Key: "subscription-token-rollback", RemainQuota: 10}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	err := AdjustSubscriptionTokenQuota(user.Id, subscription.Id, token.Id, 20)
	if !errors.Is(err, ErrInsufficientTokenQuota) {
		t.Fatalf("adjustment error = %v, want ErrInsufficientTokenQuota", err)
	}

	var reloaded UserSubscription
	if err := DB.First(&reloaded, subscription.Id).Error; err != nil {
		t.Fatalf("read subscription: %v", err)
	}
	if reloaded.AmountUsed != 50 {
		t.Fatalf("subscription used = %d, want 50", reloaded.AmountUsed)
	}
	assertQuotaReservationState(t, user.Id, token.Id, 0, 10, 0)
}

func TestQuotaMutationsIgnoreBatchUpdaterAuthority(t *testing.T) {
	setupQuotaReservationTestDB(t)
	previousBatchEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() {
		common.BatchUpdateEnabled = previousBatchEnabled
	})

	user := User{Username: "quota-direct-authority", Quota: 100}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	token := Token{UserId: user.Id, Key: "quota-direct-authority-token", RemainQuota: 100}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	if err := DecreaseUserQuota(user.Id, 30); err != nil {
		t.Fatalf("decrease user quota: %v", err)
	}
	if err := DecreaseTokenQuota(token.Id, 30); err != nil {
		t.Fatalf("decrease token quota: %v", err)
	}
	assertQuotaReservationState(t, user.Id, token.Id, 70, 70, 30)
}

// sqlOrderRecorder 捕获事务内 SQL 顺序，用于锁定锁序（凭据 → users → tokens）。
type sqlOrderRecorder struct {
	logger.Interface
	mu   sync.Mutex
	sqls []string
}

func (r *sqlOrderRecorder) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	r.mu.Lock()
	r.sqls = append(r.sqls, sql)
	r.mu.Unlock()
}

func TestReserveLockOrderCredentialBeforeUsers(t *testing.T) {
	setupQuotaReservationTestDB(t)
	if err := DB.AutoMigrate(&WalletPreConsumeRecord{}); err != nil {
		t.Fatalf("migrate wallet records: %v", err)
	}
	user := User{Username: "lock-order", Quota: 1000}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	token := Token{UserId: user.Id, Key: "lock-order-token", RemainQuota: 1000}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	recorder := &sqlOrderRecorder{Interface: DB.Logger}
	previousLogger := DB.Logger
	DB.Logger = recorder
	t.Cleanup(func() { DB.Logger = previousLogger })

	if err := ReserveUserTokenQuotaWithRecord("lock-order-req", user.Id, token.Id, 100); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	credIdx, userIdx := -1, -1
	for i, q := range recorder.sqls {
		lower := strings.ToLower(q)
		if credIdx == -1 && strings.Contains(lower, "insert") && strings.Contains(lower, "wallet_pre_consume_records") {
			credIdx = i
		}
		if userIdx == -1 && strings.Contains(lower, "update") && strings.Contains(lower, "users") && strings.Contains(lower, "quota") {
			userIdx = i
		}
	}
	if credIdx == -1 || userIdx == -1 {
		t.Fatalf("expected both credential INSERT and users UPDATE in trace, got %v", recorder.sqls)
	}
	// 锁序恒为 凭据 → users：预扣先锁 users 再写凭据会与结算路径构成 AB-BA 死锁。
	if credIdx > userIdx {
		t.Fatalf("lock order regression: users UPDATE (idx %d) before credential INSERT (idx %d)", userIdx, credIdx)
	}
}

func TestTrustedSettleUserQuotaBatchesWhenEnabled(t *testing.T) {
	setupQuotaReservationTestDB(t)
	user := User{Username: "trusted-settle-batch", Quota: 1000}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	// 批量器未启用：直连 DB 扣减，且无 quota>=X 守卫（债务必须落账）。
	if err := TrustedSettleUserQuota(user.Id, 300); err != nil {
		t.Fatalf("direct trusted settle: %v", err)
	}
	var reloaded User
	if err := DB.First(&reloaded, user.Id).Error; err != nil {
		t.Fatalf("read user: %v", err)
	}
	if reloaded.Quota != 700 {
		t.Fatalf("quota = %d, want 700", reloaded.Quota)
	}
	// 余额不足阈值也照记：允许打穿到负数。
	if err := TrustedSettleUserQuota(user.Id, 900); err != nil {
		t.Fatalf("overdraft trusted settle: %v", err)
	}
	if err := DB.First(&reloaded, user.Id).Error; err != nil {
		t.Fatalf("read user: %v", err)
	}
	if reloaded.Quota != -200 {
		t.Fatalf("quota = %d, want -200", reloaded.Quota)
	}

	// 批量器启用：进聚合队列，flush 后合并落库。
	previousBatchEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = previousBatchEnabled })
	if err := DB.AutoMigrate(&FlushOperationLedger{}, &BatchUpdateDeleteLedger{}); err != nil {
		t.Fatalf("migrate flush ledgers: %v", err)
	}
	updater := NewBatchUpdater(3600, applyDefaultBatchUpdates)
	if err := updater.Start(); err != nil {
		t.Fatalf("start batch updater: %v", err)
	}
	defaultBatchUpdaterMu.Lock()
	previousUpdater := defaultBatchUpdater
	defaultBatchUpdater = updater
	defaultBatchUpdaterMu.Unlock()
	t.Cleanup(func() {
		defaultBatchUpdaterMu.Lock()
		defaultBatchUpdater = previousUpdater
		defaultBatchUpdaterMu.Unlock()
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = updater.StopAndFlush(stopCtx)
	})

	if err := TrustedSettleUserQuota(user.Id, 150); err != nil {
		t.Fatalf("batched trusted settle: %v", err)
	}
	if err := TrustedSettleUserQuota(user.Id, 50); err != nil {
		t.Fatalf("batched trusted settle: %v", err)
	}
	// flush 前 DB 不动。
	if err := DB.First(&reloaded, user.Id).Error; err != nil {
		t.Fatalf("read user: %v", err)
	}
	if reloaded.Quota != -200 {
		t.Fatalf("quota before flush = %d, want -200", reloaded.Quota)
	}
	if err := updater.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := DB.First(&reloaded, user.Id).Error; err != nil {
		t.Fatalf("read user: %v", err)
	}
	if reloaded.Quota != -400 {
		t.Fatalf("quota after flush = %d, want -400 (merged -150-50)", reloaded.Quota)
	}
}

func assertQuotaReservationState(t *testing.T, userID int, tokenID int, userQuota int, tokenQuota int, tokenUsed int) {
	t.Helper()
	var user User
	if err := DB.First(&user, userID).Error; err != nil {
		t.Fatalf("read user: %v", err)
	}
	var token Token
	if err := DB.First(&token, tokenID).Error; err != nil {
		t.Fatalf("read token: %v", err)
	}
	if user.Quota != userQuota || token.RemainQuota != tokenQuota || token.UsedQuota != tokenUsed {
		t.Fatalf(
			"quota state = user:%d token_remain:%d token_used:%d, want user:%d token_remain:%d token_used:%d",
			user.Quota, token.RemainQuota, token.UsedQuota, userQuota, tokenQuota, tokenUsed,
		)
	}
}
