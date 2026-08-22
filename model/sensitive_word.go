package model

import (
	"time"

	"gorm.io/gorm"
)

// SensitiveWord 关键词配置表
type SensitiveWord struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Pattern     string `json:"pattern" gorm:"type:varchar(512);not null"`
	IsRegex     bool   `json:"is_regex" gorm:"default:false"`
	Enabled     bool   `json:"enabled" gorm:"default:true;index"`
	Action      int    `json:"action" gorm:"default:1;index"`
	Description string `json:"description" gorm:"type:varchar(255);default:''"`
	HitCount    int64  `json:"hit_count" gorm:"bigint;default:0;index"`
	LastHitAt   int64  `json:"last_hit_at" gorm:"bigint;default:0"`
	CreatedAt   int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt   int64  `json:"updated_at" gorm:"bigint"`
}

// 命中后行为
const (
	SensitiveActionBlock   = 1 // 拦截 + 记录 + 禁 token（默认）
	SensitiveActionMonitor = 2 // 仅记录，放行
)

// SensitiveBlockLog 拦截记录表
//
// 阶段 1 改造：Body 内容从 DB(longtext) 迁到本地 dump 文件
//   - request_body 保留作为存量数据兼容字段（新写入路径不再写入，阶段 4 再删列）
//   - 新增 dump_path / body_sha256 / body_size / dump_exists 指向 ${SENSITIVE_DUMP_ROOT}
//     下 gzip 压缩的 JSON 文件，由异步审计 worker 写入；前端按需通过 /api/sensitive_block/:id/body 读取
type SensitiveBlockLog struct {
	Id             int     `json:"id" gorm:"primaryKey;autoIncrement"`
	AuditJobId     *string `json:"audit_job_id,omitempty" gorm:"column:audit_job_id;type:varchar(64);uniqueIndex:idx_sensitive_audit_job_word,priority:1"`
	RequestId      string  `json:"request_id" gorm:"type:varchar(64);index;default:''"`
	UserId         int     `json:"user_id" gorm:"index"`
	Username       string  `json:"username" gorm:"type:varchar(255);index;default:''"`
	TokenId        int     `json:"token_id" gorm:"index;default:0"`
	TokenName      string  `json:"token_name" gorm:"type:varchar(128);default:''"`
	ChannelId      int     `json:"channel_id" gorm:"index;default:0"`
	ChannelName    string  `json:"channel_name" gorm:"type:varchar(255);default:''"`
	ModelName      string  `json:"model_name" gorm:"type:varchar(128);index;default:''"`
	Path           string  `json:"path" gorm:"type:varchar(255);default:''"`
	MatchedWordId  int     `json:"matched_word_id" gorm:"index;default:0;uniqueIndex:idx_sensitive_audit_job_word,priority:2"`
	MatchedPattern string  `json:"matched_pattern" gorm:"type:varchar(512);default:''"`
	IsRegex        bool    `json:"is_regex" gorm:"default:false"`
	Action         int     `json:"action" gorm:"default:1;index"`
	MatchedSnippet string  `json:"matched_snippet" gorm:"type:varchar(512);default:''"`
	// Deprecated: 阶段 1 起新写入路径不再写本字段。存量数据仍可读，阶段 4 删列。
	// MySQL 类型必须保持 longtext 以匹配生产存量：incident
	// 2026-04-29-incident-sensitive-block-logs-text-downgrade.md 已经踩过一次 text 缩回的坑，
	// 表内有 56/59 行 > 65KB 的存量记录会让 ALTER 撞 Error 1406 并重启循环 8 分钟。
	// 不显式写 MySQL-only 类型：GORM MySQL 驱动会把无长度 string 映射为 longtext，
	// PostgreSQL/SQLite 则映射为各自的 text 类型。
	RequestBody   string `json:"request_body"`
	DumpPath      string `json:"dump_path" gorm:"type:varchar(512);default:''"`
	BodySha256    string `json:"body_sha256" gorm:"type:varchar(64);index;default:''"`
	BodySize      int64  `json:"body_size" gorm:"default:0"`
	DumpExists    bool   `json:"dump_exists" gorm:"default:true"`
	Ip            string `json:"ip" gorm:"type:varchar(64);index;default:''"`
	UserAgent     string `json:"user_agent" gorm:"type:varchar(512);default:''"`
	TokenDisabled bool   `json:"token_disabled" gorm:"default:false"`
	CreatedAt     int64  `json:"created_at" gorm:"bigint;index"`
}

func (SensitiveWord) TableName() string {
	return "sensitive_words"
}

func (SensitiveBlockLog) TableName() string {
	return "sensitive_block_logs"
}

func (w *SensitiveWord) BeforeCreate(tx *gorm.DB) error {
	now := time.Now().Unix()
	if w.CreatedAt == 0 {
		w.CreatedAt = now
	}
	w.UpdatedAt = now
	return nil
}

func (w *SensitiveWord) BeforeUpdate(tx *gorm.DB) error {
	w.UpdatedAt = time.Now().Unix()
	return nil
}

