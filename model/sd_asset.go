package model

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/common"
)

// SdAsset 记录经 /v1/sd/assets 代理上传的 Seedance 素材。
// 素材绑定创建时的渠道：GET 查询按 ChannelId 取渠道 key 实时拉上游 GetAsset，
// 归属校验语义对齐 Task（仅创建者可查，见 GetByTaskId）。
type SdAsset struct {
	ID        int64           `json:"id" gorm:"primary_key;AUTO_INCREMENT"`
	CreatedAt int64           `json:"created_at" gorm:"index"`
	UpdatedAt int64           `json:"updated_at"`
	AssetID   string          `json:"asset_id" gorm:"type:varchar(191);index"` // 上游素材 ID（asset-xxx）
	UserId    int             `json:"user_id" gorm:"index"`
	ChannelId int             `json:"channel_id" gorm:"index"`
	AssetType string          `json:"asset_type" gorm:"type:varchar(20)"` // Image / Video / Audio
	Name      string          `json:"name" gorm:"type:varchar(191)"`
	Status    string          `json:"status" gorm:"type:varchar(20);index"` // Processing / Active / Failed（最后一次查询快照）
	Data      json.RawMessage `json:"data" gorm:"type:json"`                // 上游响应快照（白名单字段，已脱敏）
	// 上游素材协议（GET 查询按此分发）：""/"sd1" = /v1/sd/assets 旧体系（HC 空间）；
	// "sd2" = 素材组体系 /v1/assets（260128/ep/mini 系模型）
	Protocol string `json:"protocol" gorm:"type:varchar(10)"`
	// sd2 协议专用：上传返回的处理 task_id 与素材组 id（查询 /v1/assets/get 需要）
	UpstreamTaskID string `json:"upstream_task_id" gorm:"type:varchar(191)"`
	GroupID        string `json:"group_id" gorm:"type:varchar(191)"`
}

func (a *SdAsset) Insert() error {
	now := common.GetTimestamp()
	a.CreatedAt = now
	a.UpdatedAt = now
	return DB.Create(a).Error
}

// UpdateStatusSnapshot 回写最近一次上游查询的状态快照（best-effort，失败不影响查询响应）。
func (a *SdAsset) UpdateStatusSnapshot(status string, data json.RawMessage) error {
	a.Status = status
	a.Data = data
	a.UpdatedAt = common.GetTimestamp()
	return DB.Model(a).Select("status", "data", "updated_at").Updates(a).Error
}

func GetSdAssetByAssetId(userId int, assetId string) (*SdAsset, bool, error) {
	if assetId == "" {
		return nil, false, nil
	}
	var asset *SdAsset
	err := DB.Where("user_id = ? and asset_id = ?", userId, assetId).First(&asset).Error
	exist, err := RecordExist(err)
	if err != nil {
		return nil, false, err
	}
	return asset, exist, nil
}
