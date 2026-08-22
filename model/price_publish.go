package model

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// 价格变动发布批次（含 baseline）。
// 设计见 Docs/price-change-notify-design.md 第 5 节：发布制对账 diff，不挂保存钩子。
type PricePublishBatch struct {
	Id             int    `json:"id" gorm:"primaryKey"`
	CreatedAt      int64  `json:"created_at" gorm:"type:bigint;index"` // 发布时刻（unix 秒）= 生效告知时刻
	OperatorId     int    `json:"operator_id"`
	Note           string `json:"note" gorm:"type:text"`            // 管理员附言
	AffectedGroups string `json:"affected_groups" gorm:"type:text"` // JSON []string
	Summary        string `json:"summary" gorm:"type:text"`         // JSON {up,down,added,removed}
	// 发布时全部定价 key 的快照（map[optionKey]jsonString 的 JSON），下次 diff 的基准。
	// AutoMigrate 先建 text，MySQL 随后由 migratePricePublishSnapshotColumn 升级为 mediumtext。
	Snapshot    string `json:"-" gorm:"type:text"`
	IsBaseline  bool   `json:"is_baseline"`
	EmailState  string `json:"email_state" gorm:"type:varchar(16)"` // none/sending/done/partial
	EmailTotal  int    `json:"email_total"`
	EmailSent   int    `json:"email_sent"`
	EmailFailed int    `json:"email_failed"`
	EmailCursor int    `json:"email_cursor"` // 断点续发：已处理到的 user id
}

// 变动明细（不按分组打散，读取时应用层过滤）。
type PricePublishItem struct {
	Id        int     `json:"id" gorm:"primaryKey"`
	BatchId   int     `json:"batch_id" gorm:"index"`
	Scope     string  `json:"scope" gorm:"type:varchar(8)"` // group | model
	GroupName string  `json:"group_name" gorm:"type:varchar(191)"`
	ModelName string  `json:"model_name" gorm:"type:varchar(191)"`
	PriceType string  `json:"price_type" gorm:"type:varchar(32)"`
	OldValue  float64 `json:"old_value"`
	NewValue  float64 `json:"new_value"`
	Direction string  `json:"direction" gorm:"type:varchar(8)"` // up/down/added/removed
	// scope=model 时：JSON []string（来自 pricing enable_groups）；scope=group 时为受影响用户分组
	AffectedGroups string `json:"affected_groups" gorm:"type:text"`
}

const (
	PriceEmailStateNone    = "none"
	PriceEmailStateSending = "sending"
	PriceEmailStateDone    = "done"
	PriceEmailStatePartial = "partial"
)

// migratePricePublishSnapshotColumn 把 MySQL 下 snapshot 列从 text(64KB) 升到 mediumtext(16MB)，
// 防止大规模定价快照被截断。SQLite/PG 的 TEXT 无长度限制，无需处理。幂等。
func migratePricePublishSnapshotColumn() {
	if !common.UsingMySQL {
		return
	}
	tableName := "price_publish_batches"
	if !DB.Migrator().HasTable(tableName) {
		return
	}
	var dataType string
	if err := DB.Raw(`SELECT DATA_TYPE FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`,
		tableName, "snapshot").Scan(&dataType).Error; err != nil {
		common.SysLog(fmt.Sprintf("warning: failed to query %s.snapshot column type: %v", tableName, err))
		return
	}
	if strings.EqualFold(dataType, "mediumtext") {
		return
	}
	if err := DB.Exec(fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN snapshot MEDIUMTEXT", tableName)).Error; err != nil {
		common.SysLog(fmt.Sprintf("warning: failed to migrate %s.snapshot to mediumtext: %v", tableName, err))
	} else {
		common.SysLog(fmt.Sprintf("successfully migrated %s.snapshot to mediumtext", tableName))
	}
}

