package common

// 渠道容量计数器存储层（pure Redis + 内存 fallback，无配置依赖）
//
// 与 common/rpm_realtime.go 同构：按 1 分钟桶 INCR + EXPIRE，
// 读取用加权法估算最近 N×60s 请求开始数（N = windowSec/60，向上取整为 60 倍数）：
//   weighted = sum(buckets[0..N-1]) + buckets[N] * ((60 - elapsed_in_cur) / 60)
//   即：N 个整桶 + 1 个边缘桶按 elapsed 加权
//
// 不读 operation_setting（避免 common ↔ operation_setting 循环），
// fail mode / windowSec / ttl 全部由调用方传参决定。
//
// 详见 docs/2026-05-26-channel-routing-mode-switchable.md §4

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

const (
	// CapacityBucketTTLDefault 默认 TTL（windowSec=60 的兜底），调用方应根据 windowSec 计算合适 TTL
	CapacityBucketTTLDefault = 130 * time.Second
	capacityRedisOpTTL       = 500 * time.Millisecond
	CapacityKeyChanBase      = "channel_cap:c:"
	// CapacityMinWindowSec 桶粒度 60s，window 至少 60s
	CapacityMinWindowSec = 60
	// CapacityMaxWindowSec 上限 1 小时（避免读路径 MGET 桶数无界）
	CapacityMaxWindowSec = 3600
)

// NormalizeCapacityWindowSec 将任意秒数规整为 60 的倍数（向上取整），并 clamp 到 [60, 3600]。
// UI 已用 step=60 限制，但后端必须兜底，防止历史持久化的非法值。
func NormalizeCapacityWindowSec(w int) int {
	if w <= CapacityMinWindowSec {
		return CapacityMinWindowSec
	}
	if w >= CapacityMaxWindowSec {
		return CapacityMaxWindowSec
	}
	return ((w + 59) / 60) * 60
}

// CapacityTTLForWindow 计算给定 window 对应的 Redis TTL：window + 70s buffer，最小 130s
func CapacityTTLForWindow(windowSec int) time.Duration {
	w := NormalizeCapacityWindowSec(windowSec)
	ttl := time.Duration(w+70) * time.Second
	if ttl < CapacityBucketTTLDefault {
		ttl = CapacityBucketTTLDefault
	}
	return ttl
}

// CapacityStoreFailMode 控制 Redis 故障时 read 函数的兜底行为
type CapacityStoreFailMode int

const (
	CapacityFailOpen   CapacityStoreFailMode = 0 // Redis 异常 → 返回 0（视为未满，业务不阻断）
	CapacityFailClosed CapacityStoreFailMode = 1 // Redis 异常 → 返回 MaxInt32（视为已满）
)

// ── 进程内 fallback 存储 ──
type capacityMemStore struct {
	mu      sync.Mutex
	buckets map[int]map[int64]int64 // channelID -> minute -> count
}

var memCapacity = &capacityMemStore{buckets: make(map[int]map[int64]int64)}

func currentCapacityMinute() int64 {
	return time.Now().Unix() / 60
}

func (m *capacityMemStore) incr(channelID int, minute int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.buckets[channelID]
	if !ok {
		bucket = make(map[int64]int64)
		m.buckets[channelID] = bucket
	}
	bucket[minute]++
	// 保留最多 CapacityMaxWindowSec/60 + 1 = 61 个桶，足够 1 小时窗口
	retain := int64(CapacityMaxWindowSec/60 + 1)
	for k := range bucket {
		if k < minute-retain {
			delete(bucket, k)
		}
	}
}

// memCapacityGetN 返回从 minute 起向前 n 个桶的计数（buckets[0]=cur, buckets[i]=cur-i）
func memCapacityGetN(channelID int, minute int64, n int) []int64 {
	memCapacity.mu.Lock()
	defer memCapacity.mu.Unlock()
	out := make([]int64, n)
	bucket, ok := memCapacity.buckets[channelID]
	if !ok {
		return out
	}
	for i := 0; i < n; i++ {
		out[i] = bucket[minute-int64(i)]
	}
	return out
}

func capacityParseRedisInt(s string, err error) int64 {
	if err != nil || s == "" {
		return 0
	}
	v, e := strconv.ParseInt(s, 10, 64)
	if e != nil {
		return 0
	}
	return v
}

// capacityWeightedMulti 多桶加权聚合：N 个整桶（buckets[0..N-1]）+ 1 个边缘桶（buckets[N]）按 elapsed 加权。
// buckets[0] 为当前桶（cur），buckets[i] 为 cur-i 桶。要求 len(buckets) >= N+1。
func capacityWeightedMulti(buckets []int64, windowSec int) float64 {
	w := NormalizeCapacityWindowSec(windowSec)
	n := w / 60 // 整桶个数
	elapsed := float64(time.Now().Unix() % 60)
	weightOldest := (60.0 - elapsed) / 60.0
	var sum float64
	for i := 0; i < n && i < len(buckets); i++ {
		sum += float64(buckets[i])
	}
	if n < len(buckets) {
		sum += float64(buckets[n]) * weightOldest
	}
	return sum
}

// bucketCount 返回 windowSec 对应需要读取的桶数（N+1）
func bucketCount(windowSec int) int {
	return NormalizeCapacityWindowSec(windowSec)/60 + 1
}