func (b *SensitiveBlockLog) BeforeCreate(tx *gorm.DB) error {
	if b.CreatedAt == 0 {
		b.CreatedAt = time.Now().Unix()
	}
	return nil
}

// ListSensitiveWords 关键词分页列表
func ListSensitiveWords(keyword string, startIdx, num int) ([]*SensitiveWord, int64, error) {
	var (
		words []*SensitiveWord
		total int64
	)
	tx := DB.Model(&SensitiveWord{})
	if keyword != "" {
		tx = tx.Where("pattern LIKE ? OR description LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	// 默认按命中次数倒序：高频规则优先排查；同命中次数下按 id desc 保持稳定顺序
	err := tx.Order("hit_count desc, id desc").Offset(startIdx).Limit(num).Find(&words).Error
	return words, total, err
}

// AllEnabledSensitiveWords 拉取所有启用中的关键词（用于中间件缓存）
func AllEnabledSensitiveWords() ([]SensitiveWord, error) {
	var words []SensitiveWord
	err := DB.Where("enabled = ?", true).Order("id asc").Find(&words).Error
	return words, err
}

func GetSensitiveWordById(id int) (*SensitiveWord, error) {
	var w SensitiveWord
	if err := DB.First(&w, id).Error; err != nil {
		return nil, err
	}
	return &w, nil
}

func (w *SensitiveWord) Insert() error {
	return DB.Create(w).Error
}

func (w *SensitiveWord) Update() error {
	return DB.Model(w).Where("id = ?", w.Id).Updates(map[string]any{
		"pattern":     w.Pattern,
		"is_regex":    w.IsRegex,
		"enabled":     w.Enabled,
		"action":      w.Action,
		"description": w.Description,
		"updated_at":  time.Now().Unix(),
	}).Error
}

func DeleteSensitiveWordById(id int) error {
	return DB.Delete(&SensitiveWord{}, id).Error
}

// ListSensitiveBlockLogs 拦截记录分页列表
func ListSensitiveBlockLogs(filter SensitiveBlockLogFilter, startIdx, num int) ([]*SensitiveBlockLog, int64, error) {
	var (
		logs  []*SensitiveBlockLog
		total int64
	)
	tx := DB.Model(&SensitiveBlockLog{})
	if filter.UserId > 0 {
		tx = tx.Where("user_id = ?", filter.UserId)
	}
	if filter.TokenId > 0 {
		tx = tx.Where("token_id = ?", filter.TokenId)
	}
	if filter.ChannelId > 0 {
		tx = tx.Where("channel_id = ?", filter.ChannelId)
	}
	if filter.ModelName != "" {
		tx = tx.Where("model_name = ?", filter.ModelName)
	}
	if filter.Username != "" {
		tx = tx.Where("username LIKE ?", "%"+filter.Username+"%")
	}
	if filter.Ip != "" {
		tx = tx.Where("ip = ?", filter.Ip)
	}
	if filter.Pattern != "" {
		tx = tx.Where("matched_pattern LIKE ?", "%"+filter.Pattern+"%")
	}
	if filter.RequestId != "" {
		tx = tx.Where("request_id = ?", filter.RequestId)
	}
	if filter.StartTimestamp > 0 {
		tx = tx.Where("created_at >= ?", filter.StartTimestamp)
	}
	if filter.EndTimestamp > 0 {
		tx = tx.Where("created_at <= ?", filter.EndTimestamp)
	}
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := tx.Order("id desc").Offset(startIdx).Limit(num).Find(&logs).Error
	return logs, total, err
}

// SensitiveBlockLogFilter 拦截记录过滤条件
type SensitiveBlockLogFilter struct {
	UserId         int
	TokenId        int
	ChannelId      int
	ModelName      string
	Username       string
	Ip             string
	Pattern        string
	RequestId      string
	StartTimestamp int64
	EndTimestamp   int64
}

func GetSensitiveBlockLogById(id int) (*SensitiveBlockLog, error) {
	var l SensitiveBlockLog
	if err := DB.First(&l, id).Error; err != nil {
		return nil, err
	}
	return &l, nil
}

func (b *SensitiveBlockLog) Insert() error {
	return DB.Create(b).Error
}

// IncrementSensitiveHit 命中后原子 +1 + 刷新最近命中时间
func IncrementSensitiveHit(id int) error {
	return DB.Model(&SensitiveWord{}).Where("id = ?", id).Updates(map[string]any{
		"hit_count":   gorm.Expr("hit_count + 1"),
		"last_hit_at": time.Now().Unix(),
	}).Error
}

// UpdateSensitiveBlockLogTokenDisabled 把命中记录的 token_disabled 字段刷成最新状态
// （前端 "启用/禁用 Token" 按钮调用 service.SetSensitiveTokenStatus 后刷入）。
func UpdateSensitiveBlockLogTokenDisabled(id int, disabled bool) error {
	return DB.Model(&SensitiveBlockLog{}).Where("id = ?", id).Update("token_disabled", disabled).Error
}
