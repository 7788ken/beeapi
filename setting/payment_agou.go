package setting

import (
	"strings"

	"github.com/QuantumNous/new-api/constant"
)

// sfpay（S-Client / Sfpay，四方聚合支付）代收充值配置。凭证仅在运行时由 admin 后台填入，
// 不写入代码仓 / 前端 / env。
var (
	AgouEnabled            bool
	AgouBaseURL            string
	AgouAppId              string
	AgouAppSecret          string
	AgouGroupCode          string
	AgouNotifyUrl          string        // 可选覆盖；为空则用 GetCallbackAddress() + /api/sfpay/notify
	AgouReturnUrl          string        // 可选覆盖
	AgouUnitPrice          float64 = 7.3 // 人民币 / 充值单位
	AgouMinTopUp           int     = 1
	AgouMaxTopUp           int     = 0 // 0 = 不额外限制上限（仍受通道 ¥5000 约束）
	// 留空 = 不限制来源 IP。sfpay 的回调源 IP 未知，硬编码旧 agou IP 会让全部回调被 403 拒掉、
	// 充值静默不到账；安全底线是回调验签（MD5+appSecret），上线后观察真实源 IP 再收紧。
	AgouAllowedCallbackIPs string = ""
	AgouAlipayPayType      string = "ZFBPAY"
	AgouWechatEnabled      bool
	AgouWechatPayType      string // 待运营绑微信通道组后取真实码
	AgouAllowedGroups      string // ';' 分隔的用户分组白名单，空=全部分组可用
	AgouLogo               string // 平台 logo：图片 URL 或 base64 dataURL，充值页卡片展示
)

// AgouPayMethod 暴露给前端的支付方式（前端按 Type 复用现成图标）。
// 保留供 classic 前端与旧 topup/info 字段 sfpay_pay_methods 向后兼容。
type AgouPayMethod struct {
	Name    string `json:"name"`
	Type    string `json:"type"`     // 前端图标键：alipay / wxpay
	PayType string `json:"pay_type"` // agou payType 码，如 ZFBPAY
	Enabled bool   `json:"enabled"`
}

// defaultAgouPayChannels 用当前 setting 变量构造默认渠道：支付宝（有 payType 即启用）+ 微信（占位）。
// 这样后台从未配置过 AgouPayChannels 时，行为与历史 setting 配置保持一致。
func defaultAgouPayChannels() []constant.PayChannel {
	return []constant.PayChannel{
		{
			Key:     "alipay",
			Name:    "支付宝",
			Icon:    "alipay",
			Enabled: strings.TrimSpace(AgouAlipayPayType) != "",
			Params:  map[string]string{"pay_type": AgouAlipayPayType},
		},
		{
			Key:     "wxpay",
			Name:    "微信支付",
			Icon:    "wechat",
			Enabled: AgouWechatEnabled && strings.TrimSpace(AgouWechatPayType) != "",
			Params:  map[string]string{"pay_type": AgouWechatPayType},
		},
	}
}

// GetAgouPayChannels 读后台配置的 Agou 支付渠道，为空回退 defaultAgouPayChannels。
func GetAgouPayChannels() []constant.PayChannel {
	return getPayChannels(optionKeyAgouPayChannels, defaultAgouPayChannels())
}

// SetAgouPayChannels 写回 Agou 支付渠道配置。
func SetAgouPayChannels(channels []constant.PayChannel) error {
	return setPayChannels(optionKeyAgouPayChannels, channels)
}

// AgouPayChannels2JsonString 供 InitOptionMap 注入初始值。
func AgouPayChannels2JsonString() string {
	return payChannels2JsonString(defaultAgouPayChannels())
}

// GetAgouPayMethods 从可配置的 PayChannels 派生旧格式（向后兼容 classic 前端与 agou_pay_methods 字段）。
func GetAgouPayMethods() []AgouPayMethod {
	channels := GetAgouPayChannels()
	methods := make([]AgouPayMethod, 0, len(channels))
	for _, ch := range channels {
		payType := strings.TrimSpace(ch.Params["pay_type"])
		methods = append(methods, AgouPayMethod{
			Name:    ch.Name,
			Type:    ch.Key,
			PayType: payType,
			Enabled: ch.Enabled && payType != "",
		})
	}
	return methods
}
