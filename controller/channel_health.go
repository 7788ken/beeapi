package controller

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// GetChannelHealthEvents 查询某渠道近 N 天的健康度审计事件。
//
// GET /api/channel/:id/health/events?days=30&limit=100
func GetChannelHealthEvents(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}

	days := 30
	if v := c.Query("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	limit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	events, err := model.GetChannelHealthEvents(id, days, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// 同时返回当前渠道健康度快照（前端 Health tab 用）
	channel, err := model.GetChannelById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if channel == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "channel not found"})
		return
	}

	snapshot := gin.H{
		"degrade_level":      derefIntDefault(channel.DegradeLevel, 0),
		"original_priority":  derefInt64Default(channel.OriginalPriority, 0),
		"original_weight":    derefUintDefault(channel.OriginalWeight, 0),
		"current_priority":   derefInt64Default(channel.Priority, 0),
		"current_weight":     derefUintDefault(channel.Weight, 0),
		"last_demote_at":     derefInt64Default(channel.LastDemoteAt, 0),
		"last_upgrade_at":    derefInt64Default(channel.LastUpgradeAt, 0),
		"last_demote_reason": derefStringDefault(channel.LastDemoteReason, ""),
		"last_disabled_at":   derefInt64Default(channel.LastDisabledAt, 0),
		"permanent_disabled": derefIntDefault(channel.PermanentDisabled, 0),
		"rebounce_count":     derefIntDefault(channel.RebounceCount, 0),
		"status":             channel.Status,
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"snapshot": snapshot,
			"events":   events,
		},
	})
}

// RecoverChannelHealth 立即手动恢复渠道健康度：清快照、Reset L0、解除锁死、Reset 反弹计数。
// 如果当前 status 是 AutoDisabled，同时启用渠道；如果是 Enabled，仅重置健康度字段。
//
// POST /api/channel/:id/health/recover
func RecoverChannelHealth(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}

	channel, err := model.GetChannelById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if channel == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "channel not found"})
		return
	}

	// 1. 计算恢复后的 priority/weight：优先用 original snapshot，否则保持当前值
	originalP := derefInt64Default(channel.OriginalPriority, 0)
	originalW := derefUintDefault(channel.OriginalWeight, 0)
	currentP := derefInt64Default(channel.Priority, 0)
	currentW := derefUintDefault(channel.Weight, 0)

	restoreP := currentP
	restoreW := currentW
	currentLevel := derefIntDefault(channel.DegradeLevel, 0)
	if currentLevel > 0 {
		// 已经在降级态：恢复到 snapshot
		restoreP = originalP
		restoreW = originalW
	}

	fields := map[string]interface{}{
		"degrade_level":      0,
		"original_priority":  int64(0),
		"original_weight":    uint(0),
		"last_demote_reason": "",
		"priority":           restoreP,
		"weight":             restoreW,
		"permanent_disabled": 0,
		"rebounce_count":     0,
	}
	if err := model.UpdateChannelHealthFields(id, fields); err != nil {
		common.ApiError(c, err)
		return
	}

	// 2. 如果状态是 AutoDisabled，启用渠道
	//    - 单 key 渠道：调 UpdateChannelStatus 即可（同步刷 cache + abilities）
	//    - 多 key 渠道：UpdateChannelStatus(usingKey="") 会重置 multi_key_status_list，把已知坏 key 重新放回轮询池——
	//      所以这里只改 channels.status 字段，不动 multi_key_status_list；用户需另到 multi-key 管理页处理坏 key
	if channel.Status == common.ChannelStatusAutoDisabled {
		if channel.ChannelInfo.IsMultiKey {
			_ = model.UpdateChannelHealthFields(id, map[string]interface{}{
				"status": common.ChannelStatusEnabled,
			})
			model.CacheUpdateChannelStatus(id, common.ChannelStatusEnabled)
		} else {
			model.UpdateChannelStatus(id, "", common.ChannelStatusEnabled, "")
		}
	}

	// 3. 写审计
	userId := c.GetInt("id")
	_ = backgroundtask.Submit("manual-channel-health-recovery-audit", func(context.Context) {
		defer func() {
			if r := recover(); r != nil {
				common.SysError("RecordChannelHealthEvent recover panic: " + toStr(r))
			}
		}()
		operator := "manual"
		if userId > 0 {
			operator = "manual:" + strconv.Itoa(userId)
		}
		_ = model.RecordChannelHealthEvent(id, model.HealthEventEnable, currentLevel, model.HealthLevelHealthy, "manual recovery", operator)
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"id":            id,
			"degrade_level": 0,
			"priority":      restoreP,
			"weight":        restoreW,
		},
	})
}

