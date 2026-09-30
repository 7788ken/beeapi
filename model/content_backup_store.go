package model

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrLeaseLost means a state commit did not match the full lease identity, so
// the caller must stop uploading and close its FTP connections.
var ErrLeaseLost = errors.New("content backup job lease is no longer held")

// ErrCleanupConflict means a cleanup write did not match its guard: the job is
// not uploaded, is not pending cleanup, or belongs to another storage node.
var ErrCleanupConflict = errors.New("content backup cleanup guard did not match")

const contentBackupLeaseGuard = "site_id = ? AND job_id = ? AND storage_node_id = ? AND lease_owner = ? AND lease_token = ? AND status = ?"

// ContentBackupStore is the only supported way to reach the backup tables.
// It carries an explicit *gorm.DB so tests and admin handlers never have to
// swap the package level model.DB, and every query is pinned to one site_id.
type ContentBackupStore struct {
	db     *gorm.DB
	siteID string
}

func NewContentBackupStore(db *gorm.DB, siteID string) *ContentBackupStore {
	return &ContentBackupStore{db: db, siteID: siteID}
}

func (s *ContentBackupStore) SiteID() string { return s.siteID }

// ContentBackupModels lists the tables owned by this feature.
func ContentBackupModels() []interface{} {
	return []interface{}{
		&ContentBackupJob{},
		&ContentBackupDailyStat{},
		&ContentBackupStatDelta{},
		&ContentBackupNodeStatus{},
		&ContentBackupAlert{},
	}
}

func contentBackupTableName(item interface{}) string {
	switch item.(type) {
	case *ContentBackupJob:
		return (ContentBackupJob{}).TableName()
	case *ContentBackupDailyStat:
		return (ContentBackupDailyStat{}).TableName()
	case *ContentBackupStatDelta:
		return (ContentBackupStatDelta{}).TableName()
	case *ContentBackupNodeStatus:
		return (ContentBackupNodeStatus{}).TableName()
	case *ContentBackupAlert:
		return (ContentBackupAlert{}).TableName()
	}
	return fmt.Sprintf("%T", item)
}

func contentBackupUnix(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().Unix()
}

func contentBackupTime(unixSeconds int64) time.Time {
	if unixSeconds <= 0 {
		return time.Time{}
	}
	return time.Unix(unixSeconds, 0).UTC()
}

// contentBackupStatLocation 把日统计的日历日键对齐到北京时间，与 remote_path 的
// 目录日期同源；否则客查"某天的备份"会与统计日错开 8 小时。回退写法同
// subscription.go，避免容器缺 tzdata 时静默退化成 UTC。
var contentBackupStatLocation = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	return time.FixedZone("CST", 8*3600)
}()

func contentBackupDayFromUnix(unixSeconds int64) string {
	if unixSeconds <= 0 {
		return ""
	}
	return time.Unix(unixSeconds, 0).In(contentBackupStatLocation).Format(contentBackupStatDateLayout)
}

// EnsurePending admits a job exactly once. A retry or an orphan rebuild that
// finds an existing row leaves it untouched, so created_at stays the request
// start instant and an in-flight lease is never clobbered.
func (s *ContentBackupStore) EnsurePending(ctx context.Context, job ContentBackupJob) error {
	if s.db == nil {
		return errors.New("content backup database is not initialized")
	}
	job.SiteID = s.siteID
	if job.JobID == "" || job.StorageNodeID == "" {
		return errors.New("content backup job identity is incomplete")
	}
	if job.CreatedAt <= 0 {
		return errors.New("content backup job created_at must be the request start instant")
	}
	if job.Status == "" {
		job.Status = ContentBackupStatusPending
	}
	if job.CleanupState == "" {
		job.CleanupState = ContentBackupCleanupNotApplicable
	}
	if job.AvailableAt == 0 {
		job.AvailableAt = job.CreatedAt
	}
	if job.UpdatedAt == 0 {
		job.UpdatedAt = job.CreatedAt
	}

	job.ID = 0
	err := s.db.WithContext(ctx).Create(&job).Error
	if err == nil {
		return nil
	}
	var existing ContentBackupJob
	lookupErr := s.db.WithContext(ctx).
		Select("id", "created_at").
		Where("site_id = ? AND job_id = ?", s.siteID, job.JobID).
		First(&existing).Error
	if lookupErr != nil {
		return fmt.Errorf("ensure content backup job %s: %w", job.JobID, err)
	}
	return nil
}

func (s *ContentBackupStore) GetJob(ctx context.Context, jobID string) (ContentBackupJob, error) {
	var job ContentBackupJob
	err := s.db.WithContext(ctx).
		Where("site_id = ? AND job_id = ?", s.siteID, jobID).
		First(&job).Error
	return job, err
}

// contentBackupIDChunk bounds one IN (...) list. SQLite allows 999 bound
// parameters by default, so 500 ids plus the site_id stays inside every dialect.
const contentBackupIDChunk = 500

// JobStatusesByIDs reports which of the given job ids already have a row, and in
// which status. The reconcile scan uses it to answer "is this spool file already
// registered" without decompressing the file first: only a job id that is absent
// here is a real orphan worth rebuilding from its envelope.
func (s *ContentBackupStore) JobStatusesByIDs(ctx context.Context, jobIDs []string) (map[string]string, error) {
	found := make(map[string]string, len(jobIDs))
	for start := 0; start < len(jobIDs); start += contentBackupIDChunk {
		end := start + contentBackupIDChunk
		if end > len(jobIDs) {
			end = len(jobIDs)
		}
		var rows []struct {
			JobID  string
			Status string
		}
		err := s.db.WithContext(ctx).
			Table((ContentBackupJob{}).TableName()).
			Select("job_id", "status").
			Where("site_id = ? AND job_id IN ?", s.siteID, jobIDs[start:end]).
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			found[row.JobID] = row.Status
		}
	}
	return found, nil
}

