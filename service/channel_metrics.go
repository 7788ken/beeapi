// Package service - channel_metrics.go
// 渠道运行质量快照：每 15min 重算所有渠道的 quality_score（0-100）和 rpm_24h。
// 数据源：logs 表 24h 滚动窗口（type=2 成功 / type=5 错误）。
//
// 设计要点（docs/2026-05-12-channel-quality-rpm-list-plan.md）：
//   - SQL 端精确统计成功/失败数，响应时长使用最近样本，避免大表全量回表；
//   - JSON 字段（frt）和错误码（status_code）在 Go 端解析，避免 JSON_EXTRACT / REGEXP 方言；
//   - 流式/非流式渠道使用不同评分子公式，避免天花板不公平；
//   - 非重入 + 4min 超时；启动时立即跑一次，避免列表前 15min 空白。
package service

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
)

const (
	channelMetricsTickInterval      = 15 * time.Minute
	channelMetricsRunTimeout        = 4 * time.Minute
	channelMetricsServerAbortMargin = 5 * time.Second
	channelMetricsWindow            = int64(86400) // 24h
	channelMetricsSampleLimit       = 500          // 每 channel 每 type 取样上限（用于 frt + error code 解析）
	channelMetricsAggregateQuery    = `SELECT type, COUNT(*) AS count
			FROM logs
			WHERE channel_id = ? AND type IN (2, 5) AND created_at >= ?
			GROUP BY type`
	channelMetricsSampleQuery = `SELECT channel_id, type, content, other, use_time, is_stream
				FROM logs
				WHERE channel_id = ? AND type = ? AND created_at >= ?
				ORDER BY id DESC
				LIMIT ?`
)

var (
	channelMetricsOnce    sync.Once
	channelMetricsRunning atomic.Bool
)

// guardChannelMetricsQuery gives MySQL a server-side deadline slightly shorter
// than the Go context. Closing a client connection after context cancellation
// does not reliably stop an already-running MySQL SELECT, so without this hint
// an orphan query can keep consuming I/O after the task has failed.
func guardChannelMetricsQuery(ctx context.Context, dialect, query string) string {
	if dialect != "mysql" {
		return query
	}

	maxExecutionTime := channelMetricsRunTimeout - channelMetricsServerAbortMargin
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline) - channelMetricsServerAbortMargin
		if remaining < maxExecutionTime {
			maxExecutionTime = remaining
		}
	}
	if maxExecutionTime < time.Millisecond {
		maxExecutionTime = time.Millisecond
	}

	hint := fmt.Sprintf("SELECT /*+ MAX_EXECUTION_TIME(%d) */", maxExecutionTime.Milliseconds())
	return strings.Replace(query, "SELECT", hint, 1)
}

func buildChannelMetricsSampleQuery(dialect string) string {
	if dialect != common.DatabaseTypeClickHouse {
		return channelMetricsSampleQuery
	}
	return strings.Replace(
		channelMetricsSampleQuery,
		"ORDER BY id DESC",
		"ORDER BY created_at DESC, event_id DESC",
		1,
	)
}

// StartChannelMetricsTask 启动渠道质量评分后台任务。
// 启动时立刻跑一次（避免列表前 15min 空白），随后每 15min tick。
// 非 master 节点不参与，避免多 replica 重复 UPDATE。
func StartChannelMetricsTask() error {
	var startErr error
	channelMetricsOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		startErr = backgroundtask.Start("channel-metrics", func(ctx context.Context) {
			logger.LogInfo(context.Background(),
				fmt.Sprintf("channel metrics task started: tick=%s window=24h", channelMetricsTickInterval))
			backgroundtask.RunPeriodic(ctx, channelMetricsTickInterval, true, func() {
				runChannelMetricsOnce()
			})
		})
	})
	return startErr
}

// runChannelMetricsOnce 非重入封装：上次没跑完直接跳过本轮 tick。
func runChannelMetricsOnce() {
	if !channelMetricsRunning.CompareAndSwap(false, true) {
		logger.LogWarn(context.Background(), "channel metrics: previous run still inflight, skip")
		return
	}
	defer channelMetricsRunning.Store(false)

	ctx, cancel := context.WithTimeout(context.Background(), channelMetricsRunTimeout)
	defer cancel()

	start := time.Now()
	if err := RecomputeChannelMetricsOnce(ctx); err != nil {
		logger.LogError(ctx, fmt.Sprintf("channel metrics: failed: %v", err))
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf("channel metrics: done elapsed=%s", time.Since(start)))
}

// ──────────────────────────────────────────────────────────────────────────
// 聚合 & 重算
// ──────────────────────────────────────────────────────────────────────────

