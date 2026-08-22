package service

// 渠道容量计数器（business 层 wrapper）
//
// 真正的 Redis + memory fallback 实现在 common/channel_capacity_store.go，
// 本文件负责读 operation_setting.GetChannelRoutingConfig() 解析 fail mode，
// 转发到 common 层。
//
// 拆分原因：model/channel_cache.go 选渠道时需要查容量计数，但 service
// 已经依赖 model，model→service 是循环依赖。把 pure 存储下沉到 common
// 后，model 可以直接调 common，service 仅在 middleware 钩子继续用。

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// IncrChannelCapacity 选中渠道时调用，转发到 common 存储层。
// windowSec 是 per-channel effective window；TTL 必须同时覆盖全局窗口，
// 否则 channel.window < global.window 时桶会过早 EXPIRE，read 路径回看时计数低估。
func IncrChannelCapacity(channelID int, windowSec int) {
	globalWindow := operation_setting.GetChannelRoutingConfig().CapacityWindowSec
	if globalWindow > windowSec {
		windowSec = globalWindow
	}
	common.IncrChannelCapacityStore(channelID, common.CapacityTTLForWindow(windowSec))
}

// GetChannelCapacityCount 返回某渠道当前窗口估算请求数。
// 读 operation_setting 的 fail_mode 决定 Redis 故障时的兜底行为。
func GetChannelCapacityCount(channelID int, windowSec int) float64 {
	return common.GetChannelCapacityCountStore(channelID, windowSec, resolveFailMode())
}

// BatchGetChannelCapacityCounts MGET 批量查询。
func BatchGetChannelCapacityCounts(channelIDs []int, windowSec int) map[int]float64 {
	return common.BatchGetChannelCapacityCountsStore(channelIDs, windowSec, resolveFailMode())
}

func resolveFailMode() common.CapacityStoreFailMode {
	if operation_setting.GetChannelRoutingConfig().FailMode == "fail_closed" {
		return common.CapacityFailClosed
	}
	return common.CapacityFailOpen
}
