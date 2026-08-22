package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/go-redis/redis/v8"
)

// ─────────────────────────────────────────────────────────────────────────────
// 渠道健康度状态机（被动机制）
//
// 完整设计见 docs/2026-05-04-channel-health-auto-degrade-plan.md
//
// 入口：RecordChannelResult — 在每次真实用户请求结束后调用
// 信号：success / fail / 单 key 失效
// 输出：渠道 Priority / Weight 调整、AutoDisabled 切换、审计事件
// ─────────────────────────────────────────────────────────────────────────────

// ── Redis 计数键命名 ──
func keyErrStreak(channelId int) string      { return fmt.Sprintf("channel:err_streak:%d", channelId) }
func keyOkStreak(channelId int) string       { return fmt.Sprintf("channel:ok_streak:%d", channelId) }
func keyLatStreak(channelId int) string      { return fmt.Sprintf("channel:lat_streak:%d", channelId) }
func keyHealthLock(channelId int) string     { return fmt.Sprintf("channel:health_lock:%d", channelId) }
func keyDemoteCooldown(channelId int) string { return fmt.Sprintf("channel:demote_cooldown:%d", channelId) }

// ── 进程内 fallback（Redis 不可用时启用，单实例够用，多实例计数偏低=安全方向）──
type fallbackEntry struct {
	val       int64
	expiresAt int64 // unix nano，0 表示永不过期
}

var (
	fallbackStreaks   sync.Map // key string -> *fallbackEntry
	fallbackLocks     sync.Map // key string -> *atomic.Int64（持锁到期 unix nano）
	fallbackCooldowns sync.Map // key string -> *atomicInt64Wrapper（冷却到期 unix nano）
)

func fallbackIncr(key string, ttl time.Duration) int64 {
	now := time.Now().UnixNano()
	exp := now + ttl.Nanoseconds()
	for {
		raw, _ := fallbackStreaks.LoadOrStore(key, &fallbackEntry{val: 0, expiresAt: exp})
		entry := raw.(*fallbackEntry)
		// 过期则重置
		if entry.expiresAt > 0 && entry.expiresAt < now {
			fallbackStreaks.Delete(key)
			continue
		}
		newVal := atomic.AddInt64(&entry.val, 1)
		entry.expiresAt = exp
		return newVal
	}
}

// fallbackDel 同时清 streaks 和 locks 两个 map。
// key 命名空间不重叠（"channel:err_streak:" / "channel:ok_streak:" / "channel:health_lock:"），
// 但调用方意图不同——streak 类调用 delKey，lock 类调用 unlock 都最终走这里，所以两个 map 都尝试删。
func fallbackDel(key string) {
	fallbackStreaks.Delete(key)
	fallbackLocks.Delete(key)
}

func fallbackTryLock(key string, ttl time.Duration) bool {
	now := time.Now().UnixNano()
	exp := now + ttl.Nanoseconds()
	raw, loaded := fallbackLocks.LoadOrStore(key, &atomicInt64Wrapper{v: exp})
	if !loaded {
		return true
	}
	w := raw.(*atomicInt64Wrapper)
	old := atomic.LoadInt64(&w.v)
	if old > now {
		return false
	}
	return atomic.CompareAndSwapInt64(&w.v, old, exp)
}

type atomicInt64Wrapper struct {
	v int64
}

// ── 计数与锁封装：优先 Redis，失败降级到 sync.Map ──
func incrWithTTL(key string, ttl time.Duration) int64 {
	if !common.RedisEnabled || common.RDB == nil {
		return fallbackIncr(key, ttl)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	pipe := common.RDB.TxPipeline()
	incrCmd := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		common.SysLog(fmt.Sprintf("channel_health: redis incr failed (%s), falling back to in-process: %v", key, err))
		return fallbackIncr(key, ttl)
	}
	return incrCmd.Val()
}