// channelAgg 单 channel 的计数与响应时长；计数来自 SQL，响应时长来自最近样本。
type channelAgg struct {
	ChannelId  int     `gorm:"column:channel_id"`
	SuccessCnt int64   `gorm:"column:success_cnt"`
	ErrorCnt   int64   `gorm:"column:error_cnt"`
	AvgUseTime float64 `gorm:"column:avg_use_time"`
}

// channelSample 单 channel 的样本（Go 端解析后）。
type channelSample struct {
	FrtMsSum      float64 // type=2 行里 other.frt 之和（仅 >0 部分）
	FrtMsCount    int     // 对应样本数
	UseTimeSum    float64 // type=2 行里 use_time 之和（最近样本）
	UseTimeCount  int     // 对应样本数
	StreamCount   int     // type=2 行里 is_stream=true 的数量
	NonStreamCnt  int     // type=2 行里 is_stream=false 的数量
	E429          int
	E503          int
	E504OrTimeout int
	E500          int
	E4xxOther     int
}

// RecomputeChannelMetricsOnce 重算所有渠道指标并写回 channels 表。
// 实现：
//  1. SQL 精确计数（每 channel 的 success_cnt / error_cnt）
//  2. SQL 取样原始 logs（每 channel 每 type 限量 500 行），Go 端计算响应时长并解析 JSON+正则
//  3. 计算 quality_score + rpm_24h，单条 UPDATE 写回 channels（按 id WHERE，避免锁全表）
func RecomputeChannelMetricsOnce(ctx context.Context) error {
	if model.DB == nil || model.LOG_DB == nil {
		return fmt.Errorf("database not initialized")
	}
	now := common.GetTimestamp()
	from := now - channelMetricsWindow

	// ── Step 1: 精确成功/失败计数（per-channel 循环）──
	//
	// 历史：原实现一条 GROUP BY channel_id 的 GROUP aggregate。MySQL 优化器在 2.7M+ 行 logs
	// 上倾向选 idx_logs_channel_id（单列），扫全索引 + filter 24h，cold buffer pool 下能跑 4min+
	// 超 ctx 死。
	//
	// 现在先从 channels 表拿所有 channel ID（小表，<200 行），逐个按 type COUNT。查询只读取
	// channel_id/type/created_at，MySQL 可走对应覆盖索引，避免为了 AVG(use_time) 对数百万行
	// 回聚簇表。响应时长和已有 FRT/错误分类一样，在 Step 2 最近样本中计算。
	var allChannelIDs []int
	if err := model.DB.WithContext(ctx).Raw(`SELECT id FROM channels`).Scan(&allChannelIDs).Error; err != nil {
		return fmt.Errorf("channels enumerate failed: %w", err)
	}
	type channelTypeCount struct {
		Type  int   `gorm:"column:type"`
		Count int64 `gorm:"column:count"`
	}
	aggs := make([]channelAgg, 0, len(allChannelIDs))
	for _, cid := range allChannelIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		var counts []channelTypeCount
		db := model.LOG_DB.WithContext(ctx)
		if err := db.Raw(
			guardChannelMetricsQuery(ctx, db.Dialector.Name(), channelMetricsAggregateQuery),
			cid, from).Scan(&counts).Error; err != nil {
			return fmt.Errorf("agg query failed for channel %d: %w", cid, err)
		}
		a := channelAgg{ChannelId: cid}
		for _, count := range counts {
			switch count.Type {
			case model.LogTypeConsume:
				a.SuccessCnt = count.Count
			case model.LogTypeError:
				a.ErrorCnt = count.Count
			}
		}
		// 跳过 24h 内无流量的 channel（quality_score 维持 NULL/历史值）
		if a.SuccessCnt+a.ErrorCnt == 0 {
			continue
		}
		aggs = append(aggs, a)
	}

	// ── Step 2: 取样并 Go 端解析 ──
	channelIDs := make([]int, 0, len(aggs))
	for _, a := range aggs {
		channelIDs = append(channelIDs, a.ChannelId)
	}
	samples, err := loadChannelSamples(ctx, channelIDs, from)
	if err != nil {
		return fmt.Errorf("sample query failed: %w", err)
	}

	// ── Step 3: 写回 ──
	// 注意：rpm_24h 字段不再写。渠道 RPM 已改为 Redis 实时滑动窗口（common.IncrChannelRPM +
	// controller.overlayRealtimeChannelRPM），DB 列保留仅向后兼容，无消费方。
	for _, a := range aggs {
		s := samples[a.ChannelId] // 缺失时使用零值结构（无样本）
		if s.UseTimeCount > 0 {
			a.AvgUseTime = s.UseTimeSum / float64(s.UseTimeCount)
		}
		score := computeQualityScore(a, s)

		updates := map[string]any{
			"quality_score":      score, // *int, NULL when no traffic
			"quality_updated_at": now,
			"quality_detail":     buildQualityDetail(a, s),
		}
		if err := model.DB.WithContext(ctx).Model(&model.Channel{}).
			Where("id = ?", a.ChannelId).
			Updates(updates).Error; err != nil {
			// 单条失败不中止，继续下一条
			logger.LogError(ctx, fmt.Sprintf("channel metrics: update channel %d failed: %v", a.ChannelId, err))
			continue
		}
	}
	return nil
}

