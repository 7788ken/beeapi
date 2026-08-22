package controller

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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

type CryptomusPayRequest struct {
	Amount     int64  `json:"amount"`
	ChannelKey string `json:"channel_key"` // 可选：选中的加密货币渠道 PayChannel.Key
}

func RequestCryptomusAmount(c *gin.Context) {
	var req CryptomusPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	if req.Amount < int64(setting.CryptomusMinTopUp) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", setting.CryptomusMinTopUp)})
		return
	}

	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	if !operation_setting.IsGroupAllowed(setting.CryptomusAllowedGroups, group) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "您的分组暂不支持该支付方式"})
		return
	}

	payMoney := getCryptomusPayMoney(req.Amount, group)
	if payMoney <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": fmt.Sprintf("%.2f", payMoney)})
}

// 跟 waffo pancake 算法对齐：单价 * 分组充值倍率 * 阶梯优惠
func getCryptomusPayMoney(amount int64, group string) float64 {
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
		Mul(decimal.NewFromFloat(setting.CryptomusUnitPrice)).
		Mul(decimal.NewFromFloat(topupGroupRatio)).
		Mul(decimal.NewFromFloat(discount))

	return payMoney.InexactFloat64()
}

func normalizeCryptomusTopUpAmount(amount int64) int64 {
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

func formatCryptomusAmount(payMoney float64) string {
	return decimal.NewFromFloat(payMoney).StringFixed(2)
}

func getCryptomusReturnURL() string {
	if strings.TrimSpace(setting.CryptomusReturnURL) != "" {
		return setting.CryptomusReturnURL
	}
	return strings.TrimRight(system_setting.ServerAddress, "/") + "/wallet?show_history=true&pay_success=true"
}

func getCryptomusCallbackURL() string {
	return strings.TrimRight(system_setting.ServerAddress, "/") + "/api/cryptomus/webhook"
}

// parseCryptomusAllowedCurrencies 解析 CSV 白名单。
// 格式："USDT:TRX,USDT:BSC,BTC,ETH"。
// - 不带 ":" 的项只填 currency；
// - 空字符串返回 nil（cryptomus 收银台默认显示全部支持币种）。
func parseCryptomusAllowedCurrencies(csv string) []service.CryptomusCurrencyOption {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	var out []service.CryptomusCurrencyOption
	for _, raw := range strings.Split(csv, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, ":", 2)
		opt := service.CryptomusCurrencyOption{
			Currency: strings.ToUpper(strings.TrimSpace(parts[0])),
		}
		if len(parts) == 2 {
			opt.Network = strings.ToUpper(strings.TrimSpace(parts[1]))
		}
		out = append(out, opt)
	}
	return out
}

// resolveCryptomusChannel 按 key 找已启用的加密货币渠道。
func resolveCryptomusChannel(key string) (constant.PayChannel, bool) {
	for _, ch := range setting.GetCryptomusPayChannels() {
		if ch.Key == key && ch.Enabled {
			return ch, true
		}
	}
	return constant.PayChannel{}, false
}

