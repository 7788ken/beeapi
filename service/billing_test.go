package service

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type recordingBillingSettler struct {
	preConsumed int
	settledWith *int
}

func (s *recordingBillingSettler) Settle(actualQuota int) error {
	s.settledWith = &actualQuota
	return nil
}

func (s *recordingBillingSettler) Refund(*gin.Context) {}

func (s *recordingBillingSettler) NeedsRefund() bool {
	return false
}

func (s *recordingBillingSettler) GetPreConsumedQuota() int {
	return s.preConsumed
}

func (s *recordingBillingSettler) Reserve(int) error {
	return nil
}

func TestSettleBillingDelegatesToBillingSession(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	session := &recordingBillingSettler{preConsumed: 0}
	relayInfo := &relaycommon.RelayInfo{Billing: session}

	require.NoError(t, SettleBilling(ctx, relayInfo, 0))
	require.NotNil(t, session.settledWith)
	require.Equal(t, 0, *session.settledWith)
}

func TestSettleBillingWithoutSessionFailsWithoutChangingQuota(t *testing.T) {
	truncate(t)
	seedUser(t, 921, 100)
	seedToken(t, 922, 921, "missing-billing-session", 100)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	relayInfo := &relaycommon.RelayInfo{
		UserId:                921,
		TokenId:               922,
		FinalPreConsumedQuota: 40,
	}

	err := SettleBilling(ctx, relayInfo, 70)

	require.True(t, errors.Is(err, ErrBillingSessionMissing))
	var user model.User
	require.NoError(t, model.DB.First(&user, 921).Error)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 922).Error)
	require.Equal(t, 100, user.Quota)
	require.Equal(t, 100, token.RemainQuota)
	require.Equal(t, 0, token.UsedQuota)
}

func TestSettleBillingFreeModelWithoutSessionSucceeds(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	relayInfo := &relaycommon.RelayInfo{
		PriceData: types.PriceData{FreeModel: true},
	}

	require.NoError(t, SettleBilling(ctx, relayInfo, 0))
}