// loadChannelSamples 取样原始 logs，按 (channel_id, type) 各取最近 N 行。
//
// 历史：原实现用 ROW_NUMBER() OVER (PARTITION BY channel_id, type ORDER BY id DESC) 一次性
// 取样，但 MySQL 8 window function **不走索引顺序**，强制物化全集 + filesort，prod 上 500k+
// 行的 logs 表超 4min ctx 死。
//
// 现在按 (channel_id, type) 逐个 SELECT ... ORDER BY id DESC LIMIT N，配合复合索引
// idx_logs_channel_type_id (channel_id, type, id) 走 covering index reverse scan，
// 每次只读 N 行（O(N)），118 桶 ≈ 数百 ms。
//
// 索引由 model.Log 的 GORM tag 声明，新装/重启时 AutoMigrate 自动创建；prod 旧库需手动
// ALTER TABLE logs ADD INDEX idx_logs_channel_type_id (channel_id, type, id) 一次。
func loadChannelSamples(ctx context.Context, channelIDs []int, from int64) (map[int]channelSample, error) {
	out := make(map[int]channelSample, len(channelIDs))
	if len(channelIDs) == 0 {
		return out, nil
	}
	type rawRow struct {
		ChannelId int    `gorm:"column:channel_id"`
		Type      int    `gorm:"column:type"`
		Content   string `gorm:"column:content"`
		Other     string `gorm:"column:other"`
		UseTime   int    `gorm:"column:use_time"`
		IsStream  bool   `gorm:"column:is_stream"`
	}
	for _, cid := range channelIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, typ := range []int{2, 5} {
			var rows []rawRow
			db := model.LOG_DB.WithContext(ctx)
			if err := db.Raw(
				guardChannelMetricsQuery(ctx, db.Dialector.Name(), buildChannelMetricsSampleQuery(db.Dialector.Name())),
				cid, typ, from, channelMetricsSampleLimit).Scan(&rows).Error; err != nil {
				return nil, err
			}
			for _, r := range rows {
				s := out[r.ChannelId]
				processSampleRow(&s, r.Type, r.Content, r.Other, r.UseTime, r.IsStream)
				out[r.ChannelId] = s
			}
		}
	}
	return out, nil
}

