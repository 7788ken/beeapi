package service

import (
	"encoding/json"
	"strings"
	"testing"
)

const bepusdtTestToken = "epusdt_password_xasddawqe"

// 官方文档 docs/api/api.md「签名示例」：期望 1cd4b52df5587cfb1968b0c0c6e156cd
func TestBepusdtSignMatchesOfficialExample(t *testing.T) {
	sign := BepusdtSign(map[string]interface{}{
		"order_id":     "20220201030210321",
		"amount":       42,
		"notify_url":   "http://example.com/notify",
		"redirect_url": "http://example.com/redirect",
	}, bepusdtTestToken)
	if sign != "1cd4b52df5587cfb1968b0c0c6e156cd" {
		t.Fatalf("签名与官方示例不符: %s", sign)
	}
}

// 数字必须按网关侧 float64 的 %v 格式化：1.0→"1"、600→"600"、28.88→"28.88"
func TestBepusdtValueStringLikeGoPercentV(t *testing.T) {
	cases := map[interface{}]string{
		1.0:               "1",
		float64(600):      "600",
		28.88:             "28.88",
		0.01:              "0.01",
		int(1200):         "1200",
		int64(7):          "7",
		true:              "true",
		false:             "false",
		"TSsCww":          "TSsCww",
		json.Number("10"): "10",
	}
	for in, want := range cases {
		got, ok := bepusdtValueString(in)
		if !ok || got != want {
			t.Errorf("%#v: got %q ok=%v want %q", in, got, ok, want)
		}
	}
	if _, ok := bepusdtValueString(nil); ok {
		t.Errorf("nil 不应参与签名")
	}
	if _, ok := bepusdtValueString(map[string]interface{}{"a": 1}); ok {
		t.Errorf("对象不应参与签名")
	}
}

func TestBepusdtSignIgnoresEmptyAndSignatureField(t *testing.T) {
	base := BepusdtSign(map[string]interface{}{"order_id": "o-1", "amount": 1.0}, bepusdtTestToken)
	same := BepusdtSign(map[string]interface{}{"order_id": "o-1", "amount": 1.0, "name": "", "currencies": nil, "signature": "x"}, bepusdtTestToken)
	if base != same {
		t.Errorf("空值与 signature 字段不应影响签名")
	}
	diff := BepusdtSign(map[string]interface{}{"order_id": "o-1", "amount": 1.0, "name": "x"}, bepusdtTestToken)
	if base == diff {
		t.Errorf("非空字段应参与签名")
	}
	// 整数与同值浮点应得到同一签名（网关侧统一是 float64）
	if BepusdtSign(map[string]interface{}{"amount": 42}, "t") != BepusdtSign(map[string]interface{}{"amount": 42.0}, "t") {
		t.Errorf("42 与 42.0 应签出同一结果")
	}
}

func bepusdtNotifyBody(t *testing.T, overrides map[string]interface{}, token string) []byte {
	t.Helper()
	payload := map[string]interface{}{
		"trade_id":             "jEkthqnMniGUmYzI3f",
		"order_id":             "BEPUSDT-1-1-abc",
		"amount":               10.0,
		"actual_amount":        "10.02",
		"token":                "TSsCwwB1KCKgmqVxxQz3BDkMTqu3HGYZWy",
		"block_transaction_id": "12ef6267b42e43959795cf31808d0cc72b3d0a48953ed19c61d4b6665a341d10",
		"status":               2.0,
	}
	for k, v := range overrides {
		payload[k] = v
	}
	payload["signature"] = BepusdtSign(payload, token)
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVerifyBepusdtNotifyAcceptsSigned(t *testing.T) {
	p, err := VerifyBepusdtNotify(bepusdtNotifyBody(t, nil, bepusdtTestToken), bepusdtTestToken)
	if err != nil {
		t.Fatalf("应验签通过: %v", err)
	}
	if p.OrderID != "BEPUSDT-1-1-abc" || p.Status != 2 || p.Amount != 10 || p.ActualAmount != "10.02" || p.TradeID != "jEkthqnMniGUmYzI3f" {
		t.Errorf("payload 解析错误: %+v", p)
	}
	if !IsBepusdtPaidStatus(p.Status) {
		t.Errorf("status=2 应判为已支付")
	}
}

func TestVerifyBepusdtNotifyRejectsTampered(t *testing.T) {
	raw := string(bepusdtNotifyBody(t, nil, bepusdtTestToken))
	tampered := strings.Replace(raw, `"amount":10`, `"amount":1000`, 1)
	if tampered == raw {
		t.Fatalf("测试体构造失败: %s", raw)
	}
	if _, err := VerifyBepusdtNotify([]byte(tampered), bepusdtTestToken); err == nil || !strings.Contains(err.Error(), "invalid signature") {
		t.Errorf("篡改金额应验签失败, err=%v", err)
	}
	if _, err := VerifyBepusdtNotify(bepusdtNotifyBody(t, nil, "other-token"), bepusdtTestToken); err == nil {
		t.Errorf("其他令牌签名应被拒")
	}
}

func TestVerifyBepusdtNotifyEdgeCases(t *testing.T) {
	if _, err := VerifyBepusdtNotify([]byte(`{"order_id":"x"}`), bepusdtTestToken); err == nil || !strings.Contains(err.Error(), "no signature") {
		t.Errorf("缺 signature 应拒, err=%v", err)
	}
	if _, err := VerifyBepusdtNotify([]byte(`not json`), bepusdtTestToken); err == nil {
		t.Errorf("非 JSON 应拒")
	}
	if _, err := VerifyBepusdtNotify(nil, bepusdtTestToken); err == nil {
		t.Errorf("空体应拒")
	}
	if _, err := VerifyBepusdtNotify(bepusdtNotifyBody(t, nil, bepusdtTestToken), ""); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("令牌未配应拒, err=%v", err)
	}
	// 签名大小写不敏感
	raw := string(bepusdtNotifyBody(t, nil, bepusdtTestToken))
	idx := strings.Index(raw, `"signature":"`)
	start := idx + len(`"signature":"`)
	upper := raw[:start] + strings.ToUpper(raw[start:start+32]) + raw[start+32:]
	if p, err := VerifyBepusdtNotify([]byte(upper), bepusdtTestToken); err != nil || p.Status != 2 {
		t.Errorf("大写签名应通过: err=%v", err)
	}
}

func TestIsBepusdtPaidStatus(t *testing.T) {
	if IsBepusdtPaidStatus(1) || IsBepusdtPaidStatus(3) || IsBepusdtPaidStatus(0) {
		t.Errorf("只有 2 算已支付")
	}
	if !IsBepusdtPaidStatus(2) {
		t.Errorf("2 应算已支付")
	}
}
