package setting

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

// 三个动态支付网关的“支付渠道”配置存储键（存 common.OptionMap，后台可增删改）。
const (
	optionKeyAgouPayChannels         = "SfpayPayChannels"
	optionKeyCryptomusPayChannels    = "CryptomusPayChannels"
	optionKeyWaffoPancakePayChannels = "WaffoPancakePayChannels"
)

// getPayChannels 从 OptionMap 读指定键的渠道列表；为空或解析失败回退默认列表。
func getPayChannels(optionKey string, defaults []constant.PayChannel) []constant.PayChannel {
	common.OptionMapRWMutex.RLock()
	jsonStr := common.OptionMap[optionKey]
	common.OptionMapRWMutex.RUnlock()

	if jsonStr == "" {
		return copyPayChannels(defaults)
	}
	var channels []constant.PayChannel
	if err := common.UnmarshalJsonStr(jsonStr, &channels); err != nil {
		return copyPayChannels(defaults)
	}
	return channels
}

// setPayChannels 序列化渠道列表并写回 OptionMap。
func setPayChannels(optionKey string, channels []constant.PayChannel) error {
	jsonBytes, err := common.Marshal(channels)
	if err != nil {
		return err
	}
	common.OptionMapRWMutex.Lock()
	common.OptionMap[optionKey] = string(jsonBytes)
	common.OptionMapRWMutex.Unlock()
	return nil
}

// payChannels2JsonString 把默认渠道列表序列化为 JSON（供 InitOptionMap 注入初始值）。
func payChannels2JsonString(defaults []constant.PayChannel) string {
	jsonBytes, err := common.Marshal(defaults)
	if err != nil {
		return "[]"
	}
	return string(jsonBytes)
}

// copyPayChannels 深拷贝渠道列表（含 Params map），避免调用方 mutate 到默认列表。
func copyPayChannels(src []constant.PayChannel) []constant.PayChannel {
	cp := make([]constant.PayChannel, len(src))
	for i, ch := range src {
		cp[i] = ch
		if ch.Params != nil {
			params := make(map[string]string, len(ch.Params))
			for k, v := range ch.Params {
				params[k] = v
			}
			cp[i].Params = params
		}
	}
	return cp
}

// 注：Agou 支付渠道的 Get/Set/2Json 在 payment_agou.go（默认值需继承运行时 setting 变量）。

// Cryptomus 支付渠道
func GetCryptomusPayChannels() []constant.PayChannel {
	return getPayChannels(optionKeyCryptomusPayChannels, constant.DefaultCryptomusPayChannels)
}
func SetCryptomusPayChannels(channels []constant.PayChannel) error {
	return setPayChannels(optionKeyCryptomusPayChannels, channels)
}
func CryptomusPayChannels2JsonString() string {
	return payChannels2JsonString(constant.DefaultCryptomusPayChannels)
}

// Waffo Pancake 支付渠道（纯展示，Params 为空）
func GetWaffoPancakePayChannels() []constant.PayChannel {
	return getPayChannels(optionKeyWaffoPancakePayChannels, constant.DefaultWaffoPancakePayChannels)
}
func SetWaffoPancakePayChannels(channels []constant.PayChannel) error {
	return setPayChannels(optionKeyWaffoPancakePayChannels, channels)
}
func WaffoPancakePayChannels2JsonString() string {
	return payChannels2JsonString(constant.DefaultWaffoPancakePayChannels)
}
