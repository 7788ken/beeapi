package controller

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// 已知业务错误（中文文案直接展示给前端）；其他都按系统错误脱敏
var subscriptionBalanceFriendlyErrs = []error{
	model.ErrSubscriptionPlanSoldOut,
	model.ErrSubscriptionPlanInactive,
	model.ErrSubscriptionPlanPriceInvalid,
	model.ErrSubscriptionInsufficientQuota,
	model.ErrSubscriptionOrderNotFound,
	model.ErrSubscriptionOrderStatusInvalid,
}

func isSubscriptionBalanceFriendlyErr(err error) bool {
	for _, target := range subscriptionBalanceFriendlyErrs {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

type SubscriptionBalancePayRequest struct {
	PlanId int `json:"plan_id"`
}

// SubscriptionRequestBalancePay 用账户余额一键支付订阅。
// 流程：校验套餐 → 校验购买上限 → 校验余额是否充足 → 创建订单 →
// model.CompleteSubscriptionOrderByBalance 在事务内原子扣余额并建订阅 →
// 返回订阅是否生效（前端关闭弹窗后刷新订阅状态）。
func SubscriptionRequestBalancePay(c *gin.Context) {
	var req SubscriptionBalancePayRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}

	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !plan.Enabled {
		common.ApiErrorMsg(c, "套餐未启用")
		return
	}
	if plan.PriceAmount < 0.01 {
		common.ApiErrorMsg(c, "套餐金额过低")
		return
	}

	userId := c.GetInt("id")
	if userId <= 0 {
		common.ApiErrorMsg(c, "用户未登录")
		return
	}

	if plan.MaxPurchasePerUser > 0 {
		count, err := model.CountUserSubscriptionsByPlan(userId, plan.Id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			common.ApiErrorMsg(c, "已达到该套餐购买上限")
			return
		}
	}

	quotaCost := int(plan.PriceAmount * common.QuotaPerUnit)
	if quotaCost <= 0 {
		common.ApiErrorMsg(c, "套餐金额异常")
		return
	}
	currentQuota, err := model.GetUserQuota(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if currentQuota < quotaCost {
		common.ApiErrorMsg(c, "账户余额不足，请先充值或选择在线支付")
		return
	}

	tradeNo := fmt.Sprintf("SUBBAL%dU%d%s", time.Now().Unix(), userId, common.GetRandomString(6))
	order := &model.SubscriptionOrder{
		UserId:          userId,
		PlanId:          plan.Id,
		Money:           plan.PriceAmount,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodBalance,
		PaymentProvider: model.PaymentProviderBalance,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := order.Insert(); err != nil {
		common.ApiErrorMsg(c, "创建订单失败")
		return
	}

	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)

	if err := model.CompleteSubscriptionOrderByBalance(tradeNo); err != nil {
		// 业务错误（中文短语）→ 标记订单过期 + 透传文案
		// 系统错误（DB 等）→ 标记订单过期 + 走 ApiError 脱敏
		_ = model.ExpireSubscriptionOrder(tradeNo, model.PaymentProviderBalance)
		if isSubscriptionBalanceFriendlyErr(err) {
			common.ApiErrorMsg(c, err.Error())
		} else {
			common.ApiError(c, err)
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "success",
		"data": gin.H{
			"trade_no": tradeNo,
		},
	})
}
