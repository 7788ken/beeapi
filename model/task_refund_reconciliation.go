package model

import (
	"errors"
	"fmt"
	"sort"

	"github.com/QuantumNous/new-api/common"
)

const (
	AsyncRefundEvidenceDurableRecord = "durable_record"
	AsyncRefundEvidenceHistoricalLog = "historical_refund_log"
	AsyncRefundEvidenceMissing       = "missing_evidence"
	AsyncRefundEvidenceAmbiguous     = "ambiguous_refund_logs"
	AsyncRefundEvidenceUnknownSource = "unknown_billing_source"

	maxAsyncRefundReconciliationLogs = 10000
	maxAsyncRefundReconciliationScan = 10000
)

type AsyncRefundReconciliationItem struct {
	TaskKind        string   `json:"task_kind"`
	TaskDbId        int64    `json:"task_db_id"`
	TaskId          string   `json:"task_id"`
	UserId          int      `json:"user_id"`
	ChannelId       int      `json:"channel_id"`
	TokenId         int      `json:"token_id"`
	SubscriptionId  int      `json:"subscription_id"`
	BillingSource   string   `json:"billing_source"`
	Quota           int      `json:"quota"`
	TaskStatus      string   `json:"task_status"`
	SubmitTime      int64    `json:"submit_time"`
	FinishTime      int64    `json:"finish_time"`
	FailReason      string   `json:"fail_reason"`
	Evidence        string   `json:"evidence"`
	RefundRecordId  int64    `json:"refund_record_id,omitempty"`
	RefundLogIds    []int    `json:"refund_log_ids"`
	RefundLogEvents []string `json:"refund_log_event_ids,omitempty"`
	ManualReview    bool     `json:"manual_review"`
	AutomaticAction bool     `json:"automatic_action"`
}

type AsyncRefundReconciliationSummary struct {
	Total             int `json:"total"`
	DurableRecords    int `json:"durable_records"`
	HistoricalLogOnly int `json:"historical_log_only"`
	MissingEvidence   int `json:"missing_evidence"`
	AmbiguousLogs     int `json:"ambiguous_logs"`
	UnknownSource     int `json:"unknown_billing_source"`
	ManualReview      int `json:"manual_review"`
}

type AsyncRefundReconciliationReport struct {
	StartTime                  int64                            `json:"start_time"`
	EndTime                    int64                            `json:"end_time"`
	ReadOnly                   bool                             `json:"read_only"`
	AutomaticProcessingAllowed bool                             `json:"automatic_processing_allowed"`
	Truncated                  bool                             `json:"truncated"`
	Items                      []AsyncRefundReconciliationItem  `json:"items"`
	Summary                    AsyncRefundReconciliationSummary `json:"summary"`
}

type asyncRefundLogCandidate struct {
	Id        int
	EventId   string
	UserId    int
	CreatedAt int64
	Quota     int
	Other     string
}

func asyncRefundIdentity(kind string, taskDbId int64) string {
	return fmt.Sprintf("%s:%d", kind, taskDbId)
}

func asyncRefundLogMatchKey(taskId string, userId int, quota int) string {
	return fmt.Sprintf("%s:%d:%d", taskId, userId, quota)
}

func appendTaskRefundCandidates(items []AsyncRefundReconciliationItem, startTime int64, endTime int64, limit int) ([]AsyncRefundReconciliationItem, bool, error) {
	var tasks []Task
	query := DB.
		Where("status = ? AND quota > 0 AND finish_time >= ? AND finish_time <= ?", TaskStatusFailure, startTime, endTime).
		Order("finish_time asc, id asc").
		Limit(limit + 1).
		Find(&tasks)
	if query.Error != nil {
		return nil, false, query.Error
	}
	truncated := len(tasks) > limit
	if truncated {
		tasks = tasks[:limit]
	}
	for _, task := range tasks {
		items = append(items, AsyncRefundReconciliationItem{
			TaskKind:       AsyncTaskRefundKindTask,
			TaskDbId:       task.ID,
			TaskId:         task.TaskID,
			UserId:         task.UserId,
			ChannelId:      task.ChannelId,
			TokenId:        task.PrivateData.TokenId,
			SubscriptionId: task.PrivateData.SubscriptionId,
			BillingSource: normalizeAsyncTaskBillingSource(
				task.PrivateData.BillingSource,
				task.PrivateData.SubscriptionId,
			),
			Quota:           task.Quota,
			TaskStatus:      string(task.Status),
			SubmitTime:      task.SubmitTime,
			FinishTime:      task.FinishTime,
			FailReason:      task.FailReason,
			RefundLogIds:    []int{},
			AutomaticAction: false,
		})
	}
	return items, truncated, nil
}