func delKey(key string) {
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_ = common.RDB.Del(ctx, key).Err()
	}
	fallbackDel(key)
}

func tryLock(key string, ttl time.Duration) bool {
	if !common.RedisEnabled || common.RDB == nil {
		return fallbackTryLock(key, ttl)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	ok, err := common.RDB.SetNX(ctx, key, "1", ttl).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		// Redis 抖动时降级，避免完全卡死状态机
		return fallbackTryLock(key, ttl)
	}
	return ok
}

func unlock(key string) {
	delKey(key)
}

// isCooldownActive 判断 cooldown 键是否存在且未过期。
// Redis: EXISTS；fallback: 检查 sync.Map 中保存的到期时间。
//
// fail-closed 设计：Redis 启用但调用失败时 → 保守认为"冷却中"返回 true，
// 而不是降级到 fallback（fallback 通常为空，会让冷却穿透击穿渠道）。
// 代价是 Redis 抖动期间几秒内拒绝降级，权衡上比"风暴穿透"安全。
func isCooldownActive(key string) bool {
	if !common.RedisEnabled || common.RDB == nil {
		return fallbackCooldownActive(key)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	n, err := common.RDB.Exists(ctx, key).Result()
	if err != nil {
		common.SysLog(fmt.Sprintf("channel_health: redis EXISTS failed for %s, fail-closed (assume cooldown active): %v", key, err))
		return true
	}
	return n > 0
}

// setCooldown 写入 cooldown 键并设置 TTL。幂等覆盖。
func setCooldown(key string, ttl time.Duration) {
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		if err := common.RDB.Set(ctx, key, "1", ttl).Err(); err == nil {
			return
		}
	}
	fallbackCooldownSet(key, ttl)
}

// fallbackCooldownActive 仅检查到期时间，不删除过期 entry——
// Delete 与并发 fallbackCooldownSet 之间会形成"读到过期→删除→另一 goroutine 已写新值被误删"的窄窗。
// 过期 entry 留在 sync.Map 中，由下次 fallbackCooldownSet 通过 CAS 覆盖即可。
// 渠道数量级 ~10²，残留 entry 内存代价可忽略。
func fallbackCooldownActive(key string) bool {
	raw, ok := fallbackCooldowns.Load(key)
	if !ok {
		return false
	}
	exp := atomic.LoadInt64(&raw.(*atomicInt64Wrapper).v)
	return exp > time.Now().UnixNano()
}

