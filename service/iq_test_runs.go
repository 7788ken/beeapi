package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const iqLeaseSeconds int64 = 60

var ErrIQDisabled = errors.New("IQ detection is disabled")
var ErrIQBusy = errors.New("an IQ detection run is already active")

type IQRunSnapshot struct {
	Setting          IQTestSetting       `json:"setting"`
	Models           []model.IQTestModel `json:"models"`
	ChannelIDs       []int               `json:"channel_ids"`
	ChannelRevisions map[int]int64       `json:"channel_revisions"`
}

func iqCandidates(channelID int, models []model.IQTestModel) ([]model.Channel, int, error) {
	if channelID < 0 {
		return nil, 0, fmt.Errorf("channel_id must be positive")
	}
	var channels []model.Channel
	q := model.DB.Where("type IN ?", iqSupportedChannelTypes).
		Where("status = ? OR (status = ? AND iq_disabled = ?)", common.ChannelStatusEnabled, common.ChannelStatusAutoDisabled, true)
	if channelID > 0 {
		q = q.Where("id = ?", channelID)
	}
	if err := q.Order("id ASC").Find(&channels).Error; err != nil {
		return nil, 0, err
	}
	eligible := make([]model.Channel, 0, len(channels))
	count := 0
	for _, ch := range channels {
		matched := 0
		for _, configured := range models {
			if iqChannelHasModel(&ch, configured.ModelName) {
				matched++
			}
		}
		if matched > 0 {
			eligible = append(eligible, ch)
			count += matched
		}
	}
	if count == 0 {
		return nil, 0, fmt.Errorf("no eligible channel/model pairs")
	}
	return eligible, count, nil
}

func iqChannelHasModel(ch *model.Channel, name string) bool {
	for _, enabled := range ch.GetModels() {
		if enabled == name {
			return true
		}
	}
	return false
}

// iqFilterModels narrows the configured model list to an explicit selection so a
// manual round can target one model without editing global configuration.
func iqFilterModels(models []model.IQTestModel, names []string) ([]model.IQTestModel, error) {
	if len(names) == 0 {
		return models, nil
	}
	available := make(map[string]model.IQTestModel, len(models))
	for _, configured := range models {
		available[configured.ModelName] = configured
	}
	seen := make(map[string]bool, len(names))
	selected := make([]model.IQTestModel, 0, len(names))
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		configured, exists := available[name]
		if !exists {
			return nil, fmt.Errorf("model %q is not an enabled IQ test model", name)
		}
		seen[name] = true
		selected = append(selected, configured)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("model_names must not be empty")
	}
	return selected, nil
}

// IQCoverageSkip is one channel/model pair that detection will not attempt.
type IQCoverageSkip struct {
	ChannelID int    `json:"channel_id"`
	Name      string `json:"name"`
	Type      int    `json:"type"`
	ModelName string `json:"model_name"`
	Reason    string `json:"reason"`
}

// IQCoveragePreview answers "what would a round actually cover" before spending
// any upstream quota, including the channels it will deliberately skip.
type IQCoveragePreview struct {
	Enabled          bool             `json:"enabled"`
	EnforcementMode  string           `json:"enforcement_mode"`
	Models           []string         `json:"models"`
	EligibleChannels int              `json:"eligible_channels"`
	EligiblePairs    int              `json:"eligible_pairs"`
	Skipped          []IQCoverageSkip `json:"skipped"`
	SkippedTotal     int              `json:"skipped_total"`
}

// iqChannelProbeable mirrors the admission predicate for a single channel, so the
// preview and a real round can never disagree about what gets probed.
func iqChannelProbeable(ch *model.Channel) bool {
	supported := false
	for _, channelType := range iqSupportedChannelTypes {
		if ch.Type == channelType {
			supported = true
			break
		}
	}
	if !supported {
		return false
	}
	return ch.Status == common.ChannelStatusEnabled || (ch.Status == common.ChannelStatusAutoDisabled && ch.IQDisabled)
}

