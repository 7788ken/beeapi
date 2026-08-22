package constant

// 渠道路由模式常量
// docs/2026-05-26-channel-routing-mode-switchable.md
const (
	RoutingModeInherit       = 0 // 继承上一级（渠道→分组→全局），默认值
	RoutingModeProbabilistic = 1 // 强制概率（现行加权随机）
	RoutingModeCapacity      = 2 // 强制容量（水桶溢出）
)

// 全局路由模式（GlobalConfig 字符串值）
const (
	GlobalRoutingProbabilistic = "probabilistic"
	GlobalRoutingCapacity      = "capacity"
)

// 容量全满策略
const (
	CapacityFullStrategyFallback = "fallback" // 降到次优先级（推荐默认）
	CapacityFullStrategyReject   = "reject"   // 返回 429
	CapacityFullStrategyDegraded = "degraded" // 选用量比例最低的（最少超载）
	CapacityFullStrategyQueue    = "queue"    // 排队等待名额释放，超时返回 429
)

// Redis 故障兜底策略
const (
	CapacityFailOpen   = "fail_open"   // 降级 probabilistic（默认）
	CapacityFailClosed = "fail_closed" // 视为已满（严格控容量）
)

// RoutingModeName 返回路由模式的可读名称
func RoutingModeName(mode int) string {
	switch mode {
	case RoutingModeProbabilistic:
		return "probabilistic"
	case RoutingModeCapacity:
		return "capacity"
	default:
		return "inherit"
	}
}
