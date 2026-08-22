package ratio_monitor_setting

import (
	"github.com/QuantumNous/new-api/setting/config"
)

// RatioMonitorSetting 上游分组倍率变化监控配置。
// 详见 docs/2026-08-05-upstream-group-ratio-monitor.md
// DB keys: ratio_monitor_setting.enabled / interval_minutes / badge_days / deviation_alert_percent
type RatioMonitorSetting struct {
	Enabled         bool `json:"enabled"`          // 总开关
	IntervalMinutes int  `json:"interval_minutes"` // 抓取间隔（分钟）
	BadgeDays       int  `json:"badge_days"`       // 渠道列表角标显示窗口（天）
	// 实测倍率高于人工登记的采购倍率超过该百分比时告警；用于抓"上游偷偷取消专属折扣"
	DeviationAlertPercent float64 `json:"deviation_alert_percent"`
}

var ratioMonitorSetting = RatioMonitorSetting{
	Enabled:         true,
	IntervalMinutes: 60,
	BadgeDays:       7,

	DeviationAlertPercent: 10,
}

func init() {
	config.GlobalConfig.Register("ratio_monitor_setting", &ratioMonitorSetting)
}

func GetRatioMonitorSetting() *RatioMonitorSetting {
	return &ratioMonitorSetting
}

// GetIntervalMinutes 返回抓取间隔，非法配置回退默认 60。
func (s *RatioMonitorSetting) GetIntervalMinutes() int {
	if s.IntervalMinutes <= 0 {
		return 60
	}
	return s.IntervalMinutes
}

// GetBadgeDays 返回角标窗口天数，非法配置回退默认 7。
func (s *RatioMonitorSetting) GetBadgeDays() int {
	if s.BadgeDays <= 0 {
		return 7
	}
	return s.BadgeDays
}