const iqCoverageSkipLimit = 200

// PreviewIQTestCoverage reports the exact reach of a round without spending any
// upstream quota, including which channels it will deliberately skip and why.
func PreviewIQTestCoverage(channelID int, modelNames []string) (*IQCoveragePreview, error) {
	if channelID < 0 {
		return nil, fmt.Errorf("channel_id must be positive")
	}
	setting, err := GetIQTestSetting()
	if err != nil {
		return nil, err
	}
	configured, err := model.ListIQTestModels(true)
	if err != nil {
		return nil, err
	}
	models, err := iqFilterModels(configured, modelNames)
	if err != nil {
		return nil, err
	}
	preview := &IQCoveragePreview{
		Enabled:         setting.Enabled,
		EnforcementMode: setting.EnforcementMode,
		Models:          make([]string, 0, len(models)),
		Skipped:         make([]IQCoverageSkip, 0),
	}
	for _, configured := range models {
		preview.Models = append(preview.Models, configured.ModelName)
	}
	var channels []model.Channel
	query := model.DB.Order("id ASC")
	if channelID > 0 {
		query = query.Where("id = ?", channelID)
	}
	if err := query.Find(&channels).Error; err != nil {
		return nil, err
	}
	for _, ch := range channels {
		matched := 0
		for _, configured := range models {
			if !iqChannelHasModel(&ch, configured.ModelName) {
				continue
			}
			matched++
			if !iqChannelProbeable(&ch) {
				preview.SkippedTotal++
				if len(preview.Skipped) < iqCoverageSkipLimit {
					preview.Skipped = append(preview.Skipped, IQCoverageSkip{
						ChannelID: ch.Id, Name: ch.Name, Type: ch.Type,
						ModelName: configured.ModelName, Reason: iqSkipReason(&ch),
					})
				}
			}
		}
		if matched > 0 && iqChannelProbeable(&ch) {
			preview.EligibleChannels++
			preview.EligiblePairs += matched
		}
	}
	return preview, nil
}

// iqSkipReason explains why an otherwise matching channel is not probed.
func iqSkipReason(ch *model.Channel) string {
	for _, channelType := range iqSupportedChannelTypes {
		if ch.Type == channelType {
			switch ch.Status {
			case common.ChannelStatusManuallyDisabled:
				return "channel_disabled"
			case common.ChannelStatusAutoDisabled:
				return "channel_auto_disabled"
			default:
				return "channel_not_enabled"
			}
		}
	}
	return "protocol_unsupported"
}

