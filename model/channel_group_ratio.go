package model

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 上游分组倍率监控（docs/2026-08-05-upstream-group-ratio-monitor.md）
//
// baselines 存"当前快照"（UPSERT），changes 只在检出差异时插入。
// 首次抓取与新出现的分组只写 baseline 不产生 changes，否则会把"首见"误报成上游涨价。

// 倍率种类：三类上游取到的字段不同，统一落一张表用 ratio_kind 区分。
// 比对基准用 resolved（稳定值），effective 含高峰时段系数只作展示。
const (
	RatioKindGroup     = "group"     // new-api /api/pricing 的 group_ratio
	RatioKindResolved  = "resolved"  // sub2api 含专属折扣的稳定倍率
	RatioKindEffective = "effective" // sub2api resolved × 当前高峰系数；随时段自然波动，不计入角标
	RatioKindAPIRate   = "api_rate"  // donehub 的 api_rate
)

type ChannelGroupRatioBaseline struct {
	Id        int64   `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelId int     `json:"channel_id" gorm:"column:channel_id;not null;uniqueIndex:idx_cgr_baseline_ch_group_kind,priority:1"`
	GroupName string  `json:"group_name" gorm:"column:group_name;type:varchar(191);not null;uniqueIndex:idx_cgr_baseline_ch_group_kind,priority:2"`
	RatioKind string  `json:"ratio_kind" gorm:"column:ratio_kind;type:varchar(16);not null;uniqueIndex:idx_cgr_baseline_ch_group_kind,priority:3"`
	Ratio     float64 `json:"ratio" gorm:"column:ratio;type:double precision;not null;default:0"`
	Extra     string  `json:"extra" gorm:"column:extra;type:varchar(1024);not null;default:''"` // JSON：sub2api 存 peak_*，donehub 存 api_rate
	UpdatedAt int64   `json:"updated_at" gorm:"column:updated_at;type:bigint;not null;default:0"`
}

func (ChannelGroupRatioBaseline) TableName() string {
	return "channel_group_ratio_baselines"
}

type ChannelGroupRatioChange struct {
	Id        int64   `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelId int     `json:"channel_id" gorm:"column:channel_id;not null;index:idx_cgr_change_ch_batch,priority:1"`
	BatchAt   int64   `json:"batch_at" gorm:"column:batch_at;type:bigint;not null;index:idx_cgr_change_ch_batch,priority:2"`
	GroupName string  `json:"group_name" gorm:"column:group_name;type:varchar(191);not null"`
	RatioKind string  `json:"ratio_kind" gorm:"column:ratio_kind;type:varchar(16);not null"`
	OldValue  float64 `json:"old_value" gorm:"column:old_value;type:double precision;not null;default:0"`
	NewValue  float64 `json:"new_value" gorm:"column:new_value;type:double precision;not null;default:0"`
	Direction int     `json:"direction" gorm:"column:direction;not null;default:0"` // 1=涨 -1=降
	CreatedAt int64   `json:"created_at" gorm:"column:created_at;type:bigint;not null;default:0"`
}

func (ChannelGroupRatioChange) TableName() string {
	return "channel_group_ratio_changes"
}

func GetChannelGroupRatioBaselines(channelId int) ([]ChannelGroupRatioBaseline, error) {
	var baselines []ChannelGroupRatioBaseline
	err := DB.Where("channel_id = ?", channelId).Find(&baselines).Error
	return baselines, err
}

// UpsertChannelGroupRatioBaselines 按 (channel_id, group_name, ratio_kind) 覆盖写。
// MySQL 走 ON DUPLICATE KEY UPDATE，PostgreSQL 走 ON CONFLICT DO UPDATE，均依赖上面的唯一索引。
func UpsertChannelGroupRatioBaselines(baselines []ChannelGroupRatioBaseline) error {
	if len(baselines) == 0 {
		return nil
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "channel_id"},
			{Name: "group_name"},
			{Name: "ratio_kind"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"ratio", "extra", "updated_at"}),
	}).CreateInBatches(&baselines, 200).Error
}

func CreateChannelGroupRatioChanges(changes []ChannelGroupRatioChange) error {
	if len(changes) == 0 {
		return nil
	}
	return DB.CreateInBatches(&changes, 200).Error
}

