package service

import (
	"errors"
	"fmt"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Enforcement modes decide whether a scored round may touch routing at all.
const (
	IQEnforcementEnforce    = "enforce"
	IQEnforcementReportOnly = "report_only"
)

// IQTestSetting is the immutable configuration snapshot passed to a run.
type IQTestSetting struct {
	Enabled                   bool   `json:"enabled"`
	EnforcementMode           string `json:"enforcement_mode"`
	IntervalMinutes           int    `json:"interval_minutes"`
	Concurrency               int    `json:"concurrency"`
	DisableBelowBaseline      bool   `json:"disable_below_baseline"`
	PriorityStep              int    `json:"priority_step"`
	QuestionsPerRound         int    `json:"questions_per_round"`
	PerQuestionTimeoutSeconds int    `json:"per_question_timeout_seconds"`
	NotifyOnAction            bool   `json:"notify_on_action"`
	RetentionDays             int    `json:"retention_days"`
	Version                   int64  `json:"version"`
}

const iqSettingOptionKey = "iq_test_setting.config"

var iqSettingMu sync.Mutex

func DefaultIQTestSetting() IQTestSetting {
	return IQTestSetting{EnforcementMode: IQEnforcementEnforce, IntervalMinutes: 60, Concurrency: 4, PriorityStep: 10,
		QuestionsPerRound: 8, PerQuestionTimeoutSeconds: 20, NotifyOnAction: true,
		RetentionDays: 30}
}

// Enforces reports whether a round may change channel routing. An unset mode
// keeps the historical enforcing behaviour for stored configs written earlier.
func (s IQTestSetting) Enforces() bool {
	return s.EnforcementMode == "" || s.EnforcementMode == IQEnforcementEnforce
}

func (s IQTestSetting) Validate() error {
	// "" is a snapshot or stored config written before the mode existed; it is
	// accepted here so in-flight runs survive a deploy, and SaveIQTestSetting
	// normalizes it to the historical enforcing behaviour before persisting.
	if s.EnforcementMode != "" && s.EnforcementMode != IQEnforcementEnforce && s.EnforcementMode != IQEnforcementReportOnly {
		return fmt.Errorf("enforcement_mode must be %q or %q", IQEnforcementEnforce, IQEnforcementReportOnly)
	}
	if s.IntervalMinutes < 5 || s.IntervalMinutes > 10080 {
		return fmt.Errorf("interval_minutes must be between 5 and 10080")
	}
	if s.Concurrency < 1 || s.Concurrency > 16 {
		return fmt.Errorf("concurrency must be between 1 and 16")
	}
	if s.PriorityStep < 1 || s.PriorityStep > 100 {
		return fmt.Errorf("priority_step must be between 1 and 100")
	}
	if s.QuestionsPerRound < 1 || s.QuestionsPerRound > 15 {
		return fmt.Errorf("questions_per_round must be between 1 and 15")
	}
	if s.PerQuestionTimeoutSeconds < 5 || s.PerQuestionTimeoutSeconds > 120 {
		return fmt.Errorf("per_question_timeout_seconds must be between 5 and 120")
	}
	if s.RetentionDays < 1 || s.RetentionDays > 3650 {
		return fmt.Errorf("retention_days must be between 1 and 3650")
	}
	return nil
}

func GetIQTestSetting() (IQTestSetting, error) {
	iqSettingMu.Lock()
	defer iqSettingMu.Unlock()
	setting := DefaultIQTestSetting()
	var row model.Option
	err := model.DB.Where(&model.Option{Key: iqSettingOptionKey}).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return setting, nil
		}
		return setting, err
	}
	if err := common.Unmarshal([]byte(row.Value), &setting); err != nil {
		return setting, fmt.Errorf("invalid IQ setting: %w", err)
	}
	if err := setting.Validate(); err != nil {
		return setting, err
	}
	return setting, nil
}

// SaveIQTestSetting compares the stored JSON when updating, so two instances
// cannot silently overwrite the same observed version.
func SaveIQTestSetting(setting IQTestSetting, expectedVersion int64) (IQTestSetting, error) {
	if setting.EnforcementMode == "" {
		setting.EnforcementMode = IQEnforcementEnforce
	}
	if err := setting.Validate(); err != nil {
		return setting, err
	}
	iqSettingMu.Lock()
	defer iqSettingMu.Unlock()
	var current IQTestSetting = DefaultIQTestSetting()
	var row model.Option
	err := model.DB.Where(&model.Option{Key: iqSettingOptionKey}).First(&row).Error
	if err == nil {
		if uerr := common.Unmarshal([]byte(row.Value), &current); uerr != nil {
			return setting, uerr
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return setting, err
	}
	if expectedVersion < 0 || current.Version != expectedVersion {
		return setting, ErrIQVersionConflict
	}
	setting.Version = current.Version + 1
	b, err := common.Marshal(setting)
	if err != nil {
		return setting, err
	}
	if row.Key == "" {
		created := model.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Option{Key: iqSettingOptionKey, Value: string(b)})
		if created.Error != nil {
			return setting, created.Error
		}
		if created.RowsAffected != 1 {
			return setting, ErrIQVersionConflict
		}
	} else {
		result := model.DB.Model(&model.Option{}).Where(map[string]any{"key": iqSettingOptionKey, "value": row.Value}).Update("value", string(b))
		if result.Error != nil {
			return setting, result.Error
		}
		if result.RowsAffected != 1 {
			return setting, ErrIQVersionConflict
		}
	}
	return setting, nil
}

var ErrIQVersionConflict = errors.New("IQ configuration changed; refresh and try again")
