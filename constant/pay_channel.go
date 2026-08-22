package constant

// PayChannel 是三个动态支付网关（Waffo Pancake / Cryptomus / Agou）通用的“支付渠道”配置项：
// 充值页在每个支付平台卡片下展示的可选渠道（如支付宝 / 微信 / USDT / 银行卡）。
// 每条渠道 = 展示信息（Name/Icon）+ 启用开关（Enabled）+ 发起支付时透传给网关的参数（Params）。
type PayChannel struct {
	Key     string            `json:"key"`     // 唯一键：alipay / wxpay / card / usdt_trc20 ...
	Name    string            `json:"name"`    // 前端显示名
	Icon    string            `json:"icon"`    // 图标键（前端 react-icons，如 alipay）或图片 URL（/pay-card.png）
	Enabled bool              `json:"enabled"` // 是否在充值页展示
	Params  map[string]string `json:"params"`  // 发起支付时透传的网关参数（见各 provider 默认值说明）
}

// 注：Agou 的默认渠道依赖运行时 setting 变量（支付宝 payType / 微信开关），
// 在 setting.defaultAgouPayChannels() 动态构造，不在此静态定义。

// DefaultCryptomusPayChannels：Cryptomus 加密货币——常见币种 / 网络组合。
// Params.to_currency + Params.network 对应 cryptomus 收款币种与链（network 留空则由 cryptomus 按币种默认链）。
var DefaultCryptomusPayChannels = []PayChannel{
	{Key: "usdt_trc20", Name: "USDT-TRC20", Icon: "tether", Enabled: true, Params: map[string]string{"to_currency": "USDT", "network": "TRX"}},
	{Key: "usdt_erc20", Name: "USDT-ERC20", Icon: "tether", Enabled: true, Params: map[string]string{"to_currency": "USDT", "network": "ETH"}},
	{Key: "btc", Name: "Bitcoin", Icon: "bitcoin", Enabled: true, Params: map[string]string{"to_currency": "BTC"}},
	{Key: "eth", Name: "Ethereum", Icon: "ethereum", Enabled: true, Params: map[string]string{"to_currency": "ETH"}},
}

// DefaultWaffoPancakePayChannels：Waffo Pancake 托管收银台——渠道仅用于充值页展示，
// 不参与下单（跳转 Waffo 收银台后由用户自行选择），故 Params 为空。
var DefaultWaffoPancakePayChannels = []PayChannel{
	{Key: "card", Name: "Card", Icon: "/pay-card.png", Enabled: true, Params: map[string]string{}},
	{Key: "applepay", Name: "Apple Pay", Icon: "/pay-apple.png", Enabled: true, Params: map[string]string{}},
	{Key: "googlepay", Name: "Google Pay", Icon: "/pay-google.png", Enabled: true, Params: map[string]string{}},
}
