package common

// 实时 RPM 滑动窗口（最近 ~60 秒）
//
// 设计：按 1 分钟切桶，key=rpm_min:{u|c}:{id}:{minute}，每次请求 INCR + EXPIRE TTL=130s。
// 读取使用 Stripe/Cloudflare 经典加权法估算最近 60s 请求数：
//   weighted = cur_bucket + prev_bucket * ((60 - elapsed_in_cur) / 60)
//
// Redis 不可用时降级到进程内 sync.Map（仅单实例可见，仍优于崩溃）。

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
	rpmBucketTTL   = 130 * time.Second
	rpmRedisOpTTL  = 500 * time.Millisecond
	rpmKeyUserBase = "rpm_min:u:"
	rpmKeyChanBase = "rpm_min:c:"
)

func currentMinute() int64 {
	return time.Now().Unix() / 60
}

type rpmMemStore struct {
	mu      sync.Mutex
	buckets map[string]map[int64]int64 // type:id -> minute -> count
}

var memRPM = &rpmMemStore{buckets: make(map[string]map[int64]int64)}

func (m *rpmMemStore) incr(key string, minute int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.buckets[key]
	if !ok {
		bucket = make(map[int64]int64)
		m.buckets[key] = bucket
	}
	bucket[minute]++
	for k := range bucket {
		if k < minute-2 {
			delete(bucket, k)
		}
	}
}

func (m *rpmMemStore) get(key string, minute int64) (int64, int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.buckets[key]
	if !ok {
		return 0, 0
	}
	return bucket[minute], bucket[minute-1]
}

func parseRedisInt(s string, err error) int64 {
	if err != nil {
		return 0
	}
	if s == "" {
		return 0
	}
	v, e := strconv.ParseInt(s, 10, 64)
	if e != nil {
		return 0
	}
	return v
}

func weightedRPM(cur, prev int64) float64 {
	elapsed := float64(time.Now().Unix() % 60)
	weightPrev := (60.0 - elapsed) / 60.0
	return float64(cur) + float64(prev)*weightPrev
}

func roundOne(v float64) float64 {
	return math.Round(v*10) / 10
}

func incrBucketRedis(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), rpmRedisOpTTL)
	defer cancel()
	pipe := RDB.TxPipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, rpmBucketTTL)
	_, _ = pipe.Exec(ctx)
}

func IncrUserRPM(userID int) {
	if userID <= 0 {
		return
	}
	minute := currentMinute()
	if RedisEnabled && RDB != nil {
		incrBucketRedis(fmt.Sprintf("%s%d:%d", rpmKeyUserBase, userID, minute))
		return
	}
	memRPM.incr(fmt.Sprintf("u:%d", userID), minute)
}

func IncrChannelRPM(channelID int) {
	if channelID <= 0 {
		return
	}
	minute := currentMinute()
	if RedisEnabled && RDB != nil {
		incrBucketRedis(fmt.Sprintf("%s%d:%d", rpmKeyChanBase, channelID, minute))
		return
	}
	memRPM.incr(fmt.Sprintf("c:%d", channelID), minute)
}

func batchGetRPMs(prefix string, ids []int) map[int]float64 {
	result := make(map[int]float64, len(ids))
	if len(ids) == 0 {
		return result
	}
	minute := currentMinute()
	if RedisEnabled && RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		pipe := RDB.Pipeline()
		curCmds := make([]*redis.StringCmd, len(ids))
		prevCmds := make([]*redis.StringCmd, len(ids))
		for i, id := range ids {
			curCmds[i] = pipe.Get(ctx, fmt.Sprintf("%s%d:%d", prefix, id, minute))
			prevCmds[i] = pipe.Get(ctx, fmt.Sprintf("%s%d:%d", prefix, id, minute-1))
		}
		_, _ = pipe.Exec(ctx)
		for i, id := range ids {
			cur := parseRedisInt(curCmds[i].Result())
			prev := parseRedisInt(prevCmds[i].Result())
			result[id] = roundOne(weightedRPM(cur, prev))
		}
		return result
	}
	memPrefix := "u:"
	if prefix == rpmKeyChanBase {
		memPrefix = "c:"
	}
	for _, id := range ids {
		cur, prev := memRPM.get(fmt.Sprintf("%s%d", memPrefix, id), minute)
		result[id] = roundOne(weightedRPM(cur, prev))
	}
	return result
}

func BatchGetUserRPMs(userIDs []int) map[int]float64 {
	return batchGetRPMs(rpmKeyUserBase, userIDs)
}

func BatchGetChannelRPMs(channelIDs []int) map[int]float64 {
	return batchGetRPMs(rpmKeyChanBase, channelIDs)
}
