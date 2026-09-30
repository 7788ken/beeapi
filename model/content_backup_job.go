package model

import "time"

// Content backup status literals are part of the public DTO (design doc 9.2),
// so they stay strings instead of the int enums used elsewhere in this package.
const (
	ContentBackupStatusPending    = "pending"
	ContentBackupStatusProcessing = "processing"
	ContentBackupStatusFailed     = "failed"
	ContentBackupStatusUploaded   = "uploaded"
)

const (
	ContentBackupCleanupNotApplicable = "not_applicable"
	ContentBackupCleanupPending       = "pending"
	ContentBackupCleanupDone          = "done"
)

const (
	ContentBackupViewArchive = "archive"
	ContentBackupViewQueue   = "queue"
)

const (
	ContentBackupRetryQueued  = "queued"
	ContentBackupRetrySkipped = "skipped"
	ContentBackupRetryFailed  = "failed"
)

// Failures carrying these codes are unrecoverable without offline repair, so
// RetryFailed refuses to re-queue them (design doc 6.2 last row).
const (
	ContentBackupErrorLocalMissing    = "local_missing"
	ContentBackupErrorHashError       = "hash_error"
	ContentBackupErrorIncompleteSpool = "incomplete_spool"
)

const (
	contentBackupDefaultPageSize  = 20
	contentBackupMaxPageSize      = 100
	contentBackupDefaultScanLimit = 100
	contentBackupStatDateLayout   = "2006-01-02"
)

// contentBackupQueueStatusOrder is the queue view's "failed first" sort key.
// It is derived from the status constants so the SQL and contentBackupQueueRank
// cannot drift apart.
const contentBackupQueueStatusOrder = "CASE status WHEN '" + ContentBackupStatusFailed +
	"' THEN 0 WHEN '" + ContentBackupStatusProcessing + "' THEN 1 ELSE 2 END"

// ContentBackupJobIndexNames lists the unique key plus the eight composite
// indexes from design doc 6.1. Every composite index starts with site_id.
func ContentBackupJobIndexNames() []string {
	return []string{
		"uk_content_backup_jobs_site_job",
		"idx_cb_jobs_request",
		"idx_cb_jobs_status_created",
		"idx_cb_jobs_channel_created",
		"idx_cb_jobs_user_session_created",
		"idx_cb_jobs_claim",
		"idx_cb_jobs_lease",
		"idx_cb_jobs_cleanup",
		"idx_cb_jobs_expire",
		"idx_cb_jobs_site_created",
	}
}

