package model

import (
	"errors"
	"fmt"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const batchDeleteChunkSize = 200

// BatchUpdateDeleteLedger is the durable terminal record for batch-updated
// entities. A matching row proves that a missing update target was deleted
// intentionally and records every delta discarded after that deletion.
type BatchUpdateDeleteLedger struct {
	Kind           int   `gorm:"primaryKey;autoIncrement:false"`
	EntityId       int   `gorm:"column:entity_id;primaryKey;autoIncrement:false"`
	DeletedAt      int64 `gorm:"column:deleted_at;type:bigint;not null"`
	DiscardedDelta int64 `gorm:"column:discarded_delta;type:bigint;not null;default:0"`
	DiscardedCount int64 `gorm:"column:discarded_count;type:bigint;not null;default:0"`
}

func batchUpdateDeleteLedgerExists(kind int, entityId int) (bool, error) {
	var count int64
	err := DB.Model(&BatchUpdateDeleteLedger{}).
		Where("kind = ? AND entity_id = ?", kind, entityId).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf(
			"check batch update delete ledger for kind=%d entity_id=%d: %w",
			kind,
			entityId,
			err,
		)
	}
	if count > 1 {
		return false, fmt.Errorf(
			"batch update delete ledger invariant violated for kind=%d entity_id=%d: found %d rows, want at most 1",
			kind,
			entityId,
			count,
		)
	}
	return count == 1, nil
}

func requireBatchUpdateEntityActive(kind int, entityId int) error {
	deleted, err := batchUpdateDeleteLedgerExists(kind, entityId)
	if err != nil {
		return err
	}
	if deleted {
		return fmt.Errorf(
			"batch update entity kind=%d entity_id=%d was deleted: %w",
			kind,
			entityId,
			gorm.ErrRecordNotFound,
		)
	}
	return nil
}

func writeCacheWithBatchDeleteBarrier(
	kind int,
	entityId int,
	write func() error,
	invalidate func() error,
) error {
	if err := requireBatchUpdateEntityActive(kind, entityId); err != nil {
		if invalidateErr := invalidate(); invalidateErr != nil {
			return fmt.Errorf("%w; additionally failed to invalidate cache: %v", err, invalidateErr)
		}
		return err
	}
	if err := write(); err != nil {
		return err
	}
	if err := requireBatchUpdateEntityActive(kind, entityId); err != nil {
		if invalidateErr := invalidate(); invalidateErr != nil {
			return fmt.Errorf("%w; additionally failed to invalidate cache: %v", err, invalidateErr)
		}
		return err
	}
	return nil
}

func uniqueBatchDeleteIDs(ids []int) []int {
	uniqueIds := make([]int, 0, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIds = append(uniqueIds, id)
	}
	sort.Ints(uniqueIds)
	return uniqueIds
}

func batchDeleteIDChunks(ids []int) [][]int {
	chunks := make([][]int, 0, (len(ids)+batchDeleteChunkSize-1)/batchDeleteChunkSize)
	for start := 0; start < len(ids); start += batchDeleteChunkSize {
		end := start + batchDeleteChunkSize
		if end > len(ids) {
			end = len(ids)
		}
		chunks = append(chunks, ids[start:end])
	}
	return chunks
}

func batchDeleteRowLock(tx *gorm.DB) *gorm.DB {
	return tx.Clauses(clause.Locking{Strength: "UPDATE"})
}

func createBatchUpdateDeleteLedgers(tx *gorm.DB, entityId int, kinds ...int) error {
	return createBatchUpdateDeleteLedgersAt(tx, entityId, common.GetTimestamp(), kinds...)
}

func createBatchUpdateDeleteLedgersAt(
	tx *gorm.DB,
	entityId int,
	deletedAt int64,
	kinds ...int,
) error {
	for _, kind := range kinds {
		ledger := BatchUpdateDeleteLedger{
			Kind:      kind,
			EntityId:  entityId,
			DeletedAt: deletedAt,
		}
		result := tx.Create(&ledger)
		if result.Error != nil {
			return fmt.Errorf(
				"batch update delete ledger invariant violated for kind=%d entity_id=%d: %w",
				kind,
				entityId,
				result.Error,
			)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf(
				"batch update delete ledger invariant violated for kind=%d entity_id=%d: inserted %d rows, want 1",
				kind,
				entityId,
				result.RowsAffected,
			)
		}
	}
	return nil
}