// CurrentLeaseTokens maps each given job id that is still processing to the token of its
// current lease, expired or not. The incoming reaper keeps exactly the temps these tokens
// name. It deliberately compares no clocks: lease_until was written with the uploading
// node's clock, and a reaper on another node running ahead would see a live lease as over.
func (s *ContentBackupStore) CurrentLeaseTokens(ctx context.Context, jobIDs []string) (map[string]string, error) {
	current := make(map[string]string, len(jobIDs))
	for start := 0; start < len(jobIDs); start += contentBackupIDChunk {
		end := start + contentBackupIDChunk
		if end > len(jobIDs) {
			end = len(jobIDs)
		}
		var rows []struct {
			JobID      string
			LeaseToken string
		}
		err := s.db.WithContext(ctx).
			Table((ContentBackupJob{}).TableName()).
			Select("job_id", "lease_token").
			Where("site_id = ? AND job_id IN ? AND status = ?", s.siteID, jobIDs[start:end], ContentBackupStatusProcessing).
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			current[row.JobID] = row.LeaseToken
		}
	}
	return current, nil
}

// GetJobByFrameSHA256 backs the duplicate-handoff lookup (design doc 4.2 step
// 7). It is a rare fallback and deliberately not covered by an extra index.
func (s *ContentBackupStore) GetJobByFrameSHA256(ctx context.Context, storageNodeID, frameSHA256 string) (ContentBackupJob, error) {
	var job ContentBackupJob
	err := s.db.WithContext(ctx).
		Where("site_id = ? AND storage_node_id = ? AND frame_sha256 = ?", s.siteID, storageNodeID, frameSHA256).
		Order("id desc").
		First(&job).Error
	return job, err
}

type contentBackupCursor struct {
	Rank      int    `json:"r"`
	CreatedAt int64  `json:"c"`
	JobID     string `json:"j"`
}

func encodeContentBackupCursor(cursor contentBackupCursor) (string, error) {
	encoded, err := common.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode content backup cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeContentBackupCursor(value string) (contentBackupCursor, error) {
	var cursor contentBackupCursor
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return cursor, fmt.Errorf("decode content backup cursor: %w", err)
	}
	if err := common.Unmarshal(raw, &cursor); err != nil {
		return cursor, fmt.Errorf("decode content backup cursor: %w", err)
	}
	if cursor.JobID == "" {
		return cursor, errors.New("content backup cursor is incomplete")
	}
	return cursor, nil
}

func contentBackupQueueRank(status string) int {
	switch status {
	case ContentBackupStatusFailed:
		return 0
	case ContentBackupStatusProcessing:
		return 1
	default:
		return 2
	}
}

// ListJobs is the operator cursor pagination. It is not a scan API: the daemon
// workers use the bounded List*Jobs queries below.
func (s *ContentBackupStore) ListJobs(ctx context.Context, filter ContentBackupJobFilter) (ContentBackupJobPage, error) {
	page := ContentBackupJobPage{Items: []ContentBackupJob{}}
	size := filter.PageSize
	if size <= 0 {
		size = contentBackupDefaultPageSize
	}
	if size > contentBackupMaxPageSize {
		size = contentBackupMaxPageSize
	}
	queue := strings.EqualFold(filter.View, ContentBackupViewQueue)

	query := s.db.WithContext(ctx).Model(&ContentBackupJob{}).Where("site_id = ?", s.siteID)
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	} else if queue {
		query = query.Where("status IN ?", []string{
			ContentBackupStatusPending,
			ContentBackupStatusProcessing,
			ContentBackupStatusFailed,
		})
	}
	if filter.CleanupState != "" {
		query = query.Where("cleanup_state = ?", filter.CleanupState)
	}
	if filter.RequestID != "" {
		query = query.Where("request_id = ?", filter.RequestID)
	}
	if filter.StorageNodeID != "" {
		query = query.Where("storage_node_id = ?", filter.StorageNodeID)
	}
	if filter.SessionSource != "" {
		query = query.Where("session_source = ?", filter.SessionSource)
	}
	if filter.SessionHash != "" {
		query = query.Where("session_hash = ?", filter.SessionHash)
	}
	if filter.UserID != nil {
		query = query.Where("user_id = ?", *filter.UserID)
	}
	if filter.ChannelID != nil {
		query = query.Where("channel_id = ?", *filter.ChannelID)
	}
	if filter.From != nil {
		query = query.Where("created_at >= ?", contentBackupUnix(*filter.From))
	}
	if filter.To != nil {
		query = query.Where("created_at <= ?", contentBackupUnix(*filter.To))
	}

	if strings.TrimSpace(filter.Cursor) != "" {
		cursor, err := decodeContentBackupCursor(filter.Cursor)
		if err != nil {
			return page, err
		}
		if queue {
			order := contentBackupQueueStatusOrder
			query = query.Where(
				"("+order+" > ?) OR ("+order+" = ? AND created_at > ?) OR ("+order+" = ? AND created_at = ? AND job_id > ?)",
				cursor.Rank, cursor.Rank, cursor.CreatedAt, cursor.Rank, cursor.CreatedAt, cursor.JobID,
			)
		} else {
			query = query.Where(
				"(created_at < ?) OR (created_at = ? AND job_id < ?)",
				cursor.CreatedAt, cursor.CreatedAt, cursor.JobID,
			)
		}
	}

	if queue {
		query = query.Order(contentBackupQueueStatusOrder + " asc, created_at asc, job_id asc")
	} else {
		query = query.Order("created_at desc, job_id desc")
	}

	var items []ContentBackupJob
	if err := query.Limit(size + 1).Find(&items).Error; err != nil {
		return page, err
	}
	page.HasMore = len(items) > size
	if page.HasMore {
		items = items[:size]
	}
	page.Items = items
	if page.HasMore && len(items) > 0 {
		last := items[len(items)-1]
		encoded, err := encodeContentBackupCursor(contentBackupCursor{
			Rank:      contentBackupQueueRank(last.Status),
			CreatedAt: last.CreatedAt,
			JobID:     last.JobID,
		})
		if err != nil {
			return page, err
		}
		page.NextCursor = &encoded
	}
	return page, nil
}

// usingSQLite prefers the store's own dialector over the global flag: the
// daemon opens a dedicated *gorm.DB, and emitting FOR UPDATE on SQLite would
// be a hard error if that global had not been set for the daemon's dialect.
func (s *ContentBackupStore) usingSQLite() bool {
	if s.db != nil && s.db.Dialector != nil {
		return s.db.Dialector.Name() == "sqlite"
	}
	return common.UsingSQLite
}

func (s *ContentBackupStore) lockForUpdate(query *gorm.DB) *gorm.DB {
	if s.usingSQLite() {
		return query
	}
	return query.Clauses(clause.Locking{Strength: "UPDATE"})
}

