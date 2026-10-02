package contentbackup

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

var configKeyPattern = regexp.MustCompile(`"([a-z0-9_]+)":`)

func TestContentBackupConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()
	assertInt64(t, "Version", cfg.Version, 1)
	assertBool(t, "Enabled", cfg.Enabled, false)
	assertBool(t, "UploadPaused", cfg.UploadPaused, false)
	assertString(t, "TargetID", cfg.TargetID, "")
	assertString(t, "FTPSHost", cfg.FTPSHost, "")
	assertInt64(t, "FTPSPort", int64(cfg.FTPSPort), 21)
	assertString(t, "CertSHA256", cfg.CertSHA256, "")
	assertString(t, "RemoteProtocol", cfg.RemoteProtocol, RemoteProtocolFTPS)
	assertString(t, "SFTPHost", cfg.SFTPHost, "")
	assertInt64(t, "SFTPPort", int64(cfg.SFTPPort), 22)
	assertString(t, "SFTPHostKeySHA256", cfg.SFTPHostKeySHA256, "")
	assertString(t, "SFTPBaseDir", cfg.SFTPBaseDir, "")

	assertInt64(t, "MaxBodyBytes", cfg.MaxBodyBytes, 8*1024*1024)
	assertInt64(t, "CaptureMemoryMB", int64(cfg.CaptureMemoryMB), 64)
	assertInt64(t, "MaxInflightCaptures", int64(cfg.MaxInflightCaptures), 128)
	assertInt64(t, "HandoffWorkers", int64(cfg.HandoffWorkers), 1)
	assertInt64(t, "SpoolWorkers", int64(cfg.SpoolWorkers), 1)
	assertInt64(t, "UploadWorkers", int64(cfg.UploadWorkers), 8)
	assertInt64(t, "ReadWorkers", int64(cfg.ReadWorkers), 1)
	assertInt64(t, "MaxSpoolMB", int64(cfg.MaxSpoolMB), 2048)

	assertInt64(t, "HandoffAttemptTimeoutSeconds", int64(cfg.HandoffAttemptTimeoutSeconds), 30)
	assertInt64(t, "HandoffDeadlineSeconds", int64(cfg.HandoffDeadlineSeconds), 60)
	assertInt64(t, "HandoffMaxAttempts", int64(cfg.HandoffMaxAttempts), 2)

	assertInt64(t, "DaemonDBMaxOpen", int64(cfg.DaemonDBMaxOpen), 4)
	assertInt64(t, "DaemonDBMaxIdle", int64(cfg.DaemonDBMaxIdle), 2)
	assertInt64(t, "DaemonSQLiteDBMaxOpen", int64(cfg.DaemonSQLiteDBMaxOpen), 1)
	assertInt64(t, "DaemonDBTimeoutSeconds", int64(cfg.DaemonDBTimeoutSeconds), 3)
	assertInt64(t, "ConfigReloadSeconds", int64(cfg.ConfigReloadSeconds), 5)

	assertInt64(t, "UploadBandwidthMiB", int64(cfg.UploadBandwidthMiB), 32)
	assertInt64(t, "ReadBandwidthMiB", int64(cfg.ReadBandwidthMiB), 1)
	assertInt64(t, "UploadTimeoutSeconds", int64(cfg.UploadTimeoutSeconds), 120)
	assertInt64(t, "FTPSConnectTimeoutSeconds", int64(cfg.FTPSConnectTimeoutSeconds), 10)
	assertInt64(t, "LeaseSeconds", int64(cfg.LeaseSeconds), 180)
	assertInt64(t, "LeaseRenewSeconds", int64(cfg.LeaseRenewSeconds), 30)
	assertInt64(t, "MaxUploadAttempts", int64(cfg.MaxUploadAttempts), 16)

	assertInt64(t, "ReadTimeoutSeconds", int64(cfg.ReadTimeoutSeconds), 30)
	assertInt64(t, "PreviewBytesPerSide", cfg.PreviewBytesPerSide, 256*1024)
	assertInt64(t, "MaxDecompressBytes", cfg.MaxDecompressBytes, 24*1024*1024)
	assertInt64(t, "ReadBudgetMB", int64(cfg.ReadBudgetMB), 128)

	assertInt64(t, "ReconcileIntervalSeconds", int64(cfg.ReconcileIntervalSeconds), 30)
	assertInt64(t, "HeartbeatIntervalSeconds", int64(cfg.HeartbeatIntervalSeconds), 15)
	assertInt64(t, "NodeOfflineSeconds", int64(cfg.NodeOfflineSeconds), 45)
	assertInt64(t, "QueueRefreshSeconds", int64(cfg.QueueRefreshSeconds), 10)
	assertInt64(t, "NodeStatsRefreshSeconds", int64(cfg.NodeStatsRefreshSeconds), 30)

	assertInt64(t, "SpoolAlertPercent", int64(cfg.SpoolAlertPercent), 80)
	assertInt64(t, "SpoolStopPercent", int64(cfg.SpoolStopPercent), 90)
	assertInt64(t, "InodeAlertPercent", int64(cfg.InodeAlertPercent), 90)
	assertInt64(t, "InodeRecoverPercent", int64(cfg.InodeRecoverPercent), 80)
	assertInt64(t, "MinFreeBytes", cfg.MinFreeBytes, 1024*1024*1024)
	assertInt64(t, "MinFreePercent", int64(cfg.MinFreePercent), 10)

	assertInt64(t, "OldestPendingAlertMinutes", int64(cfg.OldestPendingAlertMinutes), 15)
	assertInt64(t, "CleanupPendingAlertMinutes", int64(cfg.CleanupPendingAlertMinutes), 30)
	assertInt64(t, "AlertDedupMinutes", int64(cfg.AlertDedupMinutes), 30)
	assertBool(t, "NotifyEmailEnabled", cfg.NotifyEmailEnabled, false)
	assertString(t, "NotifyEmails", cfg.NotifyEmails, "")
	assertBool(t, "NotifyOldestPending", cfg.NotifyOldestPending, true)
	assertBool(t, "NotifyCleanupPending", cfg.NotifyCleanupPending, true)
	assertBool(t, "NotifySpoolHigh", cfg.NotifySpoolHigh, true)
	assertBool(t, "NotifyInodeHigh", cfg.NotifyInodeHigh, true)
	assertBool(t, "NotifyFailed", cfg.NotifyFailed, true)
	assertBool(t, "NotifyHandoffRejected", cfg.NotifyHandoffRejected, true)
	assertBool(t, "NotifyNodeOffline", cfg.NotifyNodeOffline, true)

	assertInt64(t, "ContentRetentionDays", int64(cfg.ContentRetentionDays), 30)
	assertInt64(t, "IndexRetentionDays", int64(cfg.IndexRetentionDays), 30)
	assertInt64(t, "StatsRetentionDays", int64(cfg.StatsRetentionDays), 90)

	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("ValidateConfig(DefaultConfig()) = %v", err)
	}
}

