package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

const (
	IQTestRunStatusRunning    = "running"
	IQTestRunStatusFinished   = "finished"
	IQTestRunStatusPartial    = "partial"
	IQTestRunStatusAborted    = "aborted"
	IQTestRunStatusFailed     = "failed"
	IQTestRunTriggerSchedule  = "schedule"
	IQTestRunTriggerRunNow    = "run_now"
	IQTestRunTriggerReconcile = "reconcile"
)

// IQTestRun records a complete detection round and its immutable configuration snapshot.
type IQTestRun struct {
	Id             int64   `json:"id" gorm:"primaryKey;autoIncrement"`
	RunID          string  `json:"run_id" gorm:"column:run_id;type:varchar(64);not null;uniqueIndex"`
	IdempotencyKey *string `json:"idempotency_key,omitempty" gorm:"column:idempotency_key;type:varchar(128);uniqueIndex"`
	Status         string  `json:"status" gorm:"type:varchar(16);not null;index"`
	Trigger        string  `json:"trigger" gorm:"column:trigger;type:varchar(16);not null;index"`
	OperatorID     int     `json:"operator_id" gorm:"column:operator_id;not null;default:0"`
	ConfigSnapshot string  `json:"config_snapshot" gorm:"column:config_snapshot;type:text"`
	ErrorMessage   string  `json:"error_message,omitempty" gorm:"type:text"`
	ChannelID      int     `json:"channel_id" gorm:"not null;default:0"`
	BankVersion    string  `json:"bank_version" gorm:"column:bank_version;type:varchar(64)"`
	CandidateCount int     `json:"candidate_count" gorm:"column:candidate_count;not null;default:0"`
	SuccessCount   int     `json:"success_count" gorm:"column:success_count;not null;default:0"`
	InvalidCount   int     `json:"invalid_count" gorm:"column:invalid_count;not null;default:0"`
	ErrorCount     int     `json:"error_count" gorm:"column:error_count;not null;default:0"`
	StartedAt      int64   `json:"started_at" gorm:"column:started_at;type:bigint;not null;index"`
	FinishedAt     int64   `json:"finished_at" gorm:"column:finished_at;type:bigint;index"`
	OwnerID        string  `json:"owner_id" gorm:"column:owner_id;type:varchar(128);index"`
	LeaseExpiresAt int64   `json:"lease_expires_at" gorm:"column:lease_expires_at;type:bigint;index"`
	CreatedAt      int64   `json:"created_at" gorm:"column:created_at;type:bigint;not null;index"`
}

func (IQTestRun) TableName() string { return "iq_test_runs" }

func NewIQTestRun(runID, trigger, ownerID string, operatorID int, configSnapshot, bankVersion string) *IQTestRun {
	now := time.Now().Unix()
	return &IQTestRun{RunID: runID, Trigger: trigger, OwnerID: ownerID, OperatorID: operatorID,
		ConfigSnapshot: configSnapshot, BankVersion: bankVersion, Status: IQTestRunStatusRunning,
		StartedAt: now, CreatedAt: now}
}

func CreateIQTestRun(run *IQTestRun) error {
	if run == nil {
		return gorm.ErrInvalidData
	}
	return DB.Create(run).Error
}

func GetIQTestRunByIdempotencyKey(key string) (*IQTestRun, error) {
	if key == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var run IQTestRun
	if err := DB.Where("idempotency_key = ?", key).First(&run).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

func GetIQTestRun(runID string) (*IQTestRun, error) {
	var run IQTestRun
	if err := DB.Where("run_id = ?", runID).First(&run).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

func UpdateIQTestRun(runID string, updates map[string]any) error {
	return DB.Model(&IQTestRun{}).Where("run_id = ?", runID).Updates(updates).Error
}

// ClaimIQTestRun acquires or takes over an expired lease atomically. RowsAffected
// is false when another owner still holds a live lease or the run is not running.
func ClaimIQTestRun(runID, ownerID string, now, leaseExpiresAt int64) (bool, error) {
	if runID == "" || ownerID == "" {
		return false, gorm.ErrInvalidData
	}
	res := DB.Model(&IQTestRun{}).Where("run_id = ? AND status = ? AND (owner_id = '' OR owner_id IS NULL OR lease_expires_at <= ?)",
		runID, IQTestRunStatusRunning, now).Updates(map[string]any{"owner_id": ownerID, "lease_expires_at": leaseExpiresAt})
	return res.RowsAffected == 1, res.Error
}

func RenewIQTestRunLease(runID, ownerID string, leaseExpiresAt int64) (bool, error) {
	if runID == "" || ownerID == "" {
		return false, gorm.ErrInvalidData
	}
	res := DB.Model(&IQTestRun{}).Where("run_id = ? AND status = ? AND owner_id = ? AND lease_expires_at > ?", runID, IQTestRunStatusRunning, ownerID, time.Now().Unix()).
		Update("lease_expires_at", leaseExpiresAt)
	return res.RowsAffected == 1, res.Error
}

func AbortExpiredIQTestRuns(now int64) error {
	return DB.Model(&IQTestRun{}).Where("status = ? AND lease_expires_at <= ?", IQTestRunStatusRunning, now).
		Updates(map[string]any{"status": IQTestRunStatusAborted, "finished_at": now, "error_message": "executor lease expired"}).Error
}

// LastScheduledIQTestRunAt returns when the most recent scheduled round started,
// or 0 when none exists, so a restart does not postpone the cadence.
// The trigger column is a MySQL reserved word; the map form lets GORM quote it
// per dialect (same pattern as the iq_test_leases `key` column).
func LastScheduledIQTestRunAt() (int64, error) {
	var run IQTestRun
	err := DB.Where(map[string]any{"trigger": IQTestRunTriggerSchedule}).Order("id DESC").First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if run.StartedAt > 0 {
		return run.StartedAt, nil
	}
	return run.CreatedAt, nil
}

func GetActiveIQTestRun() (*IQTestRun, error) {
	var run IQTestRun
	err := DB.Where("status = ? AND lease_expires_at > ?", IQTestRunStatusRunning, time.Now().Unix()).Order("id DESC").First(&run).Error
	return &run, err
}

func FinalizeIQTestRun(runID, status string, candidate, success, invalid, failed int, finishedAt int64) error {
	if finishedAt == 0 {
		finishedAt = time.Now().Unix()
	}
	return DB.Model(&IQTestRun{}).Where("run_id = ? AND status = ?", runID, IQTestRunStatusRunning).
		Updates(map[string]any{"status": status, "candidate_count": candidate, "success_count": success,
			"invalid_count": invalid, "error_count": failed, "finished_at": finishedAt}).Error
}

func ListIQTestRuns(page, pageSize int) ([]IQTestRun, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	var total int64
	if err := DB.Model(&IQTestRun{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]IQTestRun, 0)
	err := DB.Order("started_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error
	return rows, total, err
}
