package service

import (
	"bytes"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
)

// agouSign 按 sfpay 规则签名：除 sign 外剔 null、参数名 ASCII 升序、k=v&k=v、
// 末尾追加 &key=appSecret、整串 MD5 转大写。
// 注意：只对该接口文档定义的字段签名，混入未定义字段会被 sfpay 判为 1005 签名错误。
// 尤其不要带 nonce/timestamp——sfpay 的公共参数只有 appId + sign，多传必然 1005。
func agouSign(params map[string]interface{}, secret string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || v == nil {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		if sb.Len() > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(fmt.Sprintf("%v", params[k]))
	}
	sb.WriteString("&key=")
	sb.WriteString(secret)
	sum := md5.Sum([]byte(sb.String()))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func agouBaseURL() string {
	raw := strings.TrimSpace(setting.AgouBaseURL)
	// 无 scheme 时先按 https 补全，避免 url.Parse 把整串当成 Path。
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	// 只取 host，剥掉误填的路径（如把完整下单 URL .../payin/create 填进基础地址），并强制 https：
	// sfpay 网关强制 HTTPS，http 会被 301 跳转，Go 跟随重定向时会把 POST 降级为 GET 并丢弃 body，
	// 导致 sfpay 端收到 GET 报 code=1000 "Request method 'GET' is not supported"。
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return "https://" + u.Host
	}
	return strings.TrimRight(raw, "/")
}

// agouEnvelope sfpay 统一响应信封，成功只看 code===200。
type agouEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// AgouOrderData 代收下单返回的业务数据。
type AgouOrderData struct {
	OrderNo    string `json:"orderNo"`
	SysOrderNo string `json:"sysOrderNo"`
	TradeNo    string `json:"tradeNo"`
	PayType    string `json:"payType"`
	Currency   string `json:"currency"`
	PaidFee    string `json:"paidFee"`
	Decimals   int    `json:"decimals"`
	UrlType    int    `json:"urlType"`
	PayUrl     string `json:"payUrl"`
}

// AgouOrderStatusData 查单返回的业务数据。
type AgouOrderStatusData struct {
	OrderNo     string `json:"orderNo"`
	SysOrderNo  string `json:"sysOrderNo"`
	TradeNo     string `json:"tradeNo"`
	Currency    string `json:"currency"`
	PaidFee     string `json:"paidFee"`
	Decimals    int    `json:"decimals"`
	OrderStatus int    `json:"orderStatus"`
	PaidTime    int64  `json:"paidTime"`
}

// AgouCreateOrderParams 代收下单入参（业务字段，公共参数由本服务补齐）。
type AgouCreateOrderParams struct {
	OrderNo   string
	PayType   string
	PaidFee   string // 最小单位（分）整数字符串
	ClientIp  string
	ReturnUrl string
	NotifyUrl string
	Remark    string
}

func agouPostJSON(path string, params map[string]interface{}) (*agouEnvelope, error) {
	body, err := common.Marshal(params)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, agouBaseURL()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Timeout: 20 * time.Second,
		// 拒绝跟随重定向并直接报错：sfpay 接口只认 POST，Go 默认会把 301/302 后的 POST 降级为 GET 丢 body。
		// 与其静默降级拿到诡异的 "GET not supported"，不如 fail fast 并暴露 Location，便于定位 AgouBaseURL 配错。
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return fmt.Errorf("sfpay 端点发生重定向至 %s，已拒绝跟随（避免 POST 被降级为 GET）；请确认基础地址为 https 正确域名", r.URL.String())
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env agouEnvelope
	if err := common.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("sfpay 响应解析失败: %s", string(respBody))
	}
	return &env, nil
}

// AgouCreateOrder 调用代收下单 POST /payin/create，成功返回支付链接数据。
func AgouCreateOrder(p AgouCreateOrderParams) (*AgouOrderData, error) {
	body := map[string]interface{}{
		"appId":      setting.AgouAppId,
		"orderNo":    p.OrderNo,
		"groupCode":  setting.AgouGroupCode,
		"payType":    p.PayType,
		"currency":   "CNY",
		"paidFee":    p.PaidFee,
		"urlMode":    1, // 1 = 收银台页面
		"clientIp":   p.ClientIp,
		"deviceType": 3, // 3 = PC
		"returnUrl":  p.ReturnUrl,
		"notifyUrl":  p.NotifyUrl,
		"remark":     p.Remark,
	}
	body["sign"] = agouSign(body, setting.AgouAppSecret)

	env, err := agouPostJSON("/payin/create", body)
	if err != nil {
		return nil, err
	}
	if env.Code != 200 {
		return nil, fmt.Errorf("sfpay 下单失败 code=%d message=%s", env.Code, env.Message)
	}
	var data AgouOrderData
	if err := common.Unmarshal(env.Data, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// AgouQueryOrder 主动查单 POST /payin/status，作为回调兜底。
func AgouQueryOrder(orderNo string) (*AgouOrderStatusData, error) {
	body := map[string]interface{}{
		"appId":   setting.AgouAppId,
		"orderNo": orderNo,
	}
	body["sign"] = agouSign(body, setting.AgouAppSecret)

	env, err := agouPostJSON("/payin/status", body)
	if err != nil {
		return nil, err
	}
	if env.Code != 200 {
		return nil, fmt.Errorf("sfpay 查单失败 code=%d message=%s", env.Code, env.Message)
	}
	var data AgouOrderStatusData
	if err := common.Unmarshal(env.Data, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// AgouVerifyNotify 验证回调签名，返回解析出的字段（字符串形态）与是否合法。
// 直接对原始 JSON 取值，避免数字（如 paidTime/orderStatus）经 float64 round-trip
// 变成科学计数法导致验签字符串不一致。
func AgouVerifyNotify(body []byte) (fields map[string]string, ok bool, err error) {
	var raw map[string]json.RawMessage
	if err = common.Unmarshal(body, &raw); err != nil {
		return nil, false, err
	}
	sign := ""
	fields = make(map[string]string, len(raw))
	for k, v := range raw {
		s := strings.TrimSpace(string(v))
		if s == "null" {
			continue
		}
		// 字符串值去掉外层引号；数字/布尔保持原样字节。
		if len(s) >= 2 && s[0] == '"' {
			var str string
			if err = common.Unmarshal(v, &str); err != nil {
				return nil, false, err
			}
			s = str
		}
		if k == "sign" {
			sign = s
			continue
		}
		fields[k] = s
	}

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		if sb.Len() > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(fields[k])
	}
	sb.WriteString("&key=")
	sb.WriteString(setting.AgouAppSecret)
	sum := md5.Sum([]byte(sb.String()))
	expected := strings.ToUpper(hex.EncodeToString(sum[:]))
	match := subtle.ConstantTimeCompare([]byte(expected), []byte(sign)) == 1
	return fields, sign != "" && match, nil
}