// ContentBackupJob is both the durable work item and the time-boxed archive
// index: uploaded rows stay queryable after the local file has been released.
// Every timestamp column is int64 Unix seconds in UTC.
type ContentBackupJob struct {
	ID int64 `gorm:"column:id;primaryKey;autoIncrement" json:"-"`

	SiteID string `gorm:"column:site_id;type:varchar(32);not null;uniqueIndex:uk_content_backup_jobs_site_job,priority:1;index:idx_cb_jobs_request,priority:1;index:idx_cb_jobs_status_created,priority:1;index:idx_cb_jobs_channel_created,priority:1;index:idx_cb_jobs_user_session_created,priority:1;index:idx_cb_jobs_claim,priority:1;index:idx_cb_jobs_lease,priority:1;index:idx_cb_jobs_cleanup,priority:1;index:idx_cb_jobs_expire,priority:1;index:idx_cb_jobs_site_created,priority:1" json:"site_id"`
	// JobID is a lowercase dashed UUID v4 from contentbackup.NewJobID().
	JobID             string `gorm:"column:job_id;type:varchar(36);not null;uniqueIndex:uk_content_backup_jobs_site_job,priority:2;index:idx_cb_jobs_status_created,priority:4;index:idx_cb_jobs_channel_created,priority:4;index:idx_cb_jobs_user_session_created,priority:5;index:idx_cb_jobs_claim,priority:5;index:idx_cb_jobs_lease,priority:5;index:idx_cb_jobs_cleanup,priority:6;index:idx_cb_jobs_expire,priority:5;index:idx_cb_jobs_site_created,priority:3" json:"job_id"`
	RequestID         string `gorm:"column:request_id;type:varchar(64);not null;default:'';index:idx_cb_jobs_request,priority:2" json:"request_id"`
	UserID            int    `gorm:"column:user_id;not null;default:0;index:idx_cb_jobs_user_session_created,priority:2" json:"user_id"`
	TokenID           int    `gorm:"column:token_id;not null;default:0" json:"token_id"`
	ChannelID         int    `gorm:"column:channel_id;not null;default:0;index:idx_cb_jobs_channel_created,priority:2" json:"channel_id"`
	ChannelName       string `gorm:"column:channel_name;type:varchar(255);not null;default:''" json:"channel_name"`
	ChannelType       int    `gorm:"column:channel_type;not null;default:0" json:"channel_type"`
	Model             string `gorm:"column:model;type:varchar(128);not null;default:''" json:"model"`
	Endpoint          string `gorm:"column:endpoint;type:varchar(128);not null;default:''" json:"endpoint"`
	SessionSource     string `gorm:"column:session_source;type:varchar(32);not null;default:''" json:"session_source"`
	SessionHash       string `gorm:"column:session_hash;type:varchar(64);not null;default:'';index:idx_cb_jobs_user_session_created,priority:3" json:"session_hash"`
	SessionHint       string `gorm:"column:session_hint;type:varchar(255);not null;default:''" json:"session_hint"`
	UpstreamRequestID string `gorm:"column:upstream_request_id;type:varchar(191);not null;default:''" json:"upstream_request_id"`
	// CreatedAt is the request start instant and is never rewritten by a retry
	// or an orphan rebuild. idx_cb_jobs_site_created serves the archive list's default
	// order (site_id, created_at desc); without it every page load filesorts the whole site.
	CreatedAt int64 `gorm:"column:created_at;not null;index:idx_cb_jobs_status_created,priority:3;index:idx_cb_jobs_channel_created,priority:3;index:idx_cb_jobs_user_session_created,priority:4;index:idx_cb_jobs_site_created,priority:2" json:"created_at"`

	StorageNodeID         string `gorm:"column:storage_node_id;type:varchar(64);not null;default:'';index:idx_cb_jobs_claim,priority:2;index:idx_cb_jobs_lease,priority:2;index:idx_cb_jobs_cleanup,priority:2" json:"storage_node_id"`
	LocalPath             string `gorm:"column:local_path;type:varchar(512);not null;default:''" json:"-"`
	TargetID              string `gorm:"column:target_id;type:varchar(64);not null;default:''" json:"target_id"`
	ConfigVersion         int64  `gorm:"column:config_version;not null;default:0" json:"config_version"`
	RemotePath            string `gorm:"column:remote_path;type:varchar(512);not null;default:''" json:"remote_path"`
	FrameSHA256           string `gorm:"column:frame_sha256;type:varchar(64);not null;default:''" json:"frame_sha256"`
	CompressedSHA256      string `gorm:"column:compressed_sha256;type:varchar(64);not null;default:''" json:"compressed_sha256"`
	CompressedBytes       int64  `gorm:"column:compressed_bytes;not null;default:0" json:"compressed_bytes"`
	Stream                bool   `gorm:"column:stream;not null;default:false" json:"stream"`
	HTTPStatus            int    `gorm:"column:http_status;not null;default:0" json:"http_status"`
	TerminalReason        string `gorm:"column:terminal_reason;type:varchar(64);not null;default:''" json:"terminal_reason"`
	RequestContentType    string `gorm:"column:request_content_type;type:varchar(255);not null;default:''" json:"request_content_type"`
	RequestCapturedBytes  int64  `gorm:"column:request_captured_bytes;not null;default:0" json:"request_captured_bytes"`
	RequestObservedBytes  int64  `gorm:"column:request_observed_bytes;not null;default:0" json:"request_observed_bytes"`
	RequestTruncated      bool   `gorm:"column:request_truncated;not null;default:false" json:"request_truncated"`
	RequestComplete       bool   `gorm:"column:request_complete;not null;default:false" json:"request_complete"`
	ResponseContentType   string `gorm:"column:response_content_type;type:varchar(255);not null;default:''" json:"response_content_type"`
	ResponseCapturedBytes int64  `gorm:"column:response_captured_bytes;not null;default:0" json:"response_captured_bytes"`
	ResponseObservedBytes int64  `gorm:"column:response_observed_bytes;not null;default:0" json:"response_observed_bytes"`
	ResponseTruncated     bool   `gorm:"column:response_truncated;not null;default:false" json:"response_truncated"`
	ResponseComplete      bool   `gorm:"column:response_complete;not null;default:false" json:"response_complete"`

	Status           string `gorm:"column:status;type:varchar(16);not null;default:'pending';index:idx_cb_jobs_status_created,priority:2;index:idx_cb_jobs_claim,priority:3;index:idx_cb_jobs_lease,priority:3;index:idx_cb_jobs_cleanup,priority:3;index:idx_cb_jobs_expire,priority:2" json:"status"`
	Attempts         int    `gorm:"column:attempts;not null;default:0" json:"attempts"`
	RetryRound       int    `gorm:"column:retry_round;not null;default:0" json:"retry_round"`
	TotalAttempts    int    `gorm:"column:total_attempts;not null;default:0" json:"total_attempts"`
	LastErrorCode    string `gorm:"column:last_error_code;type:varchar(64);not null;default:''" json:"last_error_code"`
	LastErrorMessage string `gorm:"column:last_error_message;type:text" json:"last_error_message"`
	AvailableAt      int64  `gorm:"column:available_at;not null;default:0;index:idx_cb_jobs_claim,priority:4" json:"available_at"`
	LeaseOwner       string `gorm:"column:lease_owner;type:varchar(64);not null;default:''" json:"lease_owner"`
	LeaseToken       string `gorm:"column:lease_token;type:varchar(64);not null;default:''" json:"lease_token"`
	LeaseUntil       int64  `gorm:"column:lease_until;not null;default:0;index:idx_cb_jobs_lease,priority:4" json:"lease_until"`
	LeaseGeneration  int64  `gorm:"column:lease_generation;not null;default:0" json:"lease_generation"`
	UpdatedAt        int64  `gorm:"column:updated_at;not null;default:0" json:"updated_at"`

	UploadedAt         int64  `gorm:"column:uploaded_at;not null;default:0;index:idx_cb_jobs_expire,priority:4" json:"uploaded_at"`
	CleanupState       string `gorm:"column:cleanup_state;type:varchar(16);not null;default:'not_applicable';index:idx_cb_jobs_cleanup,priority:4;index:idx_cb_jobs_expire,priority:3" json:"cleanup_state"`
	CleanupAttempts    int    `gorm:"column:cleanup_attempts;not null;default:0" json:"cleanup_attempts"`
	CleanupAvailableAt int64  `gorm:"column:cleanup_available_at;not null;default:0;index:idx_cb_jobs_cleanup,priority:5" json:"cleanup_available_at"`
	CleanupError       string `gorm:"column:cleanup_error;type:text" json:"cleanup_error"`
	CleanedAt          int64  `gorm:"column:cleaned_at;not null;default:0" json:"cleaned_at"`
}

