package controller

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
)

// BEpusdt 自建加密币收款网关：建单走网关 create-order 收银台（用户自选 USDT 链），
// 链上到账后网关回调 /api/bepusdt/webhook，验签 + 金额护栏后入账。
// 计价与 Cryptomus 同口径：单价 × 分组充值倍率 × 阶梯优惠。

type BepusdtPayRequest struct {
	Amount int64 `json:"amount"`
}

// bepusdtAmountTolerance 回调法币金额与订单金额允许的最大偏差（订单金额已按 2 位小数取整）。
const bepusdtAmountTolerance = 0.005

func RequestBepusdtAmount(c *gin.Context) {
	var req BepusdtPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	if req.Amount < int64(setting.BepusdtMinTopUp) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", setting.BepusdtMinTopUp)})
		return
	}

	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	if !operation_setting.IsGroupAllowed(setting.BepusdtAllowedGroups, group) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "您的分组暂不支持该支付方式"})
		return
	}

	payMoney := getBepusdtPayMoney(req.Amount, group)
	if payMoney <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": fmt.Sprintf("%.2f", payMoney)})
}

// getBepusdtPayMoney 跟 cryptomus 算法对齐：单价 * 分组充值倍率 * 阶梯优惠，结果取 2 位小数
// （网关以法币金额建单并在回调里原样回传，订单金额必须与之精确对得上）。
func getBepusdtPayMoney(amount int64, group string) float64 {
	dAmount := decimal.NewFromInt(amount)
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dAmount = dAmount.Div(decimal.NewFromFloat(common.QuotaPerUnit))
	}

	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio == 0 {
		topupGroupRatio = 1
	}

	discount := 1.0
	if ds, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(amount)]; ok && ds > 0 {
		discount = ds
	}

	payMoney := dAmount.
		Mul(decimal.NewFromFloat(setting.BepusdtUnitPrice)).
		Mul(decimal.NewFromFloat(topupGroupRatio)).
		Mul(decimal.NewFromFloat(discount)).
		Round(2)

	return payMoney.InexactFloat64()
}

func normalizeBepusdtTopUpAmount(amount int64) int64 {
	if operation_setting.GetQuotaDisplayType() != operation_setting.QuotaDisplayTypeTokens {
		return amount
	}
	normalized := decimal.NewFromInt(amount).
		Div(decimal.NewFromFloat(common.QuotaPerUnit)).
		IntPart()
	if normalized < 1 {
		return 1
	}
	return normalized
}

func getBepusdtReturnURL() string {
	if strings.TrimSpace(setting.BepusdtReturnURL) != "" {
		return setting.BepusdtReturnURL
	}
	return strings.TrimRight(system_setting.ServerAddress, "/") + "/wallet?show_history=true&pay_success=true"
}

func getBepusdtCallbackURL() string {
	return strings.TrimRight(system_setting.ServerAddress, "/") + "/api/bepusdt/webhook"
}

// bepusdtAmountMatches 回调法币金额必须等于订单金额（2 位小数内），否则拒绝入账。
func bepusdtAmountMatches(notifyAmount, orderMoney float64) bool {
	return math.Abs(notifyAmount-orderMoney) < bepusdtAmountTolerance
}

func RequestBepusdtPay(c *gin.Context) {
	if !isBepusdtTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "USDT 支付未启用"})
		return
	}

	var req BepusdtPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	if req.Amount < int64(setting.BepusdtMinTopUp) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", setting.BepusdtMinTopUp)})
		return
	}

	id := c.GetInt("id")
	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "用户不存在"})
		return
	}

	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	if !operation_setting.IsGroupAllowed(setting.BepusdtAllowedGroups, group) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "您的分组暂不支持该支付方式"})
		return
	}

	payMoney := getBepusdtPayMoney(req.Amount, group)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	tradeNo := fmt.Sprintf("BEPUSDT-%d-%d-%s", id, time.Now().UnixMilli(), randstr.String(6))
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          normalizeBepusdtTopUpAmount(req.Amount),
		Money:           payMoney,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodBepusdt,
		PaymentProvider: model.PaymentProviderBepusdt,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("BEpusdt 创建充值订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, tradeNo, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	order, err := service.CreateBepusdtOrder(c.Request.Context(), &service.BepusdtCreateOrderRequest{
		OrderID:     tradeNo,
		Amount:      payMoney,
		Fiat:        "USD",
		NotifyURL:   getBepusdtCallbackURL(),
		RedirectURL: getBepusdtReturnURL(),
		Name:        fmt.Sprintf("%s Top-up", common.SystemName),
		Currencies:  strings.TrimSpace(setting.BepusdtCurrencies),
		TimeoutSec:  setting.GetBepusdtLifetimeSec(),
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("BEpusdt 创建支付订单失败 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	topUp.ProviderOrderID = order.TradeID
	if err := topUp.Update(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("BEpusdt 回写 ProviderOrderID 失败 user_id=%d trade_no=%s trade_id=%s error=%q", id, tradeNo, order.TradeID, err.Error()))
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("BEpusdt 充值订单创建成功 user_id=%d trade_no=%s trade_id=%s amount=%d money=%.2f", id, tradeNo, order.TradeID, req.Amount, payMoney))

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"checkout_url": order.PaymentURL,
			"trade_id":     order.TradeID,
			"order_id":     tradeNo,
		},
	})
}

