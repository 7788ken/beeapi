package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	AsyncTaskRefundKindTask       = "task"
	AsyncTaskRefundKindMidjourney = "midjourney"
	AsyncTaskRefundStatusRefunded = "refunded"
	AsyncTaskBillingSourceUnknown = "unknown"
)

// AsyncTaskRefundRecord is the durable main-database proof that a terminal
// async task refund committed. LOG_DB may be a separate database, so user logs
// cannot be the atomic idempotency authority for financial changes.
type AsyncTaskRefundRecord struct {
	Id int64 `json:"id" gorm:"primaryKey;autoIncrement"`

	TaskKind string `json:"task_kind" gorm:"type:varchar(32);not null;uniqueIndex:idx_async_task_refund_identity,priority:1"`
	TaskDbId int64  `json:"task_db_id" gorm:"not null;uniqueIndex:idx_async_task_refund_identity,priority:2"`
	TaskId   string `json:"task_id" gorm:"type:varchar(191);not null;index"`

	UserId             int    `json:"user_id" gorm:"not null;index"`
	ChannelId          int    `json:"channel_id" gorm:"not null;default:0;index"`
	TokenId            int    `json:"token_id" gorm:"not null;default:0;index"`
	UserSubscriptionId int    `json:"user_subscription_id" gorm:"not null;default:0;index"`
	BillingSource      string `json:"billing_source" gorm:"type:varchar(32);not null"`
	Quota              int    `json:"quota" gorm:"not null"`
	Reason             string `json:"reason" gorm:"type:text"`
	Status             string `json:"status" gorm:"type:varchar(32);not null"`
	CreatedAt          int64  `json:"created_at" gorm:"not null;index"`
}

func (r *AsyncTaskRefundRecord) BeforeCreate(tx *gorm.DB) error {
	if r.CreatedAt == 0 {
		r.CreatedAt = common.GetTimestamp()
	}
	return nil
}

func normalizeAsyncTaskBillingSource(source string, subscriptionId int) string {
	if strings.TrimSpace(source) == "subscription" && subscriptionId > 0 {
		return "subscription"
	}
	if strings.TrimSpace(source) == "wallet" && subscriptionId == 0 {
		return "wallet"
	}
	return AsyncTaskBillingSourceUnknown
}

func applyAsyncTaskRefundFundingTx(tx *gorm.DB, record *AsyncTaskRefundRecord) error {
	if tx == nil || record == nil || record.UserId <= 0 || record.Quota <= 0 {
		return errors.New("invalid async task refund")
	}
	switch record.BillingSource {
	case "subscription":
		if record.UserSubscriptionId <= 0 {
			return errors.New("async task subscription refund source is invalid")
		}
		result := tx.Model(&UserSubscription{}).
			Where(
				"id = ? AND user_id = ? AND amount_used >= ?",
				record.UserSubscriptionId,
				record.UserId,
				record.Quota,
			).
			Update("amount_used", gorm.Expr("amount_used - ?", record.Quota))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("async task subscription refund rejected")
		}
	case "wallet":
		if record.UserSubscriptionId != 0 {
			return errors.New("async task wallet refund source is invalid")
		}
		result := tx.Unscoped().Model(&User{}).
			Where("id = ?", record.UserId).
			Update("quota", gorm.Expr("quota + ?", record.Quota))
		if result.Error != nil {
			return fmt.Errorf("refund async task user quota: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("refund async task user quota: user %d not found", record.UserId)
		}
	default:
		return errors.New("async task refund billing source is unknown")
	}
	if record.TokenId > 0 {
		return adjustTokenQuotaTx(tx, record.TokenId, record.UserId, -record.Quota)
	}
	return nil
}

func createAsyncTaskRefundRecordTx(tx *gorm.DB, record *AsyncTaskRefundRecord) error {
	if tx == nil || record == nil || record.TaskDbId <= 0 || record.TaskId == "" {
		return errors.New("invalid async task refund record")
	}
	return tx.Create(record).Error
}

// UpdateFailureWithRefund atomically commits a Task FAILURE transition,
// durable refund proof, and all funding-source/token changes. A lost CAS
// returns won=false and performs no financial writes.
func (t *Task) UpdateFailureWithRefund(fromStatus TaskStatus) (won bool, record *AsyncTaskRefundRecord, err error) {
	if t == nil || t.ID <= 0 || t.UserId <= 0 {
		return false, nil, errors.New("invalid task refund target")
	}
	if t.Status != TaskStatusFailure {
		return false, nil, errors.New("task refund requires FAILURE terminal")
	}
	if fromStatus == TaskStatusFailure || fromStatus == TaskStatusSuccess {
		return false, nil, nil
	}
	if t.Quota <= 0 {
		return false, nil, errors.New("task refund quota must be positive")
	}

	taskId := strings.TrimSpace(t.TaskID)
	if taskId == "" {
		taskId = fmt.Sprintf("task-db-%d", t.ID)
	}
	refundRecord := &AsyncTaskRefundRecord{
		TaskKind:           AsyncTaskRefundKindTask,
		TaskDbId:           t.ID,
		TaskId:             taskId,
		UserId:             t.UserId,
		ChannelId:          t.ChannelId,
		TokenId:            t.PrivateData.TokenId,
		UserSubscriptionId: t.PrivateData.SubscriptionId,
		BillingSource: normalizeAsyncTaskBillingSource(
			t.PrivateData.BillingSource,
			t.PrivateData.SubscriptionId,
		),
		Quota:  t.Quota,
		Reason: t.FailReason,
		Status: AsyncTaskRefundStatusRefunded,
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Task{}).
			Where("id = ? AND status = ?", t.ID, fromStatus).
			Select("*").
			Updates(t)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		won = true
		if err := createAsyncTaskRefundRecordTx(tx, refundRecord); err != nil {
			return err
		}
		return applyAsyncTaskRefundFundingTx(tx, refundRecord)
	})
	if err != nil {
		return false, nil, err
	}
	if !won {
		return false, nil, nil
	}
	return true, refundRecord, nil
}
