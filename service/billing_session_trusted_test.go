package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func withWalletTrustThreshold(t *testing.T, usd float64) {
	t.Helper()
	setting := operation_setting.GetQuotaSetting()
	previous := setting.WalletTrustQuotaUsd
	setting.WalletTrustQuotaUsd = usd
	t.Cleanup(func() {
		setting.WalletTrustQuotaUsd = previous
	})
}

func newTrustEligibleRelayInfo(userId int, tokenId int) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:         userId,
		TokenId:        tokenId,
		TokenUnlimited: true,
	}
}

func TestTrustedBypassSkipsReservationAndSettlesDirect(t *testing.T) {
	truncate(t)
	trustQuota := int(1 * common.QuotaPerUnit)
	seedUser(t, 921, trustQuota+10000)
	seedToken(t, 922, 921, "trusted-bypass-settle", 0)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 922).Update("unlimited_quota", true).Error)
	withWalletTrustThreshold(t, 1)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := newTrustEligibleRelayInfo(921, 922)
	session, apiErr := NewBillingSession(ctx, info, 5000)
	require.Nil(t, apiErr)
	require.True(t, session.trusted)
	require.Zero(t, session.GetPreConsumedQuota())

	// 未预扣：余额未动，无凭据落库。
	var user model.User
	require.NoError(t, model.DB.First(&user, 921).Error)
	require.Equal(t, trustQuota+10000, user.Quota)
	var recordCount int64
	require.NoError(t, model.DB.Model(&model.WalletPreConsumeRecord{}).Count(&recordCount).Error)
	require.Zero(t, recordCount)

	// Reserve 是 no-op，不会触发追加预扣。
	require.NoError(t, session.Reserve(999999))
	require.NoError(t, model.DB.First(&user, 921).Error)
	require.Equal(t, trustQuota+10000, user.Quota)

	// 结算按实际消耗直扣（批量器未启用时直连 DB）。
	require.NoError(t, session.Settle(3000))
	require.NoError(t, model.DB.First(&user, 921).Error)
	require.Equal(t, trustQuota+7000, user.Quota)

	// 结算后无退款余地。
	require.False(t, session.NeedsRefund())
}

func TestTrustedSettleRoutesThroughBatchUpdater(t *testing.T) {
	truncate(t)
	seedUser(t, 933, int(2*common.QuotaPerUnit))
	seedToken(t, 934, 933, "trusted-bypass-batch-route", 0)
	withWalletTrustThreshold(t, 1)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := newTrustEligibleRelayInfo(933, 934)
	session, apiErr := NewBillingSession(ctx, info, 5000)
	require.Nil(t, apiErr)
	require.True(t, session.trusted)

	// 锁定路由行为：trusted 结算必须走批量通道，不得退回 Finalize 直扣。
	// BatchUpdateEnabled=true 但批量器未初始化时，批量入队会报错；若实现被
	// 回归为直扣路径，这里会静默成功且余额立即变化——测试即 FAIL。
	previousBatchEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = previousBatchEnabled })

	err := session.Settle(3000)
	require.Error(t, err)
	require.Contains(t, err.Error(), "batch updater")

	var user model.User
	require.NoError(t, model.DB.First(&user, 933).Error)
	require.Equal(t, int(2*common.QuotaPerUnit), user.Quota, "余额不得被直扣路径立即扣减")
}

func TestTrustedSettlePiercesSoftDeletedUser(t *testing.T) {
	truncate(t)
	seedUser(t, 935, 10000)
	// 请求进行中用户被软删：债务仍须落账（与退款/sweeper 同口径）。
	require.NoError(t, model.DB.Delete(&model.User{}, 935).Error)

	require.NoError(t, model.TrustedSettleUserQuota(935, 4000))
	var user model.User
	require.NoError(t, model.DB.Unscoped().First(&user, 935).Error)
	require.Equal(t, 6000, user.Quota)
}

func TestTrustedBypassRefundIsNoOp(t *testing.T) {
	truncate(t)
	seedUser(t, 923, int(2*common.QuotaPerUnit))
	seedToken(t, 924, 923, "trusted-bypass-refund", 0)
	withWalletTrustThreshold(t, 1)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := newTrustEligibleRelayInfo(923, 924)
	session, apiErr := NewBillingSession(ctx, info, 5000)
	require.Nil(t, apiErr)
	require.True(t, session.trusted)

	// 无预扣即无可退：Refund 不得动余额。
	require.False(t, session.NeedsRefund())
	session.Refund(ctx)
	var user model.User
	require.NoError(t, model.DB.First(&user, 923).Error)
	require.Equal(t, int(2*common.QuotaPerUnit), user.Quota)
}