// processSampleRow 把单条 logs 行的 type/content/other/use_time/is_stream 累加进 channelSample。
// 抽出来给上面 loadChannelSamples 复用，避免 type=2/type=5 两个分支在 loop 里重复写。
func processSampleRow(s *channelSample, typ int, content, other string, useTime int, isStream bool) {
	switch typ {
	case 2:
		s.UseTimeSum += float64(useTime)
		s.UseTimeCount++
		if isStream {
			s.StreamCount++
		} else {
			s.NonStreamCnt++
		}
		if frt, ok := parseFrtFromOther(other); ok && frt > 0 {
			s.FrtMsSum += frt
			s.FrtMsCount++
		}
	case 5:
		code := parseStatusCode(content)
		switch {
		case code == 429:
			s.E429++
		case code == 503:
			s.E503++
		case code == 504:
			s.E504OrTimeout++
		case code == 0 && containsTimeout(content):
			s.E504OrTimeout++
		case code == 500:
			s.E500++
		case code >= 400 && code < 500:
			s.E4xxOther++
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────
// 评分公式 v2（流式/非流式公平）
// ──────────────────────────────────────────────────────────────────────────

// qualityDetail 评分参与计算的原始指标快照，随 quality_score 一起写回 channels.quality_detail，
// 供列表"质量"列 hover 展示。成功率/错误率由前端用 success_cnt/error_cnt 推导。
type qualityDetail struct {
	SuccessCnt   int64 `json:"success_cnt"`     // 24h 成功请求数（全量聚合）
	ErrorCnt     int64 `json:"error_cnt"`       // 24h 失败请求数（全量聚合）
	AvgUseTimeMs int64 `json:"avg_use_time_ms"` // 最近成功样本的平均响应时长（ms）
	AvgFrtMs     int64 `json:"avg_frt_ms"`      // 首包延迟平均值（ms，取样口径）；0=无流式样本
}

// buildQualityDetail 序列化指标快照；仅在 total>0 时被调用（与 computeQualityScore 同前提）。
func buildQualityDetail(agg channelAgg, s channelSample) string {
	d := qualityDetail{
		SuccessCnt:   agg.SuccessCnt,
		ErrorCnt:     agg.ErrorCnt,
		AvgUseTimeMs: int64(math.Round(agg.AvgUseTime * 1000)),
	}
	if s.FrtMsCount > 0 {
		d.AvgFrtMs = int64(math.Round(s.FrtMsSum / float64(s.FrtMsCount)))
	}
	b, err := common.Marshal(d)
	if err != nil {
		return ""
	}
	return string(b)
}

// computeQualityScore 综合评分 0-100；无流量返回 nil（DB 写 NULL）。
// 维度权重：成功率 40 / 性能 45（流式 25+20 / 非流式 45）/ 错误模式 15。
func computeQualityScore(agg channelAgg, s channelSample) *int {
	total := agg.SuccessCnt + agg.ErrorCnt
	if total == 0 {
		return nil
	}
	successRate := float64(agg.SuccessCnt) / float64(total)

	// 成功率 40 分
	p1 := 40.0 * successRate

	// 性能 45 分（流式拆 TTFT 25 + 总时长 20；非流式全部 45 给总时长，标准更严）
	var p2 float64
	streamCount := s.StreamCount + s.NonStreamCnt
	isStreamingChannel := streamCount > 0 && float64(s.StreamCount)/float64(streamCount) > 0.5
	avgFrt := 0.0
	if s.FrtMsCount > 0 {
		avgFrt = s.FrtMsSum / float64(s.FrtMsCount)
	}
	if isStreamingChannel && s.FrtMsCount > 0 {
		p2 = ttftScore(avgFrt) + useTimeScoreStream(agg.AvgUseTime)
	} else {
		// 非流式渠道或无 frt 数据，按总时长打分（45 max，标准更严）
		p2 = useTimeScoreStrict(agg.AvgUseTime)
	}

	// 错误模式 15 分（罚分）
	p3 := 15.0
	p3 -= math.Min(15, float64(s.E503+s.E504OrTimeout)*0.5)
	p3 -= math.Min(8, float64(s.E429)*0.3)
	p3 -= math.Min(6, float64(s.E500)*0.2)
	p3 -= math.Min(4, float64(s.E4xxOther)*0.05)
	if p3 < 0 {
		p3 = 0
	}

	score := int(math.Round(p1 + p2 + p3))
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return &score
}

// ttftScore 流式渠道 TTFT 子项，25 max。
func ttftScore(frtMs float64) float64 {
	switch {
	case frtMs < 1000:
		return 25
	case frtMs < 2000:
		return 22
	case frtMs < 3000:
		return 18
	case frtMs < 5000:
		return 12
	case frtMs < 10000:
		return 5
	default:
		return 0
	}
}

// useTimeScoreStream 流式渠道总时长子项，20 max。
func useTimeScoreStream(seconds float64) float64 {
	switch {
	case seconds < 5:
		return 20
	case seconds < 10:
		return 17
	case seconds < 20:
		return 13
	case seconds < 30:
		return 8
	case seconds < 60:
		return 3
	default:
		return 0
	}
}

// useTimeScoreStrict 非流式渠道总时长子项，45 max（标准更严）。
func useTimeScoreStrict(seconds float64) float64 {
	switch {
	case seconds < 1:
		return 45
	case seconds < 2:
		return 38
	case seconds < 5:
		return 30
	case seconds < 10:
		return 20
	case seconds < 30:
		return 10
	default:
		return 0
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────

var statusCodeRegex = regexp.MustCompile(`status_code=(\d+)`)

// parseStatusCode 从 content 提取 status_code 值（如 "status_code=503, upstream..." → 503）。
func parseStatusCode(content string) int {
	m := statusCodeRegex.FindStringSubmatch(content)
	if len(m) < 2 {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// containsTimeout 检测 content 是否包含 timeout 关键字（用于归类 504 同档）。
func containsTimeout(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded")
}

// parseFrtFromOther 从 logs.other JSON 字符串里读取 frt 字段（ms）。
// other 可能为空或非合法 JSON，遇到任何错误返回 ok=false。
func parseFrtFromOther(other string) (float64, bool) {
	if other == "" {
		return 0, false
	}
	var raw map[string]any
	if err := common.UnmarshalJsonStr(other, &raw); err != nil {
		return 0, false
	}
	v, ok := raw["frt"]
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}
