package model

import (
	"time"

	"gorm.io/gorm"
)

// AffiliateCommissionDaily 推荐分成日结记录。
// 每个 (date, inviter_id, invitee_id) 唯一，cron 任务保证同一天不重复结算。
type AffiliateCommissionDaily struct {
	Id           int    `json:"id" gorm:"primaryKey"`
	Date         string `json:"date" gorm:"type:varchar(10);not null;uniqueIndex:uniq_aff_commission_daily,priority:1;index"`
	InviterId    int    `json:"inviter_id" gorm:"not null;uniqueIndex:uniq_aff_commission_daily,priority:2;index"`
	InviteeId    int    `json:"invitee_id" gorm:"not null;uniqueIndex:uniq_aff_commission_daily,priority:3;index"`
	InviteeQuota int    `json:"invitee_quota" gorm:"not null;default:0"`
	Commission   int    `json:"commission" gorm:"not null;default:0"`
	CreatedAt    int64  `json:"created_at" gorm:"index"`
}

// SaveAffiliateCommissionDaily 写入一天的分成记录；唯一索引兜底防重复。
func SaveAffiliateCommissionDaily(records []AffiliateCommissionDaily) error {
	if len(records) == 0 {
		return nil
	}
	now := time.Now().Unix()
	for i := range records {
		if records[i].CreatedAt == 0 {
			records[i].CreatedAt = now
		}
	}
	return DB.Create(&records).Error
}

// HasAffiliateCommissionForDate 检查某天是否已结算过（用于 cron 幂等）。
func HasAffiliateCommissionForDate(date string) (bool, error) {
	var cnt int64
	err := DB.Model(&AffiliateCommissionDaily{}).Where("date = ?", date).Count(&cnt).Error
	if err != nil {
		return false, err
	}
	return cnt > 0, nil
}

// AffiliateCommissionStat 单个被推荐人累计分成统计。
type AffiliateCommissionStat struct {
	InviteeId       int `json:"invitee_id"`
	TotalCommission int `json:"total_commission"`
	TotalConsume    int `json:"total_consume"`
}

// GetCommissionStatsByInviter 按推荐人聚合每个被推荐人的累计分成 + 累计被分成的消费量。
func GetCommissionStatsByInviter(inviterId int) ([]AffiliateCommissionStat, error) {
	var stats []AffiliateCommissionStat
	err := DB.Model(&AffiliateCommissionDaily{}).
		Select("invitee_id, SUM(commission) AS total_commission, SUM(invitee_quota) AS total_consume").
		Where("inviter_id = ?", inviterId).
		Group("invitee_id").
		Find(&stats).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	return stats, nil
}
