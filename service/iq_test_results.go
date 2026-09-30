package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/model"
)

func RecordIQTestResult(result *model.IQTestResult) error {
	if result == nil {
		return fmt.Errorf("result is required")
	}
	if result.RunID == "" || result.ChannelID <= 0 || result.RequestedModel == "" {
		return fmt.Errorf("run_id, channel_id and requested_model are required")
	}
	switch result.Status {
	case model.IQTestResultStatusSuccess:
		if result.Score == nil || *result.Score < 0 || *result.Score > 100 {
			return fmt.Errorf("success result score must be between 0 and 100")
		}
		if result.Margin == nil || *result.Margin != *result.Score-result.BaselineScoreSnapshot {
			return fmt.Errorf("success result margin must match score minus baseline")
		}
	case model.IQTestResultStatusInvalid, model.IQTestResultStatusError:
		if result.Score != nil || result.Margin != nil {
			return fmt.Errorf("invalid/error result score and margin must be null")
		}
	default:
		return fmt.Errorf("invalid result status %q", result.Status)
	}
	return model.CreateIQTestResult(result)
}

func GetIQTestResult(id int64) (*model.IQTestResult, error) { return model.GetIQTestResult(id) }

func ListIQTestResults(runID string, channelID, page, pageSize int) ([]model.IQTestResult, int64, error) {
	return model.ListIQTestResults(runID, channelID, page, pageSize)
}

func ListIQTestResultsFiltered(runID string, channelID int, requestedModel, status string, page, pageSize int) ([]model.IQTestResult, int64, error) {
	switch status {
	case "", model.IQTestResultStatusSuccess, model.IQTestResultStatusInvalid, model.IQTestResultStatusError:
	default:
		return nil, 0, fmt.Errorf("invalid result status")
	}
	return model.ListIQTestResultsFiltered(runID, channelID, requestedModel, status, page, pageSize)
}