func (s *ContentBackupStore) validateLeaseIdentity(lease ContentBackupLease) error {
	if lease.JobID == "" || lease.StorageNodeID == "" || lease.Owner == "" || lease.Token == "" {
		return errors.New("content backup lease identity is incomplete")
	}
	if lease.SiteID != "" && lease.SiteID != s.siteID {
		return ErrLeaseLost
	}
	return nil
}

// Claim uses a conditional UPDATE guarded by lease_generation instead of
// SKIP LOCKED, so one claim protocol works on SQLite, MySQL and PostgreSQL.
// It also never scans the uploaded archive: the guard only matches pending or
// lapsed-processing rows on the owning node.
func (s *ContentBackupStore) Claim(
	ctx context.Context,
	jobID, storageNodeID, owner string,
	now, until time.Time,
) (ContentBackupLease, bool, error) {
	if jobID == "" || storageNodeID == "" || owner == "" {
		return ContentBackupLease{}, false, errors.New("content backup claim identity is incomplete")
	}
	nowUnix := contentBackupUnix(now)
	untilUnix := contentBackupUnix(until)
	if untilUnix <= nowUnix {
		return ContentBackupLease{}, false, errors.New("content backup lease must expire after the claim instant")
	}
	token := common.GetUUID()

	var won bool
	var generation int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row ContentBackupJob
		query := tx.Select("id", "lease_generation").
			Where(
				"site_id = ? AND job_id = ? AND storage_node_id = ? AND ((status = ? AND available_at <= ?) OR (status = ? AND lease_until <= ?))",
				s.siteID, jobID, storageNodeID,
				ContentBackupStatusPending, nowUnix,
				ContentBackupStatusProcessing, nowUnix,
			)
		if err := s.lockForUpdate(query).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		generation = row.LeaseGeneration + 1
		result := tx.Model(&ContentBackupJob{}).
			Where("id = ? AND lease_generation = ?", row.ID, row.LeaseGeneration).
			Updates(map[string]any{
				"status":           ContentBackupStatusProcessing,
				"lease_owner":      owner,
				"lease_token":      token,
				"lease_until":      untilUnix,
				"lease_generation": generation,
				"attempts":         gorm.Expr("attempts + 1"),
				"total_attempts":   gorm.Expr("total_attempts + 1"),
				"updated_at":       nowUnix,
			})
		if result.Error != nil {
			return result.Error
		}
		won = result.RowsAffected == 1
		return nil
	})
	if err != nil {
		return ContentBackupLease{}, false, err
	}
	if !won {
		return ContentBackupLease{}, false, nil
	}
	return ContentBackupLease{
		SiteID:        s.siteID,
		JobID:         jobID,
		StorageNodeID: storageNodeID,
		Owner:         owner,
		Token:         token,
		Generation:    generation,
		Until:         contentBackupTime(untilUnix),
	}, true, nil
}

// Renew is changed-rows safe: MySQL reports 0 affected rows when the renewed
// lease_until equals the stored one, which is not the same as losing the lease.
// Ownership is therefore proved by a locked guard read, not by RowsAffected.
func (s *ContentBackupStore) Renew(
	ctx context.Context,
	lease ContentBackupLease,
	now, until time.Time,
) (ContentBackupLease, error) {
	if err := s.validateLeaseIdentity(lease); err != nil {
		return ContentBackupLease{}, err
	}
	nowUnix := contentBackupUnix(now)
	untilUnix := contentBackupUnix(until)
	if untilUnix <= nowUnix {
		return ContentBackupLease{}, errors.New("content backup lease must expire after the renewal instant")
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row ContentBackupJob
		query := tx.Select("id", "lease_generation").
			Where(
				"site_id = ? AND job_id = ? AND storage_node_id = ? AND lease_owner = ? AND lease_token = ? AND status = ? AND lease_until > ?",
				s.siteID, lease.JobID, lease.StorageNodeID, lease.Owner, lease.Token,
				ContentBackupStatusProcessing, nowUnix,
			)
		if err := s.lockForUpdate(query).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrLeaseLost
			}
			return err
		}
		result := tx.Model(&ContentBackupJob{}).
			Where("id = ?", row.ID).
			Updates(map[string]any{"lease_until": untilUnix, "updated_at": nowUnix})
		return result.Error
	})
	if err != nil {
		return ContentBackupLease{}, err
	}
	renewed := lease
	renewed.SiteID = s.siteID
	renewed.Until = contentBackupTime(untilUnix)
	return renewed, nil
}

// MarkUploaded commits status=uploaded and cleanup_state=pending together with
// exactly one stat delta, in one transaction gated by the lease CAS. A repeated or
// ACK-lost commit returns nil without counting again. The delta is a row of its own;
// FoldStatDeltas later moves it into the daily stats.
func (s *ContentBackupStore) MarkUploaded(ctx context.Context, lease ContentBackupLease, now time.Time) error {
	if err := s.validateLeaseIdentity(lease); err != nil {
		return err
	}
	nowUnix := contentBackupUnix(now)
	day := contentBackupDayFromUnix(nowUnix)
	if day == "" {
		return errors.New("content backup upload instant is required")
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job ContentBackupJob
		query := tx.Select("id", "status", "cleanup_state", "compressed_bytes", "lease_owner", "lease_token", "storage_node_id").
			Where("site_id = ? AND job_id = ?", s.siteID, lease.JobID)
		if err := s.lockForUpdate(query).First(&job).Error; err != nil {
			return err
		}
		if job.Status == ContentBackupStatusUploaded {
			return nil
		}
		if job.Status != ContentBackupStatusProcessing ||
			job.LeaseToken != lease.Token ||
			job.LeaseOwner != lease.Owner ||
			job.StorageNodeID != lease.StorageNodeID {
			return ErrLeaseLost
		}
		result := tx.Model(&ContentBackupJob{}).
			Where("id = ? AND status = ? AND lease_token = ?", job.ID, ContentBackupStatusProcessing, lease.Token).
			Updates(map[string]any{
				"status":               ContentBackupStatusUploaded,
				"cleanup_state":        ContentBackupCleanupPending,
				"uploaded_at":          nowUnix,
				"cleanup_available_at": nowUnix,
				"lease_owner":          "",
				"lease_token":          "",
				"lease_until":          0,
				"last_error_code":      "",
				"last_error_message":   "",
				"updated_at":           nowUnix,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLeaseLost
		}
		return tx.Create(&ContentBackupStatDelta{
			SiteID:        s.siteID,
			StorageNodeID: lease.StorageNodeID,
			StatDate:      day,
			UploadedBytes: job.CompressedBytes,
			CreatedAt:     nowUnix,
		}).Error
	})
	return s.settleJobError(ctx, err, lease.JobID, ContentBackupStatusUploaded)
}

