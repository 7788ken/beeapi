package model

import (
	"time"

	"gorm.io/gorm"
)

// IQTestModel is a model enabled for channel intelligence tests.
// BaselineScore is the expected score in the inclusive range 0..100.
type IQTestModel struct {
	Id            int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	ModelName     string `json:"model_name" gorm:"column:model_name;type:varchar(128);not null;uniqueIndex"`
	BaselineScore int    `json:"baseline_score" gorm:"column:baseline_score;not null"`
	Enabled       bool   `json:"enabled" gorm:"column:enabled;not null;index"`
	Version       int64  `json:"version" gorm:"not null;default:1"`
	CreatedAt     int64  `json:"created_at" gorm:"column:created_at;type:bigint;not null;index"`
	UpdatedAt     int64  `json:"updated_at" gorm:"column:updated_at;type:bigint;not null;index"`
}

func (IQTestModel) TableName() string { return "iq_test_models" }

func (m *IQTestModel) BeforeCreate(_ *gorm.DB) error {
	if m.CreatedAt == 0 {
		m.CreatedAt = time.Now().Unix()
	}
	if m.UpdatedAt == 0 {
		m.UpdatedAt = m.CreatedAt
	}
	return nil
}

func (m *IQTestModel) BeforeUpdate(_ *gorm.DB) error {
	m.UpdatedAt = time.Now().Unix()
	return nil
}

func CreateIQTestModel(m *IQTestModel) error {
	if m == nil {
		return gorm.ErrInvalidData
	}
	return DB.Create(m).Error
}

func GetIQTestModel(id int64) (*IQTestModel, error) {
	var m IQTestModel
	if err := DB.First(&m, id).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

func ListIQTestModels(enabledOnly bool) ([]IQTestModel, error) {
	q := DB.Order("id ASC")
	if enabledOnly {
		q = q.Where("enabled = ?", true)
	}
	rows := make([]IQTestModel, 0)
	return rows, q.Find(&rows).Error
}

func UpdateIQTestModel(id int64, updates map[string]any) error {
	res := DB.Model(&IQTestModel{}).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func DeleteIQTestModel(id int64) error {
	res := DB.Delete(&IQTestModel{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
