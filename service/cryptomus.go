package service

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Cryptomus Merchant API 客户端
// Doc: https://doc.cryptomus.com/business/payments/creating-invoice
//
// 签名规则：
//   sign = md5( base64( json_body_string ) + apiKey )
// webhook 验签时：把 body 里的 sign 字段先摘掉再 base64+md5 比对，
// 必须保留**其余字段的原始顺序**，所以这里直接走原 body 字符串处理（sjson 删字段，
// 不重 marshal），避免 Go map 顺序随机的坑。

const cryptomusAPIBase = "https://api.cryptomus.com"

type CryptomusInvoiceRequest struct {
	Amount        string `json:"amount"`         // string，cryptomus 要求十进制字符串
	Currency      string `json:"currency"`       // 金额计价币种（USD）
	OrderID       string `json:"order_id"`       // 商户订单号
	UrlCallback   string `json:"url_callback,omitempty"`
	UrlReturn     string `json:"url_return,omitempty"`
	UrlSuccess    string `json:"url_success,omitempty"`
	ToCurrency    string `json:"to_currency,omitempty"` // 固定收款币种（USDT/BTC/...）
	Network       string `json:"network,omitempty"`     // 链（TRX/ETH/BSC/...）
	Currencies    []CryptomusCurrencyOption `json:"currencies,omitempty"` // 收银台白名单
	Lifetime      int    `json:"lifetime,omitempty"`    // 订单有效期秒，默认 3600
	IsPaymentMultiple bool `json:"is_payment_multiple,omitempty"` // 是否允许多次付款（false=只允许付一次）
	AdditionalData string `json:"additional_data,omitempty"`
}

type CryptomusCurrencyOption struct {
	Currency string `json:"currency"`
	Network  string `json:"network,omitempty"`
}

type CryptomusInvoiceResult struct {
	UUID    string `json:"uuid"`
	OrderID string `json:"order_id"`
	URL     string `json:"url"`
	Status  string `json:"status"`
	Amount  string `json:"amount"`
	Currency string `json:"currency"`
}

type cryptomusEnvelope struct {
	State   int                    `json:"state"`
	Message string                 `json:"message,omitempty"`
	Errors  map[string]interface{} `json:"errors,omitempty"`
	Result  *CryptomusInvoiceResult `json:"result,omitempty"`
}

func cryptomusSign(body []byte, apiKey string) string {
	encoded := base64.StdEncoding.EncodeToString(body)
	sum := md5.Sum([]byte(encoded + apiKey))
	return hex.EncodeToString(sum[:])
}

// CreateCryptomusInvoice 调 cryptomus 创建支付订单。
// 调用方必须保证 setting.CryptomusMerchantID + setting.CryptomusPaymentApiKey 非空。
func CreateCryptomusInvoice(ctx context.Context, req *CryptomusInvoiceRequest) (*CryptomusInvoiceResult, error) {
	if req == nil {
		return nil, errors.New("nil cryptomus invoice request")
	}
	merchantID := strings.TrimSpace(setting.CryptomusMerchantID)
	apiKey := strings.TrimSpace(setting.CryptomusPaymentApiKey)
	if merchantID == "" || apiKey == "" {
		return nil, errors.New("cryptomus merchant 配置缺失")
	}

	body, err := common.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	sign := cryptomusSign(body, apiKey)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, cryptomusAPIBase+"/v1/payment", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("merchant", merchantID)
	httpReq.Header.Set("sign", sign)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call cryptomus: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cryptomus http %d: %s", resp.StatusCode, string(respBytes))
	}

	var env cryptomusEnvelope
	if err := common.Unmarshal(respBytes, &env); err != nil {
		return nil, fmt.Errorf("decode response: %w (body=%s)", err, string(respBytes))
	}
	if env.State != 0 {
		return nil, fmt.Errorf("cryptomus state=%d message=%s body=%s", env.State, env.Message, string(respBytes))
	}
	if env.Result == nil || strings.TrimSpace(env.Result.URL) == "" {
		return nil, fmt.Errorf("cryptomus 返回空结果 body=%s", string(respBytes))
	}
	return env.Result, nil
}

// CryptomusWebhookPayload 反序列化用，验签时只用关心 sign 是否匹配
type CryptomusWebhookPayload struct {
	Type           string `json:"type"`
	UUID           string `json:"uuid"`
	OrderID        string `json:"order_id"`
	Amount         string `json:"amount"`
	PaymentAmount  string `json:"payment_amount"`
	MerchantAmount string `json:"merchant_amount"`
	Status         string `json:"status"`
	IsFinal        bool   `json:"is_final"`
	Currency       string `json:"currency"`
	PayerCurrency  string `json:"payer_currency"`
	Network        string `json:"network"`
	TxID           string `json:"txid"`
	Sign           string `json:"sign"`
}

// VerifyCryptomusWebhook 接收原 webhook body，校验签名。
// 优先用 setting.CryptomusWebhookApiKey，未设置时退回 PaymentApiKey。
// 返回反序列化后的 payload，便于上层用。
func VerifyCryptomusWebhook(rawBody []byte) (*CryptomusWebhookPayload, error) {
	if len(rawBody) == 0 {
		return nil, errors.New("empty webhook body")
	}
	apiKey := strings.TrimSpace(setting.CryptomusWebhookApiKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(setting.CryptomusPaymentApiKey)
	}
	if apiKey == "" {
		return nil, errors.New("cryptomus webhook api key 未配置")
	}

	rawStr := string(rawBody)
	signRecv := gjson.Get(rawStr, "sign").String()
	if signRecv == "" {
		return nil, errors.New("webhook 缺少 sign 字段")
	}

	// sjson.Delete 在原 JSON 串上原地删 sign，保留其余字段顺序，
	// 这是跟 cryptomus PHP 端 unset+json_encode 行为对齐的关键。
	stripped, err := sjson.Delete(rawStr, "sign")
	if err != nil {
		return nil, fmt.Errorf("strip sign: %w", err)
	}

	expected := cryptomusSign([]byte(stripped), apiKey)
	if !strings.EqualFold(expected, signRecv) {
		return nil, fmt.Errorf("invalid signature")
	}

	var payload CryptomusWebhookPayload
	if err := common.UnmarshalJsonStr(rawStr, &payload); err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	return &payload, nil
}

// IsCryptomusPaidStatus 判断 webhook 状态是否代表付款已确认。
// paid / paid_over（多付）都视为成功；wrong_amount / cancel / system_fail / refund_*
// 都不入账。这里跟 cryptomus 文档保持对齐。
func IsCryptomusPaidStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "paid", "paid_over":
		return true
	default:
		return false
	}
}
