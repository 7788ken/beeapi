package model

// 渠道路由模式：容量过滤辅助函数（P2 核心改造）
//
// 入口由 GetRandomSatisfiedChannel → selectFromPriorityLayer 调用，
// 本文件只提供 helper：
//   - ResolveRoutingMode（导出）：per-channel 解析最终模式（渠道级 > 全局）
//   - hasAnyCapacityChannel：快速判定候选集是否含 capacity 模式渠道
//   - filterCapacityChannels：MGET 批量查 Redis 用量，返回过滤后存活渠道
//     + capacity 候选 + counts map（counts 供 degraded 复用，避免二次 RTT）
//   - pickLeastOverloadedChannel：degraded 策略选用量比例最低的 capacity 渠道
//   - resolveCapacityFailMode：把 setting 字符串映射到 common 层 enum
//   - weightedRandomFromChannels：复用原 GetRandomSatisfiedChannel 的
//     smoothing-factor 加权随机算法
//
// 详见 docs/2026-05-26-channel-routing-mode-switchable.md §5

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// 路由相关错误（用于上层判别）
var (
	// ErrAllChannelsCapacityFull 当前 priority 层 capacity 渠道全满，且无软桶可兜底
	//（硬档全 reject，或软桶 limit<=0 全禁用）→ 对外转 429 限流（与 queue 收尾一致）。
	ErrAllChannelsCapacityFull = errors.New("all channels at capacity (reject strategy)")
	// ErrCapacityNeedQueue queue 策略下所有优先级渠道均满，调用方应排队等待后重试选渠道
	ErrCapacityNeedQueue = errors.New("all channels at capacity (queue strategy, caller should wait and retry)")
)

// ResolveRoutingMode 解析某渠道实际生效的路由模式。
// 渠道级 routing_mode > 全局 mode。
// 返回值：constant.RoutingModeProbabilistic 或 constant.RoutingModeCapacity。
// 已导出供 middleware 复用，避免逻辑重复发散。
func ResolveRoutingMode(ch *Channel, globalMode string) int {
	switch ch.GetRoutingMode() {
	case constant.RoutingModeProbabilistic:
		return constant.RoutingModeProbabilistic
	case constant.RoutingModeCapacity:
		return constant.RoutingModeCapacity
	}
	// inherit → 看全局
	if globalMode == constant.GlobalRoutingCapacity {
		return constant.RoutingModeCapacity
	}
	return constant.RoutingModeProbabilistic
}

// ResolveFullStrategy 解析某渠道实际生效的全满策略（渠道级 > 全局）。
// 渠道级 capacity_full_strategy 为合法值时生效；空 / "inherit" / 非法值 → 回落全局 globalStrategy。
// 已导出供 channel_cache 单渠道与多渠道路径复用。
func ResolveFullStrategy(ch *Channel, globalStrategy string) string {
	switch ch.GetCapacityFullStrategy() {
	case constant.CapacityFullStrategyFallback,
		constant.CapacityFullStrategyReject,
		constant.CapacityFullStrategyDegraded,
		constant.CapacityFullStrategyQueue:
		return ch.GetCapacityFullStrategy()
	}
	// 空 / inherit / 非法 → 继承全局
	return globalStrategy
}

// hasAnyCapacityChannel 快速检查候选集是否含任何 capacity 模式渠道。
// 如果整组都是 probabilistic，主流程直接走老路径，0 开销。
func hasAnyCapacityChannel(channels []*Channel, globalMode string) bool {
	if globalMode == constant.GlobalRoutingCapacity {
		// 全局 capacity 且至少一个渠道不是强制 probabilistic
		for _, ch := range channels {
			if ch.GetRoutingMode() != constant.RoutingModeProbabilistic {
				return true
			}
		}
		return false
	}
	// 全局 probabilistic：只有渠道级显式 capacity 才生效
	for _, ch := range channels {
		if ch.GetRoutingMode() == constant.RoutingModeCapacity {
			return true
		}
	}
	return false
}

