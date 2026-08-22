package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// group_uptime.go — 分组广场「可用率」mini 图的取数（GET /api/perf-metrics/groups）。
//
// 与渠道列表「可用性」列的关系：**同一份日志，不同的取样范围**。
//   - 渠道页（service/channel_uptime.go）：面向管理员，看单个渠道"真正"的可用性，
//     不论渠道当前是启用还是被自动禁用——自动禁用的渠道仍被定时测活，曲线要能反映它是否恢复。
//   - 分组广场（本文件）：面向用户，回答"这个分组的请求成功率如何"。归属规则是
//     **按样本发生时刻是否可被路由**，而不是按渠道当前启停状态回溯：
//       1. 真实流量（非测活日志）：按 abilities 全量 (分组, 渠道) 映射计入。真实请求只会被
//          路由到发生时刻启用的渠道，样本天然自选择——历史桶如实反映"当时在服务的渠道"，
//          不随渠道事后被禁用/启用而改写（渠道在窗口内启停变化时，各桶各说各话）。
//       2. 启用态测活（token_name=模型测试）：按当前 enabled 的映射计入，用来填补低流量分组
//          的空窗、并让"启用着却坏了"的渠道把分组压红。仍叠一层当前 enabled 过滤，是为了
//          兜住尚未打标的禁用期探活日志（部署后 24h 窗口内的旧日志 + 滚动部署期旧节点新写的）。
//          已知残留：这层过滤让翻转渠道的**启用态测活样本**仍随读取时刻进出历史桶——
//          渠道当前禁用时这批样本整体隐藏、翻回启用又出现；真实流量不受影响，量级
//          只有测活频率（每渠道每小时个位数条），远小于本次消灭的禁用期探活风暴。
//       3. 禁用期探活（token_name=模型测试-停用）：永不计入。已摘掉的渠道每分钟被恢复探活，
//          这些失败与用户可感知的供给无关；不排除的话，被探活反复禁停/启用的翻转渠道会在
//          自动启用的瞬间把禁用期攒下的失败整窗带回分组曲线，曲线随刷新时机忽红忽绿
//          （us1 2026-08-14 实测：单渠道一天翻转 128 次）。
//
// 归属边界：abilities 是"当前映射表"而非历史表。启停只翻 enabled 位、不删行，所以启停维度的
// 历史归属稳定；但渠道改分组会删旧行重建、删渠道会删行——这两种操作下历史样本会追溯改挂或失联。
//
// 为什么不直接按 logs.group 聚合：渠道测试构造的假请求带的是 root 用户(id=1)的分组，
// 而非被测渠道所属分组，直接按 group 聚合会把全部测试数据堆到 default 上。
// 因此改为按 channel_id 聚合，再经 abilities 的 (group, channel) 映射归属到各分组；
// 一个渠道服务多个分组时，其成功/失败在每个分组各计一次。


const (
	groupUptimeCacheTTL  = 5 * time.Minute
	groupUptimeBucketSec = int64(3600)
	GroupUptimeMinHours  = 1
	GroupUptimeMaxHours  = 168
)

// groupUptimeAllowedHours 是 hours 的白名单档位。本接口匿名可访问，每个未命中的
// hours 都要扫一次 logs（us1 24h 窗口实测约 1.9s，168h 窗口会放大到十余秒），
// 因此只开放前端实际使用的档位，避免遍历 1..168 绕过缓存打库。
var groupUptimeAllowedHours = []int{24}

// NormalizeGroupUptimeHours 把外部传入的 hours 收敛到白名单档位；非法或超范围回落 24。
func NormalizeGroupUptimeHours(raw string) int {
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 24
	}
	for _, allowed := range groupUptimeAllowedHours {
		if parsed == allowed {
			return allowed
		}
	}
	return 24
}

type GroupUptimePoint struct {
	Ts           int64   `json:"ts"`
	RequestCount int64   `json:"request_count"`
	SuccessCount int64   `json:"success_count"`
	SuccessRate  float64 `json:"success_rate"` // 0-100，两位小数
}

// GroupUptimeResult 除了各分组的桶序列，还带出「当前有启用渠道」的分组集合：
// 调用方要据此区分「有渠道但窗口内没请求」（留空、前端灰显）与「当前一个可用渠道都没有」
// （分组现在就是不可用，须显红 0%，见 ZeroUptimeSeries）。
type GroupUptimeResult struct {
	Series           map[string][]GroupUptimePoint
	GroupsWithSupply map[string]struct{}
}

type groupUptimeCacheEntry struct {
	data      GroupUptimeResult
	expiresAt time.Time
}

// groupUptimeFlight 保证同一 cacheKey 同时只有一次查库在跑；不同 key 之间互不阻塞。
type groupUptimeFlight struct {
	done chan struct{}
	data GroupUptimeResult
	err  error
}

