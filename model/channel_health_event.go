package model

import (
	"time"
)

// ChannelHealthEvent 渠道健康度状态变更审计表。
//
// 用于复盘"为什么这个渠道在某时刻被降级/禁用/恢复"，以及前端展示
// 渠道详情抽屉里的"近 30 天健康曲线"。
//
// 不放 metric 时序数据（请求次数、延迟分布等走 Prometheus 或 usage-logs）。
//
// 详见 docs/2026-05-04-channel-health-auto-degrade-plan.md §3.2.2
type ChannelHealthEvent struct {
	Id        int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelId int    `json:"channel_id" gorm:"index;not null"`
	EventType string `json:"event_type" gorm:"type:varchar(32);index;not null"` // demote | upgrade | disable | enable | snapshot
	FromLevel int    `json:"from_level" gorm:"default:0"`                       // 0=Healthy, 1=L1, 2=L2, -1=Disabled
	ToLevel   int    `json:"to_level" gorm:"default:0"`
	Reason    string `json:"reason" gorm:"type:text"`
	Operator  string `json:"operator" gorm:"type:varchar(64);default:'auto'"` // auto | manual:{user_id}
	CreatedAt int64  `json:"created_at" gorm:"bigint;index;not null"`
}

// 事件类型常量
const (
	HealthEventDemote        = "demote"
	HealthEventLatencyDemote = "latency_demote"
	HealthEventUpgrade       = "upgrade"
	HealthEventDisable       = "disable"
	HealthEventEnable        = "enable"
	HealthEventSnapshot      = "snapshot"
)

// 等级常量（与 Channel.DegradeLevel 对齐）
const (
	HealthLevelHealthy  = 0
	HealthLevelL1       = 1
	HealthLevelL2       = 2
	HealthLevelDisabled = -1
)

// ComputeLevelFromStreak 根据连续错误/违规次数计算目标降级等级。
func ComputeLevelFromStreak(streak int64, base, step, maxLevel int) int {
	if streak < int64(base) {
		return 0
	}
	level := 1 + int(streak-int64(base))/step
	if level > maxLevel {
		return maxLevel
	}
	return level
}

// DegradeFactors 根据降级等级计算权重因子和优先级偏移。
func DegradeFactors(level, maxLevel int, minFactor float64) (factor float64, offset int) {
	if level <= 0 {
		return 1.0, 0
	}
	factor = 1.0 - float64(level)*((1.0-minFactor)/float64(maxLevel))
	if factor < minFactor {
		factor = minFactor
	}
	offset = -level
	return
}

func (ChannelHealthEvent) TableName() string {
	return "channel_health_events"
}

// RecordChannelHealthEvent 写入一条健康度审计事件。失败不阻塞主流程，调用方应在 goroutine 内调用并 recover。
func RecordChannelHealthEvent(channelId int, eventType string, fromLevel, toLevel int, reason, operator string) error {
	if operator == "" {
		operator = "auto"
	}
	evt := &ChannelHealthEvent{
		ChannelId: channelId,
		EventType: eventType,
		FromLevel: fromLevel,
		ToLevel:   toLevel,
		Reason:    reason,
		Operator:  operator,
		CreatedAt: time.Now().Unix(),
	}
	return DB.Create(evt).Error
}

// GetChannelHealthEvents 查询某渠道近 N 天的事件，按时间倒序。limit<=0 时不限。
func GetChannelHealthEvents(channelId int, days int, limit int) ([]ChannelHealthEvent, error) {
	if days <= 0 {
		days = 30
	}
	since := time.Now().AddDate(0, 0, -days).Unix()
	q := DB.Where("channel_id = ? AND created_at >= ?", channelId, since).
		Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var events []ChannelHealthEvent
	err := q.Find(&events).Error
	return events, err
}