func appendMidjourneyRefundCandidates(items []AsyncRefundReconciliationItem, startTime int64, endTime int64, limit int) ([]AsyncRefundReconciliationItem, bool, error) {
	var tasks []Midjourney
	query := DB.
		Where("status = ? AND quota > 0 AND finish_time >= ? AND finish_time <= ?", string(TaskStatusFailure), startTime, endTime).
		Order("finish_time asc, id asc").
		Limit(limit + 1).
		Find(&tasks)
	if query.Error != nil {
		return nil, false, query.Error
	}
	truncated := len(tasks) > limit
	if truncated {
		tasks = tasks[:limit]
	}
	for _, task := range tasks {
		items = append(items, AsyncRefundReconciliationItem{
			TaskKind:       AsyncTaskRefundKindMidjourney,
			TaskDbId:       int64(task.Id),
			TaskId:         task.MjId,
			UserId:         task.UserId,
			ChannelId:      task.ChannelId,
			TokenId:        task.TokenId,
			SubscriptionId: task.SubscriptionId,
			BillingSource: normalizeAsyncTaskBillingSource(
				task.BillingSource,
				task.SubscriptionId,
			),
			Quota:           task.Quota,
			TaskStatus:      task.Status,
			SubmitTime:      task.SubmitTime,
			FinishTime:      task.FinishTime,
			FailReason:      task.FailReason,
			RefundLogIds:    []int{},
			AutomaticAction: false,
		})
	}
	return items, truncated, nil
}

func appendUnknownTaskRefundCandidates(items []AsyncRefundReconciliationItem, startTime int64, endTime int64, limit int) ([]AsyncRefundReconciliationItem, bool, error) {
	var tasks []Task
	query := DB.
		Where("status NOT IN ? AND quota > 0 AND submit_time >= ? AND submit_time <= ?", []TaskStatus{TaskStatusFailure, TaskStatusSuccess}, startTime, endTime).
		Order("submit_time asc, id asc").
		Limit(maxAsyncRefundReconciliationScan + 1).
		Find(&tasks)
	if query.Error != nil {
		return nil, false, query.Error
	}
	truncated := len(tasks) > maxAsyncRefundReconciliationScan
	if truncated {
		tasks = tasks[:maxAsyncRefundReconciliationScan]
	}
	appended := 0
	for _, task := range tasks {
		source := normalizeAsyncTaskBillingSource(task.PrivateData.BillingSource, task.PrivateData.SubscriptionId)
		if source != AsyncTaskBillingSourceUnknown {
			continue
		}
		if appended >= limit {
			truncated = true
			break
		}
		items = append(items, AsyncRefundReconciliationItem{
			TaskKind:        AsyncTaskRefundKindTask,
			TaskDbId:        task.ID,
			TaskId:          task.TaskID,
			UserId:          task.UserId,
			ChannelId:       task.ChannelId,
			TokenId:         task.PrivateData.TokenId,
			SubscriptionId:  task.PrivateData.SubscriptionId,
			BillingSource:   source,
			Quota:           task.Quota,
			TaskStatus:      string(task.Status),
			SubmitTime:      task.SubmitTime,
			FinishTime:      task.FinishTime,
			FailReason:      task.FailReason,
			RefundLogIds:    []int{},
			ManualReview:    true,
			AutomaticAction: false,
		})
		appended++
	}
	return items, truncated, nil
}

