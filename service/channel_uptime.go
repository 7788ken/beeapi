package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// channel_uptime.go — 渠道列表「可用性」条形图的批量取数（GET /api/channel/uptime）。
// 一次聚合出全部渠道近 N 小时的每小时成功/失败数，避免列表逐行请求单渠道质量历史。
//
// logs 表在生产可能非常大，且目标机器规格不高，因此结果按 (hours, tz) 进程内缓存 5 分钟；
// 缓存 miss 时持锁查库，并发请求排队复用同一次结果（互斥即单飞）。

const (
	channelUptimeCacheTTL  = 5 * time.Minute
	channelUptimeBucketSec = int64(3600)
	ChannelUptimeMinHours  = 1
	ChannelUptimeMaxHours  = 168
)

type ChannelUptimePoint struct {
	Ts           int64   `json:"ts"`
	SuccessCount int64   `json:"success_count"`
	ErrorCount   int64   `json:"error_count"`
	SuccessRate  float64 `json:"success_rate"` // 0-100，两位小数
}

type channelUptimeCacheEntry struct {
	data      map[string][]ChannelUptimePoint
	expiresAt time.Time
}

var (
	channelUptimeMu    sync.Mutex
	channelUptimeCache = make(map[string]channelUptimeCacheEntry)
)

// GetChannelUptime 返回 map[channelId]桶序列（按桶时间升序）；窗口内无日志的渠道不出现在结果里。
func GetChannelUptime(ctx context.Context, hours int, tzOffsetSec int64) (map[string][]ChannelUptimePoint, error) {
	if hours < ChannelUptimeMinHours || hours > ChannelUptimeMaxHours {
		return nil, fmt.Errorf("invalid hours: must be between %d and %d", ChannelUptimeMinHours, ChannelUptimeMaxHours)
	}

	cacheKey := fmt.Sprintf("%d:%d", hours, tzOffsetSec)

	channelUptimeMu.Lock()
	defer channelUptimeMu.Unlock()

	now := time.Now()
	if entry, ok := channelUptimeCache[cacheKey]; ok && now.Before(entry.expiresAt) {
		return entry.data, nil
	}
	for k, entry := range channelUptimeCache {
		if !now.Before(entry.expiresAt) {
			delete(channelUptimeCache, k)
		}
	}

	endTs := now.Unix()
	startTs := endTs - int64(hours)*3600
	buckets, err := model.GetChannelUptimeBuckets(ctx, startTs, endTs, channelUptimeBucketSec, tzOffsetSec)
	if err != nil {
		return nil, fmt.Errorf("uptime bucket query failed: %w", err)
	}

	data := make(map[string][]ChannelUptimePoint)
	for _, b := range buckets {
		total := b.SuccessCnt + b.ErrorCnt
		if total <= 0 {
			continue
		}
		key := strconv.Itoa(b.ChannelId)
		data[key] = append(data[key], ChannelUptimePoint{
			Ts:           b.BucketStart,
			SuccessCount: b.SuccessCnt,
			ErrorCount:   b.ErrorCnt,
			SuccessRate:  math.Round(float64(b.SuccessCnt)/float64(total)*10000) / 100,
		})
	}

	channelUptimeCache[cacheKey] = channelUptimeCacheEntry{
		data:      data,
		expiresAt: now.Add(channelUptimeCacheTTL),
	}
	return data, nil
}
