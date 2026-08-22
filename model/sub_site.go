package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// SubSite 描述一个外部 new-api 站点（"分站"），用于：
//  1. 后台批量同步上游分组与价格；
//  2. 在 Channels 页通过"上游同步"按钮一键建渠道。
//
// 数据库三库兼容：使用 GORM 抽象、避免 DB-specific 列类型。
type SubSite struct {
	Id      int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	Name    string `json:"name" gorm:"type:varchar(64);uniqueIndex;not null"`
	BaseURL string `json:"base_url" gorm:"type:varchar(255);not null"`
	// UpstreamUserId 是 token 所属用户在 *上游* 的 id；newapi access_token 鉴权强制要求 New-Api-User 头。
	UpstreamUserId int    `json:"upstream_user_id" gorm:"default:0"`
	// Token 落库为 EncryptSecret 输出（enc:v1: 前缀），DAO 层 GetSubSiteTokenDecrypted 才解密。
	Token   string `json:"-" gorm:"type:varchar(512);not null"`
	Enabled bool   `json:"enabled" gorm:"index:idx_subsite_enabled_id,priority:1;default:true"`

	LastVerifiedAt        int64  `json:"last_verified_at" gorm:"default:0"`
	LastVerifiedStatus    string `json:"last_verified_status" gorm:"type:varchar(32);default:''"`
	LastVerifiedMsg       string `json:"last_verified_msg" gorm:"type:varchar(255);default:''"`
	LastVerifiedLatencyMS int    `json:"last_verified_latency_ms" gorm:"default:0"`
	LastVerifiedVersion   string `json:"last_verified_version" gorm:"type:varchar(64);default:''"`

	Note        string `json:"note" gorm:"type:varchar(255);default:''"`
	CreatedTime int64  `json:"created_time" gorm:"index:idx_subsite_enabled_id,priority:2"`
	UpdatedTime int64  `json:"updated_time"`
}

// ErrSubSiteNotFound 标识查询不到记录。
var ErrSubSiteNotFound = errors.New("sub_site not found")

// GetAllSubSites 返回全部分站（含 enabled=false）。
func GetAllSubSites() ([]*SubSite, error) {
	var list []*SubSite
	err := DB.Order("id desc").Find(&list).Error
	return list, err
}

// GetEnabledSubSites 仅返回启用的分站，按 id 倒序。
func GetEnabledSubSites() ([]*SubSite, error) {
	var list []*SubSite
	err := DB.Where("enabled = ?", true).Order("id desc").Find(&list).Error
	return list, err
}

// GetSubSiteByID 取单条；不存在返回 ErrSubSiteNotFound。
func GetSubSiteByID(id int64) (*SubSite, error) {
	if id <= 0 {
		return nil, ErrSubSiteNotFound
	}
	var s SubSite
	err := DB.First(&s, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSubSiteNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// UpsertSubSite 落库新增/更新。
//
// 约定：调用方传入的 Token 字段含义：
//   - 非空：写为最终 token（调用方负责加密），覆盖旧值；
//   - 空字符串：保留原 token 不变（用于编辑场景前端 token 留空）。
//
// 该函数本身不做加密，请调用方使用 common.EncryptSecret 加密后再传入。
func UpsertSubSite(s *SubSite) error {
	if s == nil {
		return errors.New("nil sub_site")
	}
	s.Name = strings.TrimSpace(s.Name)
	s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	if s.Name == "" || s.BaseURL == "" {
		return errors.New("name and base_url are required")
	}

	now := time.Now().Unix()
	s.UpdatedTime = now

	if s.Id == 0 {
		// Insert. Token 必须非空。
		if s.Token == "" {
			return errors.New("token is required on create")
		}
		s.CreatedTime = now
		// 名称查重，friendly error。
		var cnt int64
		if err := DB.Model(&SubSite{}).Where("name = ?", s.Name).Count(&cnt).Error; err != nil {
			return err
		}
		if cnt > 0 {
			return fmt.Errorf("sub_site name %q already exists", s.Name)
		}
		return DB.Create(s).Error
	}

	// Update. 读旧记录，若 token 为空保留旧 token。
	old, err := GetSubSiteByID(s.Id)
	if err != nil {
		return err
	}
	if s.Token == "" {
		s.Token = old.Token
	}
	// 名称唯一性（排除自身）
	var cnt int64
	if err := DB.Model(&SubSite{}).Where("name = ? AND id <> ?", s.Name, s.Id).Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return fmt.Errorf("sub_site name %q already exists", s.Name)
	}
	s.CreatedTime = old.CreatedTime // 不被前端覆盖
	return DB.Save(s).Error
}

// DeleteSubSite 物理删除。
func DeleteSubSite(id int64) error {
	if id <= 0 {
		return ErrSubSiteNotFound
	}
	res := DB.Delete(&SubSite{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrSubSiteNotFound
	}
	return nil
}

// UpdateSubSiteVerifyStatus 单字段更新校验结果。
func UpdateSubSiteVerifyStatus(id int64, status, msg, version string, latencyMs int) error {
	if id <= 0 {
		return ErrSubSiteNotFound
	}
	if len(msg) > 255 {
		msg = msg[:255]
	}
	return DB.Model(&SubSite{}).Where("id = ?", id).Updates(map[string]any{
		"last_verified_at":         time.Now().Unix(),
		"last_verified_status":     status,
		"last_verified_msg":        msg,
		"last_verified_version":    version,
		"last_verified_latency_ms": latencyMs,
		"updated_time":             time.Now().Unix(),
	}).Error
}

// GetSubSiteTokenDecrypted 返回明文 token，仅 controller 调用上游时使用。
func GetSubSiteTokenDecrypted(id int64) (string, error) {
	s, err := GetSubSiteByID(id)
	if err != nil {
		return "", err
	}
	return common.DecryptSecret(s.Token)
}