// IncrChannelCapacityStore 增加渠道容量计数（业务无关，pure 存储层）。
// ttl 由调用方根据 effective window 决定，None=0 时回落 CapacityBucketTTLDefault。
func IncrChannelCapacityStore(channelID int, ttl time.Duration) {
	if channelID <= 0 {
		return
	}
	if ttl <= 0 {
		ttl = CapacityBucketTTLDefault
	}
	defer func() {
		if r := recover(); r != nil {
			SysLog(fmt.Sprintf("IncrChannelCapacityStore panic: %v", r))
		}
	}()
	if RedisEnabled && RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), capacityRedisOpTTL)
		defer cancel()
		key := fmt.Sprintf("%s%d:%d", CapacityKeyChanBase, channelID, currentCapacityMinute())
		pipe := RDB.TxPipeline()
		pipe.Incr(ctx, key)
		pipe.Expire(ctx, key, ttl)
		_, _ = pipe.Exec(ctx)
		return
	}
	memCapacity.incr(channelID, currentCapacityMinute())
}

// GetChannelCapacityCountStore 查询单渠道当前窗口估算用量。windowSec 规整为 60 倍数后聚合 N+1 个桶。
func GetChannelCapacityCountStore(channelID int, windowSec int, failMode CapacityStoreFailMode) float64 {
	if channelID <= 0 {
		return 0
	}
	minute := currentCapacityMinute()
	nBuckets := bucketCount(windowSec)
	if RedisEnabled && RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), capacityRedisOpTTL)
		defer cancel()
		pipe := RDB.Pipeline()
		cmds := make([]*redis.StringCmd, nBuckets)
		for i := 0; i < nBuckets; i++ {
			cmds[i] = pipe.Get(ctx, fmt.Sprintf("%s%d:%d", CapacityKeyChanBase, channelID, minute-int64(i)))
		}
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			if failMode == CapacityFailClosed {
				return math.MaxInt32
			}
			return 0
		}
		buckets := make([]int64, nBuckets)
		for i := 0; i < nBuckets; i++ {
			buckets[i] = capacityParseRedisInt(cmds[i].Result())
		}
		return capacityWeightedMulti(buckets, windowSec)
	}
	buckets := memCapacityGetN(channelID, minute, nBuckets)
	return capacityWeightedMulti(buckets, windowSec)
}

// BatchGetChannelCapacityCountsStore MGET 批量查询，避免 N 次 RTT
func BatchGetChannelCapacityCountsStore(channelIDs []int, windowSec int, failMode CapacityStoreFailMode) map[int]float64 {
	result := make(map[int]float64, len(channelIDs))
	if len(channelIDs) == 0 {
		return result
	}
	minute := currentCapacityMinute()
	nBuckets := bucketCount(windowSec)
	if RedisEnabled && RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		pipe := RDB.Pipeline()
		cmds := make([][]*redis.StringCmd, len(channelIDs))
		for ci, id := range channelIDs {
			cmds[ci] = make([]*redis.StringCmd, nBuckets)
			for bi := 0; bi < nBuckets; bi++ {
				cmds[ci][bi] = pipe.Get(ctx, fmt.Sprintf("%s%d:%d", CapacityKeyChanBase, id, minute-int64(bi)))
			}
		}
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			failVal := 0.0
			if failMode == CapacityFailClosed {
				failVal = math.MaxInt32
			}
			for _, id := range channelIDs {
				result[id] = failVal
			}
			return result
		}
		for ci, id := range channelIDs {
			buckets := make([]int64, nBuckets)
			for bi := 0; bi < nBuckets; bi++ {
				buckets[bi] = capacityParseRedisInt(cmds[ci][bi].Result())
			}
			result[id] = capacityWeightedMulti(buckets, windowSec)
		}
		return result
	}
	for _, id := range channelIDs {
		buckets := memCapacityGetN(id, minute, nBuckets)
		result[id] = capacityWeightedMulti(buckets, windowSec)
	}
	return result
}

// BatchGetChannelCapacityCountsStorePerWindow 与 BatchGetChannelCapacityCountsStore 类似，
// 但每个渠道用各自的窗口聚合（channelWindows: channelID -> windowSec），支持 per-channel 变长窗口。
// 一次 pipeline MGET 取"最大窗口"所需桶数，再按各渠道窗口分别加权（聚合时只取自己需要的前 N+1 桶），
// 保留批量优化的同时让渠道级 capacity_window_sec 真正影响读路径。
func BatchGetChannelCapacityCountsStorePerWindow(channelWindows map[int]int, failMode CapacityStoreFailMode) map[int]float64 {
	result := make(map[int]float64, len(channelWindows))
	if len(channelWindows) == 0 {
		return result
	}
	// 最大窗口决定 MGET 桶数；各渠道聚合时按各自窗口只取前 N+1 桶
	maxBuckets := 0
	for _, w := range channelWindows {
		if b := bucketCount(w); b > maxBuckets {
			maxBuckets = b
		}
	}
	minute := currentCapacityMinute()
	if RedisEnabled && RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		pipe := RDB.Pipeline()
		cmds := make(map[int][]*redis.StringCmd, len(channelWindows))
		for id := range channelWindows {
			cs := make([]*redis.StringCmd, maxBuckets)
			for bi := 0; bi < maxBuckets; bi++ {
				cs[bi] = pipe.Get(ctx, fmt.Sprintf("%s%d:%d", CapacityKeyChanBase, id, minute-int64(bi)))
			}
			cmds[id] = cs
		}
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			failVal := 0.0
			if failMode == CapacityFailClosed {
				failVal = math.MaxInt32
			}
			for id := range channelWindows {
				result[id] = failVal
			}
			return result
		}
		for id, cs := range cmds {
			buckets := make([]int64, maxBuckets)
			for bi := 0; bi < maxBuckets; bi++ {
				buckets[bi] = capacityParseRedisInt(cs[bi].Result())
			}
			result[id] = capacityWeightedMulti(buckets, channelWindows[id])
		}
		return result
	}
	for id, w := range channelWindows {
		buckets := memCapacityGetN(id, minute, bucketCount(w))
		result[id] = capacityWeightedMulti(buckets, w)
	}
	return result
}
