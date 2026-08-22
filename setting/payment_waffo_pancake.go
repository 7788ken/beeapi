package setting

var (
	WaffoPancakeEnabled          bool
	WaffoPancakeSandbox          bool
	WaffoPancakeMerchantID       string
	WaffoPancakePrivateKey       string
	WaffoPancakeWebhookPublicKey string
	WaffoPancakeWebhookTestKey   string
	WaffoPancakeStoreID          string
	WaffoPancakeProductID        string
	WaffoPancakeReturnURL        string
	WaffoPancakeCurrency         string  = "USD"
	WaffoPancakeUnitPrice        float64 = 1.0
	WaffoPancakeMinTopUp         int     = 1
	WaffoPancakeAllowedGroups    string  // ';' 分隔的用户分组白名单，空=全部分组可用
	WaffoPancakeLogo             string  // 平台 logo：图片 URL 或 base64 dataURL，充值页卡片展示
)
