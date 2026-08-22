package operation_setting

import (
	"sync"

	"github.com/QuantumNous/new-api/setting/config"
)

// ChannelHealthConfig 渠道健康度（被动机制）配置。
//
// 设计原则：完全被动，零额外 token 成本。所有信号源自真实用户流量；
// 唯一例外是恢复探活——被禁渠道收不到流量需要主动 ping 一次才能解锁。
//
// 详见 docs/2026-05-04-channel-health-auto-degrade-plan.md
type ChannelHealthConfig struct {
	// Enabled 被动模式总开关。默认 false，与 monitor_setting.auto_test_channel_enabled 解耦
	// （用户可任意组合：仅被动 / 仅主动 / 都开 / 都关）。
	Enabled bool `json:"enabled"`

	// ── 错误流降级（L0~L10 公式化）──
	BaseDegradeThreshold int `json:"base_degrade_threshold"` // streak 达此值 → L1（默认 5）
	LevelStepThreshold   int `json:"level_step_threshold"`   // 每多此值多降一级（默认 5）
	MaxDegradeLevel      int `json:"max_degrade_level"`      // 封顶级别（默认 10）
	MinWeightFactor      float64 `json:"min_weight_factor"`  // 最深降级时的权重下限（默认 0.05）

	// 旧字段（兼容读取，Normalize 时迁移到新字段）
	DegradeThreshold int `json:"degrade_threshold"` // 旧 L0→L1 阈值
	L2Threshold      int `json:"l2_threshold"`      // 旧 L1→L2 阈值

	DisableThreshold int `json:"disable_threshold"` // 任意态 → AutoDisabled（默认 0=禁用本功能）
	UpgradeThreshold int `json:"upgrade_threshold"` // 单步升级所需连续成功数（默认 20）

	// ── TTFT 触发降级 ──
	MaxTtftMs           int  `json:"max_ttft_ms"`            // 默认 0（关闭）。>0 时 TTFT 超此值算一次延迟违规
	LatencyDegradeBase  int  `json:"latency_degrade_base"`   // 连续违规达此次数触发首次降级（默认 5）
	LatencyDegradeStep  int  `json:"latency_degrade_step"`   // 每多 N 次违规多降一级（默认 5）
	CountLatencyAsError bool `json:"count_latency_as_error"` // true 时超阈直接走 onChannelError 路径

	// Count429AsError 429 限流是否计入 err_streak。默认 true。
	Count429AsError bool `json:"count_429_as_error"`

	// CountableStatusCodes 计入 err_streak 的状态码白名单（ranges 格式，例如 "429,500-599"）。
	CountableStatusCodes string `json:"countable_status_codes"`

	// 通知
	NotifyOnDegrade bool `json:"notify_on_degrade"`
	NotifyOnUpgrade bool `json:"notify_on_upgrade"`

	// StreakWindowSec Redis streak 的 TTL（秒）。默认 600=10min。
	StreakWindowSec int `json:"streak_window_sec"`

	// SkipChannelTags 含这些 tag 的渠道完全不参与。
	SkipChannelTags []string `json:"skip_channel_tags"`

	// ── 反弹保护 ──
	RebounceProtectionMinutes   int `json:"rebounce_protection_minutes"`
	RebounceProtectionThreshold int `json:"rebounce_protection_threshold"`

	// DemoteCooldownSec 降级动作节流窗口（秒）。默认 60。
	DemoteCooldownSec int `json:"demote_cooldown_sec"`

	// ── 恢复探活 ──
	RecoveryStrategy     string  `json:"recovery_strategy"`
	RecoveryProbeMinutes int     `json:"recovery_probe_minutes"`
	RecoveryProbeModel   string  `json:"recovery_probe_model"`
	ShadowSampleRate     float64 `json:"shadow_sample_rate"`

	// ── 降级渠道探测恢复 ──
	// DegradeProbeEnabled 开启后，定期对高降级等级的 enabled 渠道发探测请求，
	// 成功则累积 ok_streak 触发自动升级，解决"降级死锁"（priority 太低无流量→无法恢复）。
	DegradeProbeEnabled  bool `json:"degrade_probe_enabled"`
	DegradeProbeMinLevel int  `json:"degrade_probe_min_level"`   // 仅探测 >= 此级别的渠道（默认 3）
	DegradeProbeMinutes  int  `json:"degrade_probe_minutes"`     // 探测间隔（默认 10 分钟）
	DegradeProbeCount    int  `json:"degrade_probe_count"`       // 单次探测每渠道发几次请求（默认 5，凑 ok_streak）
}

