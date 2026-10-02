package setting

// BEpusdt 自建加密币收款网关配置
// 网关：https://github.com/v03413/BEpusdt（自部署，收银台由用户自选 USDT 链）
//
// 必填：BepusdtBaseURL（网关公网地址，如 https://pay.example.com）+ BepusdtApiToken（网关后台「API设置 → 对接令牌」）。
// 建单与回调验签共用同一把令牌。回调地址固定为 <ServerAddress>/api/bepusdt/webhook。
//
// 启用后顶替 Cryptomus 成为充值页唯一的加密货币入口（Cryptomus 配置保留，关掉本开关即切回）。
var (
	BepusdtEnabled       bool
	BepusdtBaseURL       string
	BepusdtApiToken      string
	BepusdtCurrencies    string  = "USDT" // 收银台限定币种：USDT / USDT,USDC / -ETH（短横线开头为黑名单）
	BepusdtLifetimeSec   int     = 1200   // 订单有效期（秒），网关最低 180，默认与网关 payment_timeout 对齐
	BepusdtUnitPrice     float64 = 1.0    // 单价（USD/额度单位），与 Cryptomus 同口径
	BepusdtMinTopUp      int     = 1
	BepusdtReturnURL     string
	BepusdtAllowedGroups string // ';' 分隔的用户分组白名单，空=全部分组可用
	BepusdtLogo          string // 平台 logo：图片 URL 或 base64 dataURL，充值页卡片展示
)

// BepusdtMinLifetimeSec 网关 create-order 接受的最小 timeout；低于它建单会被拒。
const BepusdtMinLifetimeSec = 180

// GetBepusdtLifetimeSec 返回兜底后的订单有效期（≥180），避免后台填错把整个渠道打挂。
func GetBepusdtLifetimeSec() int {
	if BepusdtLifetimeSec < BepusdtMinLifetimeSec {
		return BepusdtMinLifetimeSec
	}
	return BepusdtLifetimeSec
}
