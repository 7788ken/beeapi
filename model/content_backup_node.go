package model

// ContentBackupNodeStatus is one row per (site_id, storage_node_id) holding the
// daemon's latest self-reported snapshot. It is overwritten, never appended.
type ContentBackupNodeStatus struct {
	ID            int64  `gorm:"column:id;primaryKey;autoIncrement" json:"-"`
	SiteID        string `gorm:"column:site_id;type:varchar(32);not null;uniqueIndex:uk_content_backup_node_status,priority:1" json:"site_id"`
	StorageNodeID string `gorm:"column:storage_node_id;type:varchar(64);not null;uniqueIndex:uk_content_backup_node_status,priority:2" json:"storage_node_id"`

	// ProcessID is regenerated on every daemon start. Both version columns come
	// from the daemon's own snapshot and are therefore always equal; the "config
	// mismatch" alert compares AppliedConfigVersion against the version the
	// business process has published, not these two against each other.
	ProcessID            string `gorm:"column:process_id;type:varchar(64);not null;default:''" json:"process_id"`
	ConfigVersion        int64  `gorm:"column:config_version;not null;default:0" json:"config_version"`
	AppliedConfigVersion int64  `gorm:"column:applied_config_version;not null;default:0" json:"applied_config_version"`

	LastSeenAt int64 `gorm:"column:last_seen_at;not null;default:0" json:"last_seen_at"`
	SampledAt  int64 `gorm:"column:sampled_at;not null;default:0" json:"sampled_at"`

	SpoolBytes      int64 `gorm:"column:spool_bytes;not null;default:0" json:"spool_bytes"`
	SpoolLimitBytes int64 `gorm:"column:spool_limit_bytes;not null;default:0" json:"spool_limit_bytes"`
	DiskTotalBytes  int64 `gorm:"column:disk_total_bytes;not null;default:0" json:"disk_total_bytes"`
	FreeBytes       int64 `gorm:"column:free_bytes;not null;default:0" json:"free_bytes"`
	InodeTotal      int64 `gorm:"column:inode_total;not null;default:0" json:"inode_total"`
	FreeInodes      int64 `gorm:"column:free_inodes;not null;default:0" json:"free_inodes"`

	PendingCount    int64 `gorm:"column:pending_count;not null;default:0" json:"pending_count"`
	ProcessingCount int64 `gorm:"column:processing_count;not null;default:0" json:"processing_count"`
	FailedCount     int64 `gorm:"column:failed_count;not null;default:0" json:"failed_count"`
	OldestPendingAt int64 `gorm:"column:oldest_pending_at;not null;default:0" json:"oldest_pending_at"`

	CleanupPendingCount int64 `gorm:"column:cleanup_pending_count;not null;default:0" json:"cleanup_pending_count"`
	CleanupPendingBytes int64 `gorm:"column:cleanup_pending_bytes;not null;default:0" json:"cleanup_pending_bytes"`

	// Complete spool files that are not in the database yet, plus quarantined
	// .part files. Neither is exposed as an individual job row.
	OrphanCount          int64 `gorm:"column:orphan_count;not null;default:0" json:"orphan_count"`
	IncompleteSpoolCount int64 `gorm:"column:incomplete_spool_count;not null;default:0" json:"incomplete_spool_count"`

	HandoffRejectedCount int64 `gorm:"column:handoff_rejected_count;not null;default:0" json:"handoff_rejected_count"`
	HandoffUnknownCount  int64 `gorm:"column:handoff_unknown_count;not null;default:0" json:"handoff_unknown_count"`

	UploadBytesPerSecond int64 `gorm:"column:upload_bytes_per_second;not null;default:0" json:"upload_bytes_per_second"`

	// FTPS 用户名口令只存在于 daemon 的环境变量里（设计文档 4.1），业务进程按部署
	// 约定拿不到。这一位是业务侧唯一能知道"凭据到底配没配"的途径；没有它，后台只能
	// 去读业务进程自己那份永远为空的环境变量，在部署正确时恒显示"未配置"。
	// 只发布是否已配置，绝不发布值本身。
	FTPSCredentialsSet bool `gorm:"column:ftps_credentials_set;not null;default:false" json:"ftps_credentials_set"`

	CreatedAt int64 `gorm:"column:created_at;not null;default:0" json:"created_at"`
	UpdatedAt int64 `gorm:"column:updated_at;not null;default:0" json:"updated_at"`
}

func (ContentBackupNodeStatus) TableName() string { return "content_backup_node_status" }