func (s *ContentBackupStore) ScheduleRetry(
	ctx context.Context,
	lease ContentBackupLease,
	code, message string,
	now, next time.Time,
) error {
	if err := s.validateLeaseIdentity(lease); err != nil {
		return err
	}
	nowUnix := contentBackupUnix(now)
	nextUnix := contentBackupUnix(next)
	if nextUnix <= nowUnix {
		return errors.New("content backup retry must be scheduled in the future")
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// attempts and total_attempts were already raised by Claim, so a retry
		// write must not raise them a second time.
		result := tx.Model(&ContentBackupJob{}).
			Where(contentBackupLeaseGuard,
				s.siteID, lease.JobID, lease.StorageNodeID, lease.Owner, lease.Token, ContentBackupStatusProcessing).
			Updates(map[string]any{
				"status":             ContentBackupStatusPending,
				"available_at":       nextUnix,
				"lease_owner":        "",
				"lease_token":        "",
				"lease_until":        0,
				"last_error_code":    code,
				"last_error_message": message,
				"updated_at":         nowUnix,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLeaseLost
		}
		return nil
	})
	return s.settleJobError(ctx, err, lease.JobID, ContentBackupStatusPending)
}

func (s *ContentBackupStore) MarkFailed(
	ctx context.Context,
	lease ContentBackupLease,
	code, message string,
	now time.Time,
) error {
	if err := s.validateLeaseIdentity(lease); err != nil {
		return err
	}
	nowUnix := contentBackupUnix(now)

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&ContentBackupJob{}).
			Where(contentBackupLeaseGuard,
				s.siteID, lease.JobID, lease.StorageNodeID, lease.Owner, lease.Token, ContentBackupStatusProcessing).
			Updates(map[string]any{
				"status":             ContentBackupStatusFailed,
				"available_at":       0,
				"lease_owner":        "",
				"lease_token":        "",
				"lease_until":        0,
				"last_error_code":    code,
				"last_error_message": message,
				"updated_at":         nowUnix,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLeaseLost
		}
		return nil
	})
	return s.settleJobError(ctx, err, lease.JobID, ContentBackupStatusFailed)
}

// settleJobError covers "the commit may have landed but its acknowledgement was
// lost": a transport failure is re-checked against the durable state instead of
// being replayed. A clean guard miss stays ErrLeaseLost.
func (s *ContentBackupStore) settleJobError(ctx context.Context, err error, jobID, terminalStatus string) error {
	if err == nil ||
		errors.Is(err, ErrLeaseLost) ||
		errors.Is(err, ErrCleanupConflict) ||
		errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if current, lookupErr := s.GetJob(ctx, jobID); lookupErr == nil && current.Status == terminalStatus {
		return nil
	}
	return err
}

// MarkCleaned records that the local file is gone. It never touches the upload
// status and never raises uploaded_count; freed space lands in its own columns.
func (s *ContentBackupStore) MarkCleaned(ctx context.Context, jobID, storageNodeID string, now time.Time) error {
	if jobID == "" || storageNodeID == "" {
		return errors.New("content backup cleanup identity is incomplete")
	}
	nowUnix := contentBackupUnix(now)
	day := contentBackupDayFromUnix(nowUnix)

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job ContentBackupJob
		query := tx.Select("id", "status", "cleanup_state", "compressed_bytes").
			Where("site_id = ? AND job_id = ? AND storage_node_id = ?", s.siteID, jobID, storageNodeID)
		if err := s.lockForUpdate(query).First(&job).Error; err != nil {
			return err
		}
		if job.CleanupState == ContentBackupCleanupDone {
			// The delete succeeded earlier but its acknowledgement was lost.
			return nil
		}
		if job.Status != ContentBackupStatusUploaded || job.CleanupState != ContentBackupCleanupPending {
			return ErrCleanupConflict
		}
		result := tx.Model(&ContentBackupJob{}).
			Where("id = ? AND status = ? AND cleanup_state = ?", job.ID, ContentBackupStatusUploaded, ContentBackupCleanupPending).
			Updates(map[string]any{
				"cleanup_state":        ContentBackupCleanupDone,
				"cleaned_at":           nowUnix,
				"cleanup_available_at": 0,
				"cleanup_error":        "",
				"updated_at":           nowUnix,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrCleanupConflict
		}
		if day == "" {
			return nil
		}
		return s.addDailyCleanup(tx, day, storageNodeID, job.CompressedBytes, nowUnix)
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrCleanupConflict) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrCleanupConflict
	}
	var job ContentBackupJob
	lookupErr := s.db.WithContext(ctx).
		Select("id", "cleanup_state").
		Where("site_id = ? AND job_id = ? AND storage_node_id = ?", s.siteID, jobID, storageNodeID).
		First(&job).Error
	if lookupErr == nil && job.CleanupState == ContentBackupCleanupDone {
		return nil
	}
	return err
}