func TestContentBackupConfigJSONKeys(t *testing.T) {
	encoded, err := common.Marshal(DefaultConfig())
	if err != nil {
		t.Fatalf("marshal default config: %v", err)
	}
	t.Logf("default config json: %s", encoded)
	want := []string{
		"version", "enabled", "upload_paused", "site_label", "target_id", "remote_username", "remote_password",
		"ftps_host", "ftps_port", "cert_sha256",
		"remote_protocol", "sftp_host", "sftp_port", "sftp_host_key_sha256", "sftp_base_dir",
		"max_body_bytes", "capture_memory_mb", "max_inflight_captures", "handoff_workers",
		"spool_workers", "upload_workers", "read_workers", "max_spool_mb",
		"handoff_attempt_timeout_seconds", "handoff_deadline_seconds", "handoff_max_attempts",
		"daemon_db_max_open", "daemon_db_max_idle", "daemon_sqlite_db_max_open",
		"daemon_db_timeout_seconds", "config_reload_seconds",
		"upload_bandwidth_mib", "read_bandwidth_mib", "upload_timeout_seconds",
		"ftps_connect_timeout_seconds", "lease_seconds", "lease_renew_seconds", "max_upload_attempts",
		"read_timeout_seconds", "preview_bytes_per_side", "max_decompress_bytes", "read_budget_mb",
		"reconcile_interval_seconds", "heartbeat_interval_seconds", "node_offline_seconds",
		"queue_refresh_seconds", "node_stats_refresh_seconds",
		"spool_alert_percent", "spool_stop_percent", "inode_alert_percent", "inode_recover_percent",
		"min_free_bytes", "min_free_percent",
		"oldest_pending_alert_minutes", "cleanup_pending_alert_minutes", "alert_dedup_minutes",
		"notify_email_enabled", "notify_emails",
		"notify_oldest_pending", "notify_cleanup_pending", "notify_spool_high",
		"notify_inode_high", "notify_failed", "notify_handoff_rejected", "notify_node_offline",
		"content_retention_days", "index_retention_days", "stats_retention_days",
	}
	// 原始键集 + 后续新增键集必须恰好覆盖当前全部键：新增字段忘了登记进 LaterConfigKeys，
	// ParseContentBackupConfig 就不知道它允许缺失；塞进 OriginalConfigKeys 则会把所有旧行拒掉。
	registered := map[string]bool{}
	for _, key := range OriginalConfigKeys {
		registered[key] = true
	}
	for _, key := range LaterConfigKeys {
		if registered[key] {
			t.Fatalf("key %q is listed both as original and later", key)
		}
		registered[key] = true
	}
	for _, key := range want {
		if !registered[key] {
			t.Fatalf("json key %q is neither in OriginalConfigKeys nor LaterConfigKeys", key)
		}
	}
	if len(registered) != len(want) {
		t.Fatalf("registered %d keys, config has %d", len(registered), len(want))
	}
	matches := configKeyPattern.FindAllStringSubmatch(string(encoded), -1)
	got := make([]string, 0, len(matches))
	for _, match := range matches {
		got = append(got, match[1])
	}
	if len(got) != len(want) {
		t.Fatalf("config has %d json keys, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("json key %d = %q, want %q (full order: %v)", i, got[i], want[i], got)
		}
	}
	// 节点身份绑定 spool 卷、spool 路径是部署事实，都不能从网页改；凭据自 2026-09-18 起
	// 与 SMTP/支付密钥同一方式存在配置里（remote_username / remote_password），但只有这一对名字。
	for _, forbidden := range []string{
		"site_id", "storage_node_id", "process_id", "node_name",
		"socket", "socket_path", "spool_dir", "local_path",
		"ftps_username", "ftps_password", "sftp_username", "sftp_password", "private_key", "passphrase",
	} {
		if strings.Contains(string(encoded), `"`+forbidden+`"`) {
			t.Fatalf("node identity and spool paths must not be editable config, found %q", forbidden)
		}
	}
	redacted := DefaultConfig()
	redacted.RemoteUsername, redacted.RemotePassword = "u", "secret-p"
	if got := redacted.Redacted(); got.RemotePassword != "" || got.RemoteUsername != "u" {
		t.Fatalf("Redacted must blank only the password, got %+v", got)
	}
	if !redacted.RemoteCredentialsSet() || redacted.Redacted().RemoteCredentialsSet() {
		t.Fatal("RemoteCredentialsSet must require both halves")
	}
	var decoded Config
	if err := common.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal default config: %v", err)
	}
	if decoded != DefaultConfig() {
		t.Fatal("the default config must survive a JSON round trip unchanged")
	}
}

