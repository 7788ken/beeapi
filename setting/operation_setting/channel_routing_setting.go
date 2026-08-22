package operation_setting

import (
	"github.com/QuantumNous/new-api/setting/config"
)

// ChannelRoutingConfig 渠道路由模式（概率 ↔ 容量+溢出）全局配置。
//
// 详见 docs/2026-05-26-channel-routing-mode-switchable.md
//
// 默认全局 probabilistic，与现行加权随机行为完全一致。仅当 admin 主动切换
// 全局 / 单渠道到 capacity 模式时，才启用容量过滤逻辑。
//
// P0+P1 阶段：本配置仅承载开关与计数器参数，主路径不接入；
// P2 阶段：才在 model/channel_cache.go 内消费此配置做容量过滤。
type ChannelRoutingConfig struct {
	// Mode 全局默认路由模式。
	// "probabilistic" = 现行加权随机（默认），"capacity" = 水桶溢出。
	Mode string `json:"mode"`

	// CapacityWindowSec 容量统计滑动窗口长度（秒），默认 60。
	// 影响所有未独立配置 capacity_window_sec 的渠道。
	//
	// ⚠ P2 阶段限制：计数器实现固定 60s 滑动窗口（按 1 分钟桶 + 加权法估算）。
	// 即使配置为其它值，实际生效窗口仍是 60s。变长窗口在 P3 用 sub-minute
	// bucket 重写后启用。
	CapacityWindowSec int `json:"capacity_window_sec"`

	// FullStrategy 容量全满策略：
	// "fallback" = 降到次优先级（推荐默认）
	// "reject"   = 直接返回 429
	// "degraded" = 选用量比例最低的渠道（最少超载）
	// "queue"    = 排队等待名额释放（同优先级/低优先级渠道全满后），超时返回 429
	FullStrategy string `json:"full_strategy"`

	// QueueMaxWaitMs full_strategy="queue" 时，所有可用渠道都满后请求最长排队等待
	// 毫秒数，超过则返回 429。须远小于 nginx/RELAY_TIMEOUT，建议 ≤ 30000。
	QueueMaxWaitMs int `json:"queue_max_wait_ms"`

	// QueuePollIntervalMs 排队期间重新选渠道的轮询间隔毫秒数（实际叠加随机抖动防惊群）。
	QueuePollIntervalMs int `json:"queue_poll_interval_ms"`

	// FailMode Redis 故障兜底：
	// "fail_open"   = 降级 probabilistic，业务不阻断（默认）
	// "fail_closed" = 视为已满，配合 reject 实现严格控容量
	FailMode string `json:"fail_mode"`

	// DryRun 启用后计数器照写，但不触发容量过滤（仅用于评估切换影响）。
	DryRun bool `json:"dry_run"`

	// DryRunSampleRate dry-run 写日志的采样率（0.0~1.0，默认 0.01=1/100）。
	// 高 QPS 下避免日志爆炸。
	DryRunSampleRate float64 `json:"dry_run_sample_rate"`
}

var channelRoutingSetting = ChannelRoutingConfig{
	Mode:                "probabilistic",
	CapacityWindowSec:   60,
	FullStrategy:        "fallback",
	FailMode:            "fail_open",
	DryRun:              false,
	DryRunSampleRate:    0.01,
	QueueMaxWaitMs:      30000,
	QueuePollIntervalMs: 500,
}

func init() {
	config.GlobalConfig.Register("channel_routing_setting", &channelRoutingSetting)
}

// GetChannelRoutingConfig 返回当前路由配置（指针，调用方只读）
func GetChannelRoutingConfig() *ChannelRoutingConfig {
	return &channelRoutingSetting
}

// IsCapacityModeEnabled 全局是否启用容量模式
func IsCapacityModeEnabled() bool {
	return channelRoutingSetting.Mode == "capacity"
}

// IsDryRunEnabled dry-run 模式是否开启
func IsDryRunEnabled() bool {
	return channelRoutingSetting.DryRun
}

// GetEffectiveWindowSec 返回某渠道的有效窗口秒数（渠道独立 > 全局）
func GetEffectiveWindowSec(channelWindow int) int {
	if channelWindow > 0 {
		return channelWindow
	}
	if channelRoutingSetting.CapacityWindowSec > 0 {
		return channelRoutingSetting.CapacityWindowSec
	}
	return 60
}

// GetQueueMaxWaitMs 返回排队最大等待毫秒数（≤0 回落默认 30000）
func GetQueueMaxWaitMs() int {
	if channelRoutingSetting.QueueMaxWaitMs > 0 {
		return channelRoutingSetting.QueueMaxWaitMs
	}
	return 30000
}

// GetQueuePollIntervalMs 返回排队轮询间隔毫秒数（≤0 回落默认 500）
func GetQueuePollIntervalMs() int {
	if channelRoutingSetting.QueuePollIntervalMs > 0 {
		return channelRoutingSetting.QueuePollIntervalMs
	}
	return 500
}