// ScheduleCleanup books an independent cleanup retry. It keeps status=uploaded
// so a failed delete is never mistaken for a failed upload.
func (s *ContentBackupStore) ScheduleCleanup(
	ctx context.Context,
	jobID, storageNodeID, message string,
	now, next time.Time,
) error {
	if jobID == "" || storageNodeID == "" {
		return errors.New("content backup cleanup identity is incomplete")
	}
	result := s.db.WithContext(ctx).Model(&ContentBackupJob{}).
		Where("site_id = ? AND job_id = ? AND storage_node_id = ? AND status = ? AND cleanup_state = ?",
			s.siteID, jobID, storageNodeID, ContentBackupStatusUploaded, ContentBackupCleanupPending).
		Updates(map[string]any{
			"cleanup_attempts":     gorm.Expr("cleanup_attempts + 1"),
			"cleanup_available_at": contentBackupUnix(next),
			"cleanup_error":        message,
			"updated_at":           contentBackupUnix(now),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCleanupConflict
	}
	return nil
}

func contentBackupErrorIsUnrecoverable(code string) bool {
	switch code {
	case ContentBackupErrorLocalMissing, ContentBackupErrorHashError, ContentBackupErrorIncompleteSpool:
		return true
	}
	return false
}

// RetryFailed re-queues only failed jobs. attempts resets for the new round
// while total_attempts is preserved as the lifetime counter.
func (s *ContentBackupStore) RetryFailed(ctx context.Context, jobID string, now time.Time) (ContentBackupRetryResult, error) {
	result := ContentBackupRetryResult{JobID: jobID, Result: ContentBackupRetryFailed}
	if jobID == "" {
		result.Reason = "job_id_required"
		return result, nil
	}

	var job ContentBackupJob
	err := s.db.WithContext(ctx).
		Select("id", "status", "last_error_code").
		Where("site_id = ? AND job_id = ?", s.siteID, jobID).
		First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		result.Reason = "not_found"
		return result, nil
	}
	if err != nil {
		return result, err
	}

	switch job.Status {
	case ContentBackupStatusFailed:
	case ContentBackupStatusUploaded:
		result.Result = ContentBackupRetrySkipped
		result.Reason = "already_uploaded"
		return result, nil
	case ContentBackupStatusProcessing:
		result.Result = ContentBackupRetrySkipped
		result.Reason = "processing"
		return result, nil
	case ContentBackupStatusPending:
		result.Result = ContentBackupRetrySkipped
		result.Reason = "already_queued"
		return result, nil
	default:
		result.Reason = "unknown_status:" + job.Status
		return result, nil
	}

	if contentBackupErrorIsUnrecoverable(job.LastErrorCode) {
		result.Result = ContentBackupRetrySkipped
		result.Reason = "not_retryable:" + job.LastErrorCode
		return result, nil
	}

	update := s.db.WithContext(ctx).Model(&ContentBackupJob{}).
		Where("site_id = ? AND job_id = ? AND status = ?", s.siteID, jobID, ContentBackupStatusFailed).
		Updates(map[string]any{
			"status":             ContentBackupStatusPending,
			"attempts":           0,
			"retry_round":        gorm.Expr("retry_round + 1"),
			"available_at":       contentBackupUnix(now),
			"lease_owner":        "",
			"lease_token":        "",
			"lease_until":        0,
			"last_error_code":    "",
			"last_error_message": "",
			"updated_at":         contentBackupUnix(now),
		})
	if update.Error != nil {
		return result, update.Error
	}
	if update.RowsAffected != 1 {
		result.Result = ContentBackupRetrySkipped
		result.Reason = "state_changed"
		return result, nil
	}
	result.Result = ContentBackupRetryQueued
	result.Reason = "queued"
	return result, nil
}

var errContentBackupFoldRaced = errors.New("content backup stat deltas were folded by another process")

// FoldStatDeltas moves up to one batch of this node's upload deltas into the daily stats.
// Deleting the deltas and raising the daily rows commit together, and the delete has to
// remove every row that was read: a slot that raced to the same rows removes fewer, rolls
// back and counts nothing. It returns how many deltas were folded.
func (s *ContentBackupStore) FoldStatDeltas(ctx context.Context, storageNodeID string, limit int, now time.Time) (int, error) {
	if storageNodeID == "" {
		return 0, errors.New("content backup stat fold requires a storage node id")
	}
	if limit <= 0 || limit > contentBackupIDChunk {
		limit = contentBackupIDChunk
	}
	var deltas []ContentBackupStatDelta
	if err := s.db.WithContext(ctx).
		Where("site_id = ? AND storage_node_id = ?", s.siteID, storageNodeID).
		Order("id asc").
		Limit(limit).
		Find(&deltas).Error; err != nil {
		return 0, err
	}
	if len(deltas) == 0 {
		return 0, nil
	}
	ids := make([]int64, 0, len(deltas))
	perDay := map[string]*ContentBackupDailyStat{}
	var days []string
	for _, delta := range deltas {
		ids = append(ids, delta.ID)
		sum := perDay[delta.StatDate]
		if sum == nil {
			sum = &ContentBackupDailyStat{}
			perDay[delta.StatDate] = sum
			days = append(days, delta.StatDate)
		}
		sum.UploadedCount++
		sum.UploadedBytes += delta.UploadedBytes
	}
	nowUnix := contentBackupUnix(now)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("site_id = ? AND id IN ?", s.siteID, ids).Delete(&ContentBackupStatDelta{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(ids)) {
			return errContentBackupFoldRaced
		}
		for _, day := range days {
			sum := perDay[day]
			if err := s.addDailyUploads(tx, day, storageNodeID, sum.UploadedCount, sum.UploadedBytes, nowUnix); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errContentBackupFoldRaced) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (s *ContentBackupStore) addDailyUploads(tx *gorm.DB, day, storageNodeID string, count, bytes, nowUnix int64) error {
	row := &ContentBackupDailyStat{
		SiteID:        s.siteID,
		StatDate:      day,
		StorageNodeID: storageNodeID,
		UploadedCount: count,
		UploadedBytes: bytes,
		CreatedAt:     nowUnix,
		UpdatedAt:     nowUnix,
	}
	// PostgreSQL rejects a bare column in DO UPDATE SET as ambiguous against
	// EXCLUDED, so the increments are qualified with the table name.
	table := (ContentBackupDailyStat{}).TableName()
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "site_id"}, {Name: "stat_date"}, {Name: "storage_node_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"uploaded_count": gorm.Expr(table+".uploaded_count + ?", count),
			"uploaded_bytes": gorm.Expr(table+".uploaded_bytes + ?", bytes),
			"updated_at":     nowUnix,
		}),
	}).Create(row).Error
}

func (s *ContentBackupStore) addDailyCleanup(tx *gorm.DB, day, storageNodeID string, bytes, nowUnix int64) error {
	row := &ContentBackupDailyStat{
		SiteID:        s.siteID,
		StatDate:      day,
		StorageNodeID: storageNodeID,
		CleanedCount:  1,
		FreedBytes:    bytes,
		CreatedAt:     nowUnix,
		UpdatedAt:     nowUnix,
	}
	table := (ContentBackupDailyStat{}).TableName()
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "site_id"}, {Name: "stat_date"}, {Name: "storage_node_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"cleaned_count": gorm.Expr(table + ".cleaned_count + 1"),
			"freed_bytes":   gorm.Expr(table+".freed_bytes + ?", bytes),
			"updated_at":    nowUnix,
		}),
	}).Create(row).Error
}

