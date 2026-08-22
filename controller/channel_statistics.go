package controller

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

type ChannelStatsItem struct {
	ChannelId   int      `json:"channel_id"`
	ChannelName string   `json:"channel_name"`
	Group       string   `json:"group"`
	Status      int      `json:"status"`
	Models      []string `json:"models"`
	Quota       int64    `json:"quota"`
	CallCount   int64    `json:"call_count"`
	CurrentRpm  float64  `json:"current_rpm"`
	PeakRpm     float64  `json:"peak_rpm"`
	PeakRpmAt   int64    `json:"peak_rpm_at"`
}

type ChannelStatsGroup struct {
	ModelType  string             `json:"model_type"`
	TotalQuota int64              `json:"total_quota"`
	TotalCalls int64              `json:"total_calls"`
	Channels   []ChannelStatsItem `json:"channels"`
}

type ChannelStatsResponse struct {
	Groups        []ChannelStatsGroup `json:"groups"`
	GeneratedAt   int64               `json:"generated_at"`
	WindowSeconds int64               `json:"window_seconds"`
	SortBy        string              `json:"sort_by"`
	TopN          int                 `json:"top_n"`
}

type channelStatsGroupBuilder struct {
	group ChannelStatsGroup
	items map[int]*ChannelStatsItem
	seen  map[int]map[string]bool
}

func GetChannelStatistics(c *gin.Context) {
	// 上限 24h：prod 9M+ logs / 139 渠道下，超过 24h 的窗口单查询即可达分钟级，前端只允许 1m/今天/24h。
	rangeSec, ok := parseInt64RangeQuery(c, "range_seconds", 86400, 60, 86400)
	if !ok {
		return
	}
	topN, ok := parseIntRangeQuery(c, "top_n", 10, 1, 100)
	if !ok {
		return
	}
	sortBy := strings.TrimSpace(c.DefaultQuery("sort_by", "quota"))
	if !validChannelStatsSortBy(sortBy) {
		common.ApiErrorMsg(c, "invalid sort_by, must be quota, call_count, current_rpm or peak_rpm")
		return
	}
	filterType := strings.TrimSpace(c.Query("model_type"))

	ctx := c.Request.Context()
	now := common.GetTimestamp()
	rows, err := model.GetChannelStatsRows(ctx, now-rangeSec, now)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channels, err := model.GetAllChannelsLite(ctx)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	channelById := make(map[int]model.ChannelLite, len(channels))
	channelIds := make([]int, 0, len(channels))
	for _, ch := range channels {
		channelById[ch.Id] = ch
		channelIds = append(channelIds, ch.Id)
	}
	rpmMap := common.BatchGetChannelRPMs(channelIds)

	builders := make(map[string]*channelStatsGroupBuilder)
	for _, row := range rows {
		// 渠道在 GetChannelStatsRows / GetAllChannelsLite 两次查询之间被删除时，
		// 会出现 logs 行存在但 channels 表已无此记录的"幽灵渠道"。直接跳过，
		// 避免渲染为 name="" / status=0 被前端误标为"已禁用"。
		ch, exists := channelById[row.ChannelId]
		if !exists {
			continue
		}
		modelType := common.ClassifyModelType(row.ModelName)
		if filterType != "" && modelType != filterType {
			continue
		}
		builder := builders[modelType]
		if builder == nil {
			builder = &channelStatsGroupBuilder{
				group: ChannelStatsGroup{ModelType: modelType},
				items: make(map[int]*ChannelStatsItem),
				seen:  make(map[int]map[string]bool),
			}
			builders[modelType] = builder
		}

		item := builder.items[row.ChannelId]
		if item == nil {
			item = &ChannelStatsItem{
				ChannelId:   row.ChannelId,
				ChannelName: ch.Name,
				Group:       ch.Group,
				Status:      ch.Status,
				Models:      []string{}, // 显式空切片，避免无 model_name 行时 JSON null 让前端 .length 崩溃
				CurrentRpm:  rpmMap[row.ChannelId],
				PeakRpm:     ch.PeakRpm,
				PeakRpmAt:   ch.PeakRpmAt,
			}
			builder.items[row.ChannelId] = item
			builder.seen[row.ChannelId] = make(map[string]bool)
		}
		if row.ModelName != "" && !builder.seen[row.ChannelId][row.ModelName] {
			builder.seen[row.ChannelId][row.ModelName] = true
			item.Models = append(item.Models, row.ModelName)
		}
		item.Quota += row.Quota
		item.CallCount += row.CallCount
	}

	groups := make([]ChannelStatsGroup, 0, len(builders))
	for _, builder := range builders {
		for _, item := range builder.items {
			sort.Strings(item.Models)
			builder.group.Channels = append(builder.group.Channels, *item)
		}
		sortChannelItems(builder.group.Channels, sortBy)
		if len(builder.group.Channels) > topN {
			builder.group.Channels = builder.group.Channels[:topN]
		}
		// 总计从可见 Top-N 重算，保证 header 数字 = 卡片累加值，避免 UI 错觉。
		for _, item := range builder.group.Channels {
			builder.group.TotalQuota += item.Quota
			builder.group.TotalCalls += item.CallCount
		}
		groups = append(groups, builder.group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].TotalQuota == groups[j].TotalQuota {
			return groups[i].ModelType < groups[j].ModelType
		}
		return groups[i].TotalQuota > groups[j].TotalQuota
	})

	common.ApiSuccess(c, ChannelStatsResponse{
		Groups:        groups,
		GeneratedAt:   now,
		WindowSeconds: rangeSec,
		SortBy:        sortBy,
		TopN:          topN,
	})
}

