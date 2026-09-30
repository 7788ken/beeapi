package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type IQTestLease struct {
	Key       string `json:"key" gorm:"primaryKey;type:varchar(32)"`
	OwnerID   string `json:"owner_id" gorm:"column:owner_id;type:varchar(128);not null"`
	ExpiresAt int64  `json:"expires_at" gorm:"column:expires_at;type:bigint;not null;index"`
	UpdatedAt int64  `json:"updated_at" gorm:"column:updated_at;type:bigint;not null"`
}

func (IQTestLease) TableName() string { return "iq_test_leases" }

func ClaimIQTestLease(owner string, ttl int64) (bool, error) {
	if owner == "" || ttl <= 0 {
		return false, gorm.ErrInvalidData
	}
	now := time.Now().Unix()
	lease := IQTestLease{Key: "scheduler"}
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&lease).Error; err != nil {
		return false, err
	}
	res := DB.Model(&IQTestLease{}).Where(map[string]any{"key": lease.Key}).Where("expires_at <= ?", now).Updates(map[string]any{"owner_id": owner, "expires_at": now + ttl, "updated_at": now})
	return res.RowsAffected == 1, res.Error
}

func RenewIQTestLease(owner string, ttl int64) (bool, error) {
	if owner == "" || ttl <= 0 {
		return false, gorm.ErrInvalidData
	}
	now := time.Now().Unix()
	var lease IQTestLease
	if err := DB.Where(map[string]any{"key": "scheduler", "owner_id": owner}).
		Where("expires_at > ?", now).First(&lease).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	// Ownership was verified by the read above. RowsAffected must not be
	// interpreted here: MySQL reports changed rows, so a same-second renewal
	// writing an identical expiry returns 0 even while we still own the lease.
	err := DB.Model(&IQTestLease{}).Where(map[string]any{"key": "scheduler", "owner_id": owner}).
		Updates(map[string]any{"expires_at": now + ttl, "updated_at": now}).Error
	return true, err
}

func OwnsIQTestLease(owner string) (bool, error) {
	var count int64
	err := DB.Model(&IQTestLease{}).Where(map[string]any{"key": "scheduler", "owner_id": owner}).
		Where("expires_at > ?", time.Now().Unix()).Count(&count).Error
	return count == 1, err
}

func ReleaseIQTestLease(owner string) error {
	return DB.Model(&IQTestLease{}).Where(map[string]any{"key": "scheduler", "owner_id": owner}).
		Updates(map[string]any{"owner_id": "", "expires_at": 0, "updated_at": time.Now().Unix()}).Error
}
