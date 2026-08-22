package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// URLHealthConfig 渠道内多 base_url 故障切换的"又稳又快"路由参数（service/url_health.go 消费）。
//
// 全部 admin 后台可调、实时生效：每次选路 PickURLForChannel / 记录结果 RecordURLResult 时直接读取，
// 无需重启、无副作用（纯内存状态机的阈值）。
//
// 注：网络层超时（Dial / TLS 握手 / 流式响应头）属基础设施参数，仍由环境变量
// RELAY_DIAL_TIMEOUT / RELAY_TLS_HANDSHAKE_TIMEOUT / RELAY_STREAM_RESP_HEADER_TIMEOUT 配置，
// 改后需重启生效（它们在 InitHttpClient 时固化进 http.Transport）。
//
// 关联文档：Docs/2026-06-28-channel-multi-baseurl-failover-plan.md
type URLHealthConfig struct {
	// FailThreshold 同一 URL 连续失败达到此次数 → 触发熔断（暂时剔除该 URL）。建议 2~5。
	FailThreshold int `json:"fail_threshold"`

	// CooldownSeconds 熔断冷却时长（秒），到期后该 URL 自动半开、可再次被选中试探。
	CooldownSeconds int `json:"cooldown_seconds"`

	// EwmaAlpha TTFB 首字节延迟指数移动平均的平滑系数，(0,1]，越大越偏向最新样本。
	EwmaAlpha float64 `json:"ewma_alpha"`

	// HysteresisRatio 迟滞：候选需比当前选中 URL 快超过 current*ratio 才切换，
	// 防两个延迟接近的 URL 来回横跳。
	HysteresisRatio float64 `json:"hysteresis_ratio"`

	// HysteresisMinMs 迟滞绝对下限（毫秒）：与 current*ratio 取较大者，
	// 避免低延迟场景下 ratio 算出的阈值过小而失效。
	HysteresisMinMs float64 `json:"hysteresis_min_ms"`

	// ExplorationGapSeconds 保底探索：URL 超过此时长无新样本则强制探一次，
	// 确保非最快 URL 数据保持新鲜（最快节点骤挂时仍有可靠次优）。
	ExplorationGapSeconds int `json:"exploration_gap_seconds"`
}

// 默认值与 service/url_health.go 原硬编码常量一致，保持行为不变。
var urlHealthSetting = URLHealthConfig{
	FailThreshold:         3,
	CooldownSeconds:       60,
	EwmaAlpha:             0.2,
	HysteresisRatio:       0.2,
	HysteresisMinMs:       50,
	ExplorationGapSeconds: 30,
}

func init() {
	config.GlobalConfig.Register("url_health_setting", &urlHealthSetting)
}

// GetURLHealthConfig 返回多 base_url 路由参数（指针，DB 更新后字段实时生效）。
func GetURLHealthConfig() *URLHealthConfig {
	return &urlHealthSetting
}