func ensureBatchUpdateDeleteLedgers(
	tx *gorm.DB,
	entityId int,
	deletedAt int64,
	kinds ...int,
) error {
	for _, kind := range kinds {
		var ledger BatchUpdateDeleteLedger
		err := tx.Select("kind", "entity_id").
			Where("kind = ? AND entity_id = ?", kind, entityId).
			First(&ledger).Error
		switch {
		case err == nil:
			continue
		case errors.Is(err, gorm.ErrRecordNotFound):
			if err := createBatchUpdateDeleteLedgersAt(tx, entityId, deletedAt, kind); err != nil {
				return err
			}
		default:
			return fmt.Errorf(
				"inspect batch update delete ledger for kind=%d entity_id=%d: %w",
				kind,
				entityId,
				err,
			)
		}
	}
	return nil
}

func requireSelectedDeleteRows(entity string, selected int, result *gorm.DB) error {
	if result.Error != nil {
		return fmt.Errorf("delete %s: %w", entity, result.Error)
	}
	if result.RowsAffected != int64(selected) {
		return fmt.Errorf(
			"delete %s affected %d rows, want %d",
			entity,
			result.RowsAffected,
			selected,
		)
	}
	return nil
}

func discardDeletedBatchUpdates(
	tx *gorm.DB,
	entity string,
	entityId int,
	updates []BatchUpdate,
) error {
	missingTargetErr := fmt.Errorf("update %s id %d affected 0 rows, want 1", entity, entityId)
	for _, update := range updates {
		discarded, err := incrementDiscardedBatchUpdate(tx, update)
		if err != nil {
			return err
		}
		if discarded {
			continue
		}

		created, err := createLegacySoftDeleteLedger(tx, update)
		if err != nil {
			return err
		}
		if !created {
			return missingTargetErr
		}

		discarded, err = incrementDiscardedBatchUpdate(tx, update)
		if err != nil {
			return err
		}
		if !discarded {
			return fmt.Errorf(
				"batch update delete ledger disappeared for kind=%d entity_id=%d",
				update.Kind,
				update.ID,
			)
		}
	}
	return nil
}

func incrementDiscardedBatchUpdate(tx *gorm.DB, update BatchUpdate) (bool, error) {
	result := tx.Model(&BatchUpdateDeleteLedger{}).
		Where("kind = ? AND entity_id = ?", update.Kind, update.ID).
		Updates(map[string]interface{}{
			"discarded_delta": gorm.Expr("discarded_delta + ?", update.Delta),
			"discarded_count": gorm.Expr("discarded_count + ?", 1),
		})
	if result.Error != nil {
		return false, fmt.Errorf(
			"record discarded batch update for kind=%d entity_id=%d: %w",
			update.Kind,
			update.ID,
			result.Error,
		)
	}
	switch result.RowsAffected {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf(
			"record discarded batch update for kind=%d entity_id=%d affected %d rows, want 1",
			update.Kind,
			update.ID,
			result.RowsAffected,
		)
	}
}

func createLegacySoftDeleteLedger(tx *gorm.DB, update BatchUpdate) (bool, error) {
	switch update.Kind {
	case BatchUpdateTypeUserQuota, BatchUpdateTypeUsedQuota, BatchUpdateTypeRequestCount:
		var user User
		err := batchDeleteRowLock(tx.Unscoped()).
			Select("id", "deleted_at").
			Where("id = ?", update.ID).
			First(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && !user.DeletedAt.Valid) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("inspect soft-deleted user id %d: %w", update.ID, err)
		}
		if err := createBatchUpdateDeleteLedgersAt(
			tx,
			update.ID,
			user.DeletedAt.Time.Unix(),
			update.Kind,
		); err != nil {
			return false, err
		}
	case BatchUpdateTypeTokenQuota:
		var token Token
		err := batchDeleteRowLock(tx.Unscoped()).
			Select("id", "deleted_at").
			Where("id = ?", update.ID).
			First(&token).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && !token.DeletedAt.Valid) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("inspect soft-deleted token id %d: %w", update.ID, err)
		}
		if err := createBatchUpdateDeleteLedgersAt(
			tx,
			update.ID,
			token.DeletedAt.Time.Unix(),
			update.Kind,
		); err != nil {
			return false, err
		}
	default:
		return false, nil
	}

	return true, nil
}
