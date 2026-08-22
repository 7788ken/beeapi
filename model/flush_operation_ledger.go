package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	flushOperationResultBatchUpdated     = "batch-updated"
	flushOperationResultBatchDiscarded   = "batch-discarded"
	flushOperationResultQuotaDataWritten = "quota-data-written"
	flushOperationCleanupChunkSize       = 200
)

// FlushOperationLedger makes an in-memory flush operation replayable after an
// ambiguous database result. The ledger row and the business mutation are
// committed in the same transaction.
type FlushOperationLedger struct {
	OperationID string `gorm:"column:operation_id;type:varchar(64);primaryKey;uniqueIndex:idx_flush_operation_identity"`
	Scope       string `gorm:"type:varchar(32);not null"`
	PayloadHash string `gorm:"column:payload_hash;type:char(64);not null"`
	ResultCode  string `gorm:"column:result_code;type:varchar(32);not null;default:''"`
	AppliedAt   int64  `gorm:"column:applied_at;type:bigint;not null"`
}

func (FlushOperationLedger) TableName() string {
	return "flush_operation_ledgers"
}

func CheckFlushOperationSchemaReady() error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	migrator := DB.Migrator()
	checks := []struct {
		ready bool
		name  string
	}{
		{migrator.HasTable(&FlushOperationLedger{}), "table flush_operation_ledgers"},
		{migrator.HasColumn(&FlushOperationLedger{}, "operation_id"), "column flush_operation_ledgers.operation_id"},
		{migrator.HasColumn(&FlushOperationLedger{}, "scope"), "column flush_operation_ledgers.scope"},
		{migrator.HasColumn(&FlushOperationLedger{}, "payload_hash"), "column flush_operation_ledgers.payload_hash"},
		{migrator.HasColumn(&FlushOperationLedger{}, "result_code"), "column flush_operation_ledgers.result_code"},
		{migrator.HasColumn(&FlushOperationLedger{}, "applied_at"), "column flush_operation_ledgers.applied_at"},
		{migrator.HasIndex(&FlushOperationLedger{}, "idx_flush_operation_identity"), "index idx_flush_operation_identity"},
	}
	for _, check := range checks {
		if !check.ready {
			return fmt.Errorf("flush operation schema is not ready: missing %s", check.name)
		}
	}
	return nil
}

func newFlushOperationID(prefix string) string {
	return prefix + "-" + common.GetUUID()
}

func flushOperationPayloadHash(payload any) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode flush operation payload: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func applyFlushOperation(
	ctx context.Context,
	operationID string,
	scope string,
	payloadHash string,
	apply func(*gorm.DB) (string, error),
) (string, error) {
	if ctx == nil {
		return "", errors.New("flush operation context is nil")
	}
	if DB == nil {
		return "", errors.New("database is not initialized")
	}
	if strings.TrimSpace(operationID) == "" {
		return "", errors.New("flush operation id is empty")
	}
	if strings.TrimSpace(scope) == "" {
		return "", errors.New("flush operation scope is empty")
	}
	if len(payloadHash) != sha256.Size*2 {
		return "", fmt.Errorf("flush operation payload hash has length %d, want %d", len(payloadHash), sha256.Size*2)
	}
	if apply == nil {
		return "", errors.New("flush operation apply function is nil")
	}

	var resultCode string
	transactionErr := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ledger := FlushOperationLedger{
			OperationID: operationID,
			Scope:       scope,
			PayloadHash: payloadHash,
			AppliedAt:   common.GetTimestamp(),
		}
		insert := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ledger)
		if insert.Error != nil {
			return fmt.Errorf("insert flush operation ledger %s: %w", operationID, insert.Error)
		}
		existing, err := loadFlushOperationLedger(tx, operationID)
		if err != nil {
			return err
		}
		if existing.Scope != scope || existing.PayloadHash != payloadHash {
			return flushOperationIdentityMismatchError(existing, scope, payloadHash)
		}
		if strings.TrimSpace(existing.ResultCode) != "" {
			resultCode = existing.ResultCode
			return nil
		}

		appliedResult, err := apply(tx)
		if err != nil {
			return err
		}
		if strings.TrimSpace(appliedResult) == "" {
			return fmt.Errorf("flush operation %s returned an empty result code", operationID)
		}
		update := tx.Model(&FlushOperationLedger{}).
			Where("operation_id = ?", operationID).
			Update("result_code", appliedResult)
		if update.Error != nil {
			return fmt.Errorf("persist flush operation %s result: %w", operationID, update.Error)
		}
		if update.RowsAffected != 1 {
			return fmt.Errorf(
				"persist flush operation %s result affected %d rows, want 1",
				operationID,
				update.RowsAffected,
			)
		}
		resultCode = appliedResult
		return nil
	})
	if transactionErr == nil {
		return resultCode, nil
	}

	// A commit error can be ambiguous: the database may have committed even
	// though the client observed an error. Reading the durable identity decides
	// whether the caller should retry the business mutation.
	existing, lookupErr := loadFlushOperationLedger(DB.WithContext(ctx), operationID)
	if lookupErr != nil {
		return "", transactionErr
	}
	if err := validateFlushOperationLedger(existing, scope, payloadHash); err != nil {
		return "", err
	}
	return existing.ResultCode, nil
}

func loadFlushOperationLedger(db *gorm.DB, operationID string) (*FlushOperationLedger, error) {
	var ledger FlushOperationLedger
	if err := db.Where("operation_id = ?", operationID).First(&ledger).Error; err != nil {
		return nil, err
	}
	return &ledger, nil
}

func validateFlushOperationLedger(
	ledger *FlushOperationLedger,
	scope string,
	payloadHash string,
) error {
	if ledger.Scope != scope || ledger.PayloadHash != payloadHash {
		return flushOperationIdentityMismatchError(ledger, scope, payloadHash)
	}
	if strings.TrimSpace(ledger.ResultCode) == "" {
		return fmt.Errorf("flush operation %s has no committed result", ledger.OperationID)
	}
	return nil
}

func flushOperationIdentityMismatchError(
	ledger *FlushOperationLedger,
	scope string,
	payloadHash string,
) error {
	return fmt.Errorf(
		"flush operation %s identity mismatch: stored scope=%q payload_hash=%q, replay scope=%q payload_hash=%q",
		ledger.OperationID,
		ledger.Scope,
		ledger.PayloadHash,
		scope,
		payloadHash,
	)
}

func deleteFlushOperationLedgers(ctx context.Context, operationIDs []string) error {
	if ctx == nil || DB == nil || len(operationIDs) == 0 {
		return nil
	}
	for start := 0; start < len(operationIDs); start += flushOperationCleanupChunkSize {
		end := start + flushOperationCleanupChunkSize
		if end > len(operationIDs) {
			end = len(operationIDs)
		}
		result := DB.WithContext(ctx).
			Where("operation_id IN ?", operationIDs[start:end]).
			Delete(&FlushOperationLedger{})
		if result.Error != nil {
			return fmt.Errorf(
				"delete %d confirmed flush operation ledgers: %w",
				end-start,
				result.Error,
			)
		}
	}
	return nil
}
