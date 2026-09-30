package model

import "gorm.io/gorm"

type IQChannelScoreSnapshot struct {
	Score      *int
	Previous   *int
	Baseline   int
	At         int64
	Model      string
	Status     string
	ErrorClass string
	AttemptAt  int64
}

// GetLatestIQChannelScores publishes completed rounds, with comparable previous
// scores and the most recent completed attempt. Running rounds remain private.
func GetLatestIQChannelScores(channelIDs []int) (map[int]IQChannelScoreSnapshot, error) {
	out := make(map[int]IQChannelScoreSnapshot)
	if len(channelIDs) == 0 {
		return out, nil
	}
	type publishedResult struct {
		IQTestResult
		RunSequence    int64
		RunFinishedAt  int64
		RunBankVersion string
		RunStatus      string
	}
	completed := []string{IQTestRunStatusFinished, IQTestRunStatusPartial}
	// Correlated counts work on MySQL 5.7, PostgreSQL and SQLite, and bound
	// returned history independently for each channel to its latest two rounds.
	newer := DB.Table("iq_test_runs AS newer_run").Select("COUNT(DISTINCT newer_run.id)").
		Joins("JOIN iq_test_results AS newer_result ON newer_result.run_id = newer_run.run_id").
		Where("newer_result.channel_id = result.channel_id AND newer_result.status = ? AND newer_result.score IS NOT NULL", IQTestResultStatusSuccess).
		Where("newer_run.status IN ? AND newer_run.finished_at > 0", completed).
		Where("newer_run.finished_at > iq_run.finished_at OR (newer_run.finished_at = iq_run.finished_at AND newer_run.id > iq_run.id)")
	var rows []publishedResult
	if err := DB.Table("iq_test_results AS result").
		Select("result.*, iq_run.id AS run_sequence, iq_run.finished_at AS run_finished_at, iq_run.bank_version AS run_bank_version").
		Joins("JOIN iq_test_runs AS iq_run ON iq_run.run_id = result.run_id").
		Where("result.channel_id IN ? AND result.status = ? AND result.score IS NOT NULL", channelIDs, IQTestResultStatusSuccess).
		Where("iq_run.status IN ? AND iq_run.finished_at > 0", completed).
		Where("(?) < 2", newer).
		Order("iq_run.finished_at DESC, iq_run.id DESC, result.score - result.baseline_score_snapshot ASC, result.requested_model ASC, result.id ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	decisions := make(map[int][]publishedResult)
	for _, row := range rows {
		prior := decisions[row.ChannelID]
		if len(prior) == 0 || prior[len(prior)-1].RunSequence != row.RunSequence {
			decisions[row.ChannelID] = append(prior, row)
		}
	}
	for channelID, results := range decisions {
		current := results[0]
		snapshot := IQChannelScoreSnapshot{Score: current.Score, Baseline: current.BaselineScoreSnapshot,
			At: current.RunFinishedAt, Model: current.RequestedModel, Status: IQTestResultStatusSuccess}
		if len(results) > 1 {
			previous := results[1]
			if current.RequestedModel == previous.RequestedModel && current.BaselineScoreSnapshot == previous.BaselineScoreSnapshot &&
				current.RunBankVersion == previous.RunBankVersion && current.TotalQuestions == previous.TotalQuestions {
				snapshot.Previous = previous.Score
			}
		}
		out[channelID] = snapshot
	}
	terminal := []string{IQTestRunStatusFinished, IQTestRunStatusPartial, IQTestRunStatusFailed, IQTestRunStatusAborted}
	newerAttempt := DB.Table("iq_test_results AS newer_result").Select("1").
		Joins("JOIN iq_test_runs AS newer_run ON newer_run.run_id = newer_result.run_id").
		Where("newer_result.channel_id = result.channel_id AND newer_run.status IN ? AND newer_run.finished_at > 0", terminal).
		Where("newer_run.finished_at > iq_run.finished_at OR (newer_run.finished_at = iq_run.finished_at AND newer_run.id > iq_run.id)")
	var attempts []publishedResult
	if err := DB.Table("iq_test_results AS result").
		Select("result.*, iq_run.finished_at AS run_finished_at, iq_run.status AS run_status").
		Joins("JOIN iq_test_runs AS iq_run ON iq_run.run_id = result.run_id").
		Where("result.channel_id IN ? AND iq_run.status IN ? AND iq_run.finished_at > 0", channelIDs, terminal).
		Where("NOT EXISTS (?)", newerAttempt).
		Order("result.id ASC").Scan(&attempts).Error; err != nil {
		return nil, err
	}
	// A completed channel round is successful if at least one model was scored;
	// otherwise expose a deterministic error so never-successful channels are visible.
	latestAttempts := make(map[int]publishedResult)
	for _, attempt := range attempts {
		if attempt.RunStatus == IQTestRunStatusFailed || attempt.RunStatus == IQTestRunStatusAborted {
			attempt.Status, attempt.ErrorClass = IQTestResultStatusError, "run_"+attempt.RunStatus
		}
		previous, exists := latestAttempts[attempt.ChannelID]
		if !exists || (previous.Status != IQTestResultStatusSuccess && attempt.Status == IQTestResultStatusSuccess) {
			latestAttempts[attempt.ChannelID] = attempt
		}
	}
	for channelID, attempt := range latestAttempts {
		snapshot := out[channelID]
		snapshot.Status, snapshot.ErrorClass, snapshot.AttemptAt = attempt.Status, attempt.ErrorClass, attempt.RunFinishedAt
		if snapshot.Score == nil {
			snapshot.Model, snapshot.Baseline = attempt.RequestedModel, attempt.BaselineScoreSnapshot
		}
		if attempt.Status != IQTestResultStatusSuccess {
			snapshot.Previous = nil
		}
		out[channelID] = snapshot
	}
	return out, nil
}

// IQTestResult stores one channel/requested-model result in a run.
// Score and Margin are nullable: invalid/error results must not enter the score domain.
type IQTestResult struct {
	Id                    int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	RunID                 string `json:"run_id" gorm:"column:run_id;type:varchar(64);not null;uniqueIndex:idx_iq_result_run_channel_model,priority:1;index:idx_iq_result_run"`
	ChannelID             int    `json:"channel_id" gorm:"column:channel_id;not null;uniqueIndex:idx_iq_result_run_channel_model,priority:2;index:idx_iq_result_channel_created,priority:1"`
	RequestedModel        string `json:"requested_model" gorm:"column:requested_model;type:varchar(128);not null;uniqueIndex:idx_iq_result_run_channel_model,priority:3;index:idx_iq_result_model_created,priority:1"`
	UpstreamModel         string `json:"upstream_model" gorm:"column:upstream_model;type:varchar(128)"`
	BankVersion           string `json:"bank_version" gorm:"type:varchar(64)"`
	ChannelRevision       int64  `json:"channel_revision" gorm:"not null;default:0"`
	ConfigVersion         int64  `json:"config_version" gorm:"not null;default:0"`
	Status                string `json:"status" gorm:"type:varchar(16);not null;index:idx_iq_result_status_created,priority:1"`
	Score                 *int   `json:"score" gorm:"column:score"`
	BaselineScoreSnapshot int    `json:"baseline_score_snapshot" gorm:"column:baseline_score_snapshot;not null;default:0"`
	Margin                *int   `json:"margin" gorm:"column:margin"`
	CorrectCount          int    `json:"correct_count" gorm:"column:correct_count;not null;default:0"`
	TotalQuestions        int    `json:"total_questions" gorm:"column:total_questions;not null;default:0"`
	DurationMs            int64  `json:"duration_ms" gorm:"column:duration_ms;not null;default:0"`
	Detail                string `json:"detail,omitempty" gorm:"column:detail;type:text"`
	ErrorClass            string `json:"error_class,omitempty" gorm:"column:error_class;type:varchar(32)"`
	Action                string `json:"action" gorm:"column:action;type:varchar(24);not null;default:'none'"`
	SelectedForAction     bool   `json:"selected_for_action" gorm:"column:selected_for_action;not null;default:false;index"`
	ActionReason          string `json:"action_reason,omitempty" gorm:"column:action_reason;type:varchar(255)"`
	PriorityBefore        *int   `json:"priority_before,omitempty" gorm:"column:priority_before"`
	PriorityAfter         *int   `json:"priority_after,omitempty" gorm:"column:priority_after"`
	StartedAt             int64  `json:"started_at" gorm:"column:started_at;type:bigint;not null;index"`
	FinishedAt            int64  `json:"finished_at" gorm:"column:finished_at;type:bigint;index"`
	CreatedAt             int64  `json:"created_at" gorm:"column:created_at;type:bigint;not null;index:idx_iq_result_channel_created,priority:2;index:idx_iq_result_model_created,priority:2;index:idx_iq_result_status_created,priority:2"`
}

func (IQTestResult) TableName() string { return "iq_test_results" }

const (
	IQTestResultStatusSuccess   = "success"
	IQTestResultStatusInvalid   = "invalid"
	IQTestResultStatusError     = "error"
	IQTestActionNone            = "none"
	IQTestActionPriorityDown    = "priority_down"
	IQTestActionPriorityUp      = "priority_up"
	IQTestActionDisabled        = "disabled"
	IQTestActionEnabled         = "enabled"
	IQTestActionBreakerSkip     = "breaker_skip"
	IQTestActionSkippedConflict = "skipped_conflict"
)

func CreateIQTestResult(result *IQTestResult) error {
	if result == nil {
		return gorm.ErrInvalidData
	}
	return DB.Create(result).Error
}

func GetIQTestResult(id int64) (*IQTestResult, error) {
	var result IQTestResult
	if err := DB.First(&result, id).Error; err != nil {
		return nil, err
	}
	return &result, nil
}

func ListIQTestResults(runID string, channelID int, page, pageSize int) ([]IQTestResult, int64, error) {
	return ListIQTestResultsFiltered(runID, channelID, "", "", page, pageSize)
}

func ListIQTestResultsFiltered(runID string, channelID int, requestedModel, status string, page, pageSize int) ([]IQTestResult, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	q := DB.Model(&IQTestResult{})
	if runID != "" {
		q = q.Where("run_id = ?", runID)
	}
	if channelID > 0 {
		q = q.Where("channel_id = ?", channelID)
	}
	if requestedModel != "" {
		q = q.Where("requested_model = ?", requestedModel)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]IQTestResult, 0)
	err := q.Order("created_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error
	return rows, total, err
}

func UpdateIQTestResult(id int64, updates map[string]any) error {
	return DB.Model(&IQTestResult{}).Where("id = ?", id).Updates(updates).Error
}