func RequestCryptomusPay(c *gin.Context) {
	if !setting.CryptomusEnabled {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Cryptomus 支付未启用"})
		return
	}
	if strings.TrimSpace(setting.CryptomusMerchantID) == "" ||
		strings.TrimSpace(setting.CryptomusPaymentApiKey) == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Cryptomus 配置不完整"})
		return
	}

	var req CryptomusPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	if req.Amount < int64(setting.CryptomusMinTopUp) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", setting.CryptomusMinTopUp)})
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
	if !operation_setting.IsGroupAllowed(setting.CryptomusAllowedGroups, group) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "您的分组暂不支持该支付方式"})
		return
	}

	payMoney := getCryptomusPayMoney(req.Amount, group)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	tradeNo := fmt.Sprintf("CRYPTOMUS-%d-%d-%s", id, time.Now().UnixMilli(), randstr.String(6))
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          normalizeCryptomusTopUpAmount(req.Amount),
		Money:           payMoney,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodCryptomus,
		PaymentProvider: model.PaymentProviderCryptomus,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Cryptomus 创建充值订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, tradeNo, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	// 解析选中的加密货币渠道：命中则用渠道的 to_currency/network 锁定收款，否则用全局默认 + 白名单。
	toCurrency := strings.ToUpper(strings.TrimSpace(setting.CryptomusDefaultCurrency))
	network := strings.ToUpper(strings.TrimSpace(setting.CryptomusDefaultNetwork))
	currencies := parseCryptomusAllowedCurrencies(setting.CryptomusAllowedCurrencies)
	if key := strings.TrimSpace(req.ChannelKey); key != "" {
		if ch, ok := resolveCryptomusChannel(key); ok {
			toCurrency = strings.ToUpper(strings.TrimSpace(ch.Params["to_currency"]))
			network = strings.ToUpper(strings.TrimSpace(ch.Params["network"]))
			currencies = nil // 已锁定具体币种，收银台聚焦该币种
		}
	}

	invoice, err := service.CreateCryptomusInvoice(c.Request.Context(), &service.CryptomusInvoiceRequest{
		Amount:            formatCryptomusAmount(payMoney),
		Currency:          "USD",
		OrderID:           tradeNo,
		UrlCallback:       getCryptomusCallbackURL(),
		UrlReturn:         getCryptomusReturnURL(),
		UrlSuccess:        getCryptomusReturnURL(),
		ToCurrency:        toCurrency,
		Network:           network,
		Currencies:        currencies,
		Lifetime:          45 * 60,
		IsPaymentMultiple: false,
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Cryptomus 创建支付订单失败 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	if uuid := strings.TrimSpace(invoice.UUID); uuid != "" {
		topUp.ProviderOrderID = uuid
		if err := topUp.Update(); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Cryptomus 回写 ProviderOrderID 失败 user_id=%d trade_no=%s uuid=%s error=%q", id, tradeNo, uuid, err.Error()))
		}
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Cryptomus 充值订单创建成功 user_id=%d trade_no=%s uuid=%s amount=%d money=%.2f", id, tradeNo, invoice.UUID, req.Amount, payMoney))

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"checkout_url": invoice.URL,
			"uuid":         invoice.UUID,
			"order_id":     tradeNo,
		},
	})
}

func CryptomusWebhook(c *gin.Context) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Cryptomus webhook 读取请求体失败 client_ip=%s error=%q", c.ClientIP(), err.Error()))
		c.String(http.StatusBadRequest, "bad request")
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Cryptomus webhook 收到请求 client_ip=%s body=%q", c.ClientIP(), string(bodyBytes)))

	payload, err := service.VerifyCryptomusWebhook(bodyBytes)
	if err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Cryptomus webhook 验签失败 client_ip=%s body=%q error=%q", c.ClientIP(), string(bodyBytes), err.Error()))
		c.String(http.StatusUnauthorized, "invalid signature")
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Cryptomus webhook 验签成功 type=%s status=%s order_id=%s uuid=%s is_final=%t", payload.Type, payload.Status, payload.OrderID, payload.UUID, payload.IsFinal))

	if !service.IsCryptomusPaidStatus(payload.Status) {
		// 非付款成功状态（pending/cancel/wrong_amount/refund_*）直接返 200，不入账
		c.String(http.StatusOK, "OK")
		return
	}

	tradeNo := strings.TrimSpace(payload.OrderID)
	if tradeNo == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Cryptomus webhook 缺少 order_id uuid=%s", payload.UUID))
		c.String(http.StatusOK, "OK")
		return
	}

	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)

	if err := model.RechargeCryptomus(tradeNo); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Cryptomus 充值处理失败 trade_no=%s uuid=%s client_ip=%s error=%q", tradeNo, payload.UUID, c.ClientIP(), err.Error()))
		c.String(http.StatusInternalServerError, "retry")
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Cryptomus 充值成功 trade_no=%s uuid=%s payer_currency=%s payment_amount=%s txid=%s", tradeNo, payload.UUID, payload.PayerCurrency, payload.PaymentAmount, payload.TxID))
	c.String(http.StatusOK, "OK")
}