func (s *ContentBackupStore) GetDailyStats(ctx context.Context, day, storageNodeID string) (ContentBackupDailyStat, error) {
	query := s.db.WithContext(ctx).
		Model(&ContentBackupDailyStat{}).
		Where("site_id = ? AND stat_date = ?", s.siteID, day)
	if storageNodeID != "" {
		query = query.Where("storage_node_id = ?", storageNodeID)
	}
	var rows int64
	if err := query.Count(&rows).Error; err != nil {
		return ContentBackupDailyStat{}, err
	}
	if rows == 0 {
		return ContentBackupDailyStat{}, gorm.ErrRecordNotFound
	}

	var aggregate ContentBackupDailyStat
	aggregateQuery := s.db.WithContext(ctx).
		Table((ContentBackupDailyStat{}).TableName()).
		Select([]string{
			"COALESCE(SUM(uploaded_count), 0) AS uploaded_count",
			"COALESCE(SUM(uploaded_bytes), 0) AS uploaded_bytes",
			"COALESCE(SUM(cleaned_count), 0) AS cleaned_count",
			"COALESCE(SUM(freed_bytes), 0) AS freed_bytes",
		}).
		Where("site_id = ? AND stat_date = ?", s.siteID, day)
	if storageNodeID != "" {
		aggregateQuery = aggregateQuery.Where("storage_node_id = ?", storageNodeID)
	}
	if err := aggregateQuery.Scan(&aggregate).Error; err != nil {
		return ContentBackupDailyStat{}, err
	}
	aggregate.SiteID = s.siteID
	aggregate.StatDate = day
	aggregate.StorageNodeID = storageNodeID
	return aggregate, nil
}

func (s *ContentBackupStore) ListDailyStats(ctx context.Context, fromDay, toDay string, limit int) ([]ContentBackupDailyStat, error) {
	if limit <= 0 {
		limit = contentBackupDefaultScanLimit
	}
	var stats []ContentBackupDailyStat
	query := s.db.WithContext(ctx).
		Where("site_id = ? AND stat_date >= ? AND stat_date <= ?", s.siteID, fromDay, toDay).
		Order("stat_date desc, storage_node_id asc").
		Limit(limit)
	if err := query.Find(&stats).Error; err != nil {
		return nil, err
	}
	return stats, nil
}

// PruneDailyStats drops whole stat days older than beforeDay (stats retention).
func (s *ContentBackupStore) PruneDailyStats(ctx context.Context, beforeDay string, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	var ids []int64
	if err := s.db.WithContext(ctx).
		Model(&ContentBackupDailyStat{}).
		Where("site_id = ? AND stat_date < ?", s.siteID, beforeDay).
		Order("stat_date asc, id asc").
		Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := s.db.WithContext(ctx).
		Where("site_id = ? AND id IN ?", s.siteID, ids).
		Delete(&ContentBackupDailyStat{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func (s *ContentBackupStore) SaveNodeStatus(ctx context.Context, status ContentBackupNodeStatus) error {
	status.SiteID = s.siteID
	// The identity is (site_id, storage_node_id); the surrogate key must never
	// be supplied, or the upsert would target an arbitrary row.
	status.ID = 0
	if strings.TrimSpace(status.StorageNodeID) == "" {
		return errors.New("content backup node status requires a storage node id")
	}
	stamp := status.LastSeenAt
	if stamp <= 0 {
		stamp = status.SampledAt
	}
	if stamp <= 0 {
		stamp = time.Now().UTC().Unix()
	}
	status.UpdatedAt = stamp
	if status.CreatedAt == 0 {
		status.CreatedAt = stamp
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "site_id"}, {Name: "storage_node_id"}},
		DoUpdates: clause.AssignmentColumns(contentBackupNodeStatusMutableColumns()),
	}).Create(&status).Error
}

func contentBackupNodeStatusMutableColumns() []string {
	return []string{
		"process_id",
		"config_version",
		"applied_config_version",
		"last_seen_at",
		"sampled_at",
		"spool_bytes",
		"spool_limit_bytes",
		"disk_total_bytes",
		"free_bytes",
		"inode_total",
		"free_inodes",
		"pending_count",
		"processing_count",
		"failed_count",
		"oldest_pending_at",
		"cleanup_pending_count",
		"cleanup_pending_bytes",
		"orphan_count",
		"incomplete_spool_count",
		// 2026-09-18 起备份管道跑在业务进程内，交接计数与心跳出自同一个进程，随心跳整行更新。
		"handoff_rejected_count",
		"handoff_unknown_count",
		"upload_bytes_per_second",
		"ftps_credentials_set",
		"updated_at",
	}
}

func (s *ContentBackupStore) GetNodeStatus(ctx context.Context, storageNodeID string) (ContentBackupNodeStatus, error) {
	var status ContentBackupNodeStatus
	err := s.db.WithContext(ctx).
		Where("site_id = ? AND storage_node_id = ?", s.siteID, storageNodeID).
		First(&status).Error
	return status, err
}

func (s *ContentBackupStore) ListNodeStatus(ctx context.Context) ([]ContentBackupNodeStatus, error) {
	var statuses []ContentBackupNodeStatus
	err := s.db.WithContext(ctx).
		Where("site_id = ?", s.siteID).
		Order("storage_node_id asc").
		Find(&statuses).Error
	return statuses, err
}

// CountJobs is the bounded aggregate behind the status bar and the node heartbeat. It
// counts only the work queue and the cleanup set, both reached through status-prefixed
// indexes and both small. It used to SUM(CASE ...) over every row of the site every 15
// seconds; the uploaded archive grows by hundreds of thousands of rows a day, and the
// archive-wide total that forced the full scan had no reader. An empty storageNodeID
// aggregates the whole site.
func (s *ContentBackupStore) CountJobs(ctx context.Context, storageNodeID string) (ContentBackupJobCounts, error) {
	var counts ContentBackupJobCounts
	queue := s.db.WithContext(ctx).
		Table((ContentBackupJob{}).TableName()).
		Select(
			"COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) AS pending_count,"+
				" COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) AS processing_count,"+
				" COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) AS failed_count,"+
				" COALESCE(MIN(CASE WHEN status = ? THEN created_at END), 0) AS oldest_pending_at",
			ContentBackupStatusPending,
			ContentBackupStatusProcessing,
			ContentBackupStatusFailed,
			ContentBackupStatusPending,
		).
		Where("site_id = ? AND status IN ?", s.siteID,
			[]string{ContentBackupStatusPending, ContentBackupStatusProcessing, ContentBackupStatusFailed})
	if storageNodeID != "" {
		queue = queue.Where("storage_node_id = ?", storageNodeID)
	}
	if err := queue.Scan(&counts).Error; err != nil {
		return counts, err
	}

	var cleanup struct {
		CleanupPendingCount int64
		CleanupPendingBytes int64
	}
	pending := s.db.WithContext(ctx).
		Table((ContentBackupJob{}).TableName()).
		Select("COUNT(*) AS cleanup_pending_count, COALESCE(SUM(compressed_bytes), 0) AS cleanup_pending_bytes").
		Where("site_id = ? AND status = ? AND cleanup_state = ?",
			s.siteID, ContentBackupStatusUploaded, ContentBackupCleanupPending)
	if storageNodeID != "" {
		pending = pending.Where("storage_node_id = ?", storageNodeID)
	}
	if err := pending.Scan(&cleanup).Error; err != nil {
		return counts, err
	}
	counts.CleanupPendingCount = cleanup.CleanupPendingCount
	counts.CleanupPendingBytes = cleanup.CleanupPendingBytes
	return counts, nil
}

