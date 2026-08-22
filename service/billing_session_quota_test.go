package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBillingSessionWalletSettlementKeepsUserAndTokenInSync(t *testing.T) {
	truncate(t)
	seedUser(t, 901, 200)
	seedToken(t, 902, 901, "billing-session-settlement", 200)
	require.NoError(t, model.ReserveUserTokenQuota(901, 902, 80))

	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{
			UserId:  901,
			TokenId: 902,
		},
		funding:          &WalletFunding{userId: 901, consumed: 80},
		preConsumedQuota: 80,
		tokenConsumed:    80,
	}

	require.NoError(t, session.Settle(50))
	assertWalletTokenQuota(t, 901, 902, 150, 150, 50)
}

func TestBillingSessionWalletSupplementalSettlementRollsBackTogether(t *testing.T) {
	truncate(t)
	seedUser(t, 903, 100)
	seedToken(t, 904, 903, "billing-session-rollback", 10)

	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{
			UserId:  903,
			TokenId: 904,
		},
		funding: &WalletFunding{userId: 903},
	}

	require.ErrorIs(t, session.Settle(20), model.ErrInsufficientTokenQuota)
	assertWalletTokenQuota(t, 903, 904, 100, 10, 0)
}

func TestBillingSessionPlaygroundWalletDoesNotRequireToken(t *testing.T) {
	truncate(t)
	seedUser(t, 905, 100)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{
			UserId:       905,
			IsPlayground: true,
		},
		funding: &WalletFunding{userId: 905},
	}

	require.Nil(t, session.preConsume(ctx, 20))
	require.NoError(t, session.Reserve(30))
	require.NoError(t, session.Settle(15))

	var user model.User
	require.NoError(t, model.DB.First(&user, 905).Error)
	require.Equal(t, 85, user.Quota)
}

func TestPostConsumeQuotaWalletRollsBackWhenTokenIsInsufficient(t *testing.T) {
	truncate(t)
	seedUser(t, 906, 100)
	seedToken(t, 907, 906, "post-consume-wallet-rollback", 10)
	info := &relaycommon.RelayInfo{
		UserId:  906,
		TokenId: 907,
	}

	require.ErrorIs(t, PostConsumeQuota(info, 20, 0, false), model.ErrInsufficientTokenQuota)
	assertWalletTokenQuota(t, 906, 907, 100, 10, 0)
}

func TestPostConsumeQuotaSubscriptionRollsBackWhenTokenIsInsufficient(t *testing.T) {
	truncate(t)
	seedUser(t, 908, 0)
	seedToken(t, 909, 908, "post-consume-subscription-rollback", 10)
	seedSubscription(t, 910, 908, 100, 40)
	info := &relaycommon.RelayInfo{
		UserId:         908,
		TokenId:        909,
		BillingSource:  BillingSourceSubscription,
		SubscriptionId: 910,
	}

	require.ErrorIs(t, PostConsumeQuota(info, 20, 0, false), model.ErrInsufficientTokenQuota)

	var subscription model.UserSubscription
	require.NoError(t, model.DB.First(&subscription, 910).Error)
	require.Equal(t, int64(40), subscription.AmountUsed)
	assertWalletTokenQuota(t, 908, 909, 0, 10, 0)
	require.Equal(t, int64(0), info.SubscriptionPostDelta)
}

func TestBillingSessionSubscriptionSettlementRollsBackTogether(t *testing.T) {
	truncate(t)
	seedUser(t, 911, 0)
	seedToken(t, 912, 911, "billing-session-subscription-rollback", 10)
	seedSubscription(t, 913, 911, 100, 40)
	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{
			UserId:  911,
			TokenId: 912,
		},
		funding: &SubscriptionFunding{
			userId:         911,
			subscriptionId: 913,
		},
	}

	require.ErrorIs(t, session.Settle(20), model.ErrInsufficientTokenQuota)

	var subscription model.UserSubscription
	require.NoError(t, model.DB.First(&subscription, 913).Error)
	require.Equal(t, int64(40), subscription.AmountUsed)
	assertWalletTokenQuota(t, 911, 912, 0, 10, 0)
}

func TestBillingSessionSubscriptionPreConsumeAndRefundKeepTokenInSameOperation(t *testing.T) {
	truncate(t)
	seedUser(t, 914, 0)
	seedToken(t, 915, 914, "billing-session-subscription-reserve", 100)
	plan := &model.SubscriptionPlan{
		Id:            916,
		Title:         "billing-session-subscription-plan",
		BoundGroup:    "codex",
		PriceAmount:   1,
		Currency:      "USD",
		DurationUnit:  model.SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   100,
	}
	require.NoError(t, model.DB.Create(plan).Error)
	subscription := &model.UserSubscription{
		Id:          917,
		UserId:      914,
		PlanId:      plan.Id,
		AmountTotal: 100,
		EndTime:     time.Now().Add(24 * time.Hour).Unix(),
		Status:      "active",
		Source:      "order",
	}
	require.NoError(t, model.DB.Create(subscription).Error)

	funding := &SubscriptionFunding{
		requestId:     "billing-session-subscription-request",
		userId:        914,
		tokenId:       915,
		tokenRequired: true,
		modelName:     "gpt-4",
		amount:        30,
		usingGroup:    "codex",
	}
	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{
			RequestId:       funding.requestId,
			UserId:          914,
			TokenId:         915,
			OriginModelName: "gpt-4",
			UsingGroup:      "codex",
			TokenUnlimited:  false,
			IsPlayground:    false,
			UserQuota:       0,
		},
		funding: funding,
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	require.Nil(t, session.preConsume(ctx, 30))
	require.Equal(t, 30, session.tokenConsumed)
	require.Equal(t, 30, session.preConsumedQuota)
	require.NoError(t, session.Reserve(50))

	var currentSubscription model.UserSubscription
	require.NoError(t, model.DB.First(&currentSubscription, subscription.Id).Error)
	require.Equal(t, int64(50), currentSubscription.AmountUsed)
	assertWalletTokenQuota(t, 914, 915, 0, 50, 50)

	require.NoError(t, funding.Refund())
	require.NoError(t, model.DB.First(&currentSubscription, subscription.Id).Error)
	require.Zero(t, currentSubscription.AmountUsed)
	assertWalletTokenQuota(t, 914, 915, 0, 100, 0)
}

func assertWalletTokenQuota(t *testing.T, userID int, tokenID int, userQuota int, tokenRemain int, tokenUsed int) {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	var token model.Token
	require.NoError(t, model.DB.First(&token, tokenID).Error)
	require.Equal(t, userQuota, user.Quota)
	require.Equal(t, tokenRemain, token.RemainQuota)
	require.Equal(t, tokenUsed, token.UsedQuota)
}