type ChannelTrendPoint struct {
	BucketStart int64 `json:"bucket_start"`
	Quota       int64 `json:"quota"`
	CallCount   int64 `json:"call_count"`
}

type ChannelTrendSeries struct {
	ChannelId   int                 `json:"channel_id"`
	ChannelName string              `json:"channel_name"`
	Points      []ChannelTrendPoint `json:"points"`
}

type ChannelTrendResponse struct {
	Series        []ChannelTrendSeries `json:"series"`
	BucketSeconds int                  `json:"bucket_seconds"`
	StartTs       int64                `json:"start_ts"`
	EndTs         int64                `json:"end_ts"`
}

func GetChannelStatisticsTrend(c *gin.Context) {
	channelIds, ok := parseChannelIdsQuery(c, c.Query("channel_ids"))
	if !ok {
		return
	}
	rangeSec, ok := parseInt64RangeQuery(c, "range_seconds", 86400, 60, 86400)
	if !ok {
		return
	}
	bucketSec := autoChannelStatsBucket(rangeSec)
	if raw := strings.TrimSpace(c.Query("bucket_seconds")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 60 {
			common.ApiErrorMsg(c, "invalid bucket_seconds, must be an integer >= 60")
			return
		}
		bucketSec = v
	}
	// 桶数量上限：防御 range=90d + bucket=60 这类把 LOG_DB 拖垮的请求（理论桶数 ~3.9M）。
	if int64(bucketSec) > 0 && rangeSec/int64(bucketSec) > 2000 {
		common.ApiErrorMsg(c, "bucket_seconds too small for range_seconds (max 2000 buckets per channel)")
		return
	}
	// 时区偏移（秒，UTC = 0），用于把 SQL 桶对齐到调用方本地时间的午夜 / 整点。
	// 默认 0 保持向后兼容。前端按 -new Date().getTimezoneOffset()*60 传入。
	tzOffsetSec, _ := strconv.ParseInt(strings.TrimSpace(c.DefaultQuery("tz_offset_sec", "0")), 10, 64)
	if tzOffsetSec < -14*3600 || tzOffsetSec > 14*3600 {
		tzOffsetSec = 0
	}

	ctx := c.Request.Context()
	endTs := common.GetTimestamp()
	startTs := endTs - rangeSec
	rows, err := model.GetChannelTrend(ctx, channelIds, startTs, endTs, bucketSec, tzOffsetSec)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	names, err := model.GetChannelNamesByIds(ctx, channelIds)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	seriesById := make(map[int]*ChannelTrendSeries, len(channelIds))
	series := make([]ChannelTrendSeries, 0, len(channelIds))
	for _, id := range channelIds {
		series = append(series, ChannelTrendSeries{
			ChannelId:   id,
			ChannelName: names[id],
			Points:      []ChannelTrendPoint{},
		})
		seriesById[id] = &series[len(series)-1]
	}
	for _, row := range rows {
		if s := seriesById[row.ChannelId]; s != nil {
			s.Points = append(s.Points, ChannelTrendPoint{
				BucketStart: row.BucketStart,
				Quota:       row.Quota,
				CallCount:   row.CallCount,
			})
		}
	}

	common.ApiSuccess(c, ChannelTrendResponse{
		Series:        series,
		BucketSeconds: bucketSec,
		StartTs:       startTs,
		EndTs:         endTs,
	})
}

type ChannelTopUserItem struct {
	UserId    int     `json:"user_id"`
	Username  string  `json:"username"`
	Quota     int64   `json:"quota"`
	CallCount int64   `json:"call_count"`
	Rpm       float64 `json:"rpm"`
	Tokens    int64   `json:"tokens"`
	LastSeen  int64   `json:"last_seen"`
}

type ChannelTopUsersResponse struct {
	ChannelId     int                  `json:"channel_id"`
	ChannelName   string               `json:"channel_name"`
	WindowSeconds int64                `json:"window_seconds"`
	SortBy        string               `json:"sort_by"`
	GeneratedAt   int64                `json:"generated_at"`
	Users         []ChannelTopUserItem `json:"users"`
}

