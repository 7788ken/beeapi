package service

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/setting"

	"github.com/tidwall/gjson"
)

// BEpusdt 网关客户端（收银台建单 + 回调验签）
// Doc: https://github.com/v03413/BEpusdt/blob/main/docs/api/api.md
//
// 签名是 epusdt 老协议，请求与回调同一套：
//  1. 取所有非空、非 signature 的参数，按 key ASCII 升序
//  2. 拼成 k=v&k=v，尾部**直接**追加 API 令牌（没有 &）
//  3. MD5 → 小写
//
// 坑（2026-09-30 6vc 实测）：网关把 JSON 反序列化成 map[string]any 后用 fmt %v 拼值，
// 数字一律是 float64 —— 1.0 拼成 "1"、600 拼成 "600"、28.88 拼成 "28.88"。
// 所以这里对数字统一按 float64 的 %v 格式化；回调验签同样先 JSON 解析成 map 再重算，
// 而不是拿原始字节。

const bepusdtCreateOrderPath = "/api/v1/order/create-order"

// BepusdtPaidStatus 回调 status：1 等待 / 2 成功 / 3 超时。只有 2 入账。
const BepusdtPaidStatus = 2

// bepusdtValueString 模拟网关侧 fmt.Sprintf("%v", v) 对 JSON 解析后标量的输出。
// 非标量（对象/数组/nil）不参与签名。
func bepusdtValueString(v interface{}) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case float64:
		return fmt.Sprintf("%v", x), true
	case float32:
		return fmt.Sprintf("%v", float64(x)), true
	case int:
		return fmt.Sprintf("%v", float64(x)), true
	case int64:
		return fmt.Sprintf("%v", float64(x)), true
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return x.String(), true
		}
		return fmt.Sprintf("%v", f), true
	default:
		return "", false
	}
}

// BepusdtSign 生成签名。空串 / nil / 非标量与 signature 本身不参与。
func BepusdtSign(params map[string]interface{}, token string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "signature" {
			continue
		}
		if s, ok := bepusdtValueString(v); !ok || s == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		s, _ := bepusdtValueString(params[k])
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(s)
	}
	b.WriteString(token)
	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// BepusdtCreateOrderRequest 收银台建单参数（用户在网关收银台自选 USDT 的链）。
type BepusdtCreateOrderRequest struct {
	OrderID     string  // 业务订单号（trade_no）
	Amount      float64 // 法币金额（USD）
	Fiat        string  // 固定 USD
	NotifyURL   string
	RedirectURL string // 网关必填
	Name        string
	Currencies  string // 限定币种，如 "USDT"
	TimeoutSec  int    // 秒，网关最低 180
}

// BepusdtOrderResult 建单返回。
type BepusdtOrderResult struct {
	TradeID    string
	PaymentURL string
}

// CreateBepusdtOrder 调网关 create-order 建单。
// 网关业务错误也是 HTTP 200，只能看 status_code。
func CreateBepusdtOrder(ctx context.Context, req *BepusdtCreateOrderRequest) (*BepusdtOrderResult, error) {
	if req == nil {
		return nil, errors.New("nil bepusdt order request")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(setting.BepusdtBaseURL), "/")
	token := strings.TrimSpace(setting.BepusdtApiToken)
	if baseURL == "" || token == "" {
		return nil, errors.New("bepusdt 网关配置缺失")
	}

	params := map[string]interface{}{
		"order_id":     req.OrderID,
		"amount":       req.Amount,
		"fiat":         req.Fiat,
		"notify_url":   req.NotifyURL,
		"redirect_url": req.RedirectURL,
		"name":         req.Name,
		"currencies":   req.Currencies,
		"timeout":      float64(req.TimeoutSec),
	}
	params["signature"] = BepusdtSign(params, token)

	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+bepusdtCreateOrderPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call bepusdt: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bepusdt http %d: %s", resp.StatusCode, truncateBepusdtBody(respBytes))
	}
	if !gjson.ValidBytes(respBytes) {
		return nil, fmt.Errorf("bepusdt returned non-json body=%s", truncateBepusdtBody(respBytes))
	}
	if code := gjson.GetBytes(respBytes, "status_code").Int(); code != 200 {
		return nil, fmt.Errorf("bepusdt status_code=%d message=%s body=%s", code, gjson.GetBytes(respBytes, "message").String(), truncateBepusdtBody(respBytes))
	}
	result := &BepusdtOrderResult{
		TradeID:    strings.TrimSpace(gjson.GetBytes(respBytes, "data.trade_id").String()),
		PaymentURL: strings.TrimSpace(gjson.GetBytes(respBytes, "data.payment_url").String()),
	}
	if result.TradeID == "" || result.PaymentURL == "" {
		return nil, fmt.Errorf("bepusdt 返回空 trade_id/payment_url body=%s", truncateBepusdtBody(respBytes))
	}
	return result, nil
}

func truncateBepusdtBody(b []byte) string {
	const max = 500
	if len(b) > max {
		return string(b[:max])
	}
	return string(b)
}

// BepusdtNotifyPayload 回调通知体。
type BepusdtNotifyPayload struct {
	TradeID            string  // 网关订单号
	OrderID            string  // 业务订单号（trade_no）
	Amount             float64 // 法币金额（网关以 float64 下发）
	ActualAmount       string  // 实付加密币数量
	Token              string  // 收款地址
	BlockTransactionID string  // 链上交易哈希
	Status             int     // 1 等待 / 2 成功 / 3 超时
}

// VerifyBepusdtNotify 校验回调签名：对解析后的 JSON 全部顶层标量字段重算（剔 signature），
// 值按网关 %v 规则字符串化；对象/数组值不参与（网关通知体里不会出现）。
func VerifyBepusdtNotify(rawBody []byte, token string) (*BepusdtNotifyPayload, error) {
	if len(rawBody) == 0 {
		return nil, errors.New("empty notify body")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("bepusdt api token not configured")
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(rawBody, &obj); err != nil || obj == nil {
		return nil, errors.New("notify body is not a json object")
	}
	signRecv, _ := obj["signature"].(string)
	signRecv = strings.ToLower(strings.TrimSpace(signRecv))
	if signRecv == "" {
		return nil, errors.New("notify body has no signature")
	}
	if BepusdtSign(obj, token) != signRecv {
		return nil, errors.New("invalid signature")
	}

	raw := string(rawBody)
	return &BepusdtNotifyPayload{
		TradeID:            strings.TrimSpace(gjson.Get(raw, "trade_id").String()),
		OrderID:            strings.TrimSpace(gjson.Get(raw, "order_id").String()),
		Amount:             gjson.Get(raw, "amount").Float(),
		ActualAmount:       gjson.Get(raw, "actual_amount").String(),
		Token:              gjson.Get(raw, "token").String(),
		BlockTransactionID: gjson.Get(raw, "block_transaction_id").String(),
		Status:             int(gjson.Get(raw, "status").Int()),
	}, nil
}

// IsBepusdtPaidStatus 只有 2（支付成功）入账；1 等待每分钟推一次、3 超时推一次，都只需 200。
func IsBepusdtPaidStatus(status int) bool {
	return status == BepusdtPaidStatus
}
