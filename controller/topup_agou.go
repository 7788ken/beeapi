package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
)

// sfpay 通道金额区间（分）：¥50–¥5000。
const (
	agouMinFeeCents int64 = 5000
	agouMaxFeeCents int64 = 500000
)

type AgouPayRequest struct {
	Amount     int64  `json:"amount"`
	PayType    string `json:"pay_type"`    // 旧字段（classic 前端）：方式 type alipay / wxpay
	ChannelKey string `json:"channel_key"` // 新字段（default 两层选择）：PayChannel.Key（与 Type 同源）
}

// getAgouPayMoney 把展示金额换算成应付人民币（含分组倍率与折扣），单位元。
func getAgouPayMoney(amount int64, group string) float64 {
	dAmount := decimal.NewFromInt(amount)
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dAmount = dAmount.Div(decimal.NewFromFloat(common.QuotaPerUnit))
	}
	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio == 0 {
		topupGroupRatio = 1
	}
	discount := 1.0
	if ds, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(amount)]; ok {
		if ds > 0 {
			discount = ds
		}
	}
	payMoney := dAmount.
		Mul(decimal.NewFromFloat(setting.AgouUnitPrice)).
		Mul(decimal.NewFromFloat(topupGroupRatio)).
		Mul(decimal.NewFromFloat(discount))
	return payMoney.InexactFloat64()
}

// resolveAgouPayType 把前端渠道 key（alipay/wxpay，与 AgouPayMethod.Type 同源）解析为 sfpay payType，并校验启用态。
func resolveAgouPayType(channelKey string) (string, error) {
	for _, m := range setting.GetAgouPayMethods() {
		if m.Type == channelKey {
			if !m.Enabled || strings.TrimSpace(m.PayType) == "" {
				return "", errors.New("该支付方式暂未开放")
			}
			return m.PayType, nil
		}
	}
	return "", errors.New("不支持的支付方式")
}

func RequestAgouAmount(c *gin.Context) {
	if !setting.AgouEnabled {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Sfpay 支付未启用"})
		return
	}
	var req AgouPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	if req.Amount < int64(setting.AgouMinTopUp) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", setting.AgouMinTopUp)})
		return
	}

	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	if !operation_setting.IsGroupAllowed(setting.AgouAllowedGroups, group) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "您的分组暂不支持该支付方式"})
		return
	}

	payMoney := getAgouPayMoney(req.Amount, group)
	if payMoney <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(payMoney, 'f', 2, 64)})
}

// RequestAgouPay 创建 sfpay 代收订单，返回收银台支付链接。
func RequestAgouPay(c *gin.Context) {
	if !setting.AgouEnabled {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Sfpay 支付未启用"})
		return
	}

	var req AgouPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	if req.Amount < int64(setting.AgouMinTopUp) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", setting.AgouMinTopUp)})
		return
	}
	if setting.AgouMaxTopUp > 0 && req.Amount > int64(setting.AgouMaxTopUp) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能大于 %d", setting.AgouMaxTopUp)})
		return
	}

	id := c.GetInt("id")
	group, _ := model.GetUserGroup(id, true)
	if !operation_setting.IsGroupAllowed(setting.AgouAllowedGroups, group) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "您的分组暂不支持该支付方式"})
		return
	}

	channelKey := strings.TrimSpace(req.ChannelKey)
	if channelKey == "" {
		channelKey = strings.TrimSpace(req.PayType) // 向后兼容 classic 前端
	}
	payType, err := resolveAgouPayType(channelKey)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": err.Error()})
		return
	}

	payMoney := getAgouPayMoney(req.Amount, group)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	// 人民币换算成分，并校验 sfpay 通道金额区间 ¥50–¥5000。
	feeCents := decimal.NewFromFloat(payMoney).Mul(decimal.NewFromInt(100)).Round(0).IntPart()
	if feeCents < agouMinFeeCents || feeCents > agouMaxFeeCents {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf(
			"支付金额需在 ¥%.0f–¥%.0f 之间（当前 ¥%.2f）",
			float64(agouMinFeeCents)/100, float64(agouMaxFeeCents)/100, payMoney)})
		return
	}

	// Token 展示模式归一化 Amount（存等价 unit 数，避免回调时二次放大）。
	amount := req.Amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		amount = decimal.NewFromInt(req.Amount).Div(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart()
		if amount < 1 {
			amount = 1
		}
	}

	tradeNo := fmt.Sprintf("SFPAY-%d-%d-%s", id, time.Now().UnixMilli(), randstr.String(6))

	topUp := &model.TopUp{
		UserId:          id,
		Amount:          amount,
		Money:           payMoney,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodAgou,
		PaymentProvider: model.PaymentProviderAgou,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Sfpay 创建充值订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, tradeNo, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	callbackAddr := service.GetCallbackAddress()
	notifyUrl := callbackAddr + "/api/sfpay/notify"
	if setting.AgouNotifyUrl != "" {
		notifyUrl = setting.AgouNotifyUrl
	}
	returnUrl := paymentReturnPath("/console/topup?show_history=true&pay_success=true")
	if setting.AgouReturnUrl != "" {
		returnUrl = setting.AgouReturnUrl
	}

	data, err := service.AgouCreateOrder(service.AgouCreateOrderParams{
		OrderNo:   tradeNo,
		PayType:   payType,
		PaidFee:   strconv.FormatInt(feeCents, 10),
		ClientIp:  c.ClientIP(),
		ReturnUrl: returnUrl,
		NotifyUrl: notifyUrl,
		Remark:    fmt.Sprintf("Recharge %d", req.Amount),
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Sfpay 拉起支付失败 user_id=%d trade_no=%s pay_type=%s error=%q", id, tradeNo, payType, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	if data.SysOrderNo != "" {
		topUp.ProviderOrderID = data.SysOrderNo
		_ = topUp.Update()
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Sfpay 充值订单创建成功 user_id=%d trade_no=%s sys_order_no=%s pay_type=%s amount=%d money=¥%.2f", id, tradeNo, data.SysOrderNo, payType, req.Amount, payMoney))
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"url":      data.PayUrl,
			"order_id": tradeNo,
		},
	})
}