func (ContentBackupJob) TableName() string { return "content_backup_jobs" }

// ContentBackupLease is the full lease identity every state commit must carry.
// Until is a time.Time; the persisted lease_until column is Unix seconds.
type ContentBackupLease struct {
	SiteID        string    `json:"site_id"`
	JobID         string    `json:"job_id"`
	StorageNodeID string    `json:"storage_node_id"`
	Owner         string    `json:"owner"`
	Token         string    `json:"token"`
	Generation    int64     `json:"generation"`
	Until         time.Time `json:"until"`
}

// ContentBackupJobFilter drives operator-facing cursor pagination. It is not a
// worker scan API; use the bounded List*Jobs scans for that.
type ContentBackupJobFilter struct {
	View          string     `json:"view"`
	Status        string     `json:"status"`
	CleanupState  string     `json:"cleanup_state"`
	RequestID     string     `json:"request_id"`
	StorageNodeID string     `json:"storage_node_id"`
	SessionSource string     `json:"session_source"`
	SessionHash   string     `json:"session_hash"`
	Cursor        string     `json:"cursor"`
	UserID        *int       `json:"user_id"`
	ChannelID     *int       `json:"channel_id"`
	From          *time.Time `json:"from"`
	To            *time.Time `json:"to"`
	PageSize      int        `json:"page_size"`
}

type ContentBackupJobPage struct {
	Items      []ContentBackupJob `json:"items"`
	NextCursor *string            `json:"next_cursor"`
	HasMore    bool               `json:"has_more"`
}

type ContentBackupRetryResult struct {
	JobID  string `json:"job_id"`
	Result string `json:"result"`
	Reason string `json:"reason"`
}

// ContentBackupJobCounts backs the status bar and the node heartbeat. It covers the
// work queue and the cleanup set only; the uploaded archive is never counted here.
type ContentBackupJobCounts struct {
	PendingCount        int64 `json:"pending_count"`
	ProcessingCount     int64 `json:"processing_count"`
	FailedCount         int64 `json:"failed_count"`
	CleanupPendingCount int64 `json:"cleanup_pending_count"`
	CleanupPendingBytes int64 `json:"cleanup_pending_bytes"`
	OldestPendingAt     int64 `json:"oldest_pending_at"`
}