// FU-9 R11: 本地 deref helper 转调 common.*（统一底层，避免重复实现）
func derefIntDefault(p *int, dft int) int             { return common.DerefIntOr(p, dft) }
func derefInt64Default(p *int64, dft int64) int64     { return common.DerefInt64Or(p, dft) }
func derefUintDefault(p *uint, dft uint) uint         { return common.DerefUintOr(p, dft) }
func derefStringDefault(p *string, dft string) string { return common.DerefStringOr(p, dft) }

func toStr(v interface{}) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%v", v)
}

// maybeResetChannelHealthOnManualEdit 用户在 UI 里手工改了 priority/weight 即视为"接管"，
// 清掉健康度快照（degrade_level / original_priority / original_weight / last_demote_reason
// / permanent_disabled / rebounce_count），确保下次降级基于最新基线。
//
// 触发条件：originChannel 在某个降级态/锁死态，且新提交的 priority 或 weight 与旧值不同。
// 仅 priority/weight 变更才视为接管；其他字段（key/models/group...）的变更不影响健康度。
//
// 调用点：controller.UpdateChannel 在 channel.Update() 之后。
func maybeResetChannelHealthOnManualEdit(c *gin.Context, origin, updated *model.Channel) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("channel_health: takeover hook panic (channel_id=%d): %v", updated.Id, r))
		}
	}()

	if origin == nil || updated == nil {
		return
	}

	priorityChanged := updated.Priority != nil &&
		derefInt64Default(origin.Priority, 0) != derefInt64Default(updated.Priority, 0)
	weightChanged := updated.Weight != nil &&
		derefUintDefault(origin.Weight, 0) != derefUintDefault(updated.Weight, 0)

	if !priorityChanged && !weightChanged {
		return // 没改 priority/weight，不接管
	}

	currentLevel := derefIntDefault(origin.DegradeLevel, 0)
	permLocked := derefIntDefault(origin.PermanentDisabled, 0) == 1
	rebounce := derefIntDefault(origin.RebounceCount, 0)
	lastDisabledAt := derefInt64Default(origin.LastDisabledAt, 0)

	// 健康渠道（L0 + 没锁死 + 没反弹计数 + 没禁用历史）改 priority/weight 不需要清
	if currentLevel == 0 && !permLocked && rebounce == 0 && lastDisabledAt == 0 {
		return
	}

	// 接管=完整重置健康度时序：清快照 + 清反弹保护历史（last_disabled_at + rebounce_count）。
	// 反弹保护时序也要清——否则用户接管后 30min 内系统再次自动 disable 仍会触发反弹锁死，不合理。
	// 注意：不动 channels.status——保持显式语义，启用渠道走 Recover dialog 或列表 toggle。
	fields := map[string]interface{}{
		"degrade_level":      0,
		"original_priority":  int64(0),
		"original_weight":    uint(0),
		"last_demote_reason": "",
		"permanent_disabled": 0,
		"rebounce_count":     0,
		"last_disabled_at":   int64(0),
	}
	if err := model.UpdateChannelHealthFields(updated.Id, fields); err != nil {
		common.SysError(fmt.Sprintf("channel_health: takeover update failed (channel_id=%d): %v", updated.Id, err))
		return
	}

	common.SysLog(fmt.Sprintf("channel_health: channel #%d 用户手工接管（priority/weight 变更），清快照 L%d → L0",
		updated.Id, currentLevel))

	userId := c.GetInt("id")
	_ = backgroundtask.Submit("manual-channel-health-takeover-audit", func(context.Context) {
		defer func() {
			if r := recover(); r != nil {
				common.SysError(fmt.Sprintf("channel_health: takeover audit panic: %v", r))
			}
		}()
		operator := "manual"
		if userId > 0 {
			operator = "manual:" + strconv.Itoa(userId)
		}
		_ = model.RecordChannelHealthEvent(updated.Id, model.HealthEventSnapshot, currentLevel, model.HealthLevelHealthy,
			"manual takeover (priority/weight edited)", operator)
	})
}

