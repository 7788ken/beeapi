package model

import (
	"context"
)

// log_channel_quality.go — 渠道质量历史报表的数据访问层。
// 单渠道时间范围内的成功/失败/延迟按桶聚合 + 错误分布取数，全部走
// idx_logs_channel_type_id (channel_id, type, id) 前缀；SQL 只做纯标量聚合，
// JSON(frt) 与错误码(content) 在 service 端 Go 解析（跨库通用，同 channel_metrics.go 约定）。

type QualityHistoryBucket struct {
	BucketStart int64   `json:"bucket_start" gorm:"column:bucket_start"`
	SuccessCnt  int64   `json:"success_cnt" gorm:"column:success_cnt"`
	ErrorCnt    int64   `json:"error_cnt" gorm:"column:error_cnt"`
	AvgUseTime  float64 `json:"avg_use_time" gorm:"column:avg_use_time"` // 秒，仅成功请求；无样本为 0
}

// GetChannelQualityBuckets 单渠道按时间桶聚合成功数/失败数/平均耗时。
// 桶对齐表达式与 GetChannelTrend 一致：`((ca+tz) - ((ca+tz) % bucket)) - tz`，
// 避免日桶与调用方本地午夜错位；% 在 SQLite/MySQL/PG/ClickHouse 都是整数取模。
func GetChannelQualityBuckets(ctx context.Context, channelId int, startTs, endTs int64, bucketSec int64, tzOffsetSec int64) ([]QualityHistoryBucket, error) {
	var rows []QualityHistoryBucket
	if err := LOG_DB.WithContext(ctx).Raw(`
		SELECT ((created_at + ?) - ((created_at + ?) % ?)) - ? AS bucket_start,
		       SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS success_cnt,
		       SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS error_cnt,
		       COALESCE(AVG(CASE WHEN type = ? THEN use_time END), 0) AS avg_use_time
		FROM logs
		WHERE channel_id = ? AND type IN (?, ?) AND created_at >= ? AND created_at <= ?
		GROUP BY bucket_start
		ORDER BY bucket_start ASC`,
		tzOffsetSec, tzOffsetSec, bucketSec, tzOffsetSec,
		LogTypeConsume, LogTypeError, LogTypeConsume,
		channelId, LogTypeConsume, LogTypeError, startTs, endTs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

type ChannelUptimeBucket struct {
	ChannelId   int   `json:"channel_id" gorm:"column:channel_id"`
	BucketStart int64 `json:"bucket_start" gorm:"column:bucket_start"`
	SuccessCnt  int64 `json:"success_cnt" gorm:"column:success_cnt"`
	ErrorCnt    int64 `json:"error_cnt" gorm:"column:error_cnt"`
}

// GetChannelUptimeBuckets 全渠道按时间桶聚合成功/失败数（渠道列表「可用性」条形图）。
// 渠道 id 列表先从 channels 取（LOG_DB 可能是独立库，不能 JOIN），再用 channel_id IN (...)
// 做单条聚合：带 channel_id 前缀谓词才能走 idx_logs_channel_type_created_at_quota /
// idx_logs_channel_type_id 的逐渠道 range，否则退化成按 created_at 扫全窗口再回表。
// 桶对齐表达式与 GetChannelQualityBuckets 一致。
func GetChannelUptimeBuckets(ctx context.Context, startTs, endTs, bucketSec, tzOffsetSec int64) ([]ChannelUptimeBucket, error) {
	var channelIds []int
	if err := DB.WithContext(ctx).Table("channels").Select("id").Scan(&channelIds).Error; err != nil {
		return nil, err
	}
	if len(channelIds) == 0 {
		return nil, nil
	}

	var rows []ChannelUptimeBucket
	if err := LOG_DB.WithContext(ctx).Raw(`
		SELECT channel_id,
		       ((created_at + ?) - ((created_at + ?) % ?)) - ? AS bucket_start,
		       SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS success_cnt,
		       SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS error_cnt
		FROM logs
		WHERE channel_id IN (?) AND type IN (?, ?) AND created_at >= ? AND created_at <= ?
		GROUP BY channel_id, bucket_start
		ORDER BY channel_id ASC, bucket_start ASC`,
		tzOffsetSec, tzOffsetSec, bucketSec, tzOffsetSec,
		LogTypeConsume, LogTypeError,
		channelIds, LogTypeConsume, LogTypeError, startTs, endTs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// 测活日志的身份约定，写读两侧共用：写入方在 controller 的渠道测试链路，恒以
// ChannelTestUserId（root）落日志、token_id 恒为 0（真实用户令牌的 token_id 恒 >0，
// 靠它挡住"root 给自己的令牌起名『模型测试』"的撞名误判）。
// token_name 按被测那一刻的渠道状态打标：启用态 → ChannelTestTokenName；禁用态
// （典型是对已自动禁用渠道每分钟一次的恢复探活）→ ChannelTestOfflineTokenName。
// 状态在写入时固化，读取侧不做"按当前状态回溯"——渠道会在恢复探活中反复禁停/启用，
// 点时刻快照会把禁用期的探活失败整窗带回分组曲线。
const (
	ChannelTestUserId           = 1
	ChannelTestTokenName        = "模型测试"
	ChannelTestOfflineTokenName = "模型测试-停用"
)

type ChannelTestUptimeBucket struct {
	ChannelId   int    `gorm:"column:channel_id"`
	BucketStart int64  `gorm:"column:bucket_start"`
	TokenName   string `gorm:"column:token_name"`
	SuccessCnt  int64  `gorm:"column:success_cnt"`
	ErrorCnt    int64  `gorm:"column:error_cnt"`
}

// GetChannelTestUptimeBuckets 按时间桶聚合「渠道测活」日志（user_id=1 且 token_name 为测活标记），
// 供分组可用率把测活样本与真实流量分开归属；按 token_name 分行返回，调用方据此区分启用态测活
// 与禁用期探活。桶起点固定按 UTC 对齐（分组图桶宽为整小时、无时区入参，见 service.GetGroupUptime）。
//
// 独立成一条小查询而不是并进 GetChannelUptimeBuckets：主查询靠 (channel_id, type, created_at)
// 覆盖索引扫全窗 type=2/5（us1 24h 约 150 万行），SELECT 里加 user_id/token_name 会退化成回表。
// 本查询在 MySQL 下由 user_id 单列索引驱动（logs 无 (user_id, created_at) 复合索引），
// 扫描范围是 root 的**全部历史**测活行而非 24h 窗口——us1 2026-08-14 实测 14.8 万行、亚秒级，
// 且有 5min 缓存 + 单飞兜底；日志长期不清理导致该查询变慢时，用启动自愈迁移线补
// (user_id, created_at) 复合索引，不要改回并进主查询。ClickHouse 作 LOG_DB 时无 user_id
// 索引，靠 created_at 主键裁剪，成本口径与 MySQL 不同。
func GetChannelTestUptimeBuckets(ctx context.Context, startTs, endTs, bucketSec int64) ([]ChannelTestUptimeBucket, error) {
	var rows []ChannelTestUptimeBucket
	if err := LOG_DB.WithContext(ctx).Raw(`
		SELECT channel_id,
		       created_at - (created_at % ?) AS bucket_start,
		       token_name,
		       SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS success_cnt,
		       SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS error_cnt
		FROM logs
		WHERE user_id = ? AND token_id = 0 AND token_name IN (?, ?) AND type IN (?, ?) AND created_at >= ? AND created_at <= ?
		GROUP BY channel_id, bucket_start, token_name`,
		bucketSec,
		LogTypeConsume, LogTypeError,
		ChannelTestUserId, ChannelTestTokenName, ChannelTestOfflineTokenName,
		LogTypeConsume, LogTypeError, startTs, endTs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

type QualityErrorModelRow struct {
	ModelName string `json:"model_name" gorm:"column:model_name"`
	ErrorCnt  int64  `json:"error_cnt" gorm:"column:error_cnt"`
}

// GetChannelErrorModelDist 单渠道窗口内错误按模型分布（Top N）。
func GetChannelErrorModelDist(ctx context.Context, channelId int, startTs, endTs int64, limit int) ([]QualityErrorModelRow, error) {
	var rows []QualityErrorModelRow
	if err := LOG_DB.WithContext(ctx).Raw(`
		SELECT model_name, COUNT(*) AS error_cnt
		FROM logs
		WHERE channel_id = ? AND type = ? AND created_at >= ? AND created_at <= ?
		GROUP BY model_name
		ORDER BY error_cnt DESC
		LIMIT ?`,
		channelId, LogTypeError, startTs, endTs, limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetChannelErrorContents 取窗口内最近 N 条错误行的 content（service 端正则提取 status_code）。
func GetChannelErrorContents(ctx context.Context, channelId int, startTs, endTs int64, limit int) ([]string, error) {
	var rows []string
	if err := LOG_DB.WithContext(ctx).Raw(`
		SELECT content
		FROM logs
		WHERE channel_id = ? AND type = ? AND created_at >= ? AND created_at <= ?
		ORDER BY `+recentLogOrder("")+`
		LIMIT ?`,
		channelId, LogTypeError, startTs, endTs, limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetChannelFrtSamples 取窗口内最近 N 条成功行的 other JSON（service 端解析 frt 求首包均值）。
func GetChannelFrtSamples(ctx context.Context, channelId int, startTs, endTs int64, limit int) ([]string, error) {
	var rows []string
	if err := LOG_DB.WithContext(ctx).Raw(`
		SELECT other
		FROM logs
		WHERE channel_id = ? AND type = ? AND created_at >= ? AND created_at <= ?
		ORDER BY `+recentLogOrder("")+`
		LIMIT ?`,
		channelId, LogTypeConsume, startTs, endTs, limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