// BepusdtWebhook 网关回调。成功回调要求 HTTP 200 且响应体含 ok/success，否则网关按 2/4/8… 分钟重试最多 10 次。
// 护栏：验签失败 401 → 非 status=2 直接 200 不入账 → 找不到订单 200 → 渠道不符 200 → 法币金额不符 400 拒绝入账 → 入账失败 500 让网关重试。
func BepusdtWebhook(c *gin.Context) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("BEpusdt webhook 读取请求体失败 client_ip=%s error=%q", c.ClientIP(), err.Error()))
		c.String(http.StatusBadRequest, "bad request")
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("BEpusdt webhook 收到请求 client_ip=%s body=%q", c.ClientIP(), string(bodyBytes)))

	payload, err := service.VerifyBepusdtNotify(bodyBytes, setting.BepusdtApiToken)
	if err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("BEpusdt webhook 验签失败 client_ip=%s body=%q error=%q", c.ClientIP(), string(bodyBytes), err.Error()))
		c.String(http.StatusUnauthorized, "invalid signature")
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("BEpusdt webhook 验签成功 status=%d order_id=%s trade_id=%s amount=%v actual_amount=%s", payload.Status, payload.OrderID, payload.TradeID, payload.Amount, payload.ActualAmount))

	if !service.IsBepusdtPaidStatus(payload.Status) {
		// 1 等待 / 3 超时：回 200 不入账（超时订单若之后补单成功，网关会再推 status=2）
		c.String(http.StatusOK, "ok")
		return
	}

	tradeNo := strings.TrimSpace(payload.OrderID)
	if tradeNo == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("BEpusdt webhook 缺少 order_id trade_id=%s", payload.TradeID))
		c.String(http.StatusOK, "ok")
		return
	}

	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("BEpusdt webhook 未知订单 trade_no=%s trade_id=%s", tradeNo, payload.TradeID))
		c.String(http.StatusOK, "ok")
		return
	}
	if topUp.PaymentProvider != model.PaymentProviderBepusdt {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("BEpusdt webhook 渠道不符 trade_no=%s provider=%s", tradeNo, topUp.PaymentProvider))
		c.String(http.StatusOK, "ok")
		return
	}
	if !bepusdtAmountMatches(payload.Amount, topUp.Money) {
		logger.LogError(c.Request.Context(), fmt.Sprintf("BEpusdt webhook 法币金额不符，拒绝入账 trade_no=%s trade_id=%s notify_amount=%v order_money=%.2f txid=%s", tradeNo, payload.TradeID, payload.Amount, topUp.Money, payload.BlockTransactionID))
		c.String(http.StatusBadRequest, "amount mismatch")
		return
	}

	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)

	if err := model.RechargeBepusdt(tradeNo); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("BEpusdt 充值处理失败 trade_no=%s trade_id=%s client_ip=%s error=%q", tradeNo, payload.TradeID, c.ClientIP(), err.Error()))
		c.String(http.StatusInternalServerError, "retry")
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("BEpusdt 充值成功 trade_no=%s trade_id=%s amount=%v actual_amount=%s token=%s txid=%s", tradeNo, payload.TradeID, payload.Amount, payload.ActualAmount, payload.Token, payload.BlockTransactionID))
	c.String(http.StatusOK, "ok")
}
