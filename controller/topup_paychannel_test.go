package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

func init() {
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
}

func clearControllerOption(key string) {
	common.OptionMapRWMutex.Lock()
	delete(common.OptionMap, key)
	common.OptionMapRWMutex.Unlock()
}

// buildTopUpProvider 必须：过滤未启用渠道、只暴露 key/name/icon、绝不泄露内部网关参数 Params。
func TestBuildTopUpProvider(t *testing.T) {
	channels := []constant.PayChannel{
		{Key: "alipay", Name: "支付宝", Icon: "alipay", Enabled: true, Params: map[string]string{"pay_type": "ZFBPAY"}},
		{Key: "wxpay", Name: "微信", Icon: "wechat", Enabled: false, Params: map[string]string{"pay_type": ""}},
	}
	p := buildTopUpProvider("sfpay", "Sfpay", "https://cdn.example/sfpay.png", 1, 5000, false, channels)

	if p["id"] != "sfpay" || p["name"] != "Sfpay" {
		t.Errorf("id/name 错误: %+v", p)
	}
	if p["logo"] != "https://cdn.example/sfpay.png" {
		t.Errorf("logo 字段应原样输出: %+v", p["logo"])
	}
	if p["min_topup"] != 1 || p["max_topup"] != 5000 || p["blocked_by_group"] != false {
		t.Errorf("数值字段错误: %+v", p)
	}

	visible, ok := p["channels"].([]gin.H)
	if !ok {
		t.Fatalf("channels 类型错误: %T", p["channels"])
	}
	if len(visible) != 1 {
		t.Fatalf("应只暴露 1 个启用渠道（支付宝），实际 %d", len(visible))
	}
	if visible[0]["key"] != "alipay" || visible[0]["name"] != "支付宝" || visible[0]["icon"] != "alipay" {
		t.Errorf("渠道展示字段错误: %+v", visible[0])
	}
	if _, leaked := visible[0]["params"]; leaked {
		t.Errorf("不得向前端暴露内部网关参数 params: %+v", visible[0])
	}
}

func TestResolveCryptomusChannel(t *testing.T) {
	if err := setting.SetCryptomusPayChannels([]constant.PayChannel{
		{Key: "usdt_trc20", Name: "USDT-TRC20", Enabled: true, Params: map[string]string{"to_currency": "USDT", "network": "TRX"}},
		{Key: "btc", Name: "BTC", Enabled: false, Params: map[string]string{"to_currency": "BTC"}},
	}); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	defer clearControllerOption("CryptomusPayChannels")

	ch, ok := resolveCryptomusChannel("usdt_trc20")
	if !ok || ch.Params["network"] != "TRX" {
		t.Errorf("应命中启用的 usdt_trc20: %+v ok=%v", ch, ok)
	}
	if _, ok := resolveCryptomusChannel("btc"); ok {
		t.Errorf("未启用的 btc 不应命中")
	}
	if _, ok := resolveCryptomusChannel("doge"); ok {
		t.Errorf("不存在的 key 不应命中")
	}
}

func TestResolveAgouPayType(t *testing.T) {
	if err := setting.SetAgouPayChannels([]constant.PayChannel{
		{Key: "alipay", Name: "支付宝", Enabled: true, Params: map[string]string{"pay_type": "ZFBPAY"}},
		{Key: "wxpay", Name: "微信", Enabled: false, Params: map[string]string{"pay_type": ""}},
	}); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	defer clearControllerOption("SfpayPayChannels")

	payType, err := resolveAgouPayType("alipay")
	if err != nil || payType != "ZFBPAY" {
		t.Errorf("支付宝应解析为 ZFBPAY: %q err=%v", payType, err)
	}
	if _, err := resolveAgouPayType("wxpay"); err == nil {
		t.Errorf("未启用的微信应报错")
	}
	if _, err := resolveAgouPayType("unknown"); err == nil {
		t.Errorf("不存在的渠道应报错")
	}
}