func appendUnknownMidjourneyRefundCandidates(items []AsyncRefundReconciliationItem, startTime int64, endTime int64, limit int) ([]AsyncRefundReconciliationItem, bool, error) {
	var tasks []Midjourney
	query := DB.
		Where("status NOT IN ? AND quota > 0 AND submit_time >= ? AND submit_time <= ?", []string{string(TaskStatusFailure), string(TaskStatusSuccess)}, startTime, endTime).
		Where("billing_source NOT IN ?", []string{"wallet", "subscription"}).
		Order("submit_time asc, id asc").
		Limit(limit + 1).
		Find(&tasks)
	if query.Error != nil {
		return nil, false, query.Error
	}
	truncated := len(tasks) > limit
	if truncated {
		tasks = tasks[:limit]
	}
	for _, task := range tasks {
		items = append(items, AsyncRefundReconciliationItem{
			TaskKind:        AsyncTaskRefundKindMidjourney,
			TaskDbId:        int64(task.Id),
			TaskId:          task.MjId,
			UserId:          task.UserId,
			ChannelId:       task.ChannelId,
			TokenId:         task.TokenId,
			SubscriptionId:  task.SubscriptionId,
			BillingSource:   AsyncTaskBillingSourceUnknown,
			Quota:           task.Quota,
			TaskStatus:      task.Status,
			SubmitTime:      task.SubmitTime,
			FinishTime:      task.FinishTime,
			FailReason:      task.FailReason,
			RefundLogIds:    []int{},
			ManualReview:    true,
			AutomaticAction: false,
		})
	}
	return items, truncated, nil
}

func loadAsyncRefundRecords(items []AsyncRefundReconciliationItem) (map[string]AsyncTaskRefundRecord, error) {
	idsByKind := make(map[string][]int64)
	for _, item := range items {
		idsByKind[item.TaskKind] = append(idsByKind[item.TaskKind], item.TaskDbId)
	}
	recordsByIdentity := make(map[string]AsyncTaskRefundRecord)
	for kind, ids := range idsByKind {
		var records []AsyncTaskRefundRecord
		if err := DB.Where("task_kind = ? AND task_db_id IN ?", kind, ids).Find(&records).Error; err != nil {
			return nil, err
		}
		for _, record := range records {
			recordsByIdentity[asyncRefundIdentity(record.TaskKind, record.TaskDbId)] = record
		}
	}
	return recordsByIdentity, nil
}

type asyncRefundLogEvidence struct {
	Ids      []int
	EventIds []string
}

func (evidence asyncRefundLogEvidence) Count() int {
	return len(evidence.Ids) + len(evidence.EventIds)
}

func loadAsyncRefundLogs(startTime int64, endTime int64) (map[string]asyncRefundLogEvidence, bool, error) {
	var logs []asyncRefundLogCandidate
	query := LOG_DB.Model(&Log{}).
		Where("type = ? AND created_at >= ? AND created_at <= ?", LogTypeRefund, startTime, endTime).
		Limit(maxAsyncRefundReconciliationLogs + 1)
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		query = query.
			Select("event_id, user_id, created_at, quota, other").
			Order("created_at asc, event_id asc")
	} else {
		query = query.
			Select("id, user_id, created_at, quota, other").
			Order("created_at asc, id asc")
	}
	query = query.Find(&logs)
	if query.Error != nil {
		return nil, false, query.Error
	}
	truncated := len(logs) > maxAsyncRefundReconciliationLogs
	if truncated {
		logs = logs[:maxAsyncRefundReconciliationLogs]
	}
	logsByMatch := make(map[string]asyncRefundLogEvidence)
	for _, log := range logs {
		other, err := common.StrToMap(log.Other)
		if err != nil {
			continue
		}
		taskId, _ := other["task_id"].(string)
		if taskId == "" {
			continue
		}
		key := asyncRefundLogMatchKey(taskId, log.UserId, log.Quota)
		evidence := logsByMatch[key]
		if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
			evidence.EventIds = append(evidence.EventIds, log.EventId)
		} else {
			evidence.Ids = append(evidence.Ids, log.Id)
		}
		logsByMatch[key] = evidence
	}
	return logsByMatch, truncated, nil
}