// isAgouCallbackIPAllowed 校验回调来源 IP 是否在白名单内（白名单为空则不限制）。
func isAgouCallbackIPAllowed(ip string) bool {
	allowed := strings.TrimSpace(setting.AgouAllowedCallbackIPs)
	if allowed == "" {
		return true
	}
	for _, a := range strings.Split(allowed, ",") {
		if strings.TrimSpace(a) == ip {
			return true
		}
	}
	return false
}

// AgouNotify 处理 sfpay 代收回调：IP 白名单 → 验签 → 按 orderStatus 幂等入账，返回 success。
func AgouNotify(c *gin.Context) {
	if !isAgouWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Sfpay webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	// new-api 在 Caddy 容器后面，RemoteIP()（TCP peer）恒为 Docker 网关 IP（如 172.19.0.1），
	// 真实来源 IP 由 Caddy 透传在 X-Forwarded-For，需用 ClientIP() 读取，否则白名单恒不匹配、误杀全部回调。
	// 安全底线是回调验签（AgouVerifyNotify：MD5+appSecret）；IP 白名单仅作辅助层，留空即不限制。
	if !isAgouCallbackIPAllowed(c.ClientIP()) {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Sfpay webhook 来源 IP 不在白名单 remote_ip=%s client_ip=%s", c.RemoteIP(), c.ClientIP()))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Sfpay webhook 读取请求体失败 client_ip=%s error=%q", c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	fields, ok, err := service.AgouVerifyNotify(bodyBytes)
	if err != nil || !ok {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Sfpay webhook 验签失败 client_ip=%s body=%q error=%v", c.ClientIP(), string(bodyBytes), err))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	orderNo := fields["orderNo"]
	orderStatus := fields["orderStatus"]
	if orderNo == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Sfpay webhook 缺少 orderNo client_ip=%s body=%q", c.ClientIP(), string(bodyBytes)))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	switch orderStatus {
	case "1", "4": // 已支付 / 已补单 —— 成功入账
		LockOrder(orderNo)
		defer UnlockOrder(orderNo)
		if err := model.RechargeAgou(orderNo, c.ClientIP(), fields["paidFee"], fields["currency"]); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Sfpay 充值处理失败 trade_no=%s client_ip=%s error=%q", orderNo, c.ClientIP(), err.Error()))
			_, _ = c.Writer.Write([]byte("fail"))
			return
		}
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Sfpay 充值成功 trade_no=%s order_status=%s client_ip=%s", orderNo, orderStatus, c.ClientIP()))
		_, _ = c.Writer.Write([]byte("success"))
	case "3", "5": // 已关闭 / 已退款 —— 失败终态，标记 failed（幂等）
		if err := model.UpdatePendingTopUpStatus(orderNo, model.PaymentProviderAgou, common.TopUpStatusFailed); err != nil &&
			!errors.Is(err, model.ErrTopUpNotFound) &&
			!errors.Is(err, model.ErrTopUpStatusInvalid) {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Sfpay 标记失败订单状态失败 trade_no=%s error=%q", orderNo, err.Error()))
		}
		_, _ = c.Writer.Write([]byte("success"))
	default: // 0 待支付 / 6 异常挂起 —— 处理中，确认收到不重推，等终态回调或查单兜底
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Sfpay 回调订单处理中，忽略入账 trade_no=%s order_status=%s client_ip=%s", orderNo, orderStatus, c.ClientIP()))
		_, _ = c.Writer.Write([]byte("success"))
	}
}
