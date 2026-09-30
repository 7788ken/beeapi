package model

import (
	"reflect"
	"strings"

	"gorm.io/gorm"
)

// Channel edits invalidate IQ's observation and restore ownership atomically.
// Metric-only writes do not affect the ownership revision.
func (channel *Channel) BeforeUpdate(tx *gorm.DB) error {
	if value, ok := tx.Get("iq_enforcement"); ok && value == true {
		return nil
	}
	watched := []string{"Priority", "Status", "Models", "ModelMapping", "Key", "BaseURL", "ChannelInfo", "DegradeLevel", "PermanentDisabled", "VerifyDisabled", "HeaderOverride", "ParamOverride", "Setting"}
	selected, restricted := tx.Statement.SelectAndOmitColumns(false, true)
	value := reflect.Indirect(reflect.ValueOf(tx.Statement.Dest))
	touches := false
	for _, name := range watched {
		field := tx.Statement.Schema.LookUpField(name)
		if field == nil {
			continue
		}
		permitted, explicitlySelected := selected[field.DBName]
		if explicitlySelected && !permitted || restricted && !explicitlySelected {
			continue
		}
		if value.Kind() == reflect.Map {
			for _, key := range value.MapKeys() {
				if key.Kind() == reflect.String && (key.String() == field.DBName || key.String() == name) {
					touches = true
				}
			}
		} else if value.Kind() == reflect.Struct {
			v := value.FieldByName(name)
			if v.IsValid() && (explicitlySelected || !v.IsZero()) {
				touches = true
			}
		}
	}
	if !touches {
		return nil
	}
	// Maps let zero/NULL assignments participate in the original UPDATE.
	updates, ok := tx.Statement.Dest.(map[string]interface{})
	if !ok {
		updates = make(map[string]interface{})
		for _, field := range tx.Statement.Schema.Fields {
			permitted, exists := selected[field.DBName]
			if field.DBName == "" || !field.Updatable || field.PrimaryKey || exists && !permitted || restricted && !exists {
				continue
			}
			v, zero := field.ValueOf(tx.Statement.Context, value)
			if exists || !zero {
				updates[field.DBName] = v
			}
		}
	}
	updates["iq_revision"] = gorm.Expr("iq_revision + ?", 1)
	updates["iq_baseline_priority"] = nil
	updates["iq_applied_priority"] = nil
	updates["iq_disabled"] = false
	tx.Statement.Dest = updates
	if len(tx.Statement.Selects) > 0 && !strings.Contains(strings.Join(tx.Statement.Selects, ","), "*") {
		tx.Statement.Selects = append(tx.Statement.Selects, "iq_revision", "iq_baseline_priority", "iq_applied_priority", "iq_disabled")
	}
	return nil
}

type IQTestControl struct {
	ChannelID    int    `gorm:"primaryKey"`
	Revision     int64  `gorm:"not null;default:0"`
	LastRunID    string `gorm:"type:varchar(64);not null;default:''"`
	ModelName    string `gorm:"type:varchar(128);not null;default:''"`
	ModelVersion int64  `gorm:"not null;default:0"`
	Baseline     int    `gorm:"not null;default:0"`
	BankVersion  string `gorm:"type:varchar(64);not null;default:''"`
	FailStreak   int    `gorm:"not null;default:0"`
	PassStreak   int    `gorm:"not null;default:0"`
	Step         int64  `gorm:"not null;default:0"`
}

type IQTestAction struct {
	Id             int64  `json:"id" gorm:"primaryKey"`
	RunID          string `json:"run_id" gorm:"type:varchar(64);index"`
	ChannelID      int    `json:"channel_id" gorm:"index"`
	Action         string `json:"action" gorm:"type:varchar(24)"`
	Reason         string `json:"reason" gorm:"type:varchar(255)"`
	PriorityBefore int64  `json:"priority_before"`
	PriorityAfter  int64  `json:"priority_after"`
	CreatedAt      int64  `json:"created_at" gorm:"index"`
}

type IQTestNotice struct {
	Key        string `gorm:"primaryKey;type:varchar(160)"`
	Subject    string `gorm:"type:varchar(255)"`
	Body       string `gorm:"type:text"`
	Pending    bool   `gorm:"not null;default:false;index"`
	SentAt     int64  `gorm:"not null;default:0"`
	ClaimUntil int64  `gorm:"not null;default:0"`
	RetryAt    int64  `gorm:"not null;default:0"`
	LastError  string `gorm:"type:text"`
}