func TestContentBackupValidateConfigAcceptsEnabledTarget(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.TargetID = "example-target-1"
	cfg.SiteLabel = "sitea"
	cfg.RemoteUsername = "backup-user"
	cfg.RemotePassword = "backup-pass"
	cfg.FTPSHost = "backup.example.com"
	cfg.FTPSPort = 21
	cfg.CertSHA256 = strings.Repeat("0123456789abcdef", 4)
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("ValidateConfig(enabled target) = %v", err)
	}
	cfg.UploadPaused = true
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("pausing uploads must stay valid: %v", err)
	}
	cfg.Version = 7
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("a newer config version must stay valid: %v", err)
	}
	cfg.MaxBodyBytes = MaxBodyBytesPerSide
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("the protocol cap itself must be allowed: %v", err)
	}
	cfg.CaptureMemoryMB = 1
	cfg.IndexRetentionDays = cfg.ContentRetentionDays + 365
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("an index retention longer than the content retention must be allowed: %v", err)
	}
}

func TestContentBackupValidateConfigRetentionOrder(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IndexRetentionDays = cfg.ContentRetentionDays - 1
	err := ValidateConfig(cfg)
	if !errors.Is(err, ErrRetentionTooShort) {
		t.Fatalf("ValidateConfig error = %v, want %v", err, ErrRetentionTooShort)
	}
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Field != "index_retention_days" {
		t.Fatalf("error %v must name index_retention_days", err)
	}
	cfg.IndexRetentionDays = cfg.ContentRetentionDays
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("equal retention periods must be allowed: %v", err)
	}
	cfg.IndexRetentionDays = cfg.ContentRetentionDays + 1
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("a longer index retention must be allowed: %v", err)
	}
	cfg.ContentRetentionDays = cfg.IndexRetentionDays + 1
	if !errors.Is(ValidateConfig(cfg), ErrRetentionTooShort) {
		t.Fatalf("raising the content retention above the index retention must fail, got %v", ValidateConfig(cfg))
	}
}

