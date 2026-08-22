package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

type AnomalyQueryParams struct {
	Type      string
	Page      int
	PageSize  int
	StartTime int64
	EndTime   int64
	Username  string
	ModelName string
	ChannelId int
}

type AnomalyLog struct {
	Id               int    `json:"id"`
	UserId           int    `json:"user_id"`
	Username         string `json:"username"`
	TokenName        string `json:"token_name"`
	ModelName        string `json:"model_name"`
	ChannelId        int    `json:"channel_id"`
	Quota            int    `json:"quota"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	CreatedAt        int64  `json:"created_at"`
	RequestId        string `json:"request_id"`
	AnomalyType      string `json:"anomaly_type"`
	AnomalyDetail    string `json:"anomaly_detail"`
	Other            string `json:"other"`
}

type AnomalyStats struct {
	ClientDisconnect int64 `json:"client_disconnect"`
	HighCost         int64 `json:"high_cost"`
	ChannelDemote    int64 `json:"channel_demote"`
	ChannelDisable   int64 `json:"channel_disable"`
	ChannelUpgrade   int64 `json:"channel_upgrade"`
	TotalCount       int64 `json:"total_count"`
	TotalQuota       int64 `json:"total_quota"`
}

const highCostQuotaThreshold = 500000 // $1

func anomalyLogID(log Log, startIdx, index int) int {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return startIdx + index + 1
	}
	return log.Id
}

func GetAnomalyLogs(params AnomalyQueryParams) ([]AnomalyLog, int64, error) {
	if params.StartTime == 0 {
		params.StartTime = time.Now().Add(-24 * time.Hour).Unix()
	}
	if params.EndTime == 0 {
		params.EndTime = time.Now().Unix()
	}

	switch params.Type {
	case "client_disconnect":
		return getClientDisconnectLogs(params)
	case "high_cost":
		return getHighCostLogs(params)
	case "no_output_refund":
		return getNoOutputRefundLogs(params)
	case "channel_health":
		return getChannelHealthLogs(params)
	default:
		return getChannelHealthLogs(params)
	}
}

func getClientDisconnectLogs(params AnomalyQueryParams) ([]AnomalyLog, int64, error) {
	var logs []Log

	// LIKE '%client_gone%' 无法用索引，跳过 COUNT 避免全表扫描
	// 利用主键倒序 + LIMIT 快速取最新记录
	offset := (params.Page - 1) * params.PageSize
	query := LOG_DB.Model(&Log{}).
		Where("type = 2 AND created_at >= ? AND created_at <= ?", params.StartTime, params.EndTime).
		Where("other LIKE ?", "%client_gone%").
		Where("quota > 0")
	if params.Username != "" {
		query = query.Where("username = ?", params.Username)
	}
	if params.ModelName != "" {
		query = query.Where("model_name = ?", params.ModelName)
	}
	if params.ChannelId > 0 {
		query = query.Where("channel_id = ?", params.ChannelId)
	}
	err := query.Order(recentLogOrder("")).Offset(offset).Limit(params.PageSize + 1).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}

	hasMore := len(logs) > params.PageSize
	if hasMore {
		logs = logs[:params.PageSize]
	}
	// 估算 total：如果有更多则返回 -1 表示"更多"
	var total int64
	if hasMore {
		total = int64((params.Page)*params.PageSize + 1)
	} else {
		total = int64((params.Page-1)*params.PageSize + len(logs))
	}

	results := make([]AnomalyLog, 0, len(logs))
	for index, l := range logs {
		results = append(results, AnomalyLog{
			Id:               anomalyLogID(l, offset, index),
			UserId:           l.UserId,
			Username:         l.Username,
			TokenName:        l.TokenName,
			ModelName:        l.ModelName,
			ChannelId:        l.ChannelId,
			Quota:            l.Quota,
			PromptTokens:     l.PromptTokens,
			CompletionTokens: l.CompletionTokens,
			CreatedAt:        l.CreatedAt,
			RequestId:        l.RequestId,
			AnomalyType:      "client_disconnect",
			AnomalyDetail:    fmt.Sprintf("用户断开连接但已扣费 $%.4f", float64(l.Quota)/500000),
			Other:            l.Other,
		})
	}
	return results, total, nil
}

// getNoOutputRefundLogs 列出零产出免单的请求。
// 数据来源：service/text_quota.go::shouldRefundNoOutput 命中后，会把
// other["no_output_refund"]=true 写入 Log.Other（即便 quota=0 也照常落库）。
// 检索靠 other LIKE，无法走索引；与 client_disconnect 同样跳过 COUNT 改用 hasMore 估算。
// 注：免单成功的 quota=0，不能用 quota>0 过滤。
func getNoOutputRefundLogs(params AnomalyQueryParams) ([]AnomalyLog, int64, error) {
	var logs []Log

	offset := (params.Page - 1) * params.PageSize
	query := LOG_DB.Model(&Log{}).
		Where("type = 2 AND created_at >= ? AND created_at <= ?", params.StartTime, params.EndTime).
		Where("other LIKE ?", "%\"no_output_refund\":true%")
	if params.Username != "" {
		query = query.Where("username = ?", params.Username)
	}
	if params.ModelName != "" {
		query = query.Where("model_name = ?", params.ModelName)
	}
	if params.ChannelId > 0 {
		query = query.Where("channel_id = ?", params.ChannelId)
	}
	err := query.Order(recentLogOrder("")).Offset(offset).Limit(params.PageSize + 1).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}

	hasMore := len(logs) > params.PageSize
	if hasMore {
		logs = logs[:params.PageSize]
	}
	var total int64
	if hasMore {
		total = int64((params.Page)*params.PageSize + 1)
	} else {
		total = int64((params.Page-1)*params.PageSize + len(logs))
	}

	results := make([]AnomalyLog, 0, len(logs))
	for index, l := range logs {
		hadFirstChunk := strings.Contains(l.Other, `"had_first_chunk":true`)
		var detail string
		if hadFirstChunk {
			detail = "上游零产出已自动免单（首帧到达但无内容）"
		} else {
			detail = "上游零产出已自动免单（完全无返回）"
		}
		results = append(results, AnomalyLog{
			Id:               anomalyLogID(l, offset, index),
			UserId:           l.UserId,
			Username:         l.Username,
			TokenName:        l.TokenName,
			ModelName:        l.ModelName,
			ChannelId:        l.ChannelId,
			Quota:            l.Quota,
			PromptTokens:     l.PromptTokens,
			CompletionTokens: l.CompletionTokens,
			CreatedAt:        l.CreatedAt,
			RequestId:        l.RequestId,
			AnomalyType:      "no_output_refund",
			AnomalyDetail:    detail,
			Other:            l.Other,
		})
	}
	return results, total, nil
}

func getHighCostLogs(params AnomalyQueryParams) ([]AnomalyLog, int64, error) {
	var logs []Log
	var total int64

	query := LOG_DB.Model(&Log{}).
		Where("type = 2 AND created_at >= ? AND created_at <= ? AND quota > ?",
			params.StartTime, params.EndTime, highCostQuotaThreshold)
	if params.Username != "" {
		query = query.Where("username = ?", params.Username)
	}
	if params.ModelName != "" {
		query = query.Where("model_name = ?", params.ModelName)
	}
	if params.ChannelId > 0 {
		query = query.Where("channel_id = ?", params.ChannelId)
	}

	query.Count(&total)

	// 用 created_at DESC 排序以利用 (type, created_at, quota) 索引
	offset := (params.Page - 1) * params.PageSize
	err := query.Order(recentLogOrder("")).Offset(offset).Limit(params.PageSize).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}

	results := make([]AnomalyLog, 0, len(logs))
	for index, l := range logs {
		results = append(results, AnomalyLog{
			Id:               anomalyLogID(l, offset, index),
			UserId:           l.UserId,
			Username:         l.Username,
			TokenName:        l.TokenName,
			ModelName:        l.ModelName,
			ChannelId:        l.ChannelId,
			Quota:            l.Quota,
			PromptTokens:     l.PromptTokens,
			CompletionTokens: l.CompletionTokens,
			CreatedAt:        l.CreatedAt,
			RequestId:        l.RequestId,
			AnomalyType:      "high_cost",
			AnomalyDetail:    fmt.Sprintf("单次消费 $%.4f", float64(l.Quota)/500000),
			Other:            l.Other,
		})
	}
	return results, total, nil
}

func getChannelHealthLogs(params AnomalyQueryParams) ([]AnomalyLog, int64, error) {
	var events []ChannelHealthEvent
	var total int64

	query := DB.Model(&ChannelHealthEvent{}).
		Where("created_at >= ? AND created_at <= ?", params.StartTime, params.EndTime).
		Where("event_type IN ?", []string{"demote", "disable", "upgrade", "enable"})
	if params.ChannelId > 0 {
		query = query.Where("channel_id = ?", params.ChannelId)
	}

	query.Count(&total)

	offset := (params.Page - 1) * params.PageSize
	err := query.Order("id DESC").Offset(offset).Limit(params.PageSize).Find(&events).Error
	if err != nil {
		return nil, 0, err
	}

	results := make([]AnomalyLog, 0, len(events))
	for _, e := range events {
		anomalyType := "channel_" + e.EventType
		detail := fmt.Sprintf("渠道 #%d %s L%d→L%d: %s", e.ChannelId, e.EventType, e.FromLevel, e.ToLevel, e.Reason)
		results = append(results, AnomalyLog{
			Id:            int(e.Id),
			ChannelId:     e.ChannelId,
			CreatedAt:     e.CreatedAt,
			AnomalyType:   anomalyType,
			AnomalyDetail: detail,
		})
	}
	return results, total, nil
}

type AnomalySummary struct {
	TotalCount int64 `json:"total_count"`
	TotalQuota int64 `json:"total_quota"`
}

func GetAnomalySummary(params AnomalyQueryParams) (*AnomalySummary, error) {
	if params.StartTime == 0 {
		params.StartTime = time.Now().Add(-24 * time.Hour).Unix()
	}
	if params.EndTime == 0 {
		params.EndTime = time.Now().Unix()
	}

	summary := &AnomalySummary{}

	switch params.Type {
	case "client_disconnect":
		// LIKE 查询较慢，超过 3 天时限制范围
		startTs := params.StartTime
		if params.EndTime-startTs > 3*86400 {
			startTs = params.EndTime - 3*86400
		}
		query := LOG_DB.Model(&Log{}).
			Where("type = 2 AND created_at >= ? AND created_at <= ?", startTs, params.EndTime).
			Where("other LIKE ?", "%client_gone%").
			Where("quota > 0")
		if params.Username != "" {
			query = query.Where("username = ?", params.Username)
		}
		if params.ModelName != "" {
			query = query.Where("model_name = ?", params.ModelName)
		}
		if params.ChannelId > 0 {
			query = query.Where("channel_id = ?", params.ChannelId)
		}
		var result struct {
			Count      int64
			TotalQuota int64
		}
		query.Select("COUNT(*) as count, COALESCE(SUM(quota), 0) as total_quota").Scan(&result)
		summary.TotalCount = result.Count
		summary.TotalQuota = result.TotalQuota

	case "high_cost":
		query := LOG_DB.Model(&Log{}).
			Where("type = 2 AND created_at >= ? AND created_at <= ? AND quota > ?",
				params.StartTime, params.EndTime, highCostQuotaThreshold)
		if params.Username != "" {
			query = query.Where("username = ?", params.Username)
		}
		if params.ModelName != "" {
			query = query.Where("model_name = ?", params.ModelName)
		}
		if params.ChannelId > 0 {
			query = query.Where("channel_id = ?", params.ChannelId)
		}
		var result struct {
			Count      int64
			TotalQuota int64
		}
		query.Select("COUNT(*) as count, COALESCE(SUM(quota), 0) as total_quota").Scan(&result)
		summary.TotalCount = result.Count
		summary.TotalQuota = result.TotalQuota

	case "no_output_refund":
		// LIKE 查询无法走索引，超过 3 天时限制范围，跟 client_disconnect 同策略
		startTs := params.StartTime
		if params.EndTime-startTs > 3*86400 {
			startTs = params.EndTime - 3*86400
		}
		query := LOG_DB.Model(&Log{}).
			Where("type = 2 AND created_at >= ? AND created_at <= ?", startTs, params.EndTime).
			Where("other LIKE ?", "%\"no_output_refund\":true%")
		if params.Username != "" {
			query = query.Where("username = ?", params.Username)
		}
		if params.ModelName != "" {
			query = query.Where("model_name = ?", params.ModelName)
		}
		if params.ChannelId > 0 {
			query = query.Where("channel_id = ?", params.ChannelId)
		}
		var result struct {
			Count      int64
			TotalQuota int64
		}
		// 免单后 quota=0，TotalQuota 永远为 0；保留字段是为了和其它类型 schema 一致
		query.Select("COUNT(*) as count, COALESCE(SUM(quota), 0) as total_quota").Scan(&result)
		summary.TotalCount = result.Count
		summary.TotalQuota = result.TotalQuota

	case "channel_health":
		chQuery := DB.Model(&ChannelHealthEvent{}).
			Where("created_at >= ? AND created_at <= ?", params.StartTime, params.EndTime).
			Where("event_type IN ?", []string{"demote", "disable", "upgrade", "enable"})
		if params.ChannelId > 0 {
			chQuery = chQuery.Where("channel_id = ?", params.ChannelId)
		}
		chQuery.Count(&summary.TotalCount)
	}

	return summary, nil
}

func GetAnomalyStats(startTime, endTime int64) (*AnomalyStats, error) {
	if startTime == 0 {
		startTime = time.Now().Add(-24 * time.Hour).Unix()
	}
	if endTime == 0 {
		endTime = time.Now().Unix()
	}

	stats := &AnomalyStats{}

	// 渠道健康事件（小表，快速）
	DB.Model(&ChannelHealthEvent{}).
		Where("created_at >= ? AND created_at <= ? AND event_type = ?", startTime, endTime, "demote").
		Count(&stats.ChannelDemote)

	DB.Model(&ChannelHealthEvent{}).
		Where("created_at >= ? AND created_at <= ? AND event_type = ?", startTime, endTime, "disable").
		Count(&stats.ChannelDisable)

	DB.Model(&ChannelHealthEvent{}).
		Where("created_at >= ? AND created_at <= ? AND event_type = ?", startTime, endTime, "upgrade").
		Count(&stats.ChannelUpgrade)

	// 高消费：利用覆盖索引 (type, created_at, quota)
	LOG_DB.Model(&Log{}).
		Where("type = 2 AND created_at >= ? AND created_at <= ? AND quota > ?", startTime, endTime, highCostQuotaThreshold).
		Count(&stats.HighCost)

	// 用户断连：LIKE 查询无法用索引，不做 COUNT，返回 -1 表示需要点击查看
	stats.ClientDisconnect = -1

	// 总计：利用覆盖索引 (type, created_at, quota)
	var totalResult struct {
		Count      int64
		TotalQuota int64
	}
	LOG_DB.Model(&Log{}).
		Select("COUNT(*) as count, COALESCE(SUM(quota), 0) as total_quota").
		Where("type = 2 AND created_at >= ? AND created_at <= ? AND quota > 0", startTime, endTime).
		Scan(&totalResult)
	stats.TotalCount = totalResult.Count
	stats.TotalQuota = totalResult.TotalQuota

	return stats, nil
}
