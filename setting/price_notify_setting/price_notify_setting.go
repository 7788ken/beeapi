package price_notify_setting

import (
	"github.com/QuantumNous/new-api/setting/config"
)

// PriceNotifySetting 价格变动展示与通知配置。
// DB keys: price_notify_setting.enabled / badge_days / history_days / email_default_checked / email_pace_per_minute
type PriceNotifySetting struct {
	Enabled             bool `json:"enabled"`               // 总开关；关闭后用户接口返回空、发布拒绝，已落库数据保留
	BadgeDays           int  `json:"badge_days"`            // 价格页徽章窗口（天）
	HistoryDays         int  `json:"history_days"`          // 用户侧历史窗口默认值（天）
	EmailDefaultChecked bool `json:"email_default_checked"` // 发布面板"发送邮件"默认勾选（前端用）
	EmailPacePerMinute  int  `json:"email_pace_per_minute"` // 邮件自限速（封/分钟），低于全局限速 30/60s
}

var priceNotifySetting = PriceNotifySetting{
	Enabled:             true,
	BadgeDays:           7,
	HistoryDays:         30,
	EmailDefaultChecked: true,
	EmailPacePerMinute:  25,
}

func init() {
	config.GlobalConfig.Register("price_notify_setting", &priceNotifySetting)
}

func GetPriceNotifySetting() *PriceNotifySetting {
	return &priceNotifySetting
}

// GetBadgeDays 返回徽章窗口天数，非法配置回退默认 7。
func (s *PriceNotifySetting) GetBadgeDays() int {
	if s.BadgeDays <= 0 {
		return 7
	}
	return s.BadgeDays
}

// GetHistoryDays 返回历史窗口默认天数，非法配置回退默认 30。
func (s *PriceNotifySetting) GetHistoryDays() int {
	if s.HistoryDays <= 0 {
		return 30
	}
	return s.HistoryDays
}

// GetEmailPacePerMinute 返回邮件限速，非法配置回退默认 25。
func (s *PriceNotifySetting) GetEmailPacePerMinute() int {
	if s.EmailPacePerMinute <= 0 {
		return 25
	}
	return s.EmailPacePerMinute
}