// filterCapacityChannels 对 capacity 模式渠道做容量过滤。
// 返回 (survivors, capChannels, counts)：
//   - survivors: 经过过滤后仍可用的全部渠道（含 probabilistic 渠道，它们无条件保留）
//   - capChannels: 本次涉及的 capacity 模式渠道（供 degraded 复用，避免再查 Redis）
//   - counts: capChannels 对应的 Redis 用量 map（供 degraded 复用）
//
// 使用 MGET 批量查 Redis，避免 N 次 RTT。
func filterCapacityChannels(channels []*Channel, globalMode string) ([]*Channel, []*Channel, map[int]float64) {
	// 拆 capacity / probabilistic
	capChans := make([]*Channel, 0, len(channels))
	probChans := make([]*Channel, 0, len(channels))
	for _, ch := range channels {
		if ResolveRoutingMode(ch, globalMode) == constant.RoutingModeCapacity {
			capChans = append(capChans, ch)
		} else {
			probChans = append(probChans, ch)
		}
	}

	if len(capChans) == 0 {
		return channels, nil, nil
	}

	// 读路径按 per-channel 有效窗口聚合（渠道级 capacity_window_sec > 全局，留空继承全局）。
	// 一次批量 MGET 取最大窗口桶数，各渠道按各自窗口加权——渠道级窗口由此真正影响限流计算。
	channelWindows := make(map[int]int, len(capChans))
	for _, ch := range capChans {
		channelWindows[ch.Id] = operation_setting.GetEffectiveWindowSec(ch.GetCapacityWindowSec())
	}
	counts := common.BatchGetChannelCapacityCountsStorePerWindow(channelWindows, resolveCapacityFailMode())

	survivors := make([]*Channel, 0, len(channels))
	// probabilistic 渠道无条件保留
	survivors = append(survivors, probChans...)
	// capacity 渠道按用量过滤
	for _, ch := range capChans {
		limit := ch.GetCapacityLimit() // NULL 时复用 weight
		if limit <= 0 {
			// weight=0 & limit=NULL → 永远视为已满（用户用 weight=0 表达禁用，capacity 模式继承此语义）
			continue
		}
		if counts[ch.Id] < float64(limit) {
			survivors = append(survivors, ch)
		}
	}
	return survivors, capChans, counts
}

// pickLeastOverloadedChannel 从已查过的 capacity 渠道中选用量比例最低的。
// degraded 策略：全满时挤一挤，挑 usage / limit 最小的。
// counts 由 filterCapacityChannels 已查回的 map 复用，不再二次访问 Redis。
// 全部渠道 limit<=0 时返回 nil（让 caller 推进 retry，不返回"被禁用"渠道）。
func pickLeastOverloadedChannel(capChans []*Channel, counts map[int]float64) *Channel {
	if len(capChans) == 0 {
		return nil
	}
	var best *Channel
	bestRatio := float64(-1)
	for _, ch := range capChans {
		limit := ch.GetCapacityLimit()
		if limit <= 0 {
			continue
		}
		ratio := counts[ch.Id] / float64(limit)
		if bestRatio < 0 || ratio < bestRatio {
			bestRatio = ratio
			best = ch
		}
	}
	return best
}

// resolveCapacityFailMode 把 setting 字符串转成 common 层 enum
func resolveCapacityFailMode() common.CapacityStoreFailMode {
	if operation_setting.GetChannelRoutingConfig().FailMode == "fail_closed" {
		return common.CapacityFailClosed
	}
	return common.CapacityFailOpen
}

// weightedRandomFromChannels 从候选集按 weight 加权随机选一个。
// 复用 GetRandomSatisfiedChannel 内现有逻辑（smoothing factor），
// 抽取为独立函数便于在 capacity 过滤后复用。
// 当 channels 为空时返回 nil + 错误。
func weightedRandomFromChannels(channels []*Channel, rnd func(int) int) (*Channel, error) {
	return weightedRandomBy(channels, rnd, func(ch *Channel) int { return ch.GetWeight() })
}

// weightedRandomByCapacity capacity 模式专用：按 capacity_limit（水桶大小）加权随机。
// capacity_limit NULL 时 GetCapacityLimit 自动回落 weight，行为与原路径兼容。
// 用于同优先级 survivors 间按桶容量比例分配请求数：大桶多得，小桶少得。
func weightedRandomByCapacity(channels []*Channel, rnd func(int) int) (*Channel, error) {
	return weightedRandomBy(channels, rnd, func(ch *Channel) int { return ch.GetCapacityLimit() })
}

// weightedRandomBy 通用加权随机，权重源由 weightOf 闭包决定。
// smoothing：所有权重 0 时退化为均匀分布；均值 <10 时放大 100 倍避免整数除法精度问题。
func weightedRandomBy(channels []*Channel, rnd func(int) int, weightOf func(*Channel) int) (*Channel, error) {
	if len(channels) == 0 {
		return nil, errors.New("no channel candidates")
	}
	sumWeight := 0
	for _, ch := range channels {
		w := weightOf(ch)
		if w < 0 {
			w = 0
		}
		sumWeight += w
	}
	smoothingFactor := 1
	smoothingAdjustment := 0
	if sumWeight == 0 {
		sumWeight = len(channels) * 100
		smoothingAdjustment = 100
	} else if sumWeight/len(channels) < 10 {
		smoothingFactor = 100
	}
	totalWeight := sumWeight * smoothingFactor
	randomWeight := rnd(totalWeight)
	for _, ch := range channels {
		w := weightOf(ch)
		if w < 0 {
			w = 0
		}
		randomWeight -= w*smoothingFactor + smoothingAdjustment
		if randomWeight < 0 {
			return ch, nil
		}
	}
	// 数值边界（理论上不该到达，但有 smoothing 算术误差防御）
	return channels[len(channels)-1], nil
}
