package controller

import (
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

type ChannelReconcileModelItem struct {
	ModelName    string `json:"model_name"`
	Quota        int64  `json:"quota"`
	SuccessCount int64  `json:"success_count"`
	ErrorCount   int64  `json:"error_count"`
	TimeoutCount int64  `json:"timeout_count"`
}

type ChannelReconcileItem struct {
	ChannelId    int                         `json:"channel_id"`
	ChannelName  string                      `json:"channel_name"`
	Status       int                         `json:"status"`
	Quota        int64                       `json:"quota"`
	SuccessCount int64                       `json:"success_count"`
	ErrorCount   int64                       `json:"error_count"`
	TimeoutCount int64                       `json:"timeout_count"`
	Models       []ChannelReconcileModelItem `json:"models"`
}

type ChannelReconcileTotal struct {
	Quota        int64 `json:"quota"`
	SuccessCount int64 `json:"success_count"`
	ErrorCount   int64 `json:"error_count"`
	TimeoutCount int64 `json:"timeout_count"`
}

type ChannelReconcileResponse struct {
	Channels    []ChannelReconcileItem `json:"channels"`
	Total       ChannelReconcileTotal  `json:"total"`
	StartTs     int64                  `json:"start_ts"`
	EndTs       int64                  `json:"end_ts"`
	GeneratedAt int64                  `json:"generated_at"`
}

// GetChannelReconcile 对账视图：窗口内各渠道 × 模型的成功/失败/超时/费用全量精确聚合，
// 用于核对上游账单。窗口上限 24h（与 /statistics 相同的 prod 安全线）；按天对账时
// 前端传 [本地零点, 次日零点] 的绝对时间戳。
func GetChannelReconcile(c *gin.Context) {
	startTs, ok := parseRequiredTsQuery(c, "start_ts")
	if !ok {
		return
	}
	endTs, ok := parseRequiredTsQuery(c, "end_ts")
	if !ok {
		return
	}
	if endTs <= startTs || endTs-startTs > 86400 {
		common.ApiErrorMsg(c, "invalid window, require start_ts < end_ts and end_ts - start_ts <= 86400")
		return
	}
	summaryOnly, ok := parseStrictBoolQuery(c, "summary_only")
	if !ok {
		return
	}
	if summaryOnly {
		getChannelReconcileSummary(c, startTs, endTs)
		return
	}

	ctx := c.Request.Context()
	rows, err := model.GetChannelReconcileRows(ctx, startTs, endTs)
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
	for _, ch := range channels {
		channelById[ch.Id] = ch
	}

	items := make(map[int]*ChannelReconcileItem)
	total := ChannelReconcileTotal{}
	for _, row := range rows {
		// 两次查询之间渠道被删除的幽灵行直接跳过，与 GetChannelStatistics 口径一致。
		ch, exists := channelById[row.ChannelId]
		if !exists {
			continue
		}
		item := items[row.ChannelId]
		if item == nil {
			item = &ChannelReconcileItem{
				ChannelId:   row.ChannelId,
				ChannelName: ch.Name,
				Status:      ch.Status,
				Models:      []ChannelReconcileModelItem{},
			}
			items[row.ChannelId] = item
		}
		item.Models = append(item.Models, ChannelReconcileModelItem{
			ModelName:    row.ModelName,
			Quota:        row.Quota,
			SuccessCount: row.SuccessCount,
			ErrorCount:   row.ErrorCount,
			TimeoutCount: row.TimeoutCount,
		})
		item.Quota += row.Quota
		item.SuccessCount += row.SuccessCount
		item.ErrorCount += row.ErrorCount
		item.TimeoutCount += row.TimeoutCount
		total.Quota += row.Quota
		total.SuccessCount += row.SuccessCount
		total.ErrorCount += row.ErrorCount
		total.TimeoutCount += row.TimeoutCount
	}

	// 显式空切片，避免无数据时 JSON null（与 Models: []string{} 同教训）。
	list := make([]ChannelReconcileItem, 0, len(items))
	for _, item := range items {
		sort.Slice(item.Models, func(i, j int) bool {
			if item.Models[i].Quota != item.Models[j].Quota {
				return item.Models[i].Quota > item.Models[j].Quota
			}
			return item.Models[i].ModelName < item.Models[j].ModelName
		})
		list = append(list, *item)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Quota != list[j].Quota {
			return list[i].Quota > list[j].Quota
		}
		return list[i].ChannelId < list[j].ChannelId
	})

	common.ApiSuccess(c, ChannelReconcileResponse{
		Channels:    list,
		Total:       total,
		StartTs:     startTs,
		EndTs:       endTs,
		GeneratedAt: common.GetTimestamp(),
	})
}

func getChannelReconcileSummary(c *gin.Context, startTs, endTs int64) {
	rows, err := model.GetChannelQuotaSummaryRows(c.Request.Context(), startTs, endTs)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	list := make([]ChannelReconcileItem, 0, len(rows))
	total := ChannelReconcileTotal{}
	for _, row := range rows {
		list = append(list, ChannelReconcileItem{
			ChannelId:   row.ChannelId,
			ChannelName: row.ChannelName,
			Status:      row.Status,
			Quota:       row.Quota,
			Models:      []ChannelReconcileModelItem{},
		})
		total.Quota += row.Quota
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Quota != list[j].Quota {
			return list[i].Quota > list[j].Quota
		}
		return list[i].ChannelId < list[j].ChannelId
	})

	common.ApiSuccess(c, ChannelReconcileResponse{
		Channels:    list,
		Total:       total,
		StartTs:     startTs,
		EndTs:       endTs,
		GeneratedAt: common.GetTimestamp(),
	})
}

func parseRequiredTsQuery(c *gin.Context, key string) (int64, bool) {
	raw := strings.TrimSpace(c.Query(key))
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		common.ApiErrorMsg(c, "invalid "+key+", must be a positive unix timestamp in seconds")
		return 0, false
	}
	return v, true
}