// PrepareIQTestRun is the sole admission path. The caller submits only when
// created is true; a repeated idempotency key never executes again.
func PrepareIQTestRun(trigger string, operatorID, channelID int, key string, modelNames []string) (run *model.IQTestRun, created bool, err error) {
	if channelID < 0 || len(key) > 128 {
		return nil, false, fmt.Errorf("invalid channel_id or idempotency_key")
	}
	if key != "" {
		existing, lookupErr := model.GetIQTestRunByIdempotencyKey(key)
		if lookupErr == nil {
			if existing.ChannelID != channelID {
				return existing, false, fmt.Errorf("idempotency_key was used for a different channel")
			}
			return existing, false, nil
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return nil, false, lookupErr
		}
	}
	setting, err := GetIQTestSetting()
	if err != nil {
		return nil, false, err
	}
	if !setting.Enabled {
		return nil, false, ErrIQDisabled
	}
	models, err := model.ListIQTestModels(true)
	if err != nil {
		return nil, false, err
	}
	models, err = iqFilterModels(models, modelNames)
	if err != nil {
		return nil, false, err
	}
	channels, count, err := iqCandidates(channelID, models)
	if err != nil {
		return nil, false, err
	}
	owner := uuid.NewString()
	claimed, err := model.ClaimIQTestLease(owner, iqLeaseSeconds)
	if err != nil {
		return nil, false, err
	}
	if !claimed {
		if key != "" {
			if existing, lookupErr := model.GetIQTestRunByIdempotencyKey(key); lookupErr == nil {
				if existing.ChannelID != channelID {
					return existing, false, fmt.Errorf("idempotency_key was used for a different channel")
				}
				return existing, false, nil
			}
		}
		active, lookupErr := model.GetActiveIQTestRun()
		if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return nil, false, lookupErr
		}
		return active, false, ErrIQBusy
	}
	defer func() {
		if !created {
			if releaseErr := model.ReleaseIQTestLease(owner); releaseErr != nil {
				err = errors.Join(err, releaseErr)
			}
		}
	}()
	if err := model.AbortExpiredIQTestRuns(time.Now().Unix()); err != nil {
		return nil, false, err
	}
	// Recheck after acquisition: another instance may have created this key
	// while this request was collecting its candidate snapshot.
	if key != "" {
		existing, lookupErr := model.GetIQTestRunByIdempotencyKey(key)
		if lookupErr == nil {
			if existing.ChannelID != channelID {
				return existing, false, fmt.Errorf("idempotency_key was used for a different channel")
			}
			return existing, false, nil
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return nil, false, lookupErr
		}
	}
	snapshot := IQRunSnapshot{Setting: setting, Models: models, ChannelIDs: make([]int, len(channels)), ChannelRevisions: make(map[int]int64)}
	for i := range channels {
		snapshot.ChannelIDs[i] = channels[i].Id
		snapshot.ChannelRevisions[channels[i].Id] = channels[i].IQRevision
	}
	encoded, err := common.Marshal(snapshot)
	if err != nil {
		return nil, false, err
	}
	run = model.NewIQTestRun(uuid.NewString(), trigger, owner, operatorID, string(encoded), IQBankVersion())
	run.ChannelID, run.CandidateCount, run.LeaseExpiresAt = channelID, count, time.Now().Unix()+iqLeaseSeconds
	if key != "" {
		run.IdempotencyKey = &key
	}
	if err := model.CreateIQTestRun(run); err != nil {
		return nil, false, err
	}
	return run, true, nil
}

func SubmitIQTestRun(run *model.IQTestRun) error {
	err := backgroundtask.Start("iq-test-run-"+run.RunID, func(ctx context.Context) {
		if err := ExecuteIQTestRun(ctx, run.RunID, run.ChannelID); err != nil {
			common.SysError("IQ run " + run.RunID + ": " + err.Error())
		}
	})
	if err == nil {
		return nil
	}
	finishErr := failIQTestRun(run, model.IQTestRunStatusFailed, err)
	releaseErr := model.ReleaseIQTestLease(run.OwnerID)
	return errors.Join(err, finishErr, releaseErr)
}

func failIQTestRun(run *model.IQTestRun, status string, cause error) error {
	return model.DB.Model(&model.IQTestRun{}).Where("run_id = ? AND owner_id = ? AND status = ?", run.RunID, run.OwnerID, model.IQTestRunStatusRunning).
		Updates(map[string]any{"status": status, "finished_at": time.Now().Unix(), "error_message": cause.Error()}).Error
}

func GetIQTestRun(runID string) (*model.IQTestRun, error) { return model.GetIQTestRun(runID) }
func ListIQTestRuns(page, pageSize int) ([]model.IQTestRun, int64, error) {
	return model.ListIQTestRuns(page, pageSize)
}
func FinishIQTestRun(runID, status string, candidate, success, invalid, failed int) error {
	switch status {
	case model.IQTestRunStatusFinished, model.IQTestRunStatusPartial, model.IQTestRunStatusAborted, model.IQTestRunStatusFailed:
	default:
		return fmt.Errorf("invalid run status %q", status)
	}
	return model.FinalizeIQTestRun(runID, status, candidate, success, invalid, failed, time.Now().Unix())
}