func TestContentBackupValidateConfigRanges(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Config)
		wantField string
	}{
		{name: "version zero", mutate: func(c *Config) { c.Version = 0 }, wantField: "version"},
		{name: "negative version", mutate: func(c *Config) { c.Version = -1 }, wantField: "version"},
		{name: "body limit above the protocol cap", mutate: func(c *Config) { c.MaxBodyBytes = MaxBodyBytesPerSide + 1 }, wantField: "max_body_bytes"},
		{name: "body limit zero", mutate: func(c *Config) { c.MaxBodyBytes = 0 }, wantField: "max_body_bytes"},
		{name: "body limit negative", mutate: func(c *Config) { c.MaxBodyBytes = -1 }, wantField: "max_body_bytes"},
		{name: "capture memory zero", mutate: func(c *Config) { c.CaptureMemoryMB = 0 }, wantField: "capture_memory_mb"},
		{name: "capture memory too large", mutate: func(c *Config) { c.CaptureMemoryMB = 4097 }, wantField: "capture_memory_mb"},
		{name: "inflight captures zero", mutate: func(c *Config) { c.MaxInflightCaptures = 0 }, wantField: "max_inflight_captures"},
		{name: "inflight captures too large", mutate: func(c *Config) { c.MaxInflightCaptures = 8193 }, wantField: "max_inflight_captures"},
		{name: "handoff workers zero", mutate: func(c *Config) { c.HandoffWorkers = 0 }, wantField: "handoff_workers"},
		{name: "handoff workers too many", mutate: func(c *Config) { c.HandoffWorkers = 17 }, wantField: "handoff_workers"},
		{name: "spool workers zero", mutate: func(c *Config) { c.SpoolWorkers = 0 }, wantField: "spool_workers"},
		{name: "upload workers zero", mutate: func(c *Config) { c.UploadWorkers = 0 }, wantField: "upload_workers"},
		{name: "upload workers too many", mutate: func(c *Config) { c.UploadWorkers = 49 }, wantField: "upload_workers"},
		{name: "read workers zero", mutate: func(c *Config) { c.ReadWorkers = 0 }, wantField: "read_workers"},
		{name: "read workers too many", mutate: func(c *Config) { c.ReadWorkers = 5 }, wantField: "read_workers"},
		{name: "spool budget zero", mutate: func(c *Config) { c.MaxSpoolMB = 0 }, wantField: "max_spool_mb"},
		{name: "handoff attempt timeout zero", mutate: func(c *Config) { c.HandoffAttemptTimeoutSeconds = 0 }, wantField: "handoff_attempt_timeout_seconds"},
		{name: "handoff deadline zero", mutate: func(c *Config) { c.HandoffDeadlineSeconds = 0 }, wantField: "handoff_deadline_seconds"},
		{name: "handoff attempts zero", mutate: func(c *Config) { c.HandoffMaxAttempts = 0 }, wantField: "handoff_max_attempts"},
		{name: "handoff attempts too many", mutate: func(c *Config) { c.HandoffMaxAttempts = 6 }, wantField: "handoff_max_attempts"},
		{name: "daemon db max open zero", mutate: func(c *Config) { c.DaemonDBMaxOpen = 0 }, wantField: "daemon_db_max_open"},
		{name: "daemon db max open too large", mutate: func(c *Config) { c.DaemonDBMaxOpen = 65 }, wantField: "daemon_db_max_open"},
		{name: "daemon db max idle negative", mutate: func(c *Config) { c.DaemonDBMaxIdle = -1 }, wantField: "daemon_db_max_idle"},
		{name: "daemon db max idle above max open", mutate: func(c *Config) { c.DaemonDBMaxIdle = c.DaemonDBMaxOpen + 1 }, wantField: "daemon_db_max_idle"},
		{name: "sqlite daemon pool not one", mutate: func(c *Config) { c.DaemonSQLiteDBMaxOpen = 2 }, wantField: "daemon_sqlite_db_max_open"},
		{name: "sqlite daemon pool zero", mutate: func(c *Config) { c.DaemonSQLiteDBMaxOpen = 0 }, wantField: "daemon_sqlite_db_max_open"},
		{name: "daemon db timeout zero", mutate: func(c *Config) { c.DaemonDBTimeoutSeconds = 0 }, wantField: "daemon_db_timeout_seconds"},
		{name: "config reload zero", mutate: func(c *Config) { c.ConfigReloadSeconds = 0 }, wantField: "config_reload_seconds"},
		{name: "upload bandwidth zero", mutate: func(c *Config) { c.UploadBandwidthMiB = 0 }, wantField: "upload_bandwidth_mib"},
		{name: "read bandwidth zero", mutate: func(c *Config) { c.ReadBandwidthMiB = 0 }, wantField: "read_bandwidth_mib"},
		{name: "upload timeout zero", mutate: func(c *Config) { c.UploadTimeoutSeconds = 0 }, wantField: "upload_timeout_seconds"},
		{name: "connect timeout zero", mutate: func(c *Config) { c.FTPSConnectTimeoutSeconds = 0 }, wantField: "ftps_connect_timeout_seconds"},
		{name: "lease seconds zero", mutate: func(c *Config) { c.LeaseSeconds = 0 }, wantField: "lease_seconds"},
		{name: "lease renew above the lease", mutate: func(c *Config) { c.LeaseRenewSeconds = c.LeaseSeconds }, wantField: "lease_renew_seconds"},
		{name: "max upload attempts zero", mutate: func(c *Config) { c.MaxUploadAttempts = 0 }, wantField: "max_upload_attempts"},
		{name: "max upload attempts too many", mutate: func(c *Config) { c.MaxUploadAttempts = 65 }, wantField: "max_upload_attempts"},
		{name: "read timeout zero", mutate: func(c *Config) { c.ReadTimeoutSeconds = 0 }, wantField: "read_timeout_seconds"},
		{name: "preview bytes zero", mutate: func(c *Config) { c.PreviewBytesPerSide = 0 }, wantField: "preview_bytes_per_side"},
		{name: "preview bytes above a side limit", mutate: func(c *Config) { c.PreviewBytesPerSide = MaxBodyBytesPerSide + 1 }, wantField: "preview_bytes_per_side"},
		{name: "decompress limit below the preview", mutate: func(c *Config) { c.MaxDecompressBytes = c.PreviewBytesPerSide - 1 }, wantField: "max_decompress_bytes"},
		{name: "read budget zero", mutate: func(c *Config) { c.ReadBudgetMB = 0 }, wantField: "read_budget_mb"},
		{name: "reconcile interval zero", mutate: func(c *Config) { c.ReconcileIntervalSeconds = 0 }, wantField: "reconcile_interval_seconds"},
		{name: "heartbeat interval zero", mutate: func(c *Config) { c.HeartbeatIntervalSeconds = 0 }, wantField: "heartbeat_interval_seconds"},
		{name: "offline threshold below the heartbeat", mutate: func(c *Config) { c.NodeOfflineSeconds = c.HeartbeatIntervalSeconds }, wantField: "node_offline_seconds"},
		{name: "queue refresh zero", mutate: func(c *Config) { c.QueueRefreshSeconds = 0 }, wantField: "queue_refresh_seconds"},
		{name: "node stats refresh zero", mutate: func(c *Config) { c.NodeStatsRefreshSeconds = 0 }, wantField: "node_stats_refresh_seconds"},
		{name: "spool alert zero", mutate: func(c *Config) { c.SpoolAlertPercent = 0 }, wantField: "spool_alert_percent"},
		{name: "spool alert above 99", mutate: func(c *Config) { c.SpoolAlertPercent = 100 }, wantField: "spool_alert_percent"},
		{name: "spool stop at or below the alert", mutate: func(c *Config) { c.SpoolStopPercent = c.SpoolAlertPercent }, wantField: "spool_stop_percent"},
		{name: "inode alert zero", mutate: func(c *Config) { c.InodeAlertPercent = 0 }, wantField: "inode_alert_percent"},
		{name: "inode recover above the alert", mutate: func(c *Config) { c.InodeRecoverPercent = c.InodeAlertPercent }, wantField: "inode_recover_percent"},
		{name: "min free bytes negative", mutate: func(c *Config) { c.MinFreeBytes = -1 }, wantField: "min_free_bytes"},
		{name: "min free percent negative", mutate: func(c *Config) { c.MinFreePercent = -1 }, wantField: "min_free_percent"},
		{name: "min free percent above 90", mutate: func(c *Config) { c.MinFreePercent = 91 }, wantField: "min_free_percent"},
		{name: "oldest pending alert zero", mutate: func(c *Config) { c.OldestPendingAlertMinutes = 0 }, wantField: "oldest_pending_alert_minutes"},
		{name: "cleanup pending alert zero", mutate: func(c *Config) { c.CleanupPendingAlertMinutes = 0 }, wantField: "cleanup_pending_alert_minutes"},
		{name: "alert dedup zero", mutate: func(c *Config) { c.AlertDedupMinutes = 0 }, wantField: "alert_dedup_minutes"},
		{name: "content retention zero", mutate: func(c *Config) { c.ContentRetentionDays = 0 }, wantField: "content_retention_days"},
		{name: "content retention too long", mutate: func(c *Config) { c.ContentRetentionDays = 3651 }, wantField: "content_retention_days"},
		{name: "index retention zero", mutate: func(c *Config) { c.IndexRetentionDays = 0 }, wantField: "index_retention_days"},
		{name: "stats retention zero", mutate: func(c *Config) { c.StatsRetentionDays = 0 }, wantField: "stats_retention_days"},
		{name: "ftps port zero", mutate: func(c *Config) { c.FTPSPort = 0 }, wantField: "ftps_port"},
		{name: "ftps port above 65535", mutate: func(c *Config) { c.FTPSPort = 65536 }, wantField: "ftps_port"},
		{name: "short cert pin", mutate: func(c *Config) { c.CertSHA256 = strings.Repeat("a", 63) }, wantField: "cert_sha256"},
		{name: "uppercase cert pin", mutate: func(c *Config) { c.CertSHA256 = strings.Repeat("A", 64) }, wantField: "cert_sha256"},
		{name: "non hex cert pin", mutate: func(c *Config) { c.CertSHA256 = strings.Repeat("g", 64) }, wantField: "cert_sha256"},
		{name: "handoff deadline below one attempt", mutate: func(c *Config) { c.HandoffDeadlineSeconds = c.HandoffAttemptTimeoutSeconds - 1 }, wantField: "handoff_deadline_seconds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := DefaultConfig()
			test.mutate(&cfg)
			err := ValidateConfig(cfg)
			if err == nil {
				t.Fatal("ValidateConfig accepted an out of range budget")
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidConfig)
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error %v must be a *ValidationError", err)
			}
			if validation.Field != test.wantField {
				t.Fatalf("field = %q, want %q (%v)", validation.Field, test.wantField, err)
			}
			if validation.Reason == "" {
				t.Fatal("the error must carry a reason")
			}
		})
	}
}