// BuildAsyncRefundReconciliation returns a bounded, read-only evidence report.
// It never mutates task, funding, refund-record, or log state and deliberately
// exposes no batch-processing action.
func BuildAsyncRefundReconciliation(startTime int64, endTime int64, limit int) (*AsyncRefundReconciliationReport, error) {
	if startTime <= 0 || endTime <= startTime {
		return nil, errors.New("invalid reconciliation time range")
	}
	if limit <= 0 || limit > 500 {
		return nil, errors.New("reconciliation limit must be between 1 and 500")
	}
	items := make([]AsyncRefundReconciliationItem, 0, limit)
	var taskTruncated bool
	var err error
	items, taskTruncated, err = appendTaskRefundCandidates(items, startTime, endTime, limit)
	if err != nil {
		return nil, err
	}
	var midjourneyTruncated bool
	items, midjourneyTruncated, err = appendMidjourneyRefundCandidates(items, startTime, endTime, limit)
	if err != nil {
		return nil, err
	}
	var unknownTaskTruncated bool
	items, unknownTaskTruncated, err = appendUnknownTaskRefundCandidates(items, startTime, endTime, limit)
	if err != nil {
		return nil, err
	}
	var unknownMidjourneyTruncated bool
	items, unknownMidjourneyTruncated, err = appendUnknownMidjourneyRefundCandidates(items, startTime, endTime, limit)
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i int, j int) bool {
		leftTime := items[i].FinishTime
		if leftTime == 0 {
			leftTime = items[i].SubmitTime
		}
		rightTime := items[j].FinishTime
		if rightTime == 0 {
			rightTime = items[j].SubmitTime
		}
		if leftTime != rightTime {
			return leftTime < rightTime
		}
		if items[i].TaskKind != items[j].TaskKind {
			return items[i].TaskKind < items[j].TaskKind
		}
		return items[i].TaskDbId < items[j].TaskDbId
	})
	combinedTruncated := len(items) > limit
	if combinedTruncated {
		items = items[:limit]
	}

	recordsByIdentity, err := loadAsyncRefundRecords(items)
	if err != nil {
		return nil, err
	}
	logsByMatch, logsTruncated, err := loadAsyncRefundLogs(startTime, endTime)
	if err != nil {
		return nil, err
	}

	summary := AsyncRefundReconciliationSummary{Total: len(items)}
	for i := range items {
		item := &items[i]
		if record, ok := recordsByIdentity[asyncRefundIdentity(item.TaskKind, item.TaskDbId)]; ok {
			item.Evidence = AsyncRefundEvidenceDurableRecord
			item.RefundRecordId = record.Id
			item.BillingSource = record.BillingSource
			summary.DurableRecords++
			continue
		}
		if item.BillingSource == AsyncTaskBillingSourceUnknown {
			item.Evidence = AsyncRefundEvidenceUnknownSource
			item.ManualReview = true
			summary.UnknownSource++
			summary.ManualReview++
			continue
		}
		logEvidence := logsByMatch[asyncRefundLogMatchKey(item.TaskId, item.UserId, item.Quota)]
		item.RefundLogIds = logEvidence.Ids
		item.RefundLogEvents = logEvidence.EventIds
		switch logEvidence.Count() {
		case 0:
			item.Evidence = AsyncRefundEvidenceMissing
			item.ManualReview = true
			summary.MissingEvidence++
			summary.ManualReview++
		case 1:
			item.Evidence = AsyncRefundEvidenceHistoricalLog
			summary.HistoricalLogOnly++
		default:
			item.Evidence = AsyncRefundEvidenceAmbiguous
			item.ManualReview = true
			summary.AmbiguousLogs++
			summary.ManualReview++
		}
	}
	return &AsyncRefundReconciliationReport{
		StartTime:                  startTime,
		EndTime:                    endTime,
		ReadOnly:                   true,
		AutomaticProcessingAllowed: false,
		Truncated: taskTruncated ||
			midjourneyTruncated ||
			unknownTaskTruncated ||
			unknownMidjourneyTruncated ||
			combinedTruncated ||
			logsTruncated,
		Items:   items,
		Summary: summary,
	}, nil
}
