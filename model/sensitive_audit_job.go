package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SensitiveAuditJobPending = iota + 1
	SensitiveAuditJobProcessing
	SensitiveAuditJobSucceeded
	SensitiveAuditJobDead
)

const (
	SensitiveAuditDumpReady = iota + 1
	SensitiveAuditDumpDeleting
	SensitiveAuditDumpDeleted
)

const SensitiveAuditMaxAttempts = 5

// SensitiveAuditJobRecord is the durable source of work for a sensitive audit
// dump. The dump remains on the node-local persistent volume, so StorageNode
// is part of the recovery boundary.
type SensitiveAuditJobRecord struct {
	JobID           string `gorm:"column:job_id;type:varchar(64);primaryKey"`
	StorageNode     string `gorm:"type:varchar(128);not null;index:idx_sensitive_audit_claim,priority:1"`
	Status          int    `gorm:"not null;index:idx_sensitive_audit_claim,priority:2"`
	AvailableAt     int64  `gorm:"not null;index:idx_sensitive_audit_claim,priority:3"`
	LeaseOwner      string `gorm:"type:varchar(64);not null;default:''"`
	LeaseToken      string `gorm:"type:varchar(64);not null;default:''"`
	LeaseUntil      int64  `gorm:"not null;default:0;index"`
	LeaseGeneration int64  `gorm:"not null;default:0"`
	Attempts        int    `gorm:"not null;default:0"`
	LastError       string `gorm:"type:text"`
	CompletedAt     int64  `gorm:"not null;default:0"`

	RequestID   string `gorm:"type:varchar(64);index;not null;default:''"`
	UserID      int    `gorm:"index"`
	Username    string `gorm:"type:varchar(255);not null;default:''"`
	TokenID     int    `gorm:"index;not null;default:0"`
	TokenName   string `gorm:"type:varchar(128);not null;default:''"`
	ChannelID   int    `gorm:"index;not null;default:0"`
	ChannelName string `gorm:"type:varchar(255);not null;default:''"`
	ModelName   string `gorm:"type:varchar(128);index;not null;default:''"`
	Path        string `gorm:"type:varchar(255);not null;default:''"`
	IP          string `gorm:"column:ip;type:varchar(64);index;not null;default:''"`
	UserAgent   string `gorm:"type:varchar(512);not null;default:''"`

	DumpPath       string `gorm:"type:varchar(512);not null"`
	BodySHA256     string `gorm:"column:body_sha256;type:varchar(64);index;not null"`
	BodySize       int64  `gorm:"not null"`
	DumpState      int    `gorm:"not null;index"`
	DumpLeaseOwner string `gorm:"type:varchar(64);not null;default:''"`
	DumpLeaseToken string `gorm:"type:varchar(64);not null;default:''"`
	DumpLeaseUntil int64  `gorm:"not null;default:0"`
	DumpDeletedAt  int64  `gorm:"not null;default:0"`

	CreatedAt int64 `gorm:"not null;index"`
	UpdatedAt int64 `gorm:"not null"`
}

func (SensitiveAuditJobRecord) TableName() string {
	return "sensitive_audit_jobs"
}

func CheckSensitiveAuditSchemaReady() error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	migrator := DB.Migrator()
	checks := []struct {
		ready bool
		name  string
	}{
		{migrator.HasTable(&SensitiveAuditJobRecord{}), "table sensitive_audit_jobs"},
		{migrator.HasColumn(&SensitiveBlockLog{}, "audit_job_id"), "column sensitive_block_logs.audit_job_id"},
		{migrator.HasIndex(&SensitiveBlockLog{}, "idx_sensitive_audit_job_word"), "index idx_sensitive_audit_job_word"},
		{migrator.HasIndex(&SensitiveAuditJobRecord{}, "idx_sensitive_audit_claim"), "index idx_sensitive_audit_claim"},
	}
	for _, check := range checks {
		if !check.ready {
			return fmt.Errorf("sensitive audit schema is not ready: missing %s", check.name)
		}
	}
	return nil
}

func (job *SensitiveAuditJobRecord) BeforeCreate(_ *gorm.DB) error {
	now := time.Now().Unix()
	if job.CreatedAt == 0 {
		job.CreatedAt = now
	}
	if job.UpdatedAt == 0 {
		job.UpdatedAt = job.CreatedAt
	}
	if job.AvailableAt == 0 {
		job.AvailableAt = job.CreatedAt
	}
	if job.Status == 0 {
		job.Status = SensitiveAuditJobPending
	}
	if job.DumpState == 0 {
		job.DumpState = SensitiveAuditDumpReady
	}
	return nil
}

