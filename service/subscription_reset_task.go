package service

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billinglifecycle"
)

const (
	subscriptionResetTickInterval = 1 * time.Minute
	subscriptionResetBatchSize    = 300
	subscriptionCleanupInterval   = 30 * time.Minute
)

var (
	subscriptionResetRunning atomic.Bool
	subscriptionCleanupLast  atomic.Int64
)

func StartSubscriptionQuotaResetTask() error {
	if !common.IsMasterNode {
		return nil
	}
	return billinglifecycle.StartProducer("subscription-quota-reset", func(ctx context.Context, _ *billinglifecycle.Ticket) {
		logger.LogInfo(context.Background(), fmt.Sprintf("subscription quota reset task started: tick=%s", subscriptionResetTickInterval))
		billinglifecycle.RunPeriodic(ctx, subscriptionResetTickInterval, true, func() {
			runSubscriptionQuotaResetOnce()
		})
	})
}

func runSubscriptionQuotaResetOnce() {
	if !subscriptionResetRunning.CompareAndSwap(false, true) {
		return
	}
	defer subscriptionResetRunning.Store(false)

	ctx := context.Background()
	totalReset := 0
	totalExpired := 0
	for {
		n, err := model.ExpireDueSubscriptions(subscriptionResetBatchSize)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("subscription expire task failed: %v", err))
			return
		}
		if n == 0 {
			break
		}
		totalExpired += n
		if n < subscriptionResetBatchSize {
			break
		}
	}
	for {
		n, err := model.ResetDueSubscriptions(subscriptionResetBatchSize)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("subscription quota reset task failed: %v", err))
			return
		}
		if n == 0 {
			break
		}
		totalReset += n
		if n < subscriptionResetBatchSize {
			break
		}
	}
	lastCleanup := time.Unix(subscriptionCleanupLast.Load(), 0)
	if time.Since(lastCleanup) >= subscriptionCleanupInterval {
		if _, err := model.CleanupSubscriptionPreConsumeRecords(7 * 24 * 3600); err == nil {
			subscriptionCleanupLast.Store(time.Now().Unix())
		}
	}
	// 耗尽处理必须在 Reset 之后：已到重置点的订阅先复活（额度清零），
	// 避免同一 tick 内先降级又恢复的抖动。
	totalExhausted := 0
	for {
		n, events, err := model.ProcessExhaustedSubscriptions(subscriptionResetBatchSize)
		for _, event := range events {
			sendSubscriptionExhaustedNotify(event)
		}
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("subscription exhaust task failed: %v", err))
			return
		}
		if n == 0 {
			break
		}
		totalExhausted += n
		if n < subscriptionResetBatchSize {
			break
		}
	}
	if common.DebugEnabled && (totalReset > 0 || totalExpired > 0 || totalExhausted > 0) {
		logger.LogDebug(ctx, "subscription maintenance: reset_count=%d, expired_count=%d, exhausted_count=%d", totalReset, totalExpired, totalExhausted)
	}
}

// sendSubscriptionExhaustedNotify 按耗尽事件的处理结果发送用户通知（best-effort，
// 失败仅记录日志；exhaust_notified_at 已落库，不重试）。
// 分组内还有其他可用订阅时（GroupExhausted=false）服务无变化，不发通知。
func sendSubscriptionExhaustedNotify(event model.SubscriptionExhaustedEvent) {
	if !event.GroupExhausted {
		return
	}
	planTitle := strings.TrimSpace(event.PlanTitle)
	if planTitle == "" {
		planTitle = "订阅套餐"
	}
	var prompt, content string
	if event.DowngradedToAuto {
		prompt = "订阅额度已用完，已切换为按量计费"
		content = "您的订阅「{{value}}」额度已全部用完。按您的消费模式设置，系统已自动将您的分组切换为 auto，后续调用将按标准按量价格从钱包余额扣费。如需继续享受套餐价格，请及时续费订阅。"
	} else if event.Pref == "subscription_then_auto" {
		// 偏好为转按量但未降级分组（分组被其他生效订阅占用或已被手动修改），仅告知耗尽。
		prompt = "订阅额度已用完"
		content = "您的订阅「{{value}}」额度已全部用完，该订阅将不再抵扣费用。如需继续享受套餐价格，请及时续费订阅。"
	} else {
		prompt = "订阅额度已用完，调用已暂停"
		content = "您的订阅「{{value}}」额度已全部用完，订阅分组的调用已暂停（不会扣减钱包余额）。您可以续费订阅，或在「我的订阅」页将消费模式切换为「优先订阅（用完转按量）」，用钱包余额继续调用。"
	}
	values := []interface{}{planTitle}
	if err := NotifyUser(event.UserId, event.UserEmail, event.UserSetting, dto.NewNotify(dto.NotifyTypeSubscriptionExhausted, prompt, content, values)); err != nil {
		common.SysError(fmt.Sprintf("failed to send subscription exhausted notify to user %d: %s", event.UserId, err.Error()))
	}
}
