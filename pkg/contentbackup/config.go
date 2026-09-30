package contentbackup

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

var ftpsHostPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,253}$`)

// Remote transfer protocols. The remote credentials (username/password) come from the
// daemon's deployment variables for either protocol; only the transport and the pin differ:
// FTPS pins the leaf certificate SHA-256, SFTP pins the SSH host public key SHA-256.
const (
	RemoteProtocolFTPS = "ftps"
	RemoteProtocolSFTP = "sftp"
)

// Config is the whole editable content_backup_setting.config blob. Since the 2026-09-18
// simplification the backup pipeline runs inside beeapi, so everything an operator needs to
// set lives here: site label, remote target, credentials, budgets and retention. Only the
// storage node id (bound to the spool volume) and the spool directory are deployment facts.
type Config struct {
	Version      int64 `json:"version"`
	Enabled      bool  `json:"enabled"`
	UploadPaused bool  `json:"upload_paused"`
	// SiteLabel is the site identity: it scopes every DB query and is the first path
	// segment on the remote. All app nodes of one site share one DB, so one value fits all.
	// Changing it after archives exist hides them from the UI; it is meant to be set once.
	SiteLabel string `json:"site_label"`
	TargetID  string `json:"target_id"`
	// RemoteUsername / RemotePassword authenticate against the FTPS or SFTP target. The
	// password is stored in the options blob like the SMTP/payment secrets, never returned
	// by GET /config (only a "set" flag), and an empty value on PUT keeps the stored one.
	RemoteUsername string `json:"remote_username"`
	RemotePassword string `json:"remote_password"`
	FTPSHost       string `json:"ftps_host"`
	FTPSPort       int    `json:"ftps_port"`
	CertSHA256     string `json:"cert_sha256"`
	// RemoteProtocol selects the transport: "ftps" (default; also assumed when a stored
	// config predates this field and carries "") or "sftp".
	RemoteProtocol string `json:"remote_protocol"`
	SFTPHost       string `json:"sftp_host"`
	SFTPPort       int    `json:"sftp_port"`
	// SFTPHostKeySHA256 is the lowercase hex SHA-256 of the server's SSH host public key
	// in wire format (the same bytes `ssh-keygen -lf` hashes; that tool prints base64).
	SFTPHostKeySHA256 string `json:"sftp_host_key_sha256"`
	// SFTPBaseDir is the physical directory under which the logical, immutable remote_path
	// tree ("/{site}/{date}/...") is stored. SFTP accounts are usually not chrooted, so "/"
	// is the real filesystem root and unwritable; "" means "the account's login directory
	// as the server reports it" (the safe default), otherwise an absolute clean path.
	SFTPBaseDir string `json:"sftp_base_dir"`

	MaxBodyBytes        int64 `json:"max_body_bytes"`
	CaptureMemoryMB     int   `json:"capture_memory_mb"`
	MaxInflightCaptures int   `json:"max_inflight_captures"`
	HandoffWorkers      int   `json:"handoff_workers"`
	SpoolWorkers        int   `json:"spool_workers"`
	UploadWorkers       int   `json:"upload_workers"`
	ReadWorkers         int   `json:"read_workers"`
	MaxSpoolMB          int   `json:"max_spool_mb"`

	HandoffAttemptTimeoutSeconds int `json:"handoff_attempt_timeout_seconds"`
	HandoffDeadlineSeconds       int `json:"handoff_deadline_seconds"`
	HandoffMaxAttempts           int `json:"handoff_max_attempts"`

	DaemonDBMaxOpen        int `json:"daemon_db_max_open"`
	DaemonDBMaxIdle        int `json:"daemon_db_max_idle"`
	DaemonSQLiteDBMaxOpen  int `json:"daemon_sqlite_db_max_open"`
	DaemonDBTimeoutSeconds int `json:"daemon_db_timeout_seconds"`
	ConfigReloadSeconds    int `json:"config_reload_seconds"`

	UploadBandwidthMiB        int `json:"upload_bandwidth_mib"`
	ReadBandwidthMiB          int `json:"read_bandwidth_mib"`
	UploadTimeoutSeconds      int `json:"upload_timeout_seconds"`
	FTPSConnectTimeoutSeconds int `json:"ftps_connect_timeout_seconds"`
	LeaseSeconds              int `json:"lease_seconds"`
	LeaseRenewSeconds         int `json:"lease_renew_seconds"`
	MaxUploadAttempts         int `json:"max_upload_attempts"`

	ReadTimeoutSeconds  int   `json:"read_timeout_seconds"`
	PreviewBytesPerSide int64 `json:"preview_bytes_per_side"`
	MaxDecompressBytes  int64 `json:"max_decompress_bytes"`
	ReadBudgetMB        int   `json:"read_budget_mb"`

	ReconcileIntervalSeconds int `json:"reconcile_interval_seconds"`
	HeartbeatIntervalSeconds int `json:"heartbeat_interval_seconds"`
	NodeOfflineSeconds       int `json:"node_offline_seconds"`
	QueueRefreshSeconds      int `json:"queue_refresh_seconds"`
	NodeStatsRefreshSeconds  int `json:"node_stats_refresh_seconds"`

	SpoolAlertPercent   int   `json:"spool_alert_percent"`
	SpoolStopPercent    int   `json:"spool_stop_percent"`
	InodeAlertPercent   int   `json:"inode_alert_percent"`
	InodeRecoverPercent int   `json:"inode_recover_percent"`
	MinFreeBytes        int64 `json:"min_free_bytes"`
	MinFreePercent      int   `json:"min_free_percent"`

	OldestPendingAlertMinutes  int `json:"oldest_pending_alert_minutes"`
	CleanupPendingAlertMinutes int `json:"cleanup_pending_alert_minutes"`
	AlertDedupMinutes          int `json:"alert_dedup_minutes"`

	// Notify* 控制哪些告警发信。旧配置缺这些键时 Parse 从 DefaultConfig 起解码，保持全开。
	NotifyOldestPending   bool `json:"notify_oldest_pending"`
	NotifyCleanupPending  bool `json:"notify_cleanup_pending"`
	NotifySpoolHigh       bool `json:"notify_spool_high"`
	NotifyInodeHigh       bool `json:"notify_inode_high"`
	NotifyFailed          bool `json:"notify_failed"`
	NotifyHandoffRejected bool `json:"notify_handoff_rejected"`
	NotifyNodeOffline     bool `json:"notify_node_offline"`

	ContentRetentionDays int `json:"content_retention_days"`
	IndexRetentionDays   int `json:"index_retention_days"`
	StatsRetentionDays   int `json:"stats_retention_days"`
}

// OriginalConfigKeys is the key set of the first released config blob. A stored blob is only
// a real (possibly older) config if it carries all of them; keys added later may be absent
// and take their defaults. Anything missing an original key is a partial write (e.g. Root
// posting a hand-made fragment through the generic /option entry) and is rejected whole.
// Append newly added keys to LaterConfigKeys instead of here.
var OriginalConfigKeys = []string{
	"version", "enabled", "upload_paused", "target_id", "ftps_host", "ftps_port", "cert_sha256",
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
	"content_retention_days", "index_retention_days", "stats_retention_days",
}

// LaterConfigKeys were added after the first release and default when absent from a stored blob.
var LaterConfigKeys = []string{
	"site_label", "remote_username", "remote_password",
	"remote_protocol", "sftp_host", "sftp_port", "sftp_host_key_sha256", "sftp_base_dir",
	"notify_oldest_pending", "notify_cleanup_pending", "notify_spool_high",
	"notify_inode_high", "notify_failed", "notify_handoff_rejected", "notify_node_offline",
}

func DefaultConfig() Config {
	return Config{
		Version:        1,
		Enabled:        false,
		UploadPaused:   false,
		SiteLabel:      "",
		TargetID:       "",
		RemoteUsername: "",
		RemotePassword: "",
		FTPSHost:       "",
		FTPSPort:       21,
		CertSHA256:     "",

		RemoteProtocol:    RemoteProtocolFTPS,
		SFTPHost:          "",
		SFTPPort:          22,
		SFTPHostKeySHA256: "",
		SFTPBaseDir:       "",

		MaxBodyBytes:        MaxBodyBytesPerSide,
		CaptureMemoryMB:     64,
		MaxInflightCaptures: 128,
		HandoffWorkers:      1,
		SpoolWorkers:        1,
		UploadWorkers:       8,
		ReadWorkers:         1,
		MaxSpoolMB:          2048,

		HandoffAttemptTimeoutSeconds: 30,
		HandoffDeadlineSeconds:       60,
		HandoffMaxAttempts:           2,

		DaemonDBMaxOpen:        4,
		DaemonDBMaxIdle:        2,
		DaemonSQLiteDBMaxOpen:  1,
		DaemonDBTimeoutSeconds: 3,
		ConfigReloadSeconds:    5,

		UploadBandwidthMiB:        32,
		ReadBandwidthMiB:          1,
		UploadTimeoutSeconds:      120,
		FTPSConnectTimeoutSeconds: 10,
		LeaseSeconds:              180,
		LeaseRenewSeconds:         30,
		MaxUploadAttempts:         16,

		ReadTimeoutSeconds:  30,
		PreviewBytesPerSide: 256 * 1024,
		MaxDecompressBytes:  24 * 1024 * 1024,
		ReadBudgetMB:        128,

		ReconcileIntervalSeconds: 30,
		HeartbeatIntervalSeconds: 15,
		NodeOfflineSeconds:       45,
		QueueRefreshSeconds:      10,
		NodeStatsRefreshSeconds:  30,

		SpoolAlertPercent:   80,
		SpoolStopPercent:    90,
		InodeAlertPercent:   90,
		InodeRecoverPercent: 80,
		MinFreeBytes:        1024 * 1024 * 1024,
		MinFreePercent:      10,

		OldestPendingAlertMinutes:  15,
		CleanupPendingAlertMinutes: 30,
		AlertDedupMinutes:          30,

		NotifyOldestPending:   true,
		NotifyCleanupPending:  true,
		NotifySpoolHigh:       true,
		NotifyInodeHigh:       true,
		NotifyFailed:          true,
		NotifyHandoffRejected: true,
		NotifyNodeOffline:     true,

		ContentRetentionDays: 30,
		IndexRetentionDays:   30,
		StatsRetentionDays:   90,
	}
}

func ValidateConfig(cfg Config) error {
	if cfg.Version < 1 {
		return invalidConfig("version", fmt.Sprintf("must be at least 1, got %d", cfg.Version))
	}
	bounds := []struct {
		field    string
		value    int64
		min, max int64
	}{
		{"max_body_bytes", cfg.MaxBodyBytes, 1024, MaxBodyBytesPerSide},
		{"capture_memory_mb", int64(cfg.CaptureMemoryMB), 1, 4096},
		{"max_inflight_captures", int64(cfg.MaxInflightCaptures), 1, 8192},
		{"handoff_workers", int64(cfg.HandoffWorkers), 1, 16},
		{"spool_workers", int64(cfg.SpoolWorkers), 1, 16},
		{"upload_workers", int64(cfg.UploadWorkers), 1, 48},
		{"read_workers", int64(cfg.ReadWorkers), 1, 4},
		{"max_spool_mb", int64(cfg.MaxSpoolMB), 1, 1048576},
		{"handoff_attempt_timeout_seconds", int64(cfg.HandoffAttemptTimeoutSeconds), 5, 300},
		{"handoff_deadline_seconds", int64(cfg.HandoffDeadlineSeconds), 10, 600},
		{"handoff_max_attempts", int64(cfg.HandoffMaxAttempts), 1, 5},
		{"daemon_db_max_open", int64(cfg.DaemonDBMaxOpen), 1, 64},
		{"daemon_db_max_idle", int64(cfg.DaemonDBMaxIdle), 0, 64},
		{"daemon_db_timeout_seconds", int64(cfg.DaemonDBTimeoutSeconds), 1, 60},
		{"config_reload_seconds", int64(cfg.ConfigReloadSeconds), 1, 300},
		{"upload_bandwidth_mib", int64(cfg.UploadBandwidthMiB), 1, 4096},
		{"read_bandwidth_mib", int64(cfg.ReadBandwidthMiB), 1, 4096},
		{"upload_timeout_seconds", int64(cfg.UploadTimeoutSeconds), 10, 3600},
		{"ftps_connect_timeout_seconds", int64(cfg.FTPSConnectTimeoutSeconds), 3, 300},
		{"lease_seconds", int64(cfg.LeaseSeconds), 30, 3600},
		{"lease_renew_seconds", int64(cfg.LeaseRenewSeconds), 5, 600},
		{"max_upload_attempts", int64(cfg.MaxUploadAttempts), 1, 64},
		{"read_timeout_seconds", int64(cfg.ReadTimeoutSeconds), 5, 600},
		{"preview_bytes_per_side", cfg.PreviewBytesPerSide, 1024, MaxBodyBytesPerSide},
		{"max_decompress_bytes", cfg.MaxDecompressBytes, 1024 * 1024, 64 * 1024 * 1024},
		{"read_budget_mb", int64(cfg.ReadBudgetMB), 1, 4096},
		{"reconcile_interval_seconds", int64(cfg.ReconcileIntervalSeconds), 5, 3600},
		{"heartbeat_interval_seconds", int64(cfg.HeartbeatIntervalSeconds), 5, 600},
		{"node_offline_seconds", int64(cfg.NodeOfflineSeconds), 10, 3600},
		{"queue_refresh_seconds", int64(cfg.QueueRefreshSeconds), 3, 600},
		{"node_stats_refresh_seconds", int64(cfg.NodeStatsRefreshSeconds), 5, 3600},
		{"spool_alert_percent", int64(cfg.SpoolAlertPercent), 1, 99},
		{"spool_stop_percent", int64(cfg.SpoolStopPercent), 2, 100},
		{"inode_alert_percent", int64(cfg.InodeAlertPercent), 1, 100},
		{"inode_recover_percent", int64(cfg.InodeRecoverPercent), 1, 99},
		{"min_free_bytes", cfg.MinFreeBytes, 0, 1024 * 1024 * 1024 * 1024},
		{"min_free_percent", int64(cfg.MinFreePercent), 0, 90},
		{"oldest_pending_alert_minutes", int64(cfg.OldestPendingAlertMinutes), 1, 1440},
		{"cleanup_pending_alert_minutes", int64(cfg.CleanupPendingAlertMinutes), 1, 14400},
		{"alert_dedup_minutes", int64(cfg.AlertDedupMinutes), 1, 1440},
		{"content_retention_days", int64(cfg.ContentRetentionDays), 1, 3650},
		{"index_retention_days", int64(cfg.IndexRetentionDays), 1, 3650},
		{"stats_retention_days", int64(cfg.StatsRetentionDays), 1, 3650},
		{"ftps_port", int64(cfg.FTPSPort), 1, 65535},
		{"sftp_port", int64(cfg.SFTPPort), 1, 65535},
	}
	for _, bound := range bounds {
		if bound.value < bound.min || bound.value > bound.max {
			return invalidConfig(bound.field, fmt.Sprintf("must be within [%d, %d], got %d", bound.min, bound.max, bound.value))
		}
	}
	if cfg.DaemonSQLiteDBMaxOpen != 1 {
		// SQLite writers must stay single connection so the daemon shares the business file lock.
		return invalidConfig("daemon_sqlite_db_max_open", fmt.Sprintf("must stay 1 for sqlite, got %d", cfg.DaemonSQLiteDBMaxOpen))
	}
	if cfg.DaemonDBMaxIdle > cfg.DaemonDBMaxOpen {
		return invalidConfig("daemon_db_max_idle", fmt.Sprintf("must not exceed daemon_db_max_open %d, got %d", cfg.DaemonDBMaxOpen, cfg.DaemonDBMaxIdle))
	}
	if cfg.NodeOfflineSeconds <= cfg.HeartbeatIntervalSeconds {
		return invalidConfig("node_offline_seconds", fmt.Sprintf("must exceed heartbeat_interval_seconds %d, got %d", cfg.HeartbeatIntervalSeconds, cfg.NodeOfflineSeconds))
	}
	if cfg.HandoffDeadlineSeconds < cfg.HandoffAttemptTimeoutSeconds {
		return invalidConfig("handoff_deadline_seconds", fmt.Sprintf("must cover handoff_attempt_timeout_seconds %d, got %d", cfg.HandoffAttemptTimeoutSeconds, cfg.HandoffDeadlineSeconds))
	}
	if cfg.LeaseRenewSeconds >= cfg.LeaseSeconds {
		return invalidConfig("lease_renew_seconds", fmt.Sprintf("must stay below lease_seconds %d, got %d", cfg.LeaseSeconds, cfg.LeaseRenewSeconds))
	}
	if cfg.SpoolStopPercent <= cfg.SpoolAlertPercent {
		return invalidConfig("spool_stop_percent", fmt.Sprintf("must exceed spool_alert_percent %d, got %d", cfg.SpoolAlertPercent, cfg.SpoolStopPercent))
	}
	if cfg.InodeRecoverPercent >= cfg.InodeAlertPercent {
		return invalidConfig("inode_recover_percent", fmt.Sprintf("must stay below inode_alert_percent %d, got %d", cfg.InodeAlertPercent, cfg.InodeRecoverPercent))
	}
	if cfg.MaxDecompressBytes < cfg.PreviewBytesPerSide {
		return invalidConfig("max_decompress_bytes", fmt.Sprintf("must cover preview_bytes_per_side %d, got %d", cfg.PreviewBytesPerSide, cfg.MaxDecompressBytes))
	}
	if cfg.IndexRetentionDays < cfg.ContentRetentionDays {
		return invalidConfig("index_retention_days",
			fmt.Sprintf("must not be shorter than content_retention_days %d, got %d", cfg.ContentRetentionDays, cfg.IndexRetentionDays),
			ErrRetentionTooShort)
	}
	if cfg.FTPSHost != "" && !ftpsHostPattern.MatchString(cfg.FTPSHost) {
		return invalidConfig("ftps_host", "must be a bare host name or ip without scheme, port, path or credentials")
	}
	if cfg.CertSHA256 != "" && !isLowerHex64(cfg.CertSHA256) {
		return invalidConfig("cert_sha256", "must be 64 lowercase hex characters")
	}
	protocol, ok := normalizeRemoteProtocol(cfg.RemoteProtocol)
	if !ok {
		return invalidConfig("remote_protocol", fmt.Sprintf("must be %q or %q, got %q", RemoteProtocolFTPS, RemoteProtocolSFTP, cfg.RemoteProtocol))
	}
	if cfg.SFTPHost != "" && !ftpsHostPattern.MatchString(cfg.SFTPHost) {
		return invalidConfig("sftp_host", "must be a bare host name or ip without scheme, port, path or credentials")
	}
	if cfg.SFTPHostKeySHA256 != "" && !isLowerHex64(cfg.SFTPHostKeySHA256) {
		return invalidConfig("sftp_host_key_sha256", "must be 64 lowercase hex characters")
	}
	if err := ValidateRemoteBaseDir(cfg.SFTPBaseDir); err != nil {
		return invalidConfig("sftp_base_dir", err.Error())
	}
	if cfg.SiteLabel != "" {
		if err := ValidateSiteLabel(cfg.SiteLabel); err != nil {
			return invalidConfig("site_label", err.Error())
		}
	}
	if cfg.Enabled {
		if cfg.SiteLabel == "" {
			return invalidConfig("site_label", "must be set before capture is enabled")
		}
		if cfg.TargetID == "" {
			return invalidConfig("target_id", "must be set before capture is enabled")
		}
		if cfg.RemoteUsername == "" || cfg.RemotePassword == "" {
			return invalidConfig("remote_username", "username and password must both be set before capture is enabled")
		}
		switch protocol {
		case RemoteProtocolSFTP:
			if cfg.SFTPHost == "" {
				return invalidConfig("sftp_host", "must be set before capture is enabled")
			}
			if cfg.SFTPHostKeySHA256 == "" {
				return invalidConfig("sftp_host_key_sha256", "must pin the ssh host key before capture is enabled")
			}
		default:
			if cfg.FTPSHost == "" {
				return invalidConfig("ftps_host", "must be set before capture is enabled")
			}
			if cfg.CertSHA256 == "" {
				return invalidConfig("cert_sha256", "must pin the leaf certificate before capture is enabled")
			}
		}
	}
	return nil
}

// ValidateRemoteBaseDir accepts "" (use the account's login directory) or an absolute,
// already-clean path without "." / ".." segments, so joining the logical remote_path onto
// it can never escape upwards or depend on a server-side working directory.
func ValidateRemoteBaseDir(dir string) error {
	if dir == "" {
		return nil
	}
	if !strings.HasPrefix(dir, "/") {
		return fmt.Errorf("must be an absolute path starting with /, got %q", dir)
	}
	if path.Clean(dir) != dir || dir != "/" && strings.HasSuffix(dir, "/") {
		return fmt.Errorf("must be a clean path without trailing slash or ./.. segments, got %q", dir)
	}
	for _, segment := range strings.Split(strings.Trim(dir, "/"), "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("must not contain . or .. segments, got %q", dir)
		}
	}
	return nil
}

// normalizeRemoteProtocol maps the stored value onto a known protocol. "" is accepted as
// FTPS so configs saved before the field existed keep validating; any other unknown value
// is rejected instead of being guessed (design doc 7.1: never run on a guessed target).
func normalizeRemoteProtocol(value string) (string, bool) {
	switch value {
	case "", RemoteProtocolFTPS:
		return RemoteProtocolFTPS, true
	case RemoteProtocolSFTP:
		return RemoteProtocolSFTP, true
	}
	return "", false
}

// AlertNotifyEnabled 是设置页「邮件通知」开关的唯一判定。未知原因默认不发，避免以后
// 新增告警在没加开关之前就打扰管理员。
func (cfg Config) AlertNotifyEnabled(reason string) bool {
	switch reason {
	case "oldest_pending":
		return cfg.NotifyOldestPending
	case "cleanup_pending":
		return cfg.NotifyCleanupPending
	case "spool_high":
		return cfg.NotifySpoolHigh
	case "inode_high":
		return cfg.NotifyInodeHigh
	case "failed":
		return cfg.NotifyFailed
	case "handoff_rejected":
		return cfg.NotifyHandoffRejected
	case "node_offline":
		return cfg.NotifyNodeOffline
	default:
		return false
	}
}

// EffectiveRemoteProtocol returns the protocol the daemon must speak for this config.
func (cfg Config) EffectiveRemoteProtocol() string {
	protocol, ok := normalizeRemoteProtocol(cfg.RemoteProtocol)
	if !ok {
		return cfg.RemoteProtocol
	}
	return protocol
}

// RemoteCredentialsSet reports whether both halves of the credential are present; half a
// credential cannot log in anywhere, so it counts as "not set".
func (cfg Config) RemoteCredentialsSet() bool {
	return cfg.RemoteUsername != "" && cfg.RemotePassword != ""
}

// Redacted returns a copy safe to hand to a browser: the password is blanked. Callers pair it
// with RemoteCredentialsSet so the UI can still show "a password is stored".
func (cfg Config) Redacted() Config {
	cfg.RemotePassword = ""
	return cfg
}

// RemoteHost is the host of the selected protocol; it is what "is a target configured"
// checks and the connection probe must look at, not ftps_host unconditionally.
func (cfg Config) RemoteHost() string {
	if cfg.EffectiveRemoteProtocol() == RemoteProtocolSFTP {
		return cfg.SFTPHost
	}
	return cfg.FTPSHost
}

func invalidConfig(field, reason string, extra ...error) error {
	errs := make([]error, 0, len(extra)+1)
	errs = append(errs, ErrInvalidConfig)
	errs = append(errs, extra...)
	return &ValidationError{Field: field, Reason: reason, Errs: errs}
}