// GetChannelTopUsers 渠道大盘卡片展开：单渠道窗口内 Top-N 用户消费/RPM 排行。
// rpm = call_count / (窗口分钟数)，即窗口平均 RPM，非实时 60s RPM（见方案 §3.2）。
func GetChannelTopUsers(c *gin.Context) {
	channelId, err := strconv.Atoi(strings.TrimSpace(c.Query("channel_id")))
	if err != nil || channelId <= 0 {
		common.ApiErrorMsg(c, "invalid channel_id, must be a positive integer")
		return
	}
	// 上限 24h 与 /statistics 一致（prod 实测安全线）。
	rangeSec, ok := parseInt64RangeQuery(c, "range_seconds", 86400, 60, 86400)
	if !ok {
		return
	}
	limit, ok := parseIntRangeQuery(c, "limit", 50, 1, 100)
	if !ok {
		return
	}
	sortBy := strings.TrimSpace(c.DefaultQuery("sort_by", "quota"))
	if sortBy != "quota" && sortBy != "rpm" {
		common.ApiErrorMsg(c, "invalid sort_by, must be quota or rpm")
		return
	}

	ctx := c.Request.Context()
	now := common.GetTimestamp()
	rows, err := model.GetChannelTopUsers(ctx, channelId, now-rangeSec, now, sortBy, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 渠道已删除时 map 无此 id → 空 name（前端显示 #id），不 404：
	// 已删渠道的历史日志仍有排查价值。
	names, err := model.GetChannelNamesByIds(ctx, []int{channelId})
	if err != nil {
		common.ApiError(c, err)
		return
	}

	minutes := float64(rangeSec) / 60
	// 显式空切片，避免无数据时 JSON null（与 Models: []string{} 同教训）。
	users := make([]ChannelTopUserItem, 0, len(rows))
	for _, row := range rows {
		users = append(users, ChannelTopUserItem{
			UserId:    row.UserId,
			Username:  row.Username,
			Quota:     row.Quota,
			CallCount: row.CallCount,
			Rpm:       math.Round(float64(row.CallCount)/minutes*100) / 100,
			Tokens:    row.Tokens,
			LastSeen:  row.LastSeen,
		})
	}

	common.ApiSuccess(c, ChannelTopUsersResponse{
		ChannelId:     channelId,
		ChannelName:   names[channelId],
		WindowSeconds: rangeSec,
		SortBy:        sortBy,
		GeneratedAt:   now,
		Users:         users,
	})
}

func parseIntRangeQuery(c *gin.Context, key string, def, min, max int) (int, bool) {
	v, ok := parseInt64RangeQuery(c, key, int64(def), int64(min), int64(max))
	return int(v), ok
}

func parseInt64RangeQuery(c *gin.Context, key string, def, min, max int64) (int64, bool) {
	raw := strings.TrimSpace(c.DefaultQuery(key, strconv.FormatInt(def, 10)))
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < min || v > max {
		common.ApiErrorMsg(c, fmt.Sprintf("invalid %s, must be between %d and %d", key, min, max))
		return 0, false
	}
	return v, true
}

func validChannelStatsSortBy(sortBy string) bool {
	switch sortBy {
	case "quota", "call_count", "current_rpm", "peak_rpm":
		return true
	default:
		return false
	}
}

func sortChannelItems(items []ChannelStatsItem, sortBy string) {
	sort.Slice(items, func(i, j int) bool {
		switch sortBy {
		case "call_count":
			if items[i].CallCount != items[j].CallCount {
				return items[i].CallCount > items[j].CallCount
			}
		case "current_rpm":
			if items[i].CurrentRpm != items[j].CurrentRpm {
				return items[i].CurrentRpm > items[j].CurrentRpm
			}
		case "peak_rpm":
			if items[i].PeakRpm != items[j].PeakRpm {
				return items[i].PeakRpm > items[j].PeakRpm
			}
		default:
			if items[i].Quota != items[j].Quota {
				return items[i].Quota > items[j].Quota
			}
		}
		return items[i].ChannelId < items[j].ChannelId
	})
}

func parseChannelIdsQuery(c *gin.Context, raw string) ([]int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		common.ApiErrorMsg(c, "channel_ids is required")
		return nil, false
	}
	parts := strings.Split(raw, ",")
	ids := make([]int, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, part := range parts {
		id, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || id <= 0 {
			common.ApiErrorMsg(c, "invalid channel_ids, must be comma-separated positive integers")
			return nil, false
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 || len(ids) > 30 {
		common.ApiErrorMsg(c, "invalid channel_ids, must contain 1 to 30 ids")
		return nil, false
	}
	return ids, true
}

func autoChannelStatsBucket(rangeSec int64) int {
	switch {
	case rangeSec <= 3600:
		return 60
	case rangeSec <= 6*3600:
		return 300
	case rangeSec <= 86400:
		return 900
	case rangeSec <= 7*86400:
		return 3600
	default:
		return 86400
	}
}