func TestContentBackupValidateConfigEnabledRequiresTarget(t *testing.T) {
	enabled := func() Config {
		cfg := DefaultConfig()
		cfg.Enabled = true
		cfg.TargetID = "example-target-1"
		cfg.SiteLabel = "sitea"
		cfg.RemoteUsername = "backup-user"
		cfg.RemotePassword = "backup-pass"
		cfg.FTPSHost = "backup.example.com"
		cfg.CertSHA256 = strings.Repeat("0123456789abcdef", 4)
		return cfg
	}
	if err := ValidateConfig(enabled()); err != nil {
		t.Fatalf("a fully configured enabled target must validate: %v", err)
	}
	tests := []struct {
		name      string
		mutate    func(*Config)
		wantField string
	}{
		{name: "missing target id", mutate: func(c *Config) { c.TargetID = "" }, wantField: "target_id"},
		{name: "missing site label", mutate: func(c *Config) { c.SiteLabel = "" }, wantField: "site_label"},
		{name: "invalid site label", mutate: func(c *Config) { c.SiteLabel = "Bad Site" }, wantField: "site_label"},
		{name: "missing username", mutate: func(c *Config) { c.RemoteUsername = "" }, wantField: "remote_username"},
		{name: "missing password", mutate: func(c *Config) { c.RemotePassword = "" }, wantField: "remote_username"},
		{name: "missing ftps host", mutate: func(c *Config) { c.FTPSHost = "" }, wantField: "ftps_host"},
		{name: "missing cert pin", mutate: func(c *Config) { c.CertSHA256 = "" }, wantField: "cert_sha256"},
		{name: "host with a scheme", mutate: func(c *Config) { c.FTPSHost = "ftps://backup.example.com" }, wantField: "ftps_host"},
		{name: "host with a path", mutate: func(c *Config) { c.FTPSHost = "backup.example.com/root" }, wantField: "ftps_host"},
		{name: "host with credentials", mutate: func(c *Config) { c.FTPSHost = "user:pass@backup.example.com" }, wantField: "ftps_host"},
		{name: "host with a port", mutate: func(c *Config) { c.FTPSHost = "backup.example.com:21" }, wantField: "ftps_host"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := enabled()
			test.mutate(&cfg)
			err := ValidateConfig(cfg)
			if err == nil {
				t.Fatal("enabling capture without a usable target must fail")
			}
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Field != test.wantField {
				t.Fatalf("error %v must name %q", err, test.wantField)
			}
		})
	}

	disabled := DefaultConfig()
	if err := ValidateConfig(disabled); err != nil {
		t.Fatalf("a disabled config without a target must stay valid so the form can be saved step by step: %v", err)
	}
	invalidPin := DefaultConfig()
	invalidPin.CertSHA256 = strings.Repeat("a", 63)
	if err := ValidateConfig(invalidPin); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("a malformed pin must be rejected even while disabled, got %v", err)
	}
	invalidHost := DefaultConfig()
	invalidHost.FTPSHost = "backup.example.com/root"
	if err := ValidateConfig(invalidHost); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("a malformed host must be rejected even while disabled, got %v", err)
	}
}