var (
	groupUptimeMu      sync.Mutex
	groupUptimeCache   = make(map[string]groupUptimeCacheEntry)
	groupUptimeFlights = make(map[string]*groupUptimeFlight)
)

type groupBucketKey struct {
	group    string
	bucketTs int64
}

type groupBucketCounters struct {
	success int64
	total   int64
}

// GetGroupUptime 返回 map[分组]桶序列（按桶时间升序）；窗口内无日志的分组不出现在结果里。
// 结果按 hours 进程内缓存 5 分钟。查库在全局锁之外进行，同一 key 由单飞去重、
// 不同 key 可并行，避免一次慢查询把所有（含本可命中缓存的）请求一起卡住。
//
// 不接受时区入参：桶宽为整小时，而任何整小时偏移都不改变整小时桶的边界，
// 桶起点 ts 由前端按本地时区渲染即可。
func GetGroupUptime(ctx context.Context, hours int) (GroupUptimeResult, error) {
	if hours < GroupUptimeMinHours || hours > GroupUptimeMaxHours {
		return GroupUptimeResult{}, fmt.Errorf("invalid hours: must be between %d and %d", GroupUptimeMinHours, GroupUptimeMaxHours)
	}

	cacheKey := strconv.Itoa(hours)

	groupUptimeMu.Lock()
	now := time.Now()
	if entry, ok := groupUptimeCache[cacheKey]; ok && now.Before(entry.expiresAt) {
		groupUptimeMu.Unlock()
		return entry.data, nil
	}
	for k, entry := range groupUptimeCache {
		if !now.Before(entry.expiresAt) {
			delete(groupUptimeCache, k)
		}
	}
	if inflight, ok := groupUptimeFlights[cacheKey]; ok {
		groupUptimeMu.Unlock()
		select {
		case <-inflight.done:
			return inflight.data, inflight.err
		case <-ctx.Done():
			return GroupUptimeResult{}, ctx.Err()
		}
	}
	flight := &groupUptimeFlight{done: make(chan struct{})}
	groupUptimeFlights[cacheKey] = flight
	groupUptimeMu.Unlock()

	data, err := computeGroupUptime(ctx, hours, now)

	groupUptimeMu.Lock()
	delete(groupUptimeFlights, cacheKey)
	if err == nil {
		groupUptimeCache[cacheKey] = groupUptimeCacheEntry{
			data:      data,
			expiresAt: time.Now().Add(groupUptimeCacheTTL),
		}
	}
	groupUptimeMu.Unlock()

	flight.data, flight.err = data, err
	close(flight.done)
	return data, err
}

