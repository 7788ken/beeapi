package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 模型级摘除「连续 N 次才摘」门槛的计数键与封装。
//
// 复用 channel_health 的 incrWithTTL / delKey（Redis 优先、不可用时降级到进程内 sync.Map），
// 使计数在多节点共享 Redis 时按 (渠道, 模型) 跨节点聚合——us1 四节点分摊流量，任何单节点的
// 进程内计数都只看到一部分请求，必须走共享 Redis 才能得到真实的「连续失败次数」。
//
// 语义：只有推断路径（限流 429 / 权限 403·400）用它——这两条路径摘除依据是推断而非证据，
// 单条错误常常只是上游账号池里某个账号被限流/无权、池子随即 failover 的瞬时信号。缺失路径
// 有 /v1/models 存在性核实作硬证据，不走此门槛。

func keyModelRemovalStreak(channelId int, modelName string) string {
	return fmt.Sprintf("channel:model_removal_streak:%d:%s", channelId, modelName)
}

// IncrModelRemovalStreak 累加并返回 (渠道,模型) 的连续失败计数。ttl 是滑动窗口：每次失败刷新
// 过期时间，只有相邻两次失败间隔超过 ttl 才让计数自然过期归零（防僵尸计数）。
func IncrModelRemovalStreak(channelId int, modelName string, ttl time.Duration) int64 {
	if channelId <= 0 || modelName == "" {
		return 0
	}
	return incrWithTTL(keyModelRemovalStreak(channelId, modelName), ttl)
}

// ResetModelRemovalStreak 成功请求时清零——这是「连续」语义的关键：中途任一成功即打断连击，
// 只有持续失败才会累积到摘除门槛。
func ResetModelRemovalStreak(channelId int, modelName string) {
	if channelId <= 0 || modelName == "" {
		return
	}
	delKey(keyModelRemovalStreak(channelId, modelName))
}

// ClearModelRemovalStreaks 清除某渠道下**所有模型**的连击计数。渠道恢复 / 手动启用 / 按标签
// 启用时由 ClearChannelHealthRuntime 统一调用，避免 TTL 窗口内的历史 (渠道,模型) 计数残留导致
// “刚启用又被摘”（与清 err_streak 防“启用即死”同理）。键按 (渠道,模型) 分布，需按前缀 SCAN 清除；
// Redis 不可用时同步清进程内降级表，否则本机残留计数同样会误触发。
func ClearModelRemovalStreaks(channelId int) {
	if channelId <= 0 {
		return
	}
	// 用空模型名复用同一 key 格式得到前缀 "channel:model_removal_streak:<id>:"，避免与 keyModelRemovalStreak 漂移。
	prefix := keyModelRemovalStreak(channelId, "")
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		var keys []string
		iter := common.RDB.Scan(ctx, 0, prefix+"*", 100).Iterator()
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
		}
		if err := iter.Err(); err != nil {
			common.SysLog(fmt.Sprintf("model_removal_streak: scan failed (channel_id=%d): %v", channelId, err))
		}
		if len(keys) > 0 {
			_ = common.RDB.Del(ctx, keys...).Err()
		}
	}
	fallbackStreaks.Range(func(k, _ any) bool {
		if s, ok := k.(string); ok && strings.HasPrefix(s, prefix) {
			fallbackStreaks.Delete(s)
		}
		return true
	})
}