// maybeResetChannelHealthOnManualEnable 用户在 UI 把 status 从非 Enabled 切到 Enabled
// （PUT /api/channel 走的就是这条路径）时，清运行时 streak + DB 健康度残留。
//
// 触发条件：origin.Status != Enabled && updated.Status == Enabled。
//
// 这是修复"启用即死"的核心补丁——之前 PUT /api/channel 只改 channels.status，
// 不动 Redis err_streak（24h TTL）和 DB 残留快照，导致：
//   - 上次 disable 时累计的 err_streak 还在 Redis
//   - 启用后第一个 503 进来 INCR → 超过阈值 → 立刻 demote/disable
//   - 同时 RebounceCount 也保留，配合开启的反弹保护可能触发永久锁死
//
// 调用点：controller.UpdateChannel 在 channel.Update() 之后，
// 且应在 maybeResetChannelHealthOnManualEdit 之后（让 priority/weight 接管逻辑先跑）。
func maybeResetChannelHealthOnManualEnable(c *gin.Context, origin, updated *model.Channel) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("channel_health: enable hook panic (channel_id=%d): %v", updated.Id, r))
		}
	}()

	if origin == nil || updated == nil {
		return
	}
	// 仅 status 从非 Enabled 切到 Enabled 才触发
	if origin.Status == common.ChannelStatusEnabled || updated.Status != common.ChannelStatusEnabled {
		return
	}

	// 1. 清 Redis 运行时计数（err_streak / ok_streak / demote_cooldown）
	service.ClearChannelHealthRuntime(updated.Id)

	// 2. 清 DB 健康度残留：snapshot + 反弹历史 + 锁死位
	//    保留 priority/weight 当前值（用户可能已经在 UI 调过；不强行回滚到 original snapshot）
	//    与 RecoverChannelHealth 不同：那里更激进会回滚 priority/weight，这里只清"会再次触发降级"的状态
	currentLevel := derefIntDefault(origin.DegradeLevel, 0)
	fields := map[string]interface{}{
		"degrade_level":      0,
		"original_priority":  int64(0),
		"original_weight":    uint(0),
		"last_demote_reason": "",
		"permanent_disabled": 0,
		"rebounce_count":     0,
		"last_disabled_at":   int64(0),
	}
	if err := model.UpdateChannelHealthFields(updated.Id, fields); err != nil {
		common.SysError(fmt.Sprintf("channel_health: enable reset failed (channel_id=%d): %v", updated.Id, err))
		return
	}

	common.SysLog(fmt.Sprintf("channel_health: channel #%d 用户手工启用（status %d → %d），清运行时 streak + 健康度残留",
		updated.Id, origin.Status, updated.Status))

	userId := c.GetInt("id")
	_ = backgroundtask.Submit("manual-channel-health-enable-audit", func(context.Context) {
		defer func() {
			if r := recover(); r != nil {
				common.SysError(fmt.Sprintf("channel_health: enable audit panic: %v", r))
			}
		}()
		operator := "manual"
		if userId > 0 {
			operator = "manual:" + strconv.Itoa(userId)
		}
		_ = model.RecordChannelHealthEvent(updated.Id, model.HealthEventEnable, currentLevel, model.HealthLevelHealthy,
			"manual enable (status toggle)", operator)
	})
}
