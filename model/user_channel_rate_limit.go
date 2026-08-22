package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// UserChannelRateLimit 按 (user_id, channel_id) 配置的软 RPM 上限。
//
// 与 model-rate-limit / per-user RPM / per-token RPM 区分：
//   - 这里是"管理员预先针对某用户在某渠道做的额度承诺"，例如：
//     给用户 1 在渠道 5 上每分钟最多 10 次，超出后返回客户端原生格式的 429，
//     且在配额内的请求做随机延迟，让外部看不出是固定额度。
//   - JitterPct 让真正生效的 RPM 在 [base*(1-j), base*(1+j)] 内按分钟桶变动，
//     单分钟内同一 (user,channel) 的 effective_rpm 是稳定的（hash-based seed）。
//   - MinDelayMs/MaxDelayMs 在通过限流后做一次睡眠抖动，掩盖固定额度的"前快后慢"指纹。
type UserChannelRateLimit struct {
	Id         int64   `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId     int64   `json:"user_id" gorm:"not null;uniqueIndex:uk_user_channel,priority:1"`
	ChannelId  int64   `json:"channel_id" gorm:"not null;uniqueIndex:uk_user_channel,priority:2;index"`
	BaseRpm    int     `json:"base_rpm" gorm:"not null"`
	JitterPct  float32 `json:"jitter_pct" gorm:"not null;default:0.2"`
	MinDelayMs int     `json:"min_delay_ms" gorm:"not null;default:50"`
	MaxDelayMs int     `json:"max_delay_ms" gorm:"not null;default:500"`
	Enabled    bool    `json:"enabled" gorm:"not null;default:true"`
	CreatedAt  int64   `json:"created_at" gorm:"autoCreateTime:milli"`
	UpdatedAt  int64   `json:"updated_at" gorm:"autoUpdateTime:milli"`
}

func (UserChannelRateLimit) TableName() string {
	return "user_channel_rate_limit"
}

// validateRule 公共预检：数值范围 + 引用存在性。
func validateRule(r *UserChannelRateLimit, checkRefs bool) error {
	if r.BaseRpm <= 0 {
		return errors.New("base_rpm must be > 0")
	}
	if r.JitterPct < 0 || r.JitterPct > 1 {
		return errors.New("jitter_pct must be in [0,1]")
	}
	if r.MinDelayMs < 0 || r.MaxDelayMs < 0 {
		return errors.New("delay_ms must be >= 0")
	}
	if r.MinDelayMs > r.MaxDelayMs {
		return errors.New("min_delay_ms must be <= max_delay_ms")
	}
	if r.UserId <= 0 {
		return errors.New("user_id is required")
	}
	if r.ChannelId <= 0 {
		return errors.New("channel_id is required")
	}
	if checkRefs {
		var cnt int64
		if err := DB.Model(&User{}).Where("id = ?", r.UserId).Count(&cnt).Error; err != nil {
			return fmt.Errorf("check user: %w", err)
		}
		if cnt == 0 {
			return fmt.Errorf("user %d not found", r.UserId)
		}
		if err := DB.Model(&Channel{}).Where("id = ?", r.ChannelId).Count(&cnt).Error; err != nil {
			return fmt.Errorf("check channel: %w", err)
		}
		if cnt == 0 {
			return fmt.Errorf("channel %d not found", r.ChannelId)
		}
	}
	return nil
}

// GetUserChannelRateLimit 单条查询，middleware 热路径调用。
// 没有匹配规则时返回 (nil, nil)，调用方据此决定 fail-open。
func GetUserChannelRateLimit(userId, channelId int64) (*UserChannelRateLimit, error) {
	var r UserChannelRateLimit
	err := DB.Where("user_id = ? AND channel_id = ?", userId, channelId).First(&r).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// ListUserChannelRateLimitsByChannel 列出某渠道下全部规则（管理界面用）。
func ListUserChannelRateLimitsByChannel(channelId int64) ([]UserChannelRateLimit, error) {
	var items []UserChannelRateLimit
	err := DB.Where("channel_id = ?", channelId).
		Order("id DESC").
		Find(&items).Error
	return items, err
}

// CreateUserChannelRateLimit 新建规则，触发预检 + (user_id,channel_id) 唯一约束。
func CreateUserChannelRateLimit(r *UserChannelRateLimit) error {
	if err := validateRule(r, true); err != nil {
		return err
	}
	return DB.Create(r).Error
}

// UpdateUserChannelRateLimit 部分字段更新（PATCH 风格）：fields 决定哪些列被写入。
// 注意 fields 必须是 DB 列名，调用方控制；本函数只做范围校验。
func UpdateUserChannelRateLimit(id int64, channelId int64, updates map[string]any) error {
	// 范围预检（仅对实际要更新的字段做边界检查）
	if v, ok := updates["base_rpm"]; ok {
		if iv, _ := v.(int); iv <= 0 {
			return errors.New("base_rpm must be > 0")
		}
	}
	if v, ok := updates["jitter_pct"]; ok {
		switch fv := v.(type) {
		case float32:
			if fv < 0 || fv > 1 {
				return errors.New("jitter_pct must be in [0,1]")
			}
		case float64:
			if fv < 0 || fv > 1 {
				return errors.New("jitter_pct must be in [0,1]")
			}
		}
	}
	if mn, hasMn := updates["min_delay_ms"]; hasMn {
		if iv, _ := mn.(int); iv < 0 {
			return errors.New("min_delay_ms must be >= 0")
		}
	}
	if mx, hasMx := updates["max_delay_ms"]; hasMx {
		if iv, _ := mx.(int); iv < 0 {
			return errors.New("max_delay_ms must be >= 0")
		}
	}
	// min/max 交叉校验：若两个都给了
	if mn, hasMn := updates["min_delay_ms"]; hasMn {
		if mx, hasMx := updates["max_delay_ms"]; hasMx {
			if mn.(int) > mx.(int) {
				return errors.New("min_delay_ms must be <= max_delay_ms")
			}
		}
	}

	if len(updates) == 0 {
		return errors.New("no fields to update")
	}

	res := DB.Model(&UserChannelRateLimit{}).
		Where("id = ? AND channel_id = ?", id, channelId).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("rule not found")
	}
	return nil
}

// DeleteUserChannelRateLimit 按 (id, channel_id) 删除，避免越权删别的渠道下的规则。
func DeleteUserChannelRateLimit(id int64, channelId int64) error {
	res := DB.Where("id = ? AND channel_id = ?", id, channelId).Delete(&UserChannelRateLimit{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("rule not found")
	}
	return nil
}