// GetChannelGroupRatioChangesSince 取某渠道 startTs 之后的变更，供角标明细弹窗展示。
func GetChannelGroupRatioChangesSince(channelId int, startTs int64) ([]ChannelGroupRatioChange, error) {
	var changes []ChannelGroupRatioChange
	err := DB.Where("channel_id = ? AND batch_at >= ?", channelId, startTs).
		Order("batch_at DESC, id DESC").
		Find(&changes).Error
	return changes, err
}

// deleteChannelGroupRatios 渠道删除时在同一事务内清理其倍率基线与流水。
func deleteChannelGroupRatios(tx *gorm.DB, channelIds []int) error {
	if len(channelIds) == 0 {
		return nil
	}
	if err := tx.Where("channel_id IN (?)", channelIds).Delete(&ChannelGroupRatioBaseline{}).Error; err != nil {
		return err
	}
	return tx.Where("channel_id IN (?)", channelIds).Delete(&ChannelGroupRatioChange{}).Error
}

// UpdateChannelRatioSnapshot 回写渠道列表角标所需的冗余快照。
// changed=false 时保留上次的 up/down 计数与 ratio_changed_at，避免"本轮没变化"把上次的角标抹掉。
func UpdateChannelRatioSnapshot(channelId int, status, msg, kind, detail, group string, up, down int, batchAt int64, changed bool) error {
	// ratio_fetch_msg 列宽 varchar(255)：TLS/超时错误串常带完整 URL 而超长，
	// MySQL 严格模式会报 1406 让整条快照回写失败（状态与角标全轮丢失）。
	if r := []rune(msg); len(r) > 255 {
		msg = string(r[:252]) + "..."
	}
	updates := map[string]any{
		"ratio_fetched_at":    batchAt,
		"ratio_fetch_status":  status,
		"ratio_fetch_msg":     msg,
		"ratio_upstream_kind": kind,
	}
	// 摘要每轮成功都刷新（倍率没变也要保持"此刻的值"是最新的），与角标计数的写入条件无关
	if detail != "" {
		updates["ratio_detail"] = detail
	}
	updates["ratio_resolved_group"] = group
	if changed {
		updates["ratio_up_count"] = up
		updates["ratio_down_count"] = down
		updates["ratio_changed_at"] = batchAt
	}
	return DB.Model(&Channel{}).Where("id = ?", channelId).Updates(updates).Error
}

// GetChannelWindowUsage 按时间窗口聚合各渠道各模型的 token 用量，供实付倍率反推算基准。
// 一次查询覆盖全部渠道（走 idx_created_at_type），避免逐渠道查 28M 行的 logs 表。
func GetChannelWindowUsage(startTs, endTs int64) (map[int][]ChannelModelUsage, error) {
	var rows []ChannelModelUsage
	err := DB.Table("logs").
		Select("channel_id, model_name, SUM(prompt_tokens) AS prompt_tokens, SUM(completion_tokens) AS completion_tokens").
		Where("type = ? AND created_at >= ? AND created_at < ?", 2, startTs, endTs).
		Group("channel_id, model_name").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int][]ChannelModelUsage, len(rows))
	for _, r := range rows {
		out[r.ChannelId] = append(out[r.ChannelId], r)
	}
	return out, nil
}

// ChannelModelUsage 单渠道单模型在窗口内的 token 合计。
type ChannelModelUsage struct {
	ChannelId        int    `gorm:"column:channel_id"`
	ModelName        string `gorm:"column:model_name"`
	PromptTokens     int64  `gorm:"column:prompt_tokens"`
	CompletionTokens int64  `gorm:"column:completion_tokens"`
}

// UpdateChannelEffectiveRatio 回写实付倍率与用量快照。
func UpdateChannelEffectiveRatio(channelId int, usage float64, usageAt int64, effective *float64, effectiveAt int64) error {
	updates := map[string]any{
		"ratio_usage_snapshot": usage,
		"ratio_usage_at":       usageAt,
	}
	if effective != nil {
		updates["ratio_effective"] = *effective
		updates["ratio_effective_at"] = effectiveAt
	}
	return DB.Model(&Channel{}).Where("id = ?", channelId).Updates(updates).Error
}
