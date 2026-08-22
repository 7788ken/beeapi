package operation_setting

import (
	"github.com/QuantumNous/new-api/setting/config"
)

// TokenHealthConfig 令牌（用户 API Key）错误率熔断配置。
//
// 目标：单个 token 在滑动窗口内的客户端错误率过高时，临时冷却该 token N 分钟，
// 防止异常 key（集成配错 / 死循环重试）持续打错误流量拖累系统。
//
// 完全被动：信号源自真实请求终态；冷却到期自动恢复（Redis TTL / 内存兜底），
// 不改 Token.Status、不动 token 缓存、无需后台任务。
//
// 统计所有错误响应（≥400 且 ≠429），含上游 5xx/超时——若某上游故障期不想误冷却
// 正常 key，可把对应码填进 ExcludedStatusCodes 排除。详见 service/token_health.go。
type TokenHealthConfig struct {
	// Enabled 总开关。默认 false（关闭时鉴权路径零开销）。
	Enabled bool `json:"enabled"`

	// WindowMinutes 统计滑动窗口（分钟）。默认 10。
	WindowMinutes int `json:"window_minutes"`

	// MinRequests 窗口内最少请求数，达到才参与判定（防 1/1=100% 误杀）。默认 50。
	MinRequests int `json:"min_requests"`

	// ErrorRateThreshold 触发冷却的错误率阈值（0~1）。默认 0.9。
	ErrorRateThreshold float64 `json:"error_rate_threshold"`

	// CooldownMinutes 命中后冷却时长（分钟）。默认 5。
	CooldownMinutes int `json:"cooldown_minutes"`

	// ExcludedStatusCodes 额外不计入错误率的状态码（逗号分隔，如 "401,403" 或 "502,503"）。
	// 429 始终不计入；4xx/5xx 均可在此排除（如上游故障期不想连坐冷却好 key）。默认空。
	ExcludedStatusCodes string `json:"excluded_status_codes"`
}

// 默认值取「保守」档：少误杀，先观察。
var tokenHealthSetting = TokenHealthConfig{
	Enabled:             false,
	WindowMinutes:       10,
	MinRequests:         50,
	ErrorRateThreshold:  0.9,
	CooldownMinutes:     5,
	ExcludedStatusCodes: "",
}

func init() {
	config.GlobalConfig.Register("token_health_setting", &tokenHealthSetting)
}

func GetTokenHealthConfig() *TokenHealthConfig {
	return &tokenHealthSetting
}