func computeGroupUptime(ctx context.Context, hours int, now time.Time) (GroupUptimeResult, error) {
	pairs, err := model.GetGroupChannelPairs(ctx)
	if err != nil {
		return GroupUptimeResult{}, fmt.Errorf("group channel mapping query failed: %w", err)
	}
	result := GroupUptimeResult{
		Series:           map[string][]GroupUptimePoint{},
		GroupsWithSupply: make(map[string]struct{}, len(pairs)),
	}
	if len(pairs) == 0 {
		return result, nil
	}
	// 同一 (分组, 渠道) 可能因 enabled 混排返回两行，按"任一行启用即启用"合并。
	type pairKey struct {
		group     string
		channelId int
	}
	pairEnabled := make(map[pairKey]bool, len(pairs))
	for _, p := range pairs {
		key := pairKey{group: p.Group, channelId: p.ChannelId}
		pairEnabled[key] = pairEnabled[key] || p.Enabled
	}
	// allGroups：真实流量的归属（全量映射，历史事实不随当前启停改写）；
	// enabledGroups：启用态测活的归属（只归当前可被路由到的分组），并据此判定「有供给」。
	allGroups := make(map[int][]string, len(pairEnabled))
	enabledGroups := make(map[int][]string, len(pairEnabled))
	for key, enabled := range pairEnabled {
		allGroups[key.channelId] = append(allGroups[key.channelId], key.group)
		if enabled {
			enabledGroups[key.channelId] = append(enabledGroups[key.channelId], key.group)
			result.GroupsWithSupply[key.group] = struct{}{}
		}
	}

	endTs := now.Unix()
	startTs := endTs - int64(hours)*3600
	buckets, err := model.GetChannelUptimeBuckets(ctx, startTs, endTs, groupUptimeBucketSec, 0)
	if err != nil {
		return GroupUptimeResult{}, fmt.Errorf("uptime bucket query failed: %w", err)
	}
	testBuckets, err := model.GetChannelTestUptimeBuckets(ctx, startTs, endTs, groupUptimeBucketSec)
	if err != nil {
		return GroupUptimeResult{}, fmt.Errorf("test uptime bucket query failed: %w", err)
	}

	type channelBucketKey struct {
		channelId int
		bucketTs  int64
	}
	type testCounters struct {
		success, errors             int64 // 全部测活（含禁用期探活），用于从总数里扣出真实流量
		onlineSuccess, onlineErrors int64 // 仅启用态测活，按 enabledGroups 计入
	}
	tests := make(map[channelBucketKey]testCounters, len(testBuckets))
	for _, b := range testBuckets {
		key := channelBucketKey{channelId: b.ChannelId, bucketTs: b.BucketStart}
		c := tests[key]
		c.success += b.SuccessCnt
		c.errors += b.ErrorCnt
		if b.TokenName == model.ChannelTestTokenName {
			c.onlineSuccess += b.SuccessCnt
			c.onlineErrors += b.ErrorCnt
		}
		tests[key] = c
	}

	merged := make(map[groupBucketKey]groupBucketCounters)
	add := func(groups []string, bucketTs, success, total int64) {
		for _, group := range groups {
			key := groupBucketKey{group: group, bucketTs: bucketTs}
			c := merged[key]
			c.success += success
			c.total += total
			merged[key] = c
		}
	}
	for _, b := range buckets {
		if b.SuccessCnt+b.ErrorCnt <= 0 {
			continue
		}
		t := tests[channelBucketKey{channelId: b.ChannelId, bucketTs: b.BucketStart}]
		// 真实流量 = 全部 − 测活。两条查询共用同一 endTs 上界但先后执行：落在主查询
		// 快照之后、测活查询之前且 created_at 恰为 endTs 的测活日志只会被后者看到，
		// 差值可为负——夹 0 是承重的，删掉会渲染出负数或 >100% 的成功率；
		// 影响只及最新未满桶，下次缓存刷新自愈。
		realSuccess := max(b.SuccessCnt-t.success, 0)
		realErrors := max(b.ErrorCnt-t.errors, 0)
		if realSuccess+realErrors > 0 {
			add(allGroups[b.ChannelId], b.BucketStart, realSuccess, realSuccess+realErrors)
		}
		if t.onlineSuccess+t.onlineErrors > 0 {
			add(enabledGroups[b.ChannelId], b.BucketStart, t.onlineSuccess, t.onlineSuccess+t.onlineErrors)
		}
	}

	data := result.Series
	for key, c := range merged {
		data[key.group] = append(data[key.group], GroupUptimePoint{
			Ts:           key.bucketTs,
			RequestCount: c.total,
			SuccessCount: c.success,
			SuccessRate:  math.Round(float64(c.success)/float64(c.total)*10000) / 100,
		})
	}
	for group := range data {
		points := data[group]
		sort.Slice(points, func(i, j int) bool { return points[i].Ts < points[j].Ts })
		data[group] = points
	}
	return result, nil
}

// ZeroUptimeSeries 生成一条覆盖整个窗口、每桶均为 0% 的序列，用于「该分组当前没有任何启用渠道」。
// 不能沿用「不返回该分组」的做法：前端对缺失序列显示灰色「无数据」，而分组没有可用渠道时
// 它现在就是不可用的，必须显红 0%——最该报警的时刻不能看起来只是没人用。
//
// 桶起点按 UTC 整点对齐，与 computeGroupUptime 的桶（以及前端补槽口径）一致。
func ZeroUptimeSeries(hours int, now time.Time) []GroupUptimePoint {
	endTs := now.Unix() - now.Unix()%groupUptimeBucketSec
	points := make([]GroupUptimePoint, 0, hours)
	for i := int64(hours) - 1; i >= 0; i-- {
		points = append(points, GroupUptimePoint{Ts: endTs - i*groupUptimeBucketSec})
	}
	return points
}

// FilterGroupUptimeSeries 把 GetGroupUptime 的计算结果收敛成对外响应：只保留 visibleGroups
// 里的分组；无供给（当前零启用渠道）的分组整窗画 0%；有供给但窗口内无日志的分组不返回
// （前端灰显"有渠道只是没人用"）。
//
// ⚠ 分支顺序是承重的：必须**先判供给、再取序列**。无供给分组的 Series 里可能仍带着
// 已禁用渠道的历史真实流量（全量映射的归属，见文件头第 1 条），先取序列会把
// "现在就不可用"的分组渲染成绿色高可用——最该报警的时刻看起来最健康。
func FilterGroupUptimeSeries(result GroupUptimeResult, visibleGroups []string, hours int, now time.Time) map[string][]GroupUptimePoint {
	filtered := make(map[string][]GroupUptimePoint, len(visibleGroups))
	for _, group := range visibleGroups {
		if _, hasSupply := result.GroupsWithSupply[group]; !hasSupply {
			filtered[group] = ZeroUptimeSeries(hours, now)
			continue
		}
		if points, ok := result.Series[group]; ok {
			filtered[group] = points
		}
	}
	return filtered
}