// InsertSensitiveAuditJob admits a job once. If the INSERT result is
// ambiguous, the stable JobID and immutable dump identity disambiguate it.
func InsertSensitiveAuditJob(job *SensitiveAuditJobRecord) error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	if job == nil || job.JobID == "" || job.StorageNode == "" || job.DumpPath == "" || job.BodySHA256 == "" {
		return errors.New("sensitive audit job identity is incomplete")
	}
	err := DB.Create(job).Error
	if err == nil {
		return nil
	}

	var existing SensitiveAuditJobRecord
	if lookupErr := DB.Where("job_id = ?", job.JobID).First(&existing).Error; lookupErr != nil {
		return fmt.Errorf("insert sensitive audit job %s: %w", job.JobID, err)
	}
	if existing.StorageNode != job.StorageNode ||
		existing.DumpPath != job.DumpPath ||
		existing.BodySHA256 != job.BodySHA256 ||
		existing.BodySize != job.BodySize {
		return fmt.Errorf("sensitive audit job %s immutable identity mismatch", job.JobID)
	}
	return nil
}

func GetSensitiveAuditJob(jobID string) (*SensitiveAuditJobRecord, error) {
	if DB == nil {
		return nil, errors.New("database is not initialized")
	}
	var job SensitiveAuditJobRecord
	if err := DB.Where("job_id = ?", jobID).First(&job).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

// ListRecoverableSensitiveAuditJobs returns node-local pending work and
// processing work whose lease has expired. Callers must still win TryClaim.
func ListRecoverableSensitiveAuditJobs(storageNode string, now int64, limit int) ([]SensitiveAuditJobRecord, error) {
	if DB == nil {
		return nil, errors.New("database is not initialized")
	}
	if storageNode == "" {
		return nil, errors.New("sensitive audit storage node is empty")
	}
	if limit <= 0 {
		limit = 100
	}
	var jobs []SensitiveAuditJobRecord
	err := DB.
		Where(
			"storage_node = ? AND ((status = ? AND available_at <= ?) OR (status = ? AND lease_until <= ?))",
			storageNode,
			SensitiveAuditJobPending,
			now,
			SensitiveAuditJobProcessing,
			now,
		).
		Order("available_at asc, job_id asc").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

// TryClaimSensitiveAuditJob uses a conditional UPDATE instead of
// SKIP LOCKED so the same claim protocol works on SQLite, MySQL and PostgreSQL.
func TryClaimSensitiveAuditJob(
	jobID string,
	storageNode string,
	leaseOwner string,
	leaseToken string,
	now int64,
	leaseUntil int64,
) (bool, error) {
	if DB == nil {
		return false, errors.New("database is not initialized")
	}
	if jobID == "" || storageNode == "" || leaseOwner == "" || leaseToken == "" {
		return false, errors.New("sensitive audit claim identity is incomplete")
	}
	if leaseUntil <= now {
		return false, errors.New("sensitive audit lease must expire after claim time")
	}
	result := DB.Model(&SensitiveAuditJobRecord{}).
		Where(
			"job_id = ? AND storage_node = ? AND ((status = ? AND available_at <= ?) OR (status = ? AND lease_until <= ?))",
			jobID,
			storageNode,
			SensitiveAuditJobPending,
			now,
			SensitiveAuditJobProcessing,
			now,
		).
		Updates(map[string]any{
			"status":           SensitiveAuditJobProcessing,
			"lease_owner":      leaseOwner,
			"lease_token":      leaseToken,
			"lease_until":      leaseUntil,
			"lease_generation": gorm.Expr("lease_generation + 1"),
			"attempts":         gorm.Expr("attempts + 1"),
			"updated_at":       now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

type SensitiveAuditRuleVersion struct {
	ID        int
	Pattern   string
	IsRegex   bool
	Action    int
	UpdatedAt int64
}

// CompleteSensitiveAuditJob atomically commits all main-DB effects and the
// durable terminal. Redis cache propagation is deliberately outside this
// transaction and remains part of the shared cache-version work in M3-0.
func CompleteSensitiveAuditJob(
	jobID string,
	leaseToken string,
	tokenID int,
	hits []*SensitiveBlockLog,
	rules []SensitiveAuditRuleVersion,
	now int64,
) error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	if jobID == "" || leaseToken == "" {
		return errors.New("sensitive audit completion identity is incomplete")
	}
	if len(hits) != len(rules) {
		return errors.New("sensitive audit hits and rule versions differ")
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		var job SensitiveAuditJobRecord
		if err := tx.
			Where("job_id = ? AND status = ? AND lease_token = ?", jobID, SensitiveAuditJobProcessing, leaseToken).
			First(&job).Error; err != nil {
			return err
		}

		disableToken := false
		for _, hit := range hits {
			if hit.Action == SensitiveActionBlock {
				disableToken = true
				break
			}
		}
		if disableToken && tokenID > 0 {
			// A token deleted before the audit finishes cannot be used again;
			// its absence is therefore already a safe terminal.
			tokenDisabled := false
			var token Token
			tokenQuery := tx.Select("id", "status").Where("id = ?", tokenID)
			if !common.UsingSQLite {
				tokenQuery = tokenQuery.Clauses(clause.Locking{Strength: "UPDATE"})
			}
			err := tokenQuery.First(&token).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil {
				tokenDisabled = true
				if token.Status != common.TokenStatusDisabled {
					result := tx.Model(&Token{}).
						Where("id = ?", tokenID).
						Update("status", common.TokenStatusDisabled)
					if result.Error != nil {
						return result.Error
					}
					if result.RowsAffected != 1 {
						return errors.New("sensitive audit token changed during completion")
					}
				}
			}
			for _, hit := range hits {
				if hit.Action == SensitiveActionBlock {
					hit.TokenDisabled = tokenDisabled
				}
			}
		}

		for index, hit := range hits {
			rule := rules[index]
			if hit == nil || hit.AuditJobId == nil || *hit.AuditJobId != jobID || hit.MatchedWordId != rule.ID {
				return errors.New("sensitive audit hit identity is invalid")
			}
			var matchedRule SensitiveWord
			if err := tx.
				Where(
					"id = ? AND enabled = ? AND updated_at = ? AND pattern = ? AND is_regex = ? AND action = ?",
					rule.ID,
					true,
					rule.UpdatedAt,
					rule.Pattern,
					rule.IsRegex,
					rule.Action,
				).
				First(&matchedRule).Error; err != nil {
				return fmt.Errorf("sensitive audit rule %d changed before completion: %w", rule.ID, err)
			}
			if err := tx.Create(hit).Error; err != nil {
				return err
			}
			result := tx.Model(&SensitiveWord{}).
				Where(
					"id = ? AND enabled = ? AND updated_at = ? AND pattern = ? AND is_regex = ? AND action = ?",
					rule.ID,
					true,
					rule.UpdatedAt,
					rule.Pattern,
					rule.IsRegex,
					rule.Action,
				).
				UpdateColumns(map[string]any{
					"hit_count":   gorm.Expr("hit_count + 1"),
					"last_hit_at": now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("sensitive audit rule %d changed during completion", rule.ID)
			}
		}

		result := tx.Model(&SensitiveAuditJobRecord{}).
			Where("job_id = ? AND status = ? AND lease_token = ?", jobID, SensitiveAuditJobProcessing, leaseToken).
			Updates(map[string]any{
				"status":       SensitiveAuditJobSucceeded,
				"completed_at": now,
				"lease_until":  0,
				"updated_at":   now,
				"last_error":   "",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("sensitive audit completion lost its lease")
		}
		return nil
	})
	if err == nil {
		return nil
	}

	// A commit can succeed server-side while its acknowledgement is lost. The
	// durable terminal disambiguates that result without replaying effects.
	job, lookupErr := GetSensitiveAuditJob(jobID)
	if lookupErr == nil && job.Status == SensitiveAuditJobSucceeded {
		return nil
	}
	return err
}

func RetrySensitiveAuditJob(jobID string, leaseToken string, nextAttemptAt int64, lastError string) error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var job SensitiveAuditJobRecord
		if err := tx.
			Where("job_id = ? AND status = ? AND lease_token = ?", jobID, SensitiveAuditJobProcessing, leaseToken).
			First(&job).Error; err != nil {
			var current SensitiveAuditJobRecord
			lookupErr := tx.Where("job_id = ?", jobID).First(&current).Error
			if lookupErr == nil && current.Status == SensitiveAuditJobSucceeded {
				return nil
			}
			return err
		}
		status := SensitiveAuditJobPending
		if job.Attempts >= SensitiveAuditMaxAttempts {
			status = SensitiveAuditJobDead
		}
		result := tx.Model(&SensitiveAuditJobRecord{}).
			Where("job_id = ? AND status = ? AND lease_token = ?", jobID, SensitiveAuditJobProcessing, leaseToken).
			Updates(map[string]any{
				"status":       status,
				"available_at": nextAttemptAt,
				"lease_owner":  "",
				"lease_token":  "",
				"lease_until":  0,
				"last_error":   lastError,
				"updated_at":   time.Now().Unix(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("sensitive audit retry lost its lease")
		}
		return nil
	})
}

func CountUnfinishedSensitiveAuditJobs(storageNode string) (int64, error) {
	if DB == nil {
		return 0, errors.New("database is not initialized")
	}
	var count int64
	err := DB.Model(&SensitiveAuditJobRecord{}).
		Where("storage_node = ? AND status <> ?", storageNode, SensitiveAuditJobSucceeded).
		Count(&count).Error
	return count, err
}

func ListDeletableSensitiveAuditJobs(storageNode string, cutoff int64, now int64, limit int) ([]SensitiveAuditJobRecord, error) {
	if DB == nil {
		return nil, errors.New("database is not initialized")
	}
	if storageNode == "" {
		return nil, errors.New("sensitive audit storage node is empty")
	}
	if limit <= 0 {
		limit = 100
	}
	var jobs []SensitiveAuditJobRecord
	err := DB.
		Where(
			"storage_node = ? AND status = ? AND created_at <= ? AND (dump_state = ? OR (dump_state = ? AND dump_lease_until <= ?))",
			storageNode,
			SensitiveAuditJobSucceeded,
			cutoff,
			SensitiveAuditDumpReady,
			SensitiveAuditDumpDeleting,
			now,
		).
		Order("created_at asc, job_id asc").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

func TryClaimSensitiveAuditDump(
	jobID string,
	storageNode string,
	leaseOwner string,
	leaseToken string,
	cutoff int64,
	now int64,
	leaseUntil int64,
) (bool, error) {
	if DB == nil {
		return false, errors.New("database is not initialized")
	}
	if jobID == "" || storageNode == "" || leaseOwner == "" || leaseToken == "" {
		return false, errors.New("sensitive audit dump claim identity is incomplete")
	}
	result := DB.Model(&SensitiveAuditJobRecord{}).
		Where(
			"job_id = ? AND storage_node = ? AND status = ? AND created_at <= ? AND (dump_state = ? OR (dump_state = ? AND dump_lease_until <= ?))",
			jobID,
			storageNode,
			SensitiveAuditJobSucceeded,
			cutoff,
			SensitiveAuditDumpReady,
			SensitiveAuditDumpDeleting,
			now,
		).
		Updates(map[string]any{
			"dump_state":       SensitiveAuditDumpDeleting,
			"dump_lease_owner": leaseOwner,
			"dump_lease_token": leaseToken,
			"dump_lease_until": leaseUntil,
			"updated_at":       now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func CompleteSensitiveAuditDumpDeletion(jobID string, leaseToken string, now int64) error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&SensitiveAuditJobRecord{}).
			Where("job_id = ? AND dump_state = ? AND dump_lease_token = ?", jobID, SensitiveAuditDumpDeleting, leaseToken).
			Updates(map[string]any{
				"dump_state":       SensitiveAuditDumpDeleted,
				"dump_deleted_at":  now,
				"dump_lease_owner": "",
				"dump_lease_token": "",
				"dump_lease_until": 0,
				"updated_at":       now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("sensitive audit dump deletion lost its lease")
		}
		return tx.Model(&SensitiveBlockLog{}).
			Where("audit_job_id = ?", jobID).
			Update("dump_exists", false).Error
	})
}

func HasSensitiveAuditJobForDumpPath(path string) (bool, error) {
	if DB == nil {
		return false, errors.New("database is not initialized")
	}
	var count int64
	err := DB.Model(&SensitiveAuditJobRecord{}).Where("dump_path = ?", path).Count(&count).Error
	return count > 0, err
}