// ExpireUploadedIndex deletes at most limit archive rows of this node that are uploaded,
// already cleaned locally and uploaded before cutoff (design doc 6.1: the uploaded index
// is kept index_retention_days, 30 by default). Only that terminal state is touched:
// pending, failed and cleanup-pending rows never expire by age, and remote bodies are not
// handled here. The retention setting used to have no reader, so the table grew by
// hundreds of thousands of rows a day on the shared business database.
func (s *ContentBackupStore) ExpireUploadedIndex(ctx context.Context, storageNodeID string, cutoff time.Time, limit int) (int64, error) {
	if storageNodeID == "" || limit <= 0 {
		return 0, nil
	}
	before := contentBackupUnix(cutoff)
	var ids []int64
	if err := s.db.WithContext(ctx).Model(&ContentBackupJob{}).
		Where("site_id = ? AND status = ? AND cleanup_state = ? AND uploaded_at < ? AND storage_node_id = ?",
			s.siteID, ContentBackupStatusUploaded, ContentBackupCleanupDone, before, storageNodeID).
		Order("uploaded_at asc").
		Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := s.db.WithContext(ctx).
		Where("id IN ? AND status = ? AND cleanup_state = ? AND uploaded_at < ?",
			ids, ContentBackupStatusUploaded, ContentBackupCleanupDone, before).
		Delete(&ContentBackupJob{})
	return result.RowsAffected, result.Error
}

func (s *ContentBackupStore) scanLimit(limit int) int {
	if limit <= 0 {
		return 0
	}
	if limit > contentBackupDefaultScanLimit*10 {
		return contentBackupDefaultScanLimit * 10
	}
	return limit
}

// ListClaimableJobs is the bounded due-work scan for the upload worker. It
// never walks the uploaded archive.
func (s *ContentBackupStore) ListClaimableJobs(ctx context.Context, storageNodeID string, now time.Time, limit int) ([]ContentBackupJob, error) {
	size := s.scanLimit(limit)
	if size == 0 {
		return []ContentBackupJob{}, nil
	}
	if strings.TrimSpace(storageNodeID) == "" {
		return nil, errors.New("content backup scan requires a storage node id")
	}
	var jobs []ContentBackupJob
	err := s.db.WithContext(ctx).
		Where("site_id = ? AND storage_node_id = ? AND status = ? AND available_at <= ?",
			s.siteID, storageNodeID, ContentBackupStatusPending, contentBackupUnix(now)).
		Order("available_at asc, job_id asc").
		Limit(size).
		Find(&jobs).Error
	return jobs, err
}

// ListExpiredLeases is the bounded lease-recovery scan, kept separate from the
// claim scan so neither has to inspect successful history.
func (s *ContentBackupStore) ListExpiredLeases(ctx context.Context, storageNodeID string, now time.Time, limit int) ([]ContentBackupJob, error) {
	size := s.scanLimit(limit)
	if size == 0 {
		return []ContentBackupJob{}, nil
	}
	if strings.TrimSpace(storageNodeID) == "" {
		return nil, errors.New("content backup scan requires a storage node id")
	}
	var jobs []ContentBackupJob
	err := s.db.WithContext(ctx).
		Where("site_id = ? AND storage_node_id = ? AND status = ? AND lease_until <= ?",
			s.siteID, storageNodeID, ContentBackupStatusProcessing, contentBackupUnix(now)).
		Order("lease_until asc, job_id asc").
		Limit(size).
		Find(&jobs).Error
	return jobs, err
}

// ListCleanupJobs is the bounded scan for the local cleanup worker: only this
// site, this node, uploaded and still waiting for space to be released.
func (s *ContentBackupStore) ListCleanupJobs(ctx context.Context, storageNodeID string, now time.Time, limit int) ([]ContentBackupJob, error) {
	size := s.scanLimit(limit)
	if size == 0 {
		return []ContentBackupJob{}, nil
	}
	if strings.TrimSpace(storageNodeID) == "" {
		return nil, errors.New("content backup scan requires a storage node id")
	}
	var jobs []ContentBackupJob
	err := s.db.WithContext(ctx).
		Where("site_id = ? AND storage_node_id = ? AND status = ? AND cleanup_state = ? AND cleanup_available_at <= ?",
			s.siteID, storageNodeID, ContentBackupStatusUploaded, ContentBackupCleanupPending, contentBackupUnix(now)).
		Order("cleanup_available_at asc, job_id asc").
		Limit(size).
		Find(&jobs).Error
	return jobs, err
}

// ListExpirableIndexes previews exactly the rows ExpireCleanedIndexes deletes.
func (s *ContentBackupStore) ListExpirableIndexes(ctx context.Context, storageNodeID string, before time.Time, limit int) ([]ContentBackupJob, error) {
	size := s.scanLimit(limit)
	if size == 0 {
		return []ContentBackupJob{}, nil
	}
	if strings.TrimSpace(storageNodeID) == "" {
		return nil, errors.New("content backup scan requires a storage node id")
	}
	var jobs []ContentBackupJob
	err := s.db.WithContext(ctx).
		Select("id", "job_id", "remote_path", "uploaded_at", "cleaned_at").
		Where("site_id = ? AND storage_node_id = ? AND status = ? AND cleanup_state = ? AND uploaded_at < ?",
			s.siteID, storageNodeID, ContentBackupStatusUploaded, ContentBackupCleanupDone, contentBackupUnix(before)).
		Order("uploaded_at asc, job_id asc").
		Limit(size).
		Find(&jobs).Error
	return jobs, err
}

