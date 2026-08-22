package setting

// Cryptomus 数字货币支付配置
// API doc: https://doc.cryptomus.com/business/payments
//
// 必填：CryptomusMerchantID + CryptomusPaymentApiKey；
//
//	webhook 验签默认复用 CryptomusPaymentApiKey，若 cryptomus 后台为 webhook
//	单独生成了 key 再填 CryptomusWebhookApiKey 覆盖。
//
// AllowedCurrencies 是英文逗号分隔白名单（cryptomus 收银台 currencies 参数）；
// DefaultCurrency / DefaultNetwork 决定 to_currency / network 字段，固定收款币种。
// 默认 USDT-TRC20（手续费低、用户最熟悉）。
var (
	CryptomusEnabled           bool
	CryptomusMerchantID        string
	CryptomusPaymentApiKey     string
	CryptomusWebhookApiKey     string
	CryptomusDefaultCurrency           = "USDT"
	CryptomusDefaultNetwork            = "TRX"
	CryptomusAllowedCurrencies string  = "USDT,BTC,ETH,SOL,TRX"
	CryptomusUnitPrice         float64 = 1.0
	CryptomusMinTopUp          int     = 1
	CryptomusReturnURL         string
	CryptomusAllowedGroups     string // ';' 分隔的用户分组白名单，空=全部分组可用
	CryptomusLogo              string // 平台 logo：图片 URL 或 base64 dataURL，充值页卡片展示
)