var channelHealthSetting = ChannelHealthConfig{
	Enabled:              false,
	BaseDegradeThreshold: 5,
	LevelStepThreshold:   5,
	MaxDegradeLevel:      10,
	MinWeightFactor:      0.05,
	DegradeThreshold:     5,
	L2Threshold:          15,
	DisableThreshold:     0,
	UpgradeThreshold:     20,

	MaxTtftMs:           0,
	LatencyDegradeBase:  5,
	LatencyDegradeStep:  5,
	CountLatencyAsError: false,

	Count429AsError:             true,
	CountableStatusCodes:        "",
	NotifyOnDegrade:             false,
	NotifyOnUpgrade:             false,
	StreakWindowSec:             600,
	SkipChannelTags:             nil,
	RebounceProtectionMinutes:   0,
	RebounceProtectionThreshold: 3,
	DemoteCooldownSec:           60,
	RecoveryStrategy:            "probe",
	RecoveryProbeMinutes:        30,
	RecoveryProbeModel:          "",
	ShadowSampleRate:            0,
	DegradeProbeEnabled:         true,
	DegradeProbeMinLevel:        1,
	DegradeProbeMinutes:         10,
	DegradeProbeCount:           5,
}

func init() {
	config.GlobalConfig.Register("channel_health_setting", &channelHealthSetting)
}

func GetChannelHealthConfig() *ChannelHealthConfig {
	return &channelHealthSetting
}

// NormalizeChannelHealthConfig 兼容旧字段：如果新字段未设置但旧字段有值，从旧字段推断。
// 在 config 加载/变更时调用一次，不在每次读取时调用。
func NormalizeChannelHealthConfig() {
	channelHealthSetting.Normalize()
}

func (c *ChannelHealthConfig) Normalize() {
	if c.BaseDegradeThreshold == 0 && c.DegradeThreshold > 0 {
		c.BaseDegradeThreshold = c.DegradeThreshold
	}
	if c.LevelStepThreshold == 0 && c.L2Threshold > 0 && c.DegradeThreshold > 0 {
		c.LevelStepThreshold = c.L2Threshold - c.DegradeThreshold
		if c.LevelStepThreshold <= 0 {
			c.LevelStepThreshold = 5
		}
	}
	if c.BaseDegradeThreshold == 0 {
		c.BaseDegradeThreshold = 5
	}
	if c.LevelStepThreshold == 0 {
		c.LevelStepThreshold = 5
	}
	if c.MaxDegradeLevel == 0 {
		c.MaxDegradeLevel = 10
	}
	if c.MinWeightFactor < 0 {
		c.MinWeightFactor = 0.05
	}
	if c.LatencyDegradeBase == 0 {
		c.LatencyDegradeBase = 5
	}
	if c.LatencyDegradeStep == 0 {
		c.LatencyDegradeStep = 5
	}
}

// ── CountableStatusCodes 解析缓存 ──
// 解析 channelHealthSetting.CountableStatusCodes 字符串到 []StatusCodeRange。
// 配置变更时自动重 parse（按 raw string 比对），无锁读路径。
var (
	countableStatusCodeMu     sync.RWMutex
	countableStatusCodeRaw    string
	countableStatusCodeRanges []StatusCodeRange
)

func ensureCountableStatusCodesParsed() []StatusCodeRange {
	raw := channelHealthSetting.CountableStatusCodes

	countableStatusCodeMu.RLock()
	if raw == countableStatusCodeRaw {
		ranges := countableStatusCodeRanges
		countableStatusCodeMu.RUnlock()
		return ranges
	}
	countableStatusCodeMu.RUnlock()

	countableStatusCodeMu.Lock()
	defer countableStatusCodeMu.Unlock()
	if raw != countableStatusCodeRaw {
		ranges, err := ParseHTTPStatusCodeRanges(raw)
		if err != nil {
			// 解析失败时清空，避免半截数据；下次 raw 一致时直接走"空"路径。
			ranges = nil
		}
		countableStatusCodeRanges = ranges
		countableStatusCodeRaw = raw
	}
	return countableStatusCodeRanges
}

// HasCountableStatusCodes 配置非空时返回 true。
// 调用方据此决定是否替代现有 5xx 兜底逻辑。
func HasCountableStatusCodes() bool {
	return len(ensureCountableStatusCodesParsed()) > 0
}

// IsCountableStatusCode 判定 code 是否在 CountableStatusCodes 列表内。
// 列表为空时永远返回 false——调用方应先用 HasCountableStatusCodes 判断模式。
func IsCountableStatusCode(code int) bool {
	ranges := ensureCountableStatusCodesParsed()
	if len(ranges) == 0 {
		return false
	}
	return shouldMatchStatusCodeRanges(ranges, code)
}
