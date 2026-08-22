package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
)

func formatNotifyType(channelId int, status int) string {
	return fmt.Sprintf("%s_%d_%d", dto.NotifyTypeChannelUpdate, channelId, status)
}

// disable & notify
func DisableChannel(channelError types.ChannelError, reason string) {
	common.SysLog(fmt.Sprintf("通道「%s」（#%d）发生错误，准备禁用，原因：%s", channelError.ChannelName, channelError.ChannelId, common.LocalLogPreview(common.MaskSensitiveInfo(reason))))

	// 检查是否启用自动禁用功能
	if !channelError.AutoBan {
		common.SysLog(fmt.Sprintf("通道「%s」（#%d）未启用自动禁用功能，跳过禁用操作", channelError.ChannelName, channelError.ChannelId))
		return
	}

	success := model.UpdateChannelStatus(channelError.ChannelId, channelError.UsingKey, common.ChannelStatusAutoDisabled, reason)
	if success {
		// 反弹保护触发条件：channels.status 真的变成 AutoDisabled。
		// - 单 key 渠道：UsingKey 一定为空，UpdateChannelStatus 会直接改 channel.Status，必然命中
		// - 多 key 渠道：仅当所有 key 都 disabled（handlerMultiKeyUpdate 检测 MultiKeyStatusList 满）
		//   或者本次 UpdateChannelStatus 是渠道级（UsingKey=""）调用时，channel.Status 才会变
		// 设计原则（2026-05-04 用户确认）：渠道的降级是渠道粒度，不是 key 粒度
		ch, _ := model.GetChannelById(channelError.ChannelId, false)
		if ch != nil && ch.Status == common.ChannelStatusAutoDisabled {
			postChannelDisableHook(channelError.ChannelId, channelError.ChannelName, reason)
		}

		currentLevel := 0
		if ch != nil && ch.DegradeLevel != nil {
			currentLevel = *ch.DegradeLevel
		}
		_ = model.RecordChannelHealthEvent(channelError.ChannelId, model.HealthEventDisable, currentLevel, -1, reason, "auto")

		cfg := operation_setting.GetChannelHealthConfig()
		if cfg.NotifyOnDegrade {
			subject := fmt.Sprintf("通道「%s」（#%d）已被禁用", channelError.ChannelName, channelError.ChannelId)
			content := fmt.Sprintf("通道「%s」（#%d）已被禁用，原因：%s", channelError.ChannelName, channelError.ChannelId, reason)
			NotifyRootUser(formatNotifyType(channelError.ChannelId, common.ChannelStatusAutoDisabled), subject, content)
		}
	}
}

func EnableChannel(channelId int, usingKey string, channelName string) {
	// 整渠道启用前先做"恢复回 L1"调整：snapshot original + 应用 L1 公式
	// 单 key 启用（usingKey != ""）不动渠道级健康度
	if usingKey == "" {
		preChannelEnableHook(channelId)
	}

	success := model.UpdateChannelStatus(channelId, usingKey, common.ChannelStatusEnabled, "")
	if success {
		if usingKey == "" {
			postChannelEnableHook(channelId, channelName)
		}
		// 启用/恢复通知受 notify_on_upgrade 控制（与禁用侧 notify_on_degrade 对称），防止间歇故障渠道震荡刷屏
		if operation_setting.GetChannelHealthConfig().NotifyOnUpgrade {
			subject := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
			content := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
			NotifyRootUser(formatNotifyType(channelId, common.ChannelStatusEnabled), subject, content)
		}
	}
}

// postChannelDisableHook 反弹保护（§3.4.5）：写 LastDisabledAt + 决定是否锁死。
// DisableChannel 成功之后调用，失败/异常都不影响主路径。
//
// RebounceProtectionMinutes <= 0 时整个反弹保护功能被禁用：
//   - 不写 last_disabled_at / 不增 rebounce_count / 不会触发 permanent_disabled
//   - 此 hook 直接 return，不影响主禁用流程
func postChannelDisableHook(channelId int, channelName, reason string) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("channel_health: postChannelDisableHook panic (channel_id=%d): %v", channelId, r))
		}
	}()

	cfg := operation_setting.GetChannelHealthConfig()
	if !cfg.Enabled {
		return
	}
	if cfg.RebounceProtectionMinutes <= 0 {
		// 反弹保护功能关闭
		return
	}

	ch, err := model.GetChannelById(channelId, false)
	if err != nil || ch == nil {
		return
	}

	now := time.Now().Unix()
	windowSec := int64(cfg.RebounceProtectionMinutes) * 60

	lastDisabledAt := int64(0)
	if ch.LastDisabledAt != nil {
		lastDisabledAt = *ch.LastDisabledAt
	}
	rebounceCount := 0
	if ch.RebounceCount != nil {
		rebounceCount = *ch.RebounceCount
	}

	// 24h 滚动衰减：上次 disable 距今超过窗口的 4 倍时清零（避免 RebounceCount 永远累积）
	if lastDisabledAt > 0 && now-lastDisabledAt > windowSec*4 {
		rebounceCount = 0
	}

	fields := map[string]interface{}{
		"last_disabled_at": now,
	}

	threshold := cfg.RebounceProtectionThreshold
	if threshold < 1 {
		threshold = 1
	}
	// 反弹判定：上次禁用还在窗口内 + 已经累计达到阈值 → 锁死
	if lastDisabledAt > 0 && (now-lastDisabledAt) < windowSec && rebounceCount >= threshold {
		fields["permanent_disabled"] = 1
		fields["rebounce_count"] = rebounceCount + 1
		_ = model.UpdateChannelHealthFields(channelId, fields)

		common.SysLog(fmt.Sprintf("channel_health: channel #%d 触发反弹保护，永久锁死（rebounce=%d, window=%ds）", channelId, rebounceCount+1, windowSec))
		_ = backgroundtask.Submit("channel-health-disable-event", func(context.Context) {
			defer func() {
				if r := recover(); r != nil {
					common.SysError(fmt.Sprintf("channel_health: RecordChannelHealthEvent panic: %v", r))
				}
			}()
			_ = model.RecordChannelHealthEvent(channelId, model.HealthEventDisable, model.HealthLevelDisabled, model.HealthLevelDisabled,
				"permanent_disabled (rebounce protection): "+reason, "auto")
		})

		// 锁死必发通知（不受 NotifyOnDegrade 控制）
		subject := fmt.Sprintf("通道「%s」（#%d）已被永久锁死", channelName, channelId)
		content := fmt.Sprintf("通道「%s」（#%d）在 %d 分钟内反复故障，已被永久锁死，请人工排查后手动启用。最近原因：%s",
			channelName, channelId, cfg.RebounceProtectionMinutes, reason)
		NotifyRootUser(fmt.Sprintf("channel_health_permanent_%d", channelId), subject, content)
		return
	}

	// 未达阈值：累加计数
	fields["rebounce_count"] = rebounceCount + 1
	_ = model.UpdateChannelHealthFields(channelId, fields)

	_ = backgroundtask.Submit("channel-health-disable-event", func(context.Context) {
		defer func() {
			if r := recover(); r != nil {
				common.SysError(fmt.Sprintf("channel_health: RecordChannelHealthEvent panic: %v", r))
			}
		}()
		_ = model.RecordChannelHealthEvent(channelId, model.HealthEventDisable, derefIntSafe(ch.DegradeLevel), model.HealthLevelDisabled, reason, "auto")
	})
}