// InsertPricePublishBatch 事务写入批次与明细，回填 batch.Id 与 item.BatchId。
func InsertPricePublishBatch(batch *PricePublishBatch, items []PricePublishItem) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(batch).Error; err != nil {
			return err
		}
		for i := range items {
			items[i].BatchId = batch.Id
		}
		if len(items) > 0 {
			if err := tx.Create(&items).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func CountPricePublishBatches() (int64, error) {
	var total int64
	err := DB.Model(&PricePublishBatch{}).Count(&total).Error
	return total, err
}

// GetLatestPricePublishBatch 返回最新批次（含 baseline），用作下次 diff 的快照基准。
func GetLatestPricePublishBatch() (*PricePublishBatch, error) {
	var batch PricePublishBatch
	err := DB.Order("id desc").First(&batch).Error
	if err != nil {
		return nil, err
	}
	return &batch, nil
}

// GetPricePublishBatches 管理端分页列表（含 baseline，倒序，不带 Snapshot 大字段）。
func GetPricePublishBatches(startIdx int, num int) ([]*PricePublishBatch, int64, error) {
	var batches []*PricePublishBatch
	var total int64
	if err := DB.Model(&PricePublishBatch{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := DB.Omit("snapshot").Order("id desc").Limit(num).Offset(startIdx).Find(&batches).Error
	return batches, total, err
}

func GetPricePublishBatchById(id int) (*PricePublishBatch, error) {
	var batch PricePublishBatch
	err := DB.Omit("snapshot").First(&batch, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &batch, nil
}

func GetPricePublishItemsByBatchId(batchId int) ([]PricePublishItem, error) {
	var items []PricePublishItem
	err := DB.Where("batch_id = ?", batchId).Order("id asc").Find(&items).Error
	return items, err
}

// GetRecentPricePublishBatches 用户侧查询：近 sinceTs 之后发布、非 baseline 的批次，倒序。
func GetRecentPricePublishBatches(sinceTs int64) ([]*PricePublishBatch, error) {
	var batches []*PricePublishBatch
	falseVal := false
	err := DB.Omit("snapshot").
		Where("created_at >= ? AND is_baseline = ?", sinceTs, falseVal).
		Order("id desc").Find(&batches).Error
	return batches, err
}

// GetSendingPricePublishBatches 启动恢复：所有 email_state=sending 的批次。
func GetSendingPricePublishBatches() ([]*PricePublishBatch, error) {
	var batches []*PricePublishBatch
	err := DB.Where("email_state = ?", PriceEmailStateSending).Find(&batches).Error
	return batches, err
}

// UpdatePricePublishBatchEmailProgress 回写邮件任务进度。
func UpdatePricePublishBatchEmailProgress(batchId int, sent int, failed int, cursor int) error {
	return DB.Model(&PricePublishBatch{}).Where("id = ?", batchId).Updates(map[string]interface{}{
		"email_sent":   sent,
		"email_failed": failed,
		"email_cursor": cursor,
	}).Error
}

// FinishPricePublishBatchEmail 结束邮件任务并落终态。
func FinishPricePublishBatchEmail(batchId int, state string, sent int, failed int, cursor int) error {
	return DB.Model(&PricePublishBatch{}).Where("id = ?", batchId).Updates(map[string]interface{}{
		"email_state":  state,
		"email_sent":   sent,
		"email_failed": failed,
		"email_cursor": cursor,
	}).Error
}

// GetPriceNotifyUsersBatch 按 id 游标分页取受影响分组内可邮件触达的用户。
// allGroups=true 时不过滤分组（受影响分组含 "all" 的场景）。
func GetPriceNotifyUsersBatch(groups []string, allGroups bool, afterId int, limit int) ([]*User, error) {
	var users []*User
	query := DB.Model(&User{}).
		Select("id", "email", commonGroupCol, "setting").
		Where("status = ? AND email != '' AND id > ?", common.UserStatusEnabled, afterId)
	if !allGroups {
		query = query.Where(commonGroupCol+" IN ?", groups)
	}
	err := query.Order("id asc").Limit(limit).Find(&users).Error
	return users, err
}

// CountPriceNotifyUsers 预计邮件触达人数（未扣除偏好关闭者，偏好存 JSON 无法跨库 SQL 过滤）。
func CountPriceNotifyUsers(groups []string, allGroups bool) (int64, error) {
	var total int64
	query := DB.Model(&User{}).
		Where("status = ? AND email != ''", common.UserStatusEnabled)
	if !allGroups {
		query = query.Where(commonGroupCol+" IN ?", groups)
	}
	err := query.Count(&total).Error
	return total, err
}
