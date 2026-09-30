package model

// Alert reasons are aggregated by (site_id, storage_node_id, reason), so one
// row is one ongoing fault rather than one notification.
const (
	ContentBackupAlertSpoolHigh       = "spool_high"
	ContentBackupAlertSpoolCritical   = "spool_critical"
	ContentBackupAlertInodeHigh       = "inode_high"
	ContentBackupAlertOldestPending   = "oldest_pending"
	ContentBackupAlertCleanupPending  = "cleanup_pending"
	ContentBackupAlertFailed          = "failed"
	ContentBackupAlertHandoffRejected = "handoff_rejected"
	ContentBackupAlertNodeOffline     = "node_offline"
	ContentBackupAlertConfigMismatch  = "config_mismatch"
	ContentBackupAlertTargetUnusable  = "target_unusable"
)

const (
	ContentBackupAlertFiring   = "firing"
	ContentBackupAlertResolved = "resolved"
)

type ContentBackupAlert struct {
	ID            int64  `gorm:"column:id;primaryKey;autoIncrement" json:"-"`
	SiteID        string `gorm:"column:site_id;type:varchar(32);not null;uniqueIndex:uk_content_backup_alerts,priority:1" json:"site_id"`
	StorageNodeID string `gorm:"column:storage_node_id;type:varchar(64);not null;uniqueIndex:uk_content_backup_alerts,priority:2" json:"storage_node_id"`
	Reason        string `gorm:"column:reason;type:varchar(64);not null;uniqueIndex:uk_content_backup_alerts,priority:3" json:"reason"`

	Message      string `gorm:"column:message;type:text" json:"message"`
	TriggerCount int64  `gorm:"column:trigger_count;not null;default:0" json:"trigger_count"`
	State        string `gorm:"column:state;type:varchar(16);not null;default:'firing'" json:"state"`

	// The send lease keeps a 30 minute dedup window from being reset by two
	// processes racing to notify about the same fault.
	SendLeaseOwner string `gorm:"column:send_lease_owner;type:varchar(64);not null;default:''" json:"send_lease_owner"`
	SendLeaseUntil int64  `gorm:"column:send_lease_until;not null;default:0" json:"send_lease_until"`
	LastSentAt     int64  `gorm:"column:last_sent_at;not null;default:0" json:"last_sent_at"`

	LastTriggeredAt int64 `gorm:"column:last_triggered_at;not null;default:0" json:"last_triggered_at"`
	ResolvedAt      int64 `gorm:"column:resolved_at;not null;default:0" json:"resolved_at"`

	CreatedAt int64 `gorm:"column:created_at;not null;default:0" json:"created_at"`
	UpdatedAt int64 `gorm:"column:updated_at;not null;default:0" json:"updated_at"`
}

func (ContentBackupAlert) TableName() string { return "content_backup_alerts" }
