package setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

func init() {
	// 模拟 InitOptionMap：单元测试不走完整启动流程，需手动初始化全局 OptionMap。
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
}

// 清理指定 option 键，模拟“后台从未配置过”。
func clearOption(key string) {
	common.OptionMapRWMutex.Lock()
	delete(common.OptionMap, key)
	common.OptionMapRWMutex.Unlock()
}

func TestDefaultCryptomusPayChannels(t *testing.T) {
	clearOption(optionKeyCryptomusPayChannels)

	channels := GetCryptomusPayChannels()
	if len(channels) != 4 {
		t.Fatalf("默认 Cryptomus 渠道应为 4 个，实际 %d", len(channels))
	}
	if channels[0].Key != "usdt_trc20" ||
		channels[0].Params["to_currency"] != "USDT" ||
		channels[0].Params["network"] != "TRX" {
		t.Errorf("usdt_trc20 渠道参数错误: %+v", channels[0])
	}
	// BTC 渠道不应带 network（由 cryptomus 用默认链）
	for _, ch := range channels {
		if ch.Key == "btc" {
			if _, ok := ch.Params["network"]; ok {
				t.Errorf("btc 渠道不应含 network: %+v", ch.Params)
			}
		}
	}
}

func TestDefaultWaffoPancakePayChannels(t *testing.T) {
	clearOption(optionKeyWaffoPancakePayChannels)

	channels := GetWaffoPancakePayChannels()
	if len(channels) != 3 {
		t.Fatalf("默认 Waffo Pancake 渠道应为 3 个，实际 %d", len(channels))
	}
	// 纯展示：Params 应为空
	for _, ch := range channels {
		if len(ch.Params) != 0 {
			t.Errorf("Waffo Pancake 渠道 %s 应无网关参数，实际 %+v", ch.Key, ch.Params)
		}
	}
}

func TestSetGetPayChannelsRoundTrip(t *testing.T) {
	custom := []constant.PayChannel{
		{Key: "card", Name: "银行卡", Icon: "/pay-card.png", Enabled: true, Params: map[string]string{}},
		{Key: "applepay", Name: "Apple Pay", Icon: "/pay-apple.png", Enabled: false, Params: map[string]string{}},
	}
	if err := SetWaffoPancakePayChannels(custom); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	got := GetWaffoPancakePayChannels()
	if len(got) != 2 || got[0].Name != "银行卡" || got[1].Enabled {
		t.Errorf("往返不一致: %+v", got)
	}
	clearOption(optionKeyWaffoPancakePayChannels)
}

func TestAgouPayChannelsDefaultInheritsSetting(t *testing.T) {
	clearOption(optionKeyAgouPayChannels)
	AgouAlipayPayType = "ZFBPAY"
	AgouWechatEnabled = false
	AgouWechatPayType = ""

	channels := GetAgouPayChannels()
	if len(channels) != 2 {
		t.Fatalf("默认应 2 个渠道，实际 %d", len(channels))
	}
	if channels[0].Key != "alipay" || !channels[0].Enabled || channels[0].Params["pay_type"] != "ZFBPAY" {
		t.Errorf("支付宝应启用且 pay_type=ZFBPAY: %+v", channels[0])
	}
	if channels[1].Key != "wxpay" || channels[1].Enabled {
		t.Errorf("微信默认应禁用（无 payType）: %+v", channels[1])
	}
}

func TestGetAgouPayMethodsDerivedFromChannels(t *testing.T) {
	// pay_type 为空的渠道，派生出的旧方式必须 Enabled=false（即便 PayChannel.Enabled=true）
	if err := SetAgouPayChannels([]constant.PayChannel{
		{Key: "alipay", Name: "支付宝", Icon: "alipay", Enabled: true, Params: map[string]string{"pay_type": "ZFBPAY"}},
		{Key: "wxpay", Name: "微信支付", Icon: "wechat", Enabled: true, Params: map[string]string{"pay_type": ""}},
	}); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	methods := GetAgouPayMethods()
	if len(methods) != 2 {
		t.Fatalf("应派生 2 个方式，实际 %d", len(methods))
	}
	if methods[0].Type != "alipay" || methods[0].PayType != "ZFBPAY" || !methods[0].Enabled {
		t.Errorf("支付宝派生错误: %+v", methods[0])
	}
	if methods[1].Enabled {
		t.Errorf("微信 pay_type 为空应派生为禁用: %+v", methods[1])
	}
	clearOption(optionKeyAgouPayChannels)
}

// TestCryptomusChannelsReadFromOptionMap 模拟后台真实保存路径：
// updateOptionMap 直接写 OptionMap[key]=json（不经 SetXxx），GetXxx 应能读出。
func TestCryptomusChannelsReadFromOptionMap(t *testing.T) {
	custom := `[{"key":"usdt_bsc","name":"USDT-BSC","icon":"tether","enabled":true,"params":{"to_currency":"USDT","network":"BSC"}}]`
	common.OptionMapRWMutex.Lock()
	common.OptionMap[optionKeyCryptomusPayChannels] = custom
	common.OptionMapRWMutex.Unlock()

	channels := GetCryptomusPayChannels()
	if len(channels) != 1 || channels[0].Key != "usdt_bsc" || channels[0].Params["network"] != "BSC" {
		t.Errorf("应读出后台保存的自定义渠道: %+v", channels)
	}
	clearOption(optionKeyCryptomusPayChannels)
}
