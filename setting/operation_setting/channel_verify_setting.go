package operation_setting

import (
	"github.com/QuantumNous/new-api/setting/config"
)

// ChannelVerifySetting 渠道「外部测评(verify)」自动定时调度配置。
// 与 MonitorSetting（普通 channel test 探活）独立。
// 详见 docs/2026-06-18-channel-verify-auto-schedule-plan.md
type ChannelVerifySetting struct {
	AutoVerifyEnabled     bool `json:"auto_verify_enabled"`     // 自动测评总开关
	GlobalIntervalMinutes int  `json:"global_interval_minutes"` // 全局测评间隔（分钟），渠道级 verify_interval_minutes 可覆盖
	ScoreDropThreshold    int  `json:"score_drop_threshold"`    // 分数下降告警阈值：本次比上次低 >= 此值才发邮件
	NotifyOnFailure       bool `json:"notify_on_failure"`       // 测评失败（测不通）是否也告警
	SchedulerTickMinutes  int  `json:"scheduler_tick_minutes"`  // 调度器扫描周期（分钟）
}

var channelVerifySetting = ChannelVerifySetting{
	AutoVerifyEnabled:     false,
	GlobalIntervalMinutes: 360,
	ScoreDropThreshold:    5,
	NotifyOnFailure:       false,
	SchedulerTickMinutes:  30,
}

func init() {
	config.GlobalConfig.Register("channel_verify_setting", &channelVerifySetting)
}

func GetChannelVerifySetting() *ChannelVerifySetting {
	return &channelVerifySetting
}