// fallbackCooldownSet 用 CAS loop 写入到期时间——只在新到期时间晚于已有时才覆盖，
// 防止并发场景下"较早到期"的写覆盖"较晚到期"的写（即冷却被意外缩短）。
func fallbackCooldownSet(key string, ttl time.Duration) {
	exp := time.Now().UnixNano() + ttl.Nanoseconds()
	raw, loaded := fallbackCooldowns.LoadOrStore(key, &atomicInt64Wrapper{v: exp})
	if !loaded {
		return
	}
	w := raw.(*atomicInt64Wrapper)
	for {
		old := atomic.LoadInt64(&w.v)
		if exp <= old {
			return // 已有更晚到期，不覆盖
		}
		if atomic.CompareAndSwapInt64(&w.v, old, exp) {
			return
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 公开入口
// ─────────────────────────────────────────────────────────────────────────────

// ClearChannelHealthRuntime 清除渠道的运行时计数（Redis err_streak / ok_streak / cooldown）。
//
// 用途：管理员手动启用渠道时（status 切换为 Enabled），避免历史 err_streak
// 残留导致"启用即死"——若上次失败留下的 err_streak 仍≥阈值，新请求一来就立刻
// 推进 demote 状态机。
//
// 不动 health_lock：它有 5s TTL 自然过期，强清反而可能干扰正在执行的 demote。
// DB 字段（degrade_level/snapshot/rebounce/permanent_disabled）由调用方决定是否清。
func ClearChannelHealthRuntime(channelId int) {
	if channelId <= 0 {
		return
	}
	delKey(keyErrStreak(channelId))
	delKey(keyOkStreak(channelId))
	delKey(keyDemoteCooldown(channelId))
}

// RecordChannelResult 记录一次真实流量结果。
//   - usingKey: 多 Key 渠道下命中的具体 key（来自 ContextKeyChannelKey）；非多 Key 渠道传 ""
//   - err: nil 表示成功
//   - ttftMs: 首字延迟（毫秒）。仅流式成功请求有效，非流式或未获取到传 -1
//
// 调用方应在 goroutine 内调用并 recover；本函数自身已尽量减少阻塞，但 DB/Redis 抖动仍可能耗时。
func RecordChannelResult(channelId int, usingKey string, err *types.NewAPIError, ttftMs int64) {
	if channelId <= 0 {
		return
	}
	cfg := operation_setting.GetChannelHealthConfig()
	if !cfg.Enabled {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("channel_health: panic recovered (channel_id=%d): %v", channelId, r))
		}
	}()

	// AutoBan=false 渠道完全跳过
	ch, lookupErr := loadChannelLite(channelId)
	if lookupErr == nil && ch != nil && !ch.GetAutoBan() {
		return
	}

	if err == nil {
		maxTtft := getEffectiveMaxTtft(ch, cfg)
		if maxTtft > 0 && ttftMs > 0 && ttftMs > int64(maxTtft) {
			onLatencyViolation(channelId, ttftMs, maxTtft, cfg)
			// TTFT 超阈但请求成功：不重置 ok_streak（与 onChannelError 不同），也不累积 ok_streak
			// （避免在延迟违规期间触发 upgrade）。非流式成功仍可正常累积 ok_streak 触发升级。
			return
		}
		onChannelSuccess(channelId, ttftMs, maxTtft, cfg)
		return
	}
	if !isCountableError(err, cfg) {
		return
	}
	onChannelError(channelId, usingKey, err, cfg)
}

// ─────────────────────────────────────────────────────────────────────────────
// 错误分类（§3.5）
// ─────────────────────────────────────────────────────────────────────────────

// isCountableError 判定错误是否计入渠道 streak。
//
// 决策顺序：
//  1. nil / IsSkipRetryError → 不计入
//  2. 单 key 失效（401/403 + invalid_api_key 关键字） → 计入（onChannelError 内部分流到 key 级处理）
//  3. CountableStatusCodes 非空时（白名单模式）：
//     - 仅匹配列表内的状态码计入
//     - 429 仍叠加 Count429AsError 开关
//  4. 列表为空时（兜底模式，向后兼容）：
//     - 429 → Count429AsError 决定
//     - IsChannelError / ShouldDisableByStatusCode / 5xx 兜底 → 计入
func isCountableError(err *types.NewAPIError, cfg *operation_setting.ChannelHealthConfig) bool {
	if err == nil {
		return false
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	// 上游瞬时过载类错误不计入渠道 streak：这类错误是上游临时容量不足，
	// 不代表渠道本身故障，快速累积 streak 会导致误禁用。
	if isTransientOverloadError(err) {
		return false
	}
	// 单 key 失效错误总是计入（onChannelError 内部走 key 级 disable，不推渠道 streak）
	if isKeyFatalError(err) {
		return true
	}

	// 白名单模式：CountableStatusCodes 一旦配置就**仅**用它判定状态码
	if operation_setting.HasCountableStatusCodes() {
		if err.StatusCode == 429 {
			return cfg.Count429AsError && operation_setting.IsCountableStatusCode(429)
		}
		return operation_setting.IsCountableStatusCode(err.StatusCode)
	}

	// 兜底模式（列表为空，向后兼容旧行为）
	if err.StatusCode == 429 {
		return cfg.Count429AsError
	}
	if types.IsChannelError(err) {
		return true
	}
	if operation_setting.ShouldDisableByStatusCode(err.StatusCode) {
		return true
	}
	// 5xx 一律算渠道错误（防止 ShouldDisableByStatusCode 默认列表漏配）
	if err.StatusCode >= 500 && err.StatusCode < 600 {
		return true
	}
	return false
}

// isTransientOverloadError 判定错误是否属于上游瞬时过载（不应计入渠道 streak）。
// 这类 503 表示上游临时容量不足或内部路由无可用节点，通常几秒内自行恢复。
func isTransientOverloadError(err *types.NewAPIError) bool {
	if err == nil || err.StatusCode != 503 {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, keyword := range transientOverloadKeywords {
		if strings.Contains(msg, keyword) {
			return true
		}
	}
	return false
}

var transientOverloadKeywords = []string{
	"cpu overloaded",
	"memory overloaded",
	"system cpu",
	"system memory",
	"no available channel",
}

// isKeyFatalError 判定错误是否属于"单把 key 失效"（vs 渠道整体故障）。
// 401/403 + 常见 invalid_api_key 关键字。
func isKeyFatalError(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	if err.StatusCode == 401 || err.StatusCode == 403 {
		return true
	}
	msg := strings.ToLower(err.Error())
	keyFatalKeywords := []string{
		"invalid api key",
		"invalid_api_key",
		"incorrect api key",
		"api key not valid",
		"unauthorized",
		"authentication failed",
	}
	for _, kw := range keyFatalKeywords {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// 成功 / 失败处理
// ─────────────────────────────────────────────────────────────────────────────

func onChannelSuccess(channelId int, ttftMs int64, maxTtft int, cfg *operation_setting.ChannelHealthConfig) {
	ttl := streakTTL(cfg)
	delKey(keyErrStreak(channelId))

	// TTFT 达标时重置 latency_streak
	if maxTtft > 0 && ttftMs > 0 && ttftMs <= int64(maxTtft) {
		delKey(keyLatStreak(channelId))
	}

	streak := incrWithTTL(keyOkStreak(channelId), ttl)

	if cfg.UpgradeThreshold > 0 && streak >= int64(cfg.UpgradeThreshold) {
		if upgradeOneLevel(channelId, cfg) {
			delKey(keyOkStreak(channelId)) // 升一级后重置，准备下一次升级
		}
	}
}

func onLatencyViolation(channelId int, ttftMs int64, maxTtft int, cfg *operation_setting.ChannelHealthConfig) {
	if cfg.CountLatencyAsError {
		// 简化模式：超阈直接走错误路径
		fakeErr := &types.NewAPIError{StatusCode: 0, Err: fmt.Errorf("ttft %dms > limit %dms", ttftMs, maxTtft)}
		onChannelError(channelId, "", fakeErr, cfg)
		return
	}

	ttl := streakTTL(cfg)
	streak := incrWithTTL(keyLatStreak(channelId), ttl)
	targetLevel := model.ComputeLevelFromStreak(streak, cfg.LatencyDegradeBase, cfg.LatencyDegradeStep, cfg.MaxDegradeLevel)
	if targetLevel > 0 {
		reason := fmt.Sprintf("ttft %dms > limit %dms (streak %d)", ttftMs, maxTtft, streak)
		demoteTo(channelId, targetLevel, reason, cfg)
	}
}

// getEffectiveMaxTtft 从已加载的 channel 获取有效 TTFT 阈值，避免重复查 DB。
func getEffectiveMaxTtft(ch *model.Channel, cfg *operation_setting.ChannelHealthConfig) int {
	if cfg.MaxTtftMs <= 0 {
		return 0
	}
	if ch != nil {
		if perChannel := ch.GetMaxTtftMs(); perChannel > 0 {
			return perChannel
		}
	}
	return cfg.MaxTtftMs
}

func onChannelError(channelId int, usingKey string, err *types.NewAPIError, cfg *operation_setting.ChannelHealthConfig) {
	// §3.5 决策 C：单 key 失效错误走单独路径，不计入渠道 streak
	if usingKey != "" && isKeyFatalError(err) {
		// 复用现有 key 级禁用（multi-key 渠道下只标这一把）
		model.UpdateChannelStatus(channelId, usingKey, common.ChannelStatusAutoDisabled, "key invalid: "+err.Error())
		// FU-6 R6: 单 key 失效虽不打断渠道稳定性，但也不该继续累计 ok_streak。
		// 否则"19 次 OK → 1 次 401 → 2 次 OK = upgrade"会触发——20 次连续成功的语义被破坏。
		// 决策：清 ok_streak，让 upgrade 重新累计，要求渠道整体稳定后才升级。
		delKey(keyOkStreak(channelId))
		return
	}

	ttl := streakTTL(cfg)
	delKey(keyOkStreak(channelId))
	streak := incrWithTTL(keyErrStreak(channelId), ttl)

	reason := err.ErrorWithStatusCode()
	switch {
	case cfg.DisableThreshold > 0 && streak >= int64(cfg.DisableThreshold):
		// 走标准禁用流程；DisableChannel 内部会写 LastDisabledAt 并触发反弹检测
		ch, dbErr := loadChannelLite(channelId)
		if dbErr != nil || ch == nil {
			return
		}
		DisableChannel(types.ChannelError{
			ChannelId:   channelId,
			ChannelType: ch.Type,
			ChannelName: ch.Name,
			IsMultiKey:  ch.ChannelInfo.IsMultiKey,
			UsingKey:    usingKey,
			AutoBan:     ch.GetAutoBan(),
		}, reason)
		// 禁用后清 streak（避免立即恢复后又因残留 streak 再次禁用）
		delKey(keyErrStreak(channelId))

	default:
		targetLevel := model.ComputeLevelFromStreak(streak, cfg.BaseDegradeThreshold, cfg.LevelStepThreshold, cfg.MaxDegradeLevel)
		if targetLevel > 0 {
			demoteTo(channelId, targetLevel, reason, cfg)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 降级 / 升级
// ─────────────────────────────────────────────────────────────────────────────

func demoteTo(channelId int, targetLevel int, reason string, cfg *operation_setting.ChannelHealthConfig) bool {
	// 降级节流：DemoteCooldownSec > 0 时，窗口内同一渠道至多触发 1 次降级。
	// 防止瞬时错误风暴在几秒内把渠道从 L0 一路打到 L2。
	if cfg.DemoteCooldownSec > 0 && isCooldownActive(keyDemoteCooldown(channelId)) {
		return false
	}

	if !tryLock(keyHealthLock(channelId), 5*time.Second) {
		return false
	}
	defer unlock(keyHealthLock(channelId))

	ch, err := loadChannelLite(channelId)
	if err != nil || ch == nil {
		return false
	}

	// AutoBan=false 渠道不参与（§6 兼容性）
	if !ch.GetAutoBan() {
		return false
	}

	current := derefInt(ch.DegradeLevel, 0)
	if current >= targetLevel {
		return false // 幂等：已经在目标档或更低
	}

	// snapshot 取值：
	//   - 首次降级（current=0）：以"当前 priority/weight"作为基线，保存到 OriginalPriority/Weight
	//   - 已降级过（L1→L2）：用已存的 OriginalPriority/Weight，避免基于 L1 的值再砍导致雪崩
	var originalP int64
	var originalW uint
	if current == 0 {
		originalP = derefInt64(ch.Priority, 0)
		originalW = derefUint(ch.Weight, 0)
	} else {
		originalP = derefInt64(ch.OriginalPriority, 0)
		originalW = derefUint(ch.OriginalWeight, 0)
	}

	newP, newW := applyDegrade(originalP, originalW, targetLevel, cfg)
	now := time.Now().Unix()

	fields := map[string]interface{}{
		"priority":           newP,
		"weight":             newW,
		"degrade_level":      targetLevel,
		"last_demote_at":     now,
		"last_demote_reason": truncateReason(reason, 1000),
	}
	if current == 0 {
		// 首次降级才写 snapshot；后续 L1→L2 不再覆盖
		fields["original_priority"] = originalP
		fields["original_weight"] = originalW
	}

	if err := model.UpdateChannelHealthFields(channelId, fields); err != nil {
		common.SysError(fmt.Sprintf("channel_health: demote update failed (channel_id=%d): %v", channelId, err))
		return false
	}

	// 写降级 cooldown：仅在 DB 更新成功后；保证下次降级动作至少间隔 DemoteCooldownSec
	if cfg.DemoteCooldownSec > 0 {
		setCooldown(keyDemoteCooldown(channelId), time.Duration(cfg.DemoteCooldownSec)*time.Second)
	}

	common.SysLog(fmt.Sprintf("channel_health: demote channel #%d %s → L%d (priority %d→%d, weight %d→%d), reason=%s",
		channelId, formatLevel(current), targetLevel, originalP, newP, originalW, newW, reason))

	_ = backgroundtask.Submit("channel-health-demote-event", func(context.Context) {
		defer recoverAndLog("RecordChannelHealthEvent demote")
		_ = model.RecordChannelHealthEvent(channelId, model.HealthEventDemote, current, targetLevel, reason, "auto")
	})

	if cfg.NotifyOnDegrade {
		subject := fmt.Sprintf("通道「%s」（#%d）已降级到 L%d", ch.Name, channelId, targetLevel)
		content := fmt.Sprintf("通道「%s」（#%d）从 L%d 降级到 L%d，原因：%s", ch.Name, channelId, current, targetLevel, reason)
		NotifyRootUser(formatHealthNotifyType(channelId, "demote"), subject, content)
	}

	return true
}

func upgradeOneLevel(channelId int, cfg *operation_setting.ChannelHealthConfig) bool {
	if !tryLock(keyHealthLock(channelId), 5*time.Second) {
		return false
	}
	defer unlock(keyHealthLock(channelId))

	ch, err := loadChannelLite(channelId)
	if err != nil || ch == nil {
		return false
	}
	current := derefInt(ch.DegradeLevel, 0)
	if current <= 0 {
		return false
	}

	originalP := derefInt64(ch.OriginalPriority, derefInt64(ch.Priority, 0))
	originalW := derefUint(ch.OriginalWeight, derefUint(ch.Weight, 0))
	now := time.Now().Unix()

	fields := map[string]interface{}{
		"last_upgrade_at": now,
	}
	newLevel := current - 1
	if newLevel <= model.HealthLevelHealthy {
		// 完全恢复：清 snapshot
		newLevel = model.HealthLevelHealthy
		fields["priority"] = originalP
		fields["weight"] = originalW
		fields["degrade_level"] = 0
		fields["original_priority"] = int64(0)
		fields["original_weight"] = uint(0)
		fields["last_demote_reason"] = ""
	} else {
		// 降一级：基于 originalP/originalW 重算新级别的公式值
		newP, newW := applyDegrade(originalP, originalW, newLevel, cfg)
		fields["priority"] = newP
		fields["weight"] = newW
		fields["degrade_level"] = newLevel
	}

	if err := model.UpdateChannelHealthFields(channelId, fields); err != nil {
		common.SysError(fmt.Sprintf("channel_health: upgrade update failed (channel_id=%d): %v", channelId, err))
		return false
	}

	common.SysLog(fmt.Sprintf("channel_health: upgrade channel #%d L%d → %s",
		channelId, current, formatLevel(newLevel)))

	_ = backgroundtask.Submit("channel-health-upgrade-event", func(context.Context) {
		defer recoverAndLog("RecordChannelHealthEvent upgrade")
		_ = model.RecordChannelHealthEvent(channelId, model.HealthEventUpgrade, current, newLevel, "ok_streak reached", "auto")
	})

	if cfg.NotifyOnUpgrade {
		subject := fmt.Sprintf("通道「%s」（#%d）已升级到 %s", ch.Name, channelId, formatLevel(newLevel))
		content := fmt.Sprintf("通道「%s」（#%d）连续成功达标，从 L%d 升级到 %s", ch.Name, channelId, current, formatLevel(newLevel))
		NotifyRootUser(formatHealthNotifyType(channelId, "upgrade"), subject, content)
	}

	return true
}

// applyDegrade 降级公式：基于 level 线性衰减权重，优先级偏移 -level。
// originalW=0 时不动 weight，保留"只靠 priority"语义。
func applyDegrade(originalP int64, originalW uint, level int, cfg *operation_setting.ChannelHealthConfig) (int64, uint) {
	if level <= 0 {
		return originalP, originalW
	}
	factor, offset := model.DegradeFactors(level, cfg.MaxDegradeLevel, cfg.MinWeightFactor)
	newP := originalP + int64(offset)
	newW := uint(0)
	if originalW > 0 {
		if cfg.MinWeightFactor <= 0 {
			// MinWeightFactor=0：允许 weight 降到 0，完全排除真实流量，恢复靠系统探测
			newW = uint(math.Floor(float64(originalW) * factor))
		} else {
			newW = uint(math.Max(1, math.Floor(float64(originalW)*factor)))
		}
	}
	return newP, newW
}

// ─────────────────────────────────────────────────────────────────────────────
// 工具函数
// ─────────────────────────────────────────────────────────────────────────────

func streakTTL(cfg *operation_setting.ChannelHealthConfig) time.Duration {
	if cfg.StreakWindowSec <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(cfg.StreakWindowSec) * time.Second
}

func loadChannelLite(channelId int) (*model.Channel, error) {
	// 直接走 DB，不走 cache：cache 字段可能不全（且 cache 是异步同步的）
	return model.GetChannelById(channelId, false)
}

// FU-9 R11: 本地 deref helper 转调 common.*（统一底层，避免重复实现）
func derefInt(p *int, dft int) int          { return common.DerefIntOr(p, dft) }
func derefInt64(p *int64, dft int64) int64  { return common.DerefInt64Or(p, dft) }
func derefUint(p *uint, dft uint) uint      { return common.DerefUintOr(p, dft) }

// truncateReason 按 rune 安全截断，避免切到 UTF-8 中间字节产生乱码。
// reason 通常是上游错误消息，可能含中文/日文/特殊字符。
func truncateReason(s string, n int) string {
	if len(s) <= n {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func formatLevel(level int) string {
	switch level {
	case model.HealthLevelHealthy:
		return "L0"
	case model.HealthLevelL1:
		return "L1"
	case model.HealthLevelL2:
		return "L2"
	case model.HealthLevelDisabled:
		return "Disabled"
	default:
		return fmt.Sprintf("L%d", level)
	}
}

// formatHealthNotifyType 通知去重 key（§3.7）。
// 只带 channelId + action（不带 level）—— 让上游 CheckNotificationLimit 按"同一渠道+同一动作"
// 在 1 小时内自然去重，避免 L0→L1→L2 短时间连续降级时每档都发一次通知造成噪音。
// 上游限频默认 NotifyLimitCount=2 / NotificationLimitDurationMinute=10，按小时桶累计。
func formatHealthNotifyType(channelId int, action string) string {
	return fmt.Sprintf("channel_health_%s_%d", action, channelId)
}

func recoverAndLog(tag string) {
	if r := recover(); r != nil {
		common.SysError(fmt.Sprintf("channel_health: panic in %s: %v", tag, r))
	}
}