// ExpireCleanedIndexes drops archive index rows only after the local file has
// been deleted (cleanup_state=done). Unuploaded, failed and pending-cleanup
// records are never removed by age. The delete is a bounded select-then-delete
// because PostgreSQL has no DELETE ... LIMIT.
func (s *ContentBackupStore) ExpireCleanedIndexes(ctx context.Context, storageNodeID string, before time.Time, limit int) (int64, error) {
	size := s.scanLimit(limit)
	if size == 0 {
		return 0, nil
	}
	if strings.TrimSpace(storageNodeID) == "" {
		return 0, errors.New("content backup index expiry requires a storage node id")
	}
	var ids []int64
	if err := s.db.WithContext(ctx).
		Model(&ContentBackupJob{}).
		Where("site_id = ? AND storage_node_id = ? AND status = ? AND cleanup_state = ? AND uploaded_at < ?",
			s.siteID, storageNodeID, ContentBackupStatusUploaded, ContentBackupCleanupDone, contentBackupUnix(before)).
		Order("uploaded_at asc, job_id asc").
		Limit(size).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := s.db.WithContext(ctx).
		Where("site_id = ? AND id IN ?", s.siteID, ids).
		Delete(&ContentBackupJob{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func (s *ContentBackupStore) UpsertAlert(ctx context.Context, alert ContentBackupAlert, now time.Time) error {
	alert.SiteID = s.siteID
	// The identity is (site_id, storage_node_id, reason); never trust a caller
	// supplied surrogate key.
	alert.ID = 0
	if strings.TrimSpace(alert.StorageNodeID) == "" || strings.TrimSpace(alert.Reason) == "" {
		return errors.New("content backup alert requires a storage node id and a reason")
	}
	nowUnix := contentBackupUnix(now)
	alert.State = ContentBackupAlertFiring
	alert.TriggerCount = 1
	alert.LastTriggeredAt = nowUnix
	alert.ResolvedAt = 0
	alert.CreatedAt = nowUnix
	alert.UpdatedAt = nowUnix
	table := (ContentBackupAlert{}).TableName()
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "site_id"}, {Name: "storage_node_id"}, {Name: "reason"}},
		DoUpdates: clause.Assignments(map[string]any{
			"message":           alert.Message,
			"trigger_count":     gorm.Expr(table + ".trigger_count + 1"),
			"state":             ContentBackupAlertFiring,
			"last_triggered_at": nowUnix,
			"resolved_at":       0,
			"updated_at":        nowUnix,
		}),
	}).Create(&alert).Error
}

func (s *ContentBackupStore) GetAlert(ctx context.Context, storageNodeID, reason string) (ContentBackupAlert, error) {
	var alert ContentBackupAlert
	err := s.db.WithContext(ctx).
		Where("site_id = ? AND storage_node_id = ? AND reason = ?", s.siteID, storageNodeID, reason).
		First(&alert).Error
	return alert, err
}

func (s *ContentBackupStore) ListAlerts(ctx context.Context, storageNodeID string, limit int) ([]ContentBackupAlert, error) {
	size := limit
	if size <= 0 {
		size = contentBackupDefaultScanLimit
	}
	query := s.db.WithContext(ctx).Where("site_id = ?", s.siteID)
	if storageNodeID != "" {
		query = query.Where("storage_node_id = ?", storageNodeID)
	}
	var alerts []ContentBackupAlert
	err := query.Order("state asc, last_triggered_at desc, reason asc").Limit(size).Find(&alerts).Error
	return alerts, err
}

// ClaimAlertSend takes the notification lease. It only succeeds for a firing
// alert whose previous notification is older than the dedup window.
func (s *ContentBackupStore) ClaimAlertSend(
	ctx context.Context,
	storageNodeID, reason, owner string,
	now, until time.Time,
	dedup time.Duration,
) (bool, error) {
	if strings.TrimSpace(storageNodeID) == "" || strings.TrimSpace(reason) == "" || strings.TrimSpace(owner) == "" {
		return false, errors.New("content backup alert send identity is incomplete")
	}
	nowUnix := contentBackupUnix(now)
	untilUnix := contentBackupUnix(until)
	if untilUnix <= nowUnix {
		return false, errors.New("content backup alert send lease must expire after now")
	}
	dedupSeconds := int64(dedup / time.Second)
	if dedupSeconds < 0 {
		dedupSeconds = 0
	}
	result := s.db.WithContext(ctx).Model(&ContentBackupAlert{}).
		Where("site_id = ? AND storage_node_id = ? AND reason = ? AND state = ? AND send_lease_until <= ? AND (last_sent_at = 0 OR last_sent_at <= ?)",
			s.siteID, storageNodeID, reason, ContentBackupAlertFiring, nowUnix, nowUnix-dedupSeconds).
		Updates(map[string]any{
			"send_lease_owner": owner,
			"send_lease_until": untilUnix,
			"updated_at":       nowUnix,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (s *ContentBackupStore) MarkAlertSent(ctx context.Context, storageNodeID, reason, owner string, now time.Time) error {
	nowUnix := contentBackupUnix(now)
	result := s.db.WithContext(ctx).Model(&ContentBackupAlert{}).
		Where("site_id = ? AND storage_node_id = ? AND reason = ? AND state = ? AND send_lease_owner = ?",
			s.siteID, storageNodeID, reason, ContentBackupAlertFiring, owner).
		Updates(map[string]any{
			"last_sent_at":     nowUnix,
			"send_lease_owner": "",
			"send_lease_until": 0,
			"updated_at":       nowUnix,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("content backup alert send lease is no longer held")
	}
	return nil
}

// ResolveAlert reports true only on the firing -> resolved transition, so the
// recovery notification is sent exactly once.
func (s *ContentBackupStore) ResolveAlert(ctx context.Context, storageNodeID, reason string, now time.Time) (bool, error) {
	nowUnix := contentBackupUnix(now)
	result := s.db.WithContext(ctx).Model(&ContentBackupAlert{}).
		Where("site_id = ? AND storage_node_id = ? AND reason = ? AND state = ?",
			s.siteID, storageNodeID, reason, ContentBackupAlertFiring).
		Updates(map[string]any{
			"state":            ContentBackupAlertResolved,
			"resolved_at":      nowUnix,
			"send_lease_owner": "",
			"send_lease_until": 0,
			"updated_at":       nowUnix,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}
