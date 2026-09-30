package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"gorm.io/gorm"
)

func TestContentBackupJobEnumLiteralsAreFrozen(t *testing.T) {
	if got := []string{
		ContentBackupStatusPending,
		ContentBackupStatusProcessing,
		ContentBackupStatusFailed,
		ContentBackupStatusUploaded,
	}; !equalContentBackupStrings(got, []string{"pending", "processing", "failed", "uploaded"}) {
		t.Fatalf("status literals = %v, want pending/processing/failed/uploaded", got)
	}
	if got := []string{
		ContentBackupCleanupNotApplicable,
		ContentBackupCleanupPending,
		ContentBackupCleanupDone,
	}; !equalContentBackupStrings(got, []string{"not_applicable", "pending", "done"}) {
		t.Fatalf("cleanup_state literals = %v, want not_applicable/pending/done", got)
	}
	if got := []string{
		ContentBackupRetryQueued,
		ContentBackupRetrySkipped,
		ContentBackupRetryFailed,
	}; !equalContentBackupStrings(got, []string{"queued", "skipped", "failed"}) {
		t.Fatalf("retry result literals = %v, want queued/skipped/failed", got)
	}
}

func equalContentBackupStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestContentBackupJobIDColumnFitsTheFrozenContract(t *testing.T) {
	jobID := contentbackup.NewJobID()
	if len(jobID) != 36 {
		t.Fatalf("contentbackup.NewJobID() length = %d, want 36", len(jobID))
	}
	columns := contentBackupColumnTypes(t, &ContentBackupJob{})
	if got := columns["job_id"]; got != "varchar(36)" {
		t.Fatalf("job_id type = %q, want varchar(36)", got)
	}
	if got := columns["site_id"]; got != "varchar(32)" {
		t.Fatalf("site_id type = %q, want varchar(32)", got)
	}
	if got := columns["frame_sha256"]; got != "varchar(64)" {
		t.Fatalf("frame_sha256 type = %q, want varchar(64)", got)
	}
	if got := columns["compressed_sha256"]; got != "varchar(64)" {
		t.Fatalf("compressed_sha256 type = %q, want varchar(64)", got)
	}
	if got := columns["status"]; got != "varchar(16)" {
		t.Fatalf("status type = %q, want varchar(16)", got)
	}
	if got := columns["cleanup_state"]; got != "varchar(16)" {
		t.Fatalf("cleanup_state type = %q, want varchar(16)", got)
	}
}

func contentBackupColumnTypes(t *testing.T, model interface{}) map[string]string {
	t.Helper()
	statement := &gorm.Statement{DB: DB}
	if err := statement.Parse(model); err != nil {
		t.Fatalf("parse %T schema: %v", model, err)
	}
	types := make(map[string]string, len(statement.Schema.Fields))
	for _, field := range statement.Schema.Fields {
		types[field.DBName] = strings.ToLower(field.TagSettings["TYPE"])
	}
	return types
}

func TestContentBackupJobTableAndColumnNames(t *testing.T) {
	if got := (ContentBackupJob{}).TableName(); got != "content_backup_jobs" {
		t.Fatalf("jobs table = %q, want content_backup_jobs", got)
	}
	if got := (ContentBackupDailyStat{}).TableName(); got != "content_backup_daily_stats" {
		t.Fatalf("stats table = %q, want content_backup_daily_stats", got)
	}
	if got := (ContentBackupStatDelta{}).TableName(); got != "content_backup_stat_deltas" {
		t.Fatalf("stat delta table = %q, want content_backup_stat_deltas", got)
	}
	if got := (ContentBackupNodeStatus{}).TableName(); got != "content_backup_node_status" {
		t.Fatalf("node table = %q, want content_backup_node_status", got)
	}
	if got := (ContentBackupAlert{}).TableName(); got != "content_backup_alerts" {
		t.Fatalf("alerts table = %q, want content_backup_alerts", got)
	}

	required := map[string][]string{
		"content_backup_jobs": {
			"site_id", "job_id", "request_id", "user_id", "token_id", "channel_id", "channel_name",
			"channel_type", "model", "endpoint", "session_source", "session_hash", "session_hint", "created_at",
			"storage_node_id", "local_path", "target_id", "config_version", "remote_path", "frame_sha256",
			"compressed_sha256", "compressed_bytes",
			"status", "attempts", "retry_round", "total_attempts", "last_error_code", "last_error_message",
			"available_at", "lease_owner", "lease_token", "lease_until", "lease_generation", "updated_at",
			"uploaded_at", "cleanup_state", "cleanup_attempts", "cleanup_available_at", "cleanup_error", "cleaned_at",
		},
		"content_backup_daily_stats": {
			"site_id", "stat_date", "storage_node_id", "uploaded_count", "uploaded_bytes",
			"cleaned_count", "freed_bytes", "created_at", "updated_at",
		},
		"content_backup_node_status": {
			"site_id", "storage_node_id", "process_id", "config_version", "applied_config_version",
			"last_seen_at", "sampled_at", "spool_bytes", "spool_limit_bytes", "disk_total_bytes", "free_bytes",
			"inode_total", "free_inodes", "pending_count", "processing_count", "failed_count",
			"oldest_pending_at", "cleanup_pending_count", "cleanup_pending_bytes", "orphan_count",
			"incomplete_spool_count", "handoff_rejected_count", "handoff_unknown_count",
			"upload_bytes_per_second", "created_at", "updated_at",
		},
		"content_backup_stat_deltas": {
			"site_id", "storage_node_id", "stat_date", "uploaded_bytes", "created_at",
		},
		"content_backup_alerts": {
			"site_id", "storage_node_id", "reason", "message", "trigger_count", "state",
			"send_lease_owner", "send_lease_until", "last_sent_at", "last_triggered_at", "resolved_at",
			"created_at", "updated_at",
		},
	}
	for _, model := range ContentBackupModels() {
		statement := &gorm.Statement{DB: DB}
		if err := statement.Parse(model); err != nil {
			t.Fatalf("parse %T: %v", model, err)
		}
		table := statement.Schema.Table
		wanted, ok := required[table]
		if !ok {
			t.Fatalf("unexpected content backup table %q", table)
		}
		present := make(map[string]bool, len(statement.Schema.Fields))
		for _, field := range statement.Schema.Fields {
			present[field.DBName] = true
		}
		for _, column := range wanted {
			if !present[column] {
				t.Fatalf("table %s is missing column %s", table, column)
			}
		}
	}
}

func TestContentBackupJobIndexNamesAreFrozen(t *testing.T) {
	names := ContentBackupJobIndexNames()
	if len(names) != 10 {
		t.Fatalf("index count = %d, want the unique key plus 9 composite indexes", len(names))
	}
	want := []string{
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
	if !equalContentBackupStrings(names, want) {
		t.Fatalf("index names = %v, want %v", names, want)
	}
}

func TestContentBackupLocalPathIsNeverSerialized(t *testing.T) {
	columns := contentBackupColumnTypes(t, &ContentBackupJob{})
	if _, ok := columns["local_path"]; !ok {
		t.Fatal("local_path must stay a persisted column")
	}
	job := ContentBackupJob{JobID: "x", LocalPath: "/spool/secret.json.gz"}
	encodedBytes, err := common.Marshal(job)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	encoded := string(encodedBytes)
	if strings.Contains(encoded, "/spool/secret.json.gz") || strings.Contains(encoded, "local_path") {
		t.Fatalf("local_path leaked into JSON: %s", encoded)
	}
}