// preChannelEnableHook 恢复探活成功前的健康度调整：恢复时回到 L1 而非 L0（§3.4.3）。
// 在 UpdateChannelStatus 之前调用，确保 cache 重排时拿到的就是降级后的 priority/weight。
func preChannelEnableHook(channelId int) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("channel_health: preChannelEnableHook panic (channel_id=%d): %v", channelId, r))
		}
	}()

	cfg := operation_setting.GetChannelHealthConfig()
	if !cfg.Enabled {
		return
	}

	ch, err := model.GetChannelById(channelId, false)
	if err != nil || ch == nil {
		return
	}

	currentLevel := derefIntSafe(ch.DegradeLevel)
	if currentLevel >= model.HealthLevelL1 {
		return // 本来就已经在降级态，保持
	}

	// 直接被 disable 的渠道（degrade_level=0），恢复时打 L1 标
	originalP := int64(0)
	if ch.Priority != nil {
		originalP = *ch.Priority
	}
	originalW := uint(0)
	if ch.Weight != nil {
		originalW = *ch.Weight
	}

	newP := originalP - 1
	newW := uint(0)
	if originalW > 0 {
		// 同 applyDegrade L1 公式：max(1, ⌊×0.5⌋)
		half := originalW / 2
		if half < 1 {
			half = 1
		}
		newW = half
	}

	fields := map[string]interface{}{
		"degrade_level":     model.HealthLevelL1,
		"original_priority": originalP,
		"original_weight":   originalW,
		"priority":          newP,
		"weight":            newW,
		"last_demote_at":    time.Now().Unix(),
	}
	_ = model.UpdateChannelHealthFields(channelId, fields)
}

// postChannelEnableHook 启用后写审计事件。
//
// FU-9 R13 澄清：from_level 写死 Disabled 是正确的——本 hook 仅在 EnableChannel(usingKey="")
// 被调用时执行，调用方只有"恢复探活成功"这一条路径（recoverAutoDisabledChannels 或老的
// AutomaticEnableChannelEnabled），渠道在调用前一定是 AutoDisabled 状态。
// 手动从 UI 启用走的是 model.UpdateChannelStatus 直接改，不经过这里。
func postChannelEnableHook(channelId int, channelName string) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("channel_health: postChannelEnableHook panic (channel_id=%d): %v", channelId, r))
		}
	}()
	_ = backgroundtask.Submit("channel-health-enable-event", func(context.Context) {
		defer func() {
			if r := recover(); r != nil {
				common.SysError(fmt.Sprintf("channel_health: RecordChannelHealthEvent panic: %v", r))
			}
		}()
		_ = model.RecordChannelHealthEvent(channelId, model.HealthEventEnable, model.HealthLevelDisabled, model.HealthLevelL1, "auto recovery", "auto")
	})
}

// FU-9 R11: 转调 common.DerefIntOr（统一底层）
func derefIntSafe(p *int) int { return common.DerefIntOr(p, 0) }

func ShouldDisableChannel(err *types.NewAPIError) bool {
	if !common.AutomaticDisableChannelEnabled {
		return false
	}
	if err == nil {
		return false
	}
	if types.IsChannelError(err) {
		return true
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	if operation_setting.ShouldDisableByStatusCode(err.StatusCode) {
		return true
	}

	lowerMessage := strings.ToLower(err.Error())
	search, _ := AcSearch(lowerMessage, operation_setting.AutomaticDisableKeywords, true)
	return search
}

func ShouldEnableChannel(newAPIError *types.NewAPIError, status int) bool {
	if !common.AutomaticEnableChannelEnabled {
		return false
	}
	if newAPIError != nil {
		return false
	}
	if status != common.ChannelStatusAutoDisabled {
		return false
	}
	return true
}
