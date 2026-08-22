package model

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupWalletPreConsumeTestDB(t *testing.T) {
	t.Helper()
	previousDB := DB
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", filepath.Join(t.TempDir(), "wallet.db"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open wallet database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get wallet sql database: %v", err)
	}
	if err := db.AutoMigrate(&User{}, &Token{}, &WalletPreConsumeRecord{}); err != nil {
		t.Fatalf("migrate wallet database: %v", err)
	}
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		_ = sqlDB.Close()
	})
}

func seedWalletUser(t *testing.T, quota int) (User, Token) {
	t.Helper()
	user := User{Username: "wallet-user", Quota: quota}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	token := Token{UserId: user.Id, Key: "wallet-token", RemainQuota: quota, UnlimitedQuota: true}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	return user, token
}

func userQuota(t *testing.T, userID int) int {
	t.Helper()
	var user User
	if err := DB.First(&user, userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	return user.Quota
}

// 复现生产事故：预扣费成功后退款失败（数据库瞬时不可用），
// 凭据停留在 reserved，清扫任务必须把钱退回去。
func TestStaleReservationSweepRefundsStrandedQuota(t *testing.T) {
	setupWalletPreConsumeTestDB(t)
	user, token := seedWalletUser(t, 1000)

	if err := ReserveUserTokenQuotaWithRecord("req-stranded", user.Id, token.Id, 400); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if got := userQuota(t, user.Id); got != 600 {
		t.Fatalf("after reserve quota = %d, want 600", got)
	}

	// 退款从未发生（进程崩溃 / 数据库拒连），凭据仍是 reserved。
	stale, err := FindStaleReservedWalletPreConsumes(-1, 10)
	if err != nil {
		t.Fatalf("find stale: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("stale records = %d, want 1", len(stale))
	}

	if err := RefundStaleWalletPreConsume(stale[0]); err != nil {
		t.Fatalf("sweep refund: %v", err)
	}
	if got := userQuota(t, user.Id); got != 1000 {
		t.Fatalf("after sweep quota = %d, want 1000 (money must be returned)", got)
	}
}

// 已结算的请求不能被清扫任务重复退款。
func TestSettledReservationIsNotSweptAgain(t *testing.T) {
	setupWalletPreConsumeTestDB(t)
	user, token := seedWalletUser(t, 1000)

	if err := ReserveUserTokenQuotaWithRecord("req-settled", user.Id, token.Id, 400); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	// 实际用量 500：再扣 100 并关闭凭据。
	if err := FinalizeUserTokenQuota("req-settled", user.Id, token.Id, 100, WalletPreConsumeStatusSettled); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if got := userQuota(t, user.Id); got != 500 {
		t.Fatalf("after settle quota = %d, want 500", got)
	}

	stale, err := FindStaleReservedWalletPreConsumes(-1, 10)
	if err != nil {
		t.Fatalf("find stale: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("settled record must not be swept, got %d", len(stale))
	}
}

// 正常退款关闭凭据后，清扫任务不得再退一次。
func TestRefundedReservationIsNotDoubleRefunded(t *testing.T) {
	setupWalletPreConsumeTestDB(t)
	user, token := seedWalletUser(t, 1000)

	if err := ReserveUserTokenQuotaWithRecord("req-refunded", user.Id, token.Id, 400); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := FinalizeUserTokenQuota("req-refunded", user.Id, token.Id, -400, WalletPreConsumeStatusRefunded); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if got := userQuota(t, user.Id); got != 1000 {
		t.Fatalf("after refund quota = %d, want 1000", got)
	}

	stale, err := FindStaleReservedWalletPreConsumes(-1, 10)
	if err != nil {
		t.Fatalf("find stale: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("refunded record must not be swept, got %d", len(stale))
	}

	// 即便清扫任务拿到旧快照，二次退款也必须是幂等的。
	if err := RefundStaleWalletPreConsume(WalletPreConsumeRecord{
		RequestId: "req-refunded", UserId: user.Id, TokenId: token.Id, PreConsumed: 400,
	}); err != nil {
		t.Fatalf("idempotent sweep: %v", err)
	}
	if got := userQuota(t, user.Id); got != 1000 {
		t.Fatalf("double refund happened: quota = %d, want 1000", got)
	}
}

// 追加预扣（发送前补扣）必须累加到同一凭据，清扫时全额退还。
func TestExtendedReservationSweepsFullAmount(t *testing.T) {
	setupWalletPreConsumeTestDB(t)
	user, token := seedWalletUser(t, 1000)

	if err := ReserveUserTokenQuotaWithRecord("req-extend", user.Id, token.Id, 300); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := ExtendUserTokenReservation("req-extend", user.Id, token.Id, 200); err != nil {
		t.Fatalf("extend: %v", err)
	}
	if got := userQuota(t, user.Id); got != 500 {
		t.Fatalf("after extend quota = %d, want 500", got)
	}

	stale, err := FindStaleReservedWalletPreConsumes(-1, 10)
	if err != nil {
		t.Fatalf("find stale: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("stale records = %d, want 1", len(stale))
	}
	if stale[0].PreConsumed != 500 {
		t.Fatalf("pre_consumed = %d, want 500", stale[0].PreConsumed)
	}
	if err := RefundStaleWalletPreConsume(stale[0]); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := userQuota(t, user.Id); got != 1000 {
		t.Fatalf("after sweep quota = %d, want 1000", got)
	}
}

// 清扫任务退款后，原请求迟到的结算不得再动一次余额。
// 旧实现先改余额、后推进凭据，且把"凭据已是终态"当成幂等成功，
// 于是这笔预扣会被退两次：清扫退全额 + 结算再补退 (preConsumed-actual)。
func TestSettleAfterSweepDoesNotRefundTwice(t *testing.T) {
	setupWalletPreConsumeTestDB(t)
	user, token := seedWalletUser(t, 1000)

	if err := ReserveUserTokenQuotaWithRecord("req-late", user.Id, token.Id, 400); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if got := userQuota(t, user.Id); got != 600 {
		t.Fatalf("after reserve quota = %d, want 600", got)
	}

	// 请求卡住超过清扫阈值，清扫任务把 400 全额退回 -> 1000。
	stale, err := FindStaleReservedWalletPreConsumes(-1, 10)
	if err != nil {
		t.Fatalf("find stale: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("stale records = %d, want 1", len(stale))
	}
	if err := RefundStaleWalletPreConsume(stale[0]); err != nil {
		t.Fatalf("sweep refund: %v", err)
	}
	if got := userQuota(t, user.Id); got != 1000 {
		t.Fatalf("after sweep quota = %d, want 1000", got)
	}

	// 上游随后返回，实际用量 250，结算 delta = 250-400 = -150。
	// 钱已经退过了，这一步必须是空操作。
	if err := FinalizeUserTokenQuota("req-late", user.Id, token.Id, -150, WalletPreConsumeStatusSettled); err != nil {
		t.Fatalf("late settle: %v", err)
	}
	if got := userQuota(t, user.Id); got != 1000 {
		t.Fatalf("late settle double-refunded: quota = %d, want 1000", got)
	}

	var record WalletPreConsumeRecord
	if err := DB.Where("request_id = ?", "req-late").First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.Status != WalletPreConsumeStatusRefunded {
		t.Fatalf("record status = %q, want %q", record.Status, WalletPreConsumeStatusRefunded)
	}
}

// 正常结算路径不受抢占改造影响：凭据仍是 reserved 时，钱必须照常结算。
func TestSettleClaimsReservationAndAdjustsQuota(t *testing.T) {
	setupWalletPreConsumeTestDB(t)
	user, token := seedWalletUser(t, 1000)

	if err := ReserveUserTokenQuotaWithRecord("req-normal", user.Id, token.Id, 400); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	// 实际用量 250 -> 退回 150。
	if err := FinalizeUserTokenQuota("req-normal", user.Id, token.Id, -150, WalletPreConsumeStatusSettled); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if got := userQuota(t, user.Id); got != 750 {
		t.Fatalf("after settle quota = %d, want 750", got)
	}

	var record WalletPreConsumeRecord
	if err := DB.Where("request_id = ?", "req-normal").First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.Status != WalletPreConsumeStatusSettled {
		t.Fatalf("record status = %q, want %q", record.Status, WalletPreConsumeStatusSettled)
	}

	// 重复结算必须幂等，不得再动余额。
	if err := FinalizeUserTokenQuota("req-normal", user.Id, token.Id, -150, WalletPreConsumeStatusSettled); err != nil {
		t.Fatalf("repeat settle: %v", err)
	}
	if got := userQuota(t, user.Id); got != 750 {
		t.Fatalf("repeat settle changed quota = %d, want 750", got)
	}
}
