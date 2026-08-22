package model

import (
	"time"

	"github.com/QuantumNous/new-api/common"
)

// ChannelVerifyReport 外部测评报告（通过外部测评网关 /api/verify/claude 触发）。
//
// 与 ChannelHealthEvent / QualityScore 区分：
//   - QualityScore：基于自身流量统计的被动质量分
//   - ChannelHealthEvent：自动降级/升级审计事件
//   - ChannelVerifyReport：管理员主动触发的第三方测评全量报告（含 12 维度细节）
type ChannelVerifyReport struct {
	Id         int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelId  int    `json:"channel_id" gorm:"index;not null"`
	Model      string `json:"model" gorm:"type:varchar(128)"`
	Score      int    `json:"score" gorm:"default:0"` // 0-100
	Grade      string `json:"grade" gorm:"type:varchar(8)"`
	Status     string `json:"status" gorm:"type:varchar(16);index;default:'running'"` // running/success/failed/cancelled
	DurationMs int    `json:"duration_ms" gorm:"default:0"`
	ReportJson string `json:"-" gorm:"type:text"` // 完整 SSE done payload，仅详情接口返回
	ErrorMsg   string `json:"error_msg,omitempty" gorm:"type:varchar(512)"`
	TestedAt   int64  `json:"tested_at" gorm:"bigint;index;not null"`
	OperatorId int    `json:"operator_id" gorm:"default:0"`
}

const (
	VerifyStatusRunning   = "running"
	VerifyStatusSuccess   = "success"
	VerifyStatusFailed    = "failed"
	VerifyStatusCancelled = "cancelled"
)

func (ChannelVerifyReport) TableName() string {
	return "channel_verify_reports"
}

// CreateRunningReport 在测评开始时插入一条 running 记录，返回 id 用于后续更新。
func CreateRunningReport(channelId, operatorId int, model string) (*ChannelVerifyReport, error) {
	r := &ChannelVerifyReport{
		ChannelId:  channelId,
		Model:      model,
		Status:     VerifyStatusRunning,
		TestedAt:   time.Now().Unix(),
		OperatorId: operatorId,
	}
	if err := DB.Create(r).Error; err != nil {
		return nil, err
	}
	return r, nil
}

// FinalizeReport 在测评结束（success/failed/cancelled）时更新报告并裁剪历史。
// reportPayload 为 done/early_stop 事件的完整 data；error 时可为空。
func FinalizeReport(reportId int64, status string, score int, grade string, reportPayload any, errMsg string, startedAt int64) error {
	updates := map[string]any{
		"status":      status,
		"score":       score,
		"grade":       grade,
		"duration_ms": int(time.Now().UnixMilli() - startedAt*1000),
		"error_msg":   errMsg,
	}
	if reportPayload != nil {
		bs, err := common.Marshal(reportPayload)
		if err == nil {
			updates["report_json"] = string(bs)
		}
	}
	return DB.Model(&ChannelVerifyReport{}).Where("id = ?", reportId).Updates(updates).Error
}

// UpdateRunningScore 在收到 health_score 事件时实时刷新当前 running 报告的分数。
func UpdateRunningScore(reportId int64, score int, grade string) error {
	return DB.Model(&ChannelVerifyReport{}).
		Where("id = ? AND status = ?", reportId, VerifyStatusRunning).
		Updates(map[string]any{"score": score, "grade": grade}).Error
}

// TrimChannelHistory 保留某 channel 最近 limit 条成功报告，更早的删除。
// 失败/取消的不计入保留名额，由调用方决定是否保留。本函数只裁剪 channel_id 维度下的总条数。
// 三库通用：先取第 limit+1 条的 tested_at，再 DELETE 更老的，避免 DELETE+子查询的方言差异。
func TrimChannelHistory(channelId, limit int) error {
	if limit <= 0 {
		return nil
	}
	var cutoff struct {
		TestedAt int64
		Id       int64
	}
	err := DB.Model(&ChannelVerifyReport{}).
		Select("tested_at, id").
		Where("channel_id = ?", channelId).
		Order("tested_at DESC, id DESC").
		Offset(limit).
		Limit(1).
		Scan(&cutoff).Error
	if err != nil {
		return err
	}
	if cutoff.TestedAt == 0 {
		return nil // 未超出
	}
	return DB.Where("channel_id = ? AND (tested_at < ? OR (tested_at = ? AND id <= ?))",
		channelId, cutoff.TestedAt, cutoff.TestedAt, cutoff.Id).
		Delete(&ChannelVerifyReport{}).Error
}

// ListReportsByChannel 列表查询（不返回 ReportJson 大字段）。
func ListReportsByChannel(channelId, page, pageSize int) ([]ChannelVerifyReport, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	var total int64
	if err := DB.Model(&ChannelVerifyReport{}).Where("channel_id = ?", channelId).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []ChannelVerifyReport
	err := DB.Select("id, channel_id, model, score, grade, status, duration_ms, error_msg, tested_at, operator_id").
		Where("channel_id = ?", channelId).
		Order("tested_at DESC, id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&items).Error
	return items, total, err
}

// GetReportById 详情查询（含 ReportJson）。
func GetReportById(reportId int64) (*ChannelVerifyReport, error) {
	var r ChannelVerifyReport
	if err := DB.Where("id = ?", reportId).First(&r).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateChannelVerifySnapshot 回写 Channel 表的"最近一次测评"快照字段。
// 列表展示直接读 channels.verify_*，避免每次 JOIN channel_verify_reports。
func UpdateChannelVerifySnapshot(channelId, score int, grade string, reportId int64, testedAt int64, prevScore *int) error {
	return DB.Model(&Channel{}).Where("id = ?", channelId).Updates(map[string]any{
		"verify_prev_score": prevScore, // 直接写调用方捕获的旧分，避免 gorm.Expr 自引用在 MySQL 左→右取新值的方言差异
		"verify_score":      score,
		"verify_grade":      grade,
		"verify_tested_at":  testedAt,
		"verify_report_id":  reportId,
	}).Error
}

// CleanupStaleRunningReports 进程启动时调用，把"过老的 running 报告"标为 failed。
// 防止 SIGTERM / OOM 导致 status=running 记录孤儿，被前端 backgroundRunning 误判为活着。
// 阈值取 staleSeconds，建议 VERIFY_TIMEOUT_SEC * 2。
func CleanupStaleRunningReports(staleSeconds int64) (int64, error) {
	if staleSeconds <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Unix() - staleSeconds
	res := DB.Model(&ChannelVerifyReport{}).
		Where("status = ? AND tested_at < ?", VerifyStatusRunning, cutoff).
		Updates(map[string]any{
			"status":    VerifyStatusFailed,
			"error_msg": "process restart or crash",
		})
	return res.RowsAffected, res.Error
}

// SetChannelVerifyDisabled 标记/清除「渠道当前被 verify 评测低分自动禁用」。
// 仅 verify 调度写：禁用置 1，自动启用清 0。健康度禁用路径不写此字段（保持 0），
// 由此隔离两套自动禁用——各自的恢复只认自己禁用的渠道（docs/2026-06-18 v5）。
func SetChannelVerifyDisabled(channelId int, disabled bool) error {
	v := 0
	if disabled {
		v = 1
	}
	return DB.Model(&Channel{}).Where("id = ?", channelId).Update("verify_disabled", v).Error
}
