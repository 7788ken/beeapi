package service

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const (
	BillingSourceWallet       = "wallet"
	BillingSourceSubscription = "subscription"
)

var ErrBillingSessionMissing = errors.New("billing session is required for settlement")

// PreConsumeBilling 根据用户计费偏好创建 BillingSession 并执行预扣费。
// 会话存储在 relayInfo.Billing 上，供后续 Settle / Refund 使用。
func PreConsumeBilling(c *gin.Context, preConsumedQuota int, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	if relayInfo != nil && relayInfo.QuotaClamp != nil {
		return types.NewErrorWithStatusCode(
			relayInfo.QuotaClamp,
			types.ErrorCodeModelPriceError,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if preConsumedQuota < 0 {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("pre-consume quota cannot be negative: %d", preConsumedQuota),
			types.ErrorCodeModelPriceError,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	session, apiErr := NewBillingSession(c, relayInfo, preConsumedQuota)
	if apiErr != nil {
		return apiErr
	}
	relayInfo.Billing = session
	return nil
}

// ---------------------------------------------------------------------------
// SettleBilling — 后结算辅助函数
// ---------------------------------------------------------------------------

// SettleBilling 执行计费结算，所有结算必须通过请求预扣阶段创建的 BillingSession。
func SettleBilling(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, actualQuota int) error {
	if relayInfo != nil && relayInfo.PriceData.FreeModel && relayInfo.Billing == nil {
		return nil
	}
	if relayInfo == nil || relayInfo.Billing == nil {
		return ErrBillingSessionMissing
	}
	preConsumed := relayInfo.Billing.GetPreConsumedQuota()
	delta := actualQuota - preConsumed

	if delta > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("预扣费后补扣费：%s（实际消耗：%s，预扣费：%s）",
			logger.FormatQuota(delta),
			logger.FormatQuota(actualQuota),
			logger.FormatQuota(preConsumed),
		))
	} else if delta < 0 {
		logger.LogInfo(ctx, fmt.Sprintf("预扣费后返还扣费：%s（实际消耗：%s，预扣费：%s）",
			logger.FormatQuota(-delta),
			logger.FormatQuota(actualQuota),
			logger.FormatQuota(preConsumed),
		))
	} else {
		logger.LogInfo(ctx, fmt.Sprintf("预扣费与实际消耗一致，无需调整：%s（按次计费）",
			logger.FormatQuota(actualQuota),
		))
	}

	if err := relayInfo.Billing.Settle(actualQuota); err != nil {
		return err
	}

	// 发送额度通知（订阅计费使用订阅剩余额度）
	if actualQuota != 0 {
		if relayInfo.BillingSource == BillingSourceSubscription {
			checkAndSendSubscriptionQuotaNotify(relayInfo)
		} else {
			checkAndSendQuotaNotify(relayInfo, actualQuota-preConsumed, preConsumed)
		}
	}
	return nil
}
