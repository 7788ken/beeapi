package operation_setting

import (
	"github.com/QuantumNous/new-api/setting/config"
)

// RetryShortCircuitConfig 非流式断连重试短路配置。
//
// 场景：下游客户端用官方 SDK 默认 600s 超时跑大输出非流式请求 → 到点客户端断开 →
// 上游已跑完照样计费 → SDK 对超时自动重试同一请求 → 反复烧钱。
// 开启后：识别"刚被客户端取消的同一请求又来了"，入口直接 400 拒绝
//（官方 SDK 对 400 不自动重试），并在错误信息里给出处置指引（stream=true / 调大超时）。
//
// options 表扁平键：retry_short_circuit_setting.<json标签>，经 SyncOptions 每 60s 热加载。
type RetryShortCircuitConfig struct {
	// Enabled 总开关，默认关闭。
	Enabled bool `json:"enabled"`

	// MinDurationSeconds 只记录运行超过该秒数才被客户端取消的请求
	//（过滤手动中断等短请求，聚焦"跑了很久才被 SDK 超时掐断"的场景）。
	MinDurationSeconds int `json:"min_duration_seconds"`

	// TTLMinutes 指纹存活时间（分钟）。命中不续期，客户调大超时/改流式后不会被永久锁死。
	TTLMinutes int `json:"ttl_minutes"`
}

var retryShortCircuitSetting = RetryShortCircuitConfig{
	Enabled:            false,
	MinDurationSeconds: 300,
	TTLMinutes:         15,
}

func init() {
	config.GlobalConfig.Register("retry_short_circuit_setting", &retryShortCircuitSetting)
}

func GetRetryShortCircuitConfig() *RetryShortCircuitConfig {
	return &retryShortCircuitSetting
}
