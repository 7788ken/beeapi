package service

import (
	"fmt"
	"github.com/QuantumNous/new-api/model"
	"strings"
)

type IQTestModelInput struct {
	ModelName     string `json:"model_name"`
	BaselineScore *int   `json:"baseline_score"`
	Enabled       *bool  `json:"enabled"`
	Version       *int64 `json:"version"`
}

func CreateIQTestModelInput(input IQTestModelInput) (*model.IQTestModel, error) {
	if strings.TrimSpace(input.ModelName) == "" || len(input.ModelName) > 128 || strings.Contains(input.ModelName, ",") {
		return nil, fmt.Errorf("model_name is required")
	}
	baseline := 70
	if input.BaselineScore != nil {
		baseline = *input.BaselineScore
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if baseline < 0 || baseline > 100 {
		return nil, fmt.Errorf("baseline_score must be between 0 and 100")
	}
	inputModel := &model.IQTestModel{ModelName: input.ModelName, BaselineScore: baseline, Enabled: enabled}
	if err := model.CreateIQTestModel(inputModel); err != nil {
		return nil, err
	}
	return inputModel, nil
}

func ListIQTestModels(enabledOnly bool) ([]model.IQTestModel, error) {
	return model.ListIQTestModels(enabledOnly)
}

func UpdateIQTestModel(id int64, input IQTestModelInput) error {
	if id <= 0 || input.Version == nil || *input.Version < 1 {
		return fmt.Errorf("id and version must be positive")
	}
	updates := map[string]any{}
	if input.ModelName != "" {
		if strings.TrimSpace(input.ModelName) == "" || len(input.ModelName) > 128 || strings.Contains(input.ModelName, ",") {
			return fmt.Errorf("invalid model_name")
		}
		updates["model_name"] = input.ModelName
	}
	if input.BaselineScore != nil {
		if *input.BaselineScore < 0 || *input.BaselineScore > 100 {
			return fmt.Errorf("baseline_score must be between 0 and 100")
		}
		updates["baseline_score"] = *input.BaselineScore
	}
	if input.Enabled != nil {
		updates["enabled"] = *input.Enabled
	}
	if len(updates) == 0 {
		return fmt.Errorf("no fields to update")
	}
	updates["version"] = *input.Version + 1
	res := model.DB.Model(&model.IQTestModel{}).Where("id = ? AND version = ?", id, *input.Version).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		if _, err := model.GetIQTestModel(id); err != nil {
			return err
		}
		return ErrIQVersionConflict
	}
	return nil
}

func DeleteIQTestModel(id int64, version int64) error {
	if id <= 0 || version < 1 {
		return fmt.Errorf("id and version must be positive")
	}
	res := model.DB.Where("id = ? AND version = ?", id, version).Delete(&model.IQTestModel{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		if _, err := model.GetIQTestModel(id); err != nil {
			return err
		}
		return ErrIQVersionConflict
	}
	return nil
}
