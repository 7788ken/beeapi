package model

// ContentBackupDailyStat is the durable source for "today uploaded count/bytes".
// stat_date is the Asia/Shanghai calendar day (YYYY-MM-DD) of uploaded_at, the
// same day key the remote_path directory uses; the timestamp columns themselves
// stay UTC Unix seconds.
type ContentBackupDailyStat struct {
	ID            int64  `gorm:"column:id;primaryKey;autoIncrement" json:"-"`
	SiteID        string `gorm:"column:site_id;type:varchar(32);not null;uniqueIndex:uk_content_backup_daily_stats,priority:1" json:"site_id"`
	StatDate      string `gorm:"column:stat_date;type:varchar(10);not null;uniqueIndex:uk_content_backup_daily_stats,priority:2" json:"stat_date"`
	StorageNodeID string `gorm:"column:storage_node_id;type:varchar(64);not null;default:'';uniqueIndex:uk_content_backup_daily_stats,priority:3" json:"storage_node_id"`

	UploadedCount int64 `gorm:"column:uploaded_count;not null;default:0" json:"uploaded_count"`
	UploadedBytes int64 `gorm:"column:uploaded_bytes;not null;default:0" json:"uploaded_bytes"`
	// Cleanup is a separate outcome: it must never raise uploaded_count.
	CleanedCount int64 `gorm:"column:cleaned_count;not null;default:0" json:"cleaned_count"`
	FreedBytes   int64 `gorm:"column:freed_bytes;not null;default:0" json:"freed_bytes"`

	CreatedAt int64 `gorm:"column:created_at;not null;default:0" json:"created_at"`
	UpdatedAt int64 `gorm:"column:updated_at;not null;default:0" json:"updated_at"`
}

func (ContentBackupDailyStat) TableName() string { return "content_backup_daily_stats" }

// ContentBackupStatDay returns the Asia/Shanghai day key used by content_backup_daily_stats.
func ContentBackupStatDay(unixSeconds int64) string {
	return contentBackupDayFromUnix(unixSeconds)
}

// ContentBackupStatDelta is one upload that is not yet in content_backup_daily_stats.
// MarkUploaded inserts it inside the commit transaction instead of raising the daily row
// there: every concurrent upload commit used to lock that one row. FoldStatDeltas moves
// deltas into the daily row in batches, so each upload is still counted exactly once.
type ContentBackupStatDelta struct {
	ID            int64  `gorm:"column:id;primaryKey;autoIncrement" json:"-"`
	SiteID        string `gorm:"column:site_id;type:varchar(32);not null;index:idx_cb_stat_deltas_node,priority:1" json:"site_id"`
	StorageNodeID string `gorm:"column:storage_node_id;type:varchar(64);not null;default:'';index:idx_cb_stat_deltas_node,priority:2" json:"storage_node_id"`
	StatDate      string `gorm:"column:stat_date;type:varchar(10);not null" json:"stat_date"`
	UploadedBytes int64  `gorm:"column:uploaded_bytes;not null;default:0" json:"uploaded_bytes"`
	CreatedAt     int64  `gorm:"column:created_at;not null;default:0" json:"created_at"`
}

func (ContentBackupStatDelta) TableName() string { return "content_backup_stat_deltas" }