func TestTrustedBypassRequiresTokenQuotaAboveThreshold(t *testing.T) {
	truncate(t)
	seedUser(t, 925, int(2*common.QuotaPerUnit))
	seedToken(t, 926, 925, "trusted-bypass-limited", 100000)
	withWalletTrustThreshold(t, 1)

	// 限额令牌且剩余额度低于阈值 → 不走信任旁路。
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("token_quota", 100000)
	info := newTrustEligibleRelayInfo(925, 926)
	info.TokenUnlimited = false
	session, apiErr := NewBillingSession(ctx, info, 5000)
	require.Nil(t, apiErr)
	require.False(t, session.trusted)
	require.Equal(t, 5000, session.GetPreConsumedQuota())
}

func TestTrustedBypassAllowsLimitedTokenAboveThreshold(t *testing.T) {
	truncate(t)
	trustQuota := int(1 * common.QuotaPerUnit)
	tokenQuota := trustQuota + 50000
	seedUser(t, 935, trustQuota+100000)
	seedToken(t, 936, 935, "trusted-bypass-limited-rich", tokenQuota)
	withWalletTrustThreshold(t, 1)

	// 限额令牌但剩余额度高于阈值（对齐上游 shouldTrust）→ 走信任旁路。
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("token_quota", tokenQuota)
	info := newTrustEligibleRelayInfo(935, 936)
	info.TokenUnlimited = false
	session, apiErr := NewBillingSession(ctx, info, 5000)
	require.Nil(t, apiErr)
	require.True(t, session.trusted)
	require.Zero(t, session.GetPreConsumedQuota())

	// 结算：用户额度记债，限额令牌 remain/used 同步补扣。
	require.NoError(t, session.Settle(3000))
	var user model.User
	require.NoError(t, model.DB.First(&user, 935).Error)
	require.Equal(t, trustQuota+100000-3000, user.Quota)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 936).Error)
	require.Equal(t, tokenQuota-3000, token.RemainQuota)
	require.Equal(t, 3000, token.UsedQuota)
}

func TestTrustedBypassRequiresBalanceAboveThresholdAfterEstimate(t *testing.T) {
	truncate(t)
	trustQuota := int(1 * common.QuotaPerUnit)
	// 余额高于阈值，但扣除预估后跌破阈值 → 不走信任旁路。
	seedUser(t, 927, trustQuota+2000)
	seedToken(t, 928, 927, "trusted-bypass-threshold", 0)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 928).Update("unlimited_quota", true).Error)
	withWalletTrustThreshold(t, 1)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := newTrustEligibleRelayInfo(927, 928)
	session, apiErr := NewBillingSession(ctx, info, 5000)
	require.Nil(t, apiErr)
	require.False(t, session.trusted)
	require.Equal(t, 5000, session.GetPreConsumedQuota())
}

func TestTrustedBypassDisabledByDefault(t *testing.T) {
	truncate(t)
	seedUser(t, 929, int(100*common.QuotaPerUnit))
	seedToken(t, 930, 929, "trusted-bypass-default-off", 0)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 930).Update("unlimited_quota", true).Error)
	// 不设置阈值（默认 0）→ 一律预扣。

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := newTrustEligibleRelayInfo(929, 930)
	session, apiErr := NewBillingSession(ctx, info, 5000)
	require.Nil(t, apiErr)
	require.False(t, session.trusted)
	require.Equal(t, 5000, session.GetPreConsumedQuota())
}

func TestTrustedBypassRespectsForcePreConsume(t *testing.T) {
	truncate(t)
	seedUser(t, 931, int(100*common.QuotaPerUnit))
	seedToken(t, 932, 931, "trusted-bypass-force", 0)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 932).Update("unlimited_quota", true).Error)
	withWalletTrustThreshold(t, 1)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := newTrustEligibleRelayInfo(931, 932)
	info.ForcePreConsume = true
	session, apiErr := NewBillingSession(ctx, info, 5000)
	require.Nil(t, apiErr)
	require.False(t, session.trusted)
	require.Equal(t, 5000, session.GetPreConsumedQuota())
}