// SFTP is selected per config; the enabled-target requirements follow the selected
// protocol, and the other protocol's fields may stay empty.
func TestContentBackupValidateConfigSFTPProtocol(t *testing.T) {
	pin := strings.Repeat("0123456789abcdef", 4)
	enabledSFTP := func() Config {
		cfg := DefaultConfig()
		cfg.Enabled = true
		cfg.TargetID = "example-target-1"
		cfg.SiteLabel = "sitea"
		cfg.RemoteUsername = "backup-user"
		cfg.RemotePassword = "backup-pass"
		cfg.RemoteProtocol = RemoteProtocolSFTP
		cfg.SFTPHost = "backup.example.com"
		cfg.SFTPHostKeySHA256 = pin
		return cfg
	}
	if err := ValidateConfig(enabledSFTP()); err != nil {
		t.Fatalf("an enabled sftp target without any ftps fields must validate: %v", err)
	}
	if got := enabledSFTP().RemoteHost(); got != "backup.example.com" {
		t.Fatalf("RemoteHost() = %q, want the sftp host", got)
	}
	if got := enabledSFTP().EffectiveRemoteProtocol(); got != RemoteProtocolSFTP {
		t.Fatalf("EffectiveRemoteProtocol() = %q", got)
	}

	tests := []struct {
		name      string
		mutate    func(*Config)
		wantField string
	}{
		{name: "missing sftp host", mutate: func(c *Config) { c.SFTPHost = "" }, wantField: "sftp_host"},
		{name: "missing host key pin", mutate: func(c *Config) { c.SFTPHostKeySHA256 = "" }, wantField: "sftp_host_key_sha256"},
		{name: "uppercase host key pin", mutate: func(c *Config) { c.SFTPHostKeySHA256 = strings.ToUpper(pin) }, wantField: "sftp_host_key_sha256"},
		{name: "openssh base64 form is not accepted as stored value", mutate: func(c *Config) { c.SFTPHostKeySHA256 = "SHA256:" + pin[:43] }, wantField: "sftp_host_key_sha256"},
		{name: "sftp host with a port", mutate: func(c *Config) { c.SFTPHost = "backup.example.com:22" }, wantField: "sftp_host"},
		{name: "sftp host with a scheme", mutate: func(c *Config) { c.SFTPHost = "sftp://backup.example.com" }, wantField: "sftp_host"},
		{name: "sftp port zero", mutate: func(c *Config) { c.SFTPPort = 0 }, wantField: "sftp_port"},
		{name: "sftp port above 65535", mutate: func(c *Config) { c.SFTPPort = 65536 }, wantField: "sftp_port"},
		{name: "unknown protocol", mutate: func(c *Config) { c.RemoteProtocol = "scp" }, wantField: "remote_protocol"},
		{name: "relative base dir", mutate: func(c *Config) { c.SFTPBaseDir = "backup" }, wantField: "sftp_base_dir"},
		{name: "base dir with ..", mutate: func(c *Config) { c.SFTPBaseDir = "/raid/../etc" }, wantField: "sftp_base_dir"},
		{name: "base dir trailing slash", mutate: func(c *Config) { c.SFTPBaseDir = "/raid/backup/" }, wantField: "sftp_base_dir"},
		{name: "base dir double slash", mutate: func(c *Config) { c.SFTPBaseDir = "/raid//backup" }, wantField: "sftp_base_dir"},
	}
	for _, ok := range []string{"", "/", "/raid/backup/b_459494"} {
		cfg := enabledSFTP()
		cfg.SFTPBaseDir = ok
		if err := ValidateConfig(cfg); err != nil {
			t.Fatalf("base dir %q must be accepted: %v", ok, err)
		}
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := enabledSFTP()
			test.mutate(&cfg)
			err := ValidateConfig(cfg)
			if err == nil {
				t.Fatal("must fail")
			}
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Field != test.wantField {
				t.Fatalf("error %v must name %q", err, test.wantField)
			}
		})
	}

	// A config stored before remote_protocol existed carries "" and must keep meaning FTPS.
	legacy := DefaultConfig()
	legacy.RemoteProtocol = ""
	if err := ValidateConfig(legacy); err != nil {
		t.Fatalf("legacy empty protocol must validate as ftps: %v", err)
	}
	if legacy.EffectiveRemoteProtocol() != RemoteProtocolFTPS {
		t.Fatalf("EffectiveRemoteProtocol() for legacy = %q", legacy.EffectiveRemoteProtocol())
	}
	legacy.Enabled = true
	legacy.TargetID = "t"
	legacy.SiteLabel = "sitea"
	legacy.RemoteUsername = "u"
	legacy.RemotePassword = "p"
	legacy.SFTPHost = "backup.example.com"
	legacy.SFTPHostKeySHA256 = pin
	err := ValidateConfig(legacy)
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Field != "ftps_host" {
		t.Fatalf("legacy protocol enabled with only sftp fields must still demand ftps_host, got %v", err)
	}
}

func TestContentBackupAlertNotifyEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.AlertNotifyEnabled("oldest_pending") || !cfg.AlertNotifyEnabled("node_offline") {
		t.Fatal("default config must email every selectable reason")
	}
	if cfg.AlertNotifyEnabled("config_mismatch") || cfg.AlertNotifyEnabled("unknown") {
		t.Fatal("reasons without a setting switch must stay silent")
	}
	cfg.NotifyOldestPending = false
	if cfg.AlertNotifyEnabled("oldest_pending") {
		t.Fatal("turning the switch off must stop that reason")
	}
	if !cfg.AlertNotifyEnabled("failed") {
		t.Fatal("other reasons must stay independent")
	}
}

// 收件人按 ; 或 , 切分，前后空白和空项忽略，顺序保持填写顺序；空白本身不是分隔符。
func TestContentBackupNotifyEmailList(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{name: "empty", value: "", want: []string{}},
		{name: "only separators and blanks", value: " ; ,\t;\n, ", want: []string{}},
		{name: "console normalized form", value: "a@x.com; b@y.com", want: []string{"a@x.com", "b@y.com"}},
		{name: "mixed separators and blanks", value: "  a@x.com ;b@y.com,,\tc@z.com\n ; , d@w.com;", want: []string{"a@x.com", "b@y.com", "c@z.com", "d@w.com"}},
		{name: "blank inside one item is not a separator", value: "a@x.com b@y.com", want: []string{"a@x.com b@y.com"}},
		{name: "keeps the written order and case", value: "Z@x.com,a@y.com", want: []string{"Z@x.com", "a@y.com"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.NotifyEmails = test.value
			got := cfg.NotifyEmailList()
			if strings.Join(got, "|") != strings.Join(test.want, "|") || len(got) != len(test.want) {
				t.Fatalf("NotifyEmailList(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

// 校验 reason 是管理端照着展示的契约，逐字锁死。
func TestContentBackupValidateConfigNotifyEmails(t *testing.T) {
	recipients := func(n int) string {
		items := make([]string, 0, n)
		for i := 0; i < n; i++ {
			items = append(items, fmt.Sprintf("ops%d@example.com", i))
		}
		return strings.Join(items, "; ")
	}
	rejected := []struct {
		name       string
		enabled    bool
		emails     string
		wantReason string
	}{
		{name: "display name form", emails: "ops@example.com; Ops <a@b.com>", wantReason: `"Ops <a@b.com>" is not a plain email address`},
		{name: "angle brackets only", emails: "<a@b.com>", wantReason: `"<a@b.com>" is not a plain email address`},
		{name: "comment form", emails: "a@b.com (Ops)", wantReason: `"a@b.com (Ops)" is not a plain email address`},
		{name: "not an address", emails: "not-an-email", wantReason: `"not-an-email" is not a plain email address`},
		{name: "blank separated pair", emails: "a@x.com b@y.com", wantReason: `"a@x.com b@y.com" is not a plain email address`},
		{name: "eleven recipients", emails: recipients(11), wantReason: "at most 10 recipients"},
		{name: "case insensitive duplicate", emails: "Ops@Example.com, a@b.com; ops@example.com", wantReason: `duplicate recipient "ops@example.com"`},
		{name: "enabled without recipients", enabled: true, emails: "", wantReason: "at least one recipient is required when notify_email_enabled is on"},
		{name: "enabled with only separators", enabled: true, emails: " ; , ", wantReason: "at least one recipient is required when notify_email_enabled is on"},
	}
	for _, test := range rejected {
		t.Run(test.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.NotifyEmailEnabled = test.enabled
			cfg.NotifyEmails = test.emails
			err := ValidateConfig(cfg)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("ValidateConfig error = %v, want %v", err, ErrInvalidConfig)
			}
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Field != "notify_emails" || validation.Reason != test.wantReason {
				t.Fatalf("error = %v, want notify_emails: %s", err, test.wantReason)
			}
			if err.Error() != "contentbackup: notify_emails: "+test.wantReason {
				t.Fatalf("error text = %q", err.Error())
			}
		})
	}

	accepted := []struct {
		name    string
		enabled bool
		emails  string
	}{
		{name: "default is off and empty", enabled: false, emails: ""},
		{name: "recipients may be saved while off", enabled: false, emails: "ops@example.com"},
		{name: "on with one recipient", enabled: true, emails: "ops@example.com"},
		{name: "on with ten recipients", enabled: true, emails: recipients(10)},
		{name: "mixed separators", enabled: true, emails: " a@x.com ;b@y.com,, c@z.com ; "},
	}
	for _, test := range accepted {
		t.Run(test.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.NotifyEmailEnabled = test.enabled
			cfg.NotifyEmails = test.emails
			if err := ValidateConfig(cfg); err != nil {
				t.Fatalf("ValidateConfig = %v", err)
			}
		})
	}
}

func TestContentBackupConfigIsAValueType(t *testing.T) {
	cfg := DefaultConfig()
	snapshot := cfg
	snapshot.UploadWorkers = 8
	snapshot.ContentRetentionDays = 1
	if cfg.UploadWorkers != DefaultConfig().UploadWorkers || cfg.ContentRetentionDays != 30 {
		t.Fatal("Config must stay a plain value type so an immutable snapshot cannot be mutated in place")
	}
}

func assertInt64(t *testing.T, field string, got, want int64) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %d, want %d", field, got, want)
	}
}

func assertBool(t *testing.T, field string, got, want bool) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

func assertString(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}
