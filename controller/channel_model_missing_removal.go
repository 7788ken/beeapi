package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 报错触发模型移出：上游报「渠道中没有该模型」（典型 404 "The model xxx does not exist"）时，
// 先做一次上游模型同步核实，确认模型确实不在上游后只把该模型从渠道移出，而不是禁用整个渠道；
// 移出后延时复核，若模型回到上游则自动加回。核实失败（拉不到模型列表 / 模型仍在）时回退到
// 原有的自动禁用判定，行为与改造前一致。
//
// 配置（后台「设置-监控告警」页，对应 option key）：
//   - ModelMissingRemovalEnabled（默认 true）
//   - ModelMissingKeywords（命中关键词，每行一个）
//   - ModelMissingRemovalCooldownSeconds（默认 60，同渠道+模型窗口内只核实一次）
//   - ModelMissingRecheckIntervalSeconds（默认 600，移出后复核间隔）
//   - ModelForbiddenRemovalEnabled（默认 false）/ ModelForbiddenStatusCodes（默认 400,403）
//     / ModelForbiddenKeywords / ModelForbiddenRecheckIntervalSeconds（默认 1800）
//   - ModelRemovalMaxRemovedPerChannel（默认 5）/ ModelRemovalCapAction（默认 alert_only）
//     / ModelMissingRemovalCapEnabled（默认 false）
//   - 渠道级开关：渠道 settings.model_missing_removal_disabled
//   - 复核保留上限：移出超过 24 小时仍未回归则放弃追踪，避免僵尸条目。
const (
	modelMissingRecheckTaskDefaultIntervalSec   = 60
	modelMissingRemovalWatchMaxHoldSeconds      = 86400
	modelMissingRecheckTaskBatchSize            = 100
	modelMissingRecheckSettingsPrefilterPattern = "%model_missing_recheck_at%"
	// 安全阀兜底值：配置非法（<1）时回落到 5，与改造前硬编码值一致。
	modelRemovalMaxRemovedPerChannelFallback = 5
	// 「连续 N 次才摘」门槛兜底值（配置非法 <1 时回落），与连击计数的滑动窗口 TTL（秒）。
	modelRemovalConsecutiveThresholdFallback = 10
	modelRemovalStreakWindowSec              = 600
)

// modelRemovalReason 区分三条触发路径：核实口径、复核间隔、安全阀都按它分流。
type modelRemovalReason string

const (
	modelRemovalReasonMissing   modelRemovalReason = "model_missing"
	modelRemovalReasonRateLimit modelRemovalReason = "rate_limit"
	modelRemovalReasonForbidden modelRemovalReason = "forbidden"
)

var (
	modelMissingRemovalTriggerState = struct {
		sync.Mutex
		lastTrigger map[string]int64
	}{lastTrigger: make(map[string]int64)}

	modelMissingRecheckTaskOnce    sync.Once
	modelMissingRecheckTaskRunning atomic.Bool
)

func modelMissingRemovalCooldownSeconds() int64 {
	seconds := operation_setting.ModelMissingRemovalCooldownSeconds
	if seconds < 1 {
		return 60
	}
	return int64(seconds)
}

func modelMissingRecheckIntervalSeconds() int64 {
	seconds := operation_setting.ModelMissingRecheckIntervalSeconds
	if seconds < 60 {
		return 600
	}
	return int64(seconds)
}

func modelRateLimitRecheckIntervalSeconds() int64 {
	seconds := operation_setting.ModelRateLimitRecheckIntervalSeconds
	if seconds < 60 {
		return 180
	}
	return int64(seconds)
}

func modelForbiddenRecheckIntervalSeconds() int64 {
	seconds := operation_setting.ModelForbiddenRecheckIntervalSeconds
	if seconds < 60 {
		return 1800
	}
	return int64(seconds)
}

// recheckIntervalSecondsFor 按移出原因取复核间隔：限流恢复远快于模型下线，用更短的周期；
// 权限变更由人工在上游控制台操作，比限流慢得多，用更长的周期。
func recheckIntervalSecondsFor(reason modelRemovalReason) int64 {
	switch reason {
	case modelRemovalReasonRateLimit:
		return modelRateLimitRecheckIntervalSeconds()
	case modelRemovalReasonForbidden:
		return modelForbiddenRecheckIntervalSeconds()
	default:
		return modelMissingRecheckIntervalSeconds()
	}
}

// modelRemovalMaxRemovedPerChannel 同渠道累计摘除上限（可配，默认 5）。
func modelRemovalMaxRemovedPerChannel() int {
	if configured := operation_setting.ModelRemovalMaxRemovedPerChannel; configured >= 1 {
		return configured
	}
	return modelRemovalMaxRemovedPerChannelFallback
}

// removalCapApplies 判定该原因是否受「同渠道累计摘除上限」约束。
//
//	限流 / 权限：摘除依据是推断——渠道级限流或凭据级无权时每个模型都报同样的错，
//	  摘到触顶正是「问题在渠道不在模型」的信号，必须受约束。
//	缺失：摘除前有存在性核实兜底（上游列表里真的没有才摘），触顶意味着上游确实批量下线了
//	  N 个模型，是事实而非误判；此时停整渠道会连带切掉仍可用的模型，与低烈度优先相反。
//	  默认不受约束（= 改造前行为），仅当管理员显式开启时纳入。
func removalCapApplies(reason modelRemovalReason) bool {
	if reason == modelRemovalReasonMissing {
		return operation_setting.ModelMissingRemovalCapEnabled
	}
	return true
}

// modelRemovalConsecutiveThreshold 「连续 N 次才摘」门槛（可配，默认 10；设 1 = 命中即摘的旧行为）。
func modelRemovalConsecutiveThreshold() int64 {
	if configured := operation_setting.ModelRemovalConsecutiveThreshold; configured >= 1 {
		return int64(configured)
	}
	return modelRemovalConsecutiveThresholdFallback
}

// removalStreakGateApplies 该原因是否受「连续 N 次」门槛约束。
// 限流 / 权限：依据是推断，单条 429/400 常是上游账号池的瞬时 failover 信号，需连续 N 次才摘。
// 缺失：有 /v1/models 存在性核实作硬证据，单次核实即可信，不叠加连击门槛。
func removalStreakGateApplies(reason modelRemovalReason) bool {
	return reason == modelRemovalReasonRateLimit || reason == modelRemovalReasonForbidden
}

// modelRemovalStreakWindow 连击计数的滑动窗口 TTL：相邻两次失败间隔超过它才让计数自然过期。
func modelRemovalStreakWindow() time.Duration {
	return time.Duration(modelRemovalStreakWindowSec) * time.Second
}

// modelRemovalStreakReached 累加 (渠道,模型) 的连续失败计数并判断是否达到摘除门槛。
//
//	推断路径（限流 / 权限）：连续 N 次才摘——未达门槛返回 false（调用方暂缓摘除），达到即清零
//	  计数并返回 true。中途任一成功请求由 relay 成功路径清零计数，打断连击。
//	缺失路径：不受门槛约束，恒返回 true（有 /v1/models 存在性核实作硬证据，单次核实即可信）。
func modelRemovalStreakReached(channelId int, channelName string, modelName string, reason modelRemovalReason) bool {
	if !removalStreakGateApplies(reason) {
		return true
	}
	streak := service.IncrModelRemovalStreak(channelId, modelName, modelRemovalStreakWindow())
	threshold := modelRemovalConsecutiveThreshold()
	if streak < threshold {
		common.SysLog(fmt.Sprintf("model removal deferred, consecutive threshold not reached: reason=%s channel_id=%d channel_name=%s model=%s streak=%d threshold=%d",
			reason, channelId, channelName, modelName, streak, threshold))
		return false
	}
	// 达到门槛：清零计数（本次摘除后重新起算）。
	service.ResetModelRemovalStreak(channelId, modelName)
	return true
}

// isUpstreamModelMissingError 判断一次 relay 错误是否像「渠道中没有该模型」。
// 双保险：错误信息回显了模型名时，404 或关键词命中其一即可；未回显模型名（可能被
// model_mapping 改名）时要求 404 与关键词同时命中，降低误判。
func isUpstreamModelMissingError(err *types.NewAPIError, originModel string) bool {
	if err == nil || originModel == "" {
		return false
	}
	// channel:* 是渠道自身问题（key 失效/超时等）；skipRetry 多为网关侧判定；
	// ErrorCodeModelNotFound 是网关侧「本站没配这个模型」，都不是上游缺模型。
	if types.IsChannelError(err) || types.IsSkipRetryError(err) || err.GetErrorCode() == types.ErrorCodeModelNotFound {
		return false
	}
	lowerMessage := strings.ToLower(err.Error())
	keywordHit := lo.SomeBy(operation_setting.ModelMissingKeywords, func(keyword string) bool {
		return strings.Contains(lowerMessage, keyword)
	})
	if strings.Contains(lowerMessage, strings.ToLower(originModel)) {
		return err.StatusCode == 404 || keywordHit
	}
	return err.StatusCode == 404 && keywordHit
}

// isUpstreamRateLimitError 判断一次 relay 错误是否为上游 429 限流。
// 不看错误文案：429 的响应体格式各家上游差异极大且常不含模型名，状态码本身已是充分信号。
// 网关自造的 429（容量排队超时 / 用户级软限流）产生在 middleware 层，不走本函数所在的
// relay 报错路径，天然不会误触发。
func isUpstreamRateLimitError(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	// channel:* 是渠道自身问题（key 用尽等）；skipRetry 多为网关侧判定，都不是上游限流。
	if types.IsChannelError(err) || types.IsSkipRetryError(err) {
		return false
	}
	return err.StatusCode == 429
}

// isUpstreamForbiddenError 判断一次 relay 错误是否为「该渠道无权使用/不支持这个模型」。
// 与限流路径相反，这里必须看文案：403/400 是宽口径状态码（密钥整体失效、参数错误都会用），
// 只看状态码会把渠道级问题误判成模型级问题。状态码与关键词双命中才成立。
func isUpstreamForbiddenError(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	// channel:* 是渠道自身问题（key 失效等）；skipRetry 多为网关侧判定；
	// ErrorCodeModelNotFound 是网关侧「本站没配这个模型」，都不是上游模型级权限问题。
	if types.IsChannelError(err) || types.IsSkipRetryError(err) || err.GetErrorCode() == types.ErrorCodeModelNotFound {
		return false
	}
	if !operation_setting.IsModelForbiddenStatusCode(err.StatusCode) {
		return false
	}
	lowerMessage := strings.ToLower(err.Error())
	return lo.SomeBy(operation_setting.ModelForbiddenKeywords, func(keyword string) bool {
		keyword = strings.TrimSpace(strings.ToLower(keyword))
		return keyword != "" && strings.Contains(lowerMessage, keyword)
	})
}

// classifyModelRemovalReason 判定该错误走哪条移出路径。按「判定依据强度」排序，强的优先：
//  1. 模型缺失——有存在性核实作硬证据。429 里若同时命中缺失关键词（部分上游用 429 表达
//     「该模型未开通」），按缺失处理可走完整核实，判定更严。
//  2. 权限/不支持——状态码 + 关键词双命中。默认状态码集（400/403）与限流（429）不相交，
//     只有管理员把 429 也配进权限状态码时才真正体现先后；此时双命中比单看状态码更严，故优先。
//  3. 限流——只看状态码 429，口径最宽，兜底。
func classifyModelRemovalReason(err *types.NewAPIError, originModel string) (modelRemovalReason, bool) {
	if operation_setting.ModelMissingRemovalEnabled && isUpstreamModelMissingError(err, originModel) {
		return modelRemovalReasonMissing, true
	}
	if operation_setting.ModelForbiddenRemovalEnabled && isUpstreamForbiddenError(err) {
		return modelRemovalReasonForbidden, true
	}
	if operation_setting.ModelRateLimitRemovalEnabled && isUpstreamRateLimitError(err) {
		return modelRemovalReasonRateLimit, true
	}
	return "", false
}

// shouldSkipRemovalBecauseModelStillListed 判定「模型仍在上游列表」是否构成不移出的理由。
// 只对缺失路径成立——缺失路径的全部依据就是「上游没这个模型了」，模型还在即证伪；
// 限流 / 权限路径若也判这一条则永远摘不掉：429 限流和 403 无权时，模型必然仍被上游列出
// （/v1/models 只反映「上游有这个模型」，不反映「本密钥能不能调」）。
func shouldSkipRemovalBecauseModelStillListed(reason modelRemovalReason, upstreamSet map[string]struct{}, upstreamTarget string) bool {
	if reason != modelRemovalReasonMissing {
		return false
	}
	_, exists := upstreamSet[upstreamTarget]
	return exists
}

// modelMissingRemovalCooldownOk 同渠道+模型在冷却窗口内只触发一次核实（报错风暴防抖）。
func modelMissingRemovalCooldownOk(channelID int, modelName string) bool {
	modelMissingRemovalTriggerState.Lock()
	defer modelMissingRemovalTriggerState.Unlock()
	now := common.GetTimestamp()
	cooldown := modelMissingRemovalCooldownSeconds()
	// 顺手清理过期条目，避免 map 随时间无限增长
	for key, last := range modelMissingRemovalTriggerState.lastTrigger {
		if now-last >= cooldown*10 {
			delete(modelMissingRemovalTriggerState.lastTrigger, key)
		}
	}
	key := fmt.Sprintf("%d:%s", channelID, modelName)
	if last, ok := modelMissingRemovalTriggerState.lastTrigger[key]; ok && now-last < cooldown {
		return false
	}
	modelMissingRemovalTriggerState.lastTrigger[key] = now
	return true
}

// tryHandleChannelModelMissing 在 relay 报错路径调用。识别为「模型缺失」「上游限流」或
// 「权限/不支持」时提交异步任务，返回 true 表示已接管：整渠道禁用逻辑移交异步任务，
// 未确认移出时在其中回退原禁用判定。
// 这就是「摘模型优先于停渠道」的机制落点——接管成功即短路 processChannelError 里的
// ShouldDisableChannel 分支，因此权限关键词表与 AutomaticDisableKeywords 重叠时天然低烈度优先。
func tryHandleChannelModelMissing(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError) bool {
	originModel := c.GetString("original_model")
	reason, matched := classifyModelRemovalReason(err, originModel)
	if !matched {
		return false
	}
	// 「连续 N 次才摘」门槛（仅对推断路径限流/权限生效，计数走共享 Redis 跨节点聚合，成功请求
	// 在 relay 成功路径清零）：未达门槛时仍返回 true 接管（抑制整渠道禁用），只是暂缓摘除——
	// 瞬时 429/400 不该停整渠道，也不该一次就摘模型。
	if !modelRemovalStreakReached(channelError.ChannelId, channelError.ChannelName, originModel, reason) {
		return true
	}
	if !modelMissingRemovalCooldownOk(channelError.ChannelId, originModel) {
		// 冷却窗口内已有任务在处理，同样跳过同步禁用路径
		return true
	}
	channelID := channelError.ChannelId
	channelName := channelError.ChannelName
	_ = backgroundtask.Submit("model-missing-removal", func(ctx context.Context) {
		runModelMissingRemoval(ctx, channelID, channelName, originModel, channelError, err, reason)
	})
	return true
}

// fallbackDisableChannelForModelMissing 核实失败（或渠道不适用移出）时回退到原有自动禁用逻辑。
func fallbackDisableChannelForModelMissing(channelError types.ChannelError, err *types.NewAPIError) {
	if service.ShouldDisableChannel(err) && channelError.AutoBan {
		service.DisableChannel(channelError, err.ErrorWithStatusCode())
	}
}

// 哨兵错误：区分行锁事务中止后的不同处理方式。
var (
	errModelMissingChannelUnavailable = errors.New("channel unavailable for model missing removal")
	errModelMissingLastModel          = errors.New("last model in channel")
	errModelMissingWatchCleared       = errors.New("model missing watch already cleared")
	errModelRemovalCapped             = errors.New("model removal cap reached for channel")
)

// handleModelRemovalCapReached 同渠道累计摘除触顶后的处置（第 2 级烈度）。
//
//	alert_only（默认）：只记日志 + 回退原有禁用判定，与改造前完全一致。
//	disable_channel：升级为停用整渠道——触发依据是「累计触顶」这个渠道级信号，
//	  而非单次错误的文案/状态码，因此不再走 ShouldDisableChannel 的关键词判定；
//	  但仍受两道既有闸门约束：全局「自动禁用渠道」总开关（管理员关掉即表示不许自动停渠道）
//	  与渠道级 AutoBan（在 service.DisableChannel 内部检查）。
//	  升级不可用时降级为 alert_only 的处置，不静默丢弃。
func handleModelRemovalCapReached(channelID int, channelName string, originModel string, reason modelRemovalReason,
	removedCount int, capValue int, channelError types.ChannelError, err *types.NewAPIError) {
	common.SysLog(fmt.Sprintf("model removal skipped, channel cap reached: reason=%s channel_id=%d channel_name=%s model=%s removed=%d cap=%d action=%s",
		reason, channelID, channelName, originModel, removedCount, capValue, operation_setting.ModelRemovalCapAction))

	if !operation_setting.ShouldDisableChannelOnRemovalCap() {
		fallbackDisableChannelForModelMissing(channelError, err)
		return
	}
	if !common.AutomaticDisableChannelEnabled {
		common.SysLog(fmt.Sprintf("model removal cap escalation skipped, automatic channel disable is globally off: channel_id=%d channel_name=%s", channelID, channelName))
		fallbackDisableChannelForModelMissing(channelError, err)
		return
	}
	// reason 文案刻意与普通错误禁用区分开：运维在渠道详情看到的是「摘除累计触顶升级」，
	// 而不是最后那次上游报错，避免把渠道级判定误读成单模型故障。
	escalationReason := fmt.Sprintf("模型级摘除累计触顶升级停用：触发原因=%s，本渠道已累计摘除 %d 个模型（上限 %d），判定为渠道级故障而非单模型故障；最近一次上游错误：%s",
		reason, removedCount, capValue, err.ErrorWithStatusCode())
	service.DisableChannel(channelError, escalationReason)
}

func runModelMissingRemoval(ctx context.Context, channelID int, channelName string, originModel string, channelError types.ChannelError, err *types.NewAPIError, reason modelRemovalReason) {
	// 事务外预检（只读），正式判定在事务内拿锁后重做。
	channel, dbErr := model.GetChannelById(channelID, true)
	if dbErr != nil || channel == nil {
		common.SysLog(fmt.Sprintf("model missing removal aborted, channel not found: channel_id=%d model=%s err=%v", channelID, originModel, dbErr))
		fallbackDisableChannelForModelMissing(channelError, err)
		return
	}
	if channel.Status != common.ChannelStatusEnabled {
		// 渠道已被禁用/手动停用，无模型可移
		return
	}
	if channel.GetOtherSettings().ModelMissingRemovalDisabled {
		fallbackDisableChannelForModelMissing(channelError, err)
		return
	}

	// 上游拉取放在锁外，避免网络请求持锁过久。两条路径都要求拉到非空列表，但用途不同：
	//   - 模型缺失：列表是「模型确实没了」的唯一证据。空列表视为核实不可信——上游降级可能返回
	//     200 + 空 data，与 404 报错常处于同一故障窗口，若当权威证据会把正常模型误移出。
	//   - 限流：列表不作证据（限流时模型必然仍在列表），而是复核加回的依赖。拉不到 = 复核链路
	//     不通 = 摘了永远加不回来（复核会一路顺延到 24h 上限），因此同样不摘。
	upstreamModels, fetchErr := fetchChannelUpstreamModelIDs(ctx, channel)
	if fetchErr != nil || len(upstreamModels) == 0 {
		common.SysLog(fmt.Sprintf("model removal sync untrusted: reason=%s channel_id=%d channel_name=%s model=%s err=%v upstream_count=%d", reason, channelID, channelName, originModel, fetchErr, len(upstreamModels)))
		fallbackDisableChannelForModelMissing(channelError, err)
		return
	}
	upstreamSet := make(map[string]struct{}, len(upstreamModels))
	for _, m := range upstreamModels {
		upstreamSet[m] = struct{}{}
	}
	upstreamTarget := originModel
	if target, ok := normalizeChannelModelMapping(channel)[originModel]; ok {
		upstreamTarget = target
	}
	if shouldSkipRemovalBecauseModelStillListed(reason, upstreamSet, upstreamTarget) {
		// 上游仍列出该模型：大概率是临时故障，不移出
		common.SysLog(fmt.Sprintf("model missing removal skipped, model still listed upstream: channel_id=%d channel_name=%s model=%s upstream_model=%s", channelID, channelName, originModel, upstreamTarget))
		fallbackDisableChannelForModelMissing(channelError, err)
		return
	}

	// 行锁下读-改-写：多节点（共享库）对同一渠道的并发移出在此串行化，
	// 避免快照读整列覆盖写导致已移出模型复活、追踪条目丢失；
	// abilities 与 models/settings 同事务提交，不产生半致状态。
	remainingModels := 0
	capValue := modelRemovalMaxRemovedPerChannel()
	cappedRemovedCount := 0
	// 提交成功后发通知要用到的复核时间：事务里算出来带出来，不在事务外重算（重算会与
	// 「取更早那个」的逻辑漂移，通知里报的时间就与实际复核时间不一致）。
	notifyRecheckAt := int64(0)
	txErr := model.DB.Transaction(func(tx *gorm.DB) error {
		locked := &model.Channel{}
		if lockErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(locked, "id = ?", channelID).Error; lockErr != nil {
			return lockErr
		}
		if locked.Status != common.ChannelStatusEnabled || locked.GetOtherSettings().ModelMissingRemovalDisabled {
			return errModelMissingChannelUnavailable
		}
		localModels := normalizeModelNames(locked.GetModels())
		if !lo.Contains(localModels, originModel) {
			// 已被其他节点/人工移出：顺手重建 abilities，修复可能残留的路由不一致（幂等）
			return locked.UpdateAbilities(tx)
		}
		if len(localModels) <= 1 {
			// 安全阀：摘成空渠道没有意义，交给原禁用逻辑处理
			return errModelMissingLastModel
		}
		lockedSettings := locked.GetOtherSettings()
		// 安全阀：整渠道 RPM 打满 / 凭据级无权时每个模型都会报同样的错，不设上限会把渠道逐个
		// 模型掏空。达到上限说明「限的/拒的是渠道不是模型」，继续摘无意义，交由触顶处置决定
		// 是仅告警还是升级停渠道。计数口径沿用改造前：数的是 ModelMissingRemovedModels 全表
		// （不分原因），不因新增路径而改变限流路径的既有判定。
		if removalCapApplies(reason) {
			removedCount := len(normalizeModelNames(lockedSettings.ModelMissingRemovedModels))
			if removedCount >= capValue {
				cappedRemovedCount = removedCount
				return errModelRemovalCapped
			}
		}
		nextModels := subtractModelNames(localModels, []string{originModel})
		locked.Models = strings.Join(nextModels, ",")
		remainingModels = len(nextModels)
		now := common.GetTimestamp()
		lockedSettings.ModelMissingRemovedModels = mergeModelNames(lockedSettings.ModelMissingRemovedModels, []string{originModel})
		lockedSettings.ModelRemovalReasons = setModelRemovalReason(lockedSettings.ModelRemovalReasons, originModel, reason)
		lockedSettings.ModelMissingRemovedAt = now
		// 同一渠道可能混有两种原因移出的模型，而复核时间只有一个字段：取更早的那个，
		// 避免限流模型（180s）被缺失模型（600s）的长周期拖慢加回。
		nextRecheckAt := now + recheckIntervalSecondsFor(reason)
		if lockedSettings.ModelMissingRecheckAt > now && lockedSettings.ModelMissingRecheckAt < nextRecheckAt {
			nextRecheckAt = lockedSettings.ModelMissingRecheckAt
		}
		lockedSettings.ModelMissingRecheckAt = nextRecheckAt
		notifyRecheckAt = nextRecheckAt
		locked.SetOtherSettings(lockedSettings)
		if updateErr := tx.Model(&model.Channel{}).Where("id = ?", channelID).Updates(map[string]interface{}{
			"models":   locked.Models,
			"settings": locked.OtherSettings,
		}).Error; updateErr != nil {
			return updateErr
		}
		return locked.UpdateAbilities(tx)
	})

	switch {
	case txErr == nil:
		refreshChannelRuntimeCache()
		common.SysLog(fmt.Sprintf("model removal done: reason=%s channel_id=%d channel_name=%s removed_model=%s upstream_model=%s remaining_models=%d recheck_after=%ds",
			reason, channelID, channelName, originModel, upstreamTarget, remainingModels, recheckIntervalSecondsFor(reason)))
		notifyModelRemoval(channelID, channelName, originModel, reason, remainingModels, notifyRecheckAt)
	case errors.Is(txErr, errModelMissingChannelUnavailable):
		// 渠道被并发禁用/开关关闭，无需回退禁用
	case errors.Is(txErr, errModelRemovalCapped):
		handleModelRemovalCapReached(channelID, channelName, originModel, reason, cappedRemovedCount, capValue, channelError, err)
	case errors.Is(txErr, errModelMissingLastModel):
		common.SysLog(fmt.Sprintf("model removal skipped, last model in channel: reason=%s channel_id=%d channel_name=%s model=%s", reason, channelID, channelName, originModel))
		fallbackDisableChannelForModelMissing(channelError, err)
	default:
		common.SysError(fmt.Sprintf("model removal transaction failed: reason=%s channel_id=%d model=%s err=%v", reason, channelID, originModel, txErr))
		fallbackDisableChannelForModelMissing(channelError, err)
	}
}

// setModelRemovalReason 往原因表写一条。map 可能为 nil（存量渠道没有这个键），按需初始化。
func setModelRemovalReason(reasons map[string]string, modelName string, reason modelRemovalReason) map[string]string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return reasons
	}
	if reasons == nil {
		reasons = make(map[string]string, 1)
	}
	reasons[modelName] = string(reason)
	return reasons
}

// pruneModelRemovalReasons 原因表只保留仍在追踪清单里的模型，跟着追踪清单同生命周期。
// 返回 nil（而不是空 map）以便 omitempty 生效，settings JSON 不留空对象。
func pruneModelRemovalReasons(reasons map[string]string, watched []string) map[string]string {
	if len(reasons) == 0 || len(watched) == 0 {
		return nil
	}
	pruned := make(map[string]string, len(watched))
	for _, modelName := range watched {
		if reason, ok := reasons[modelName]; ok {
			pruned[modelName] = reason
		}
	}
	if len(pruned) == 0 {
		return nil
	}
	return pruned
}

// notifyModelRemoval 摘除通知。默认关（ModelRemovalNotifyEnabled=false）时一封都不发，
// 与上线前完全一致——改造前只有停用渠道会发通知，摘除路径从来不发。
func notifyModelRemoval(channelID int, channelName string, modelName string,
	reason modelRemovalReason, remainingModels int, recheckAt int64) {
	if !operation_setting.ModelRemovalNotifyEnabled {
		return
	}
	recheckText := "未安排（追踪已清空）"
	if recheckAt > 0 {
		recheckText = time.Unix(recheckAt, 0).Format("2006-01-02 15:04:05")
	}
	subject := fmt.Sprintf("通道「%s」（#%d）已移出模型 %s", channelName, channelID, modelName)
	content := fmt.Sprintf(
		"通道「%s」（#%d）已移出模型「%s」。\n触发原因：%s\n渠道剩余模型数：%d\n下次复核时间：%s\n\n"+
			"该渠道仍处于「启用」状态，只是这一个模型不再参与路由；复核时若模型回到上游会自动加回。",
		channelName, channelID, modelName, describeModelRemovalReason(reason), remainingModels, recheckText)
	service.NotifyRootUser(fmt.Sprintf("%s_%d_model_removal", dto.NotifyTypeChannelUpdate, channelID), subject, content)
}

// describeModelRemovalReason 把内部 reason 值翻译成通知里能读懂的说法。
func describeModelRemovalReason(reason modelRemovalReason) string {
	switch reason {
	case modelRemovalReasonMissing:
		return "上游已无该模型（缺失，已通过上游模型列表核实）"
	case modelRemovalReasonRateLimit:
		return "上游限流（429）"
	case modelRemovalReasonForbidden:
		return "上游拒绝该模型（权限不足 / 不支持）"
	default:
		return string(reason)
	}
}

// classifyModelMissingWatchedModels 复核时把待复核模型分为「已回归（加回）」与「仍缺失（继续追踪）」。
func classifyModelMissingWatchedModels(removedModels []string, upstreamSet map[string]struct{}, modelMapping map[string]string) (restored []string, stillMissing []string) {
	for _, modelName := range normalizeModelNames(removedModels) {
		target := modelName
		if mapped, ok := modelMapping[modelName]; ok {
			target = mapped
		}
		if _, exists := upstreamSet[target]; exists {
			restored = append(restored, modelName)
		} else {
			stillMissing = append(stillMissing, modelName)
		}
	}
	return normalizeModelNames(restored), normalizeModelNames(stillMissing)
}

func recheckChannelModelMissingModels(ctx context.Context, channel *model.Channel) {
	// 上游拉取放在锁外；空列表视为降级不可信，与拉取失败同等顺延。
	upstreamModels, fetchErr := fetchChannelUpstreamModelIDs(ctx, channel)
	fetchOk := fetchErr == nil && len(upstreamModels) > 0

	refreshNeeded := false
	// 行锁下读-改-写，与移出路径互斥，避免旧快照覆盖并发变更。
	txErr := model.DB.Transaction(func(tx *gorm.DB) error {
		locked := &model.Channel{}
		if lockErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(locked, "id = ?", channel.Id).Error; lockErr != nil {
			return lockErr
		}
		if locked.Status != common.ChannelStatusEnabled {
			return errModelMissingChannelUnavailable
		}
		settings := locked.GetOtherSettings()
		if settings.ModelMissingRecheckAt <= 0 || len(settings.ModelMissingRemovedModels) == 0 {
			return errModelMissingWatchCleared
		}
		now := common.GetTimestamp()

		// 超过保留上限仍未回归：放弃追踪，避免僵尸条目被无限滚动
		if settings.ModelMissingRemovedAt > 0 && now-settings.ModelMissingRemovedAt > modelMissingRemovalWatchMaxHoldSeconds {
			common.SysLog(fmt.Sprintf("model missing watch expired: channel_id=%d models=%s", channel.Id, strings.Join(settings.ModelMissingRemovedModels, ",")))
			settings.ModelMissingRemovedModels = nil
			settings.ModelRemovalReasons = nil
			settings.ModelMissingRecheckAt = 0
			settings.ModelMissingRemovedAt = 0
			locked.SetOtherSettings(settings)
			return tx.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("settings", locked.OtherSettings).Error
		}

		if !fetchOk {
			// 拉不到上游列表或列表为空（降级不可信）：顺延一轮再试
			settings.ModelMissingRecheckAt = now + modelMissingRecheckIntervalSeconds()
			locked.SetOtherSettings(settings)
			if deferErr := tx.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("settings", locked.OtherSettings).Error; deferErr != nil {
				return deferErr
			}
			common.SysLog(fmt.Sprintf("model missing recheck deferred: channel_id=%d err=%v upstream_count=%d", channel.Id, fetchErr, len(upstreamModels)))
			return nil
		}

		upstreamSet := make(map[string]struct{}, len(upstreamModels))
		for _, m := range upstreamModels {
			upstreamSet[m] = struct{}{}
		}
		restored, stillMissing := classifyModelMissingWatchedModels(settings.ModelMissingRemovedModels, upstreamSet, normalizeChannelModelMapping(locked))
		// 原始原因表留一份：下面的 abilities 失败重试分支会把 restored 塞回追踪清单，
		// 届时要连原因一起塞回，否则重试轮的原因会永久丢成 unknown。
		originalReasons := settings.ModelRemovalReasons

		modelsChanged := false
		if len(restored) > 0 {
			originModels := normalizeModelNames(locked.GetModels())
			mergedModels := mergeModelNames(originModels, restored)
			if len(mergedModels) > len(originModels) {
				locked.Models = strings.Join(mergedModels, ",")
				modelsChanged = true
			}
			common.SysLog(fmt.Sprintf("model missing recheck restored models: channel_id=%d channel_name=%s models=%s", channel.Id, channel.Name, strings.Join(restored, ",")))
		}

		settings.ModelMissingRemovedModels = stillMissing
		settings.ModelRemovalReasons = pruneModelRemovalReasons(originalReasons, stillMissing)
		if len(stillMissing) == 0 {
			settings.ModelMissingRecheckAt = 0
			settings.ModelMissingRemovedAt = 0
		} else {
			settings.ModelMissingRecheckAt = now + modelMissingRecheckIntervalSeconds()
		}
		locked.SetOtherSettings(settings)
		updates := map[string]interface{}{"settings": locked.OtherSettings}
		if modelsChanged {
			updates["models"] = locked.Models
		}
		if updateErr := tx.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(updates).Error; updateErr != nil {
			return updateErr
		}

		// restored 非空即重建 abilities（同时覆盖上一轮 abilities 失败的重试轮，加回幂等）
		if len(restored) > 0 {
			if abilityErr := locked.UpdateAbilities(tx); abilityErr != nil {
				// 保留追踪清单（含已加回模型）下一轮重试；本轮状态正常提交。
				common.SysError(fmt.Sprintf("model missing recheck update abilities failed, will retry next round: channel_id=%d err=%v", channel.Id, abilityErr))
				retryWatch := mergeModelNames(stillMissing, restored)
				settings.ModelMissingRemovedModels = retryWatch
				settings.ModelRemovalReasons = pruneModelRemovalReasons(originalReasons, retryWatch)
				settings.ModelMissingRecheckAt = common.GetTimestamp() + modelMissingRecheckIntervalSeconds()
				locked.SetOtherSettings(settings)
				return tx.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("settings", locked.OtherSettings).Error
			}
			refreshNeeded = true
		}
		if modelsChanged {
			refreshNeeded = true
		}
		return nil
	})

	switch {
	case txErr == nil:
		if refreshNeeded {
			refreshChannelRuntimeCache()
		}
	case errors.Is(txErr, errModelMissingChannelUnavailable), errors.Is(txErr, errModelMissingWatchCleared):
		// 渠道状态变化/追踪被并发清除，无事可做
	default:
		common.SysError(fmt.Sprintf("model missing recheck failed: channel_id=%d err=%v", channel.Id, txErr))
	}
}

func runModelMissingRecheckTaskOnce(ctx context.Context) {
	if !modelMissingRecheckTaskRunning.CompareAndSwap(false, true) {
		return
	}
	defer modelMissingRecheckTaskRunning.Store(false)

	now := common.GetTimestamp()
	lastID := 0
	for {
		if ctx.Err() != nil {
			break
		}
		var channels []*model.Channel
		query := model.DB.
			Select(channelUpstreamModelUpdateSelectFields).
			Where("status = ?", common.ChannelStatusEnabled).
			Where("settings LIKE ?", modelMissingRecheckSettingsPrefilterPattern).
			Order("id asc").
			Limit(modelMissingRecheckTaskBatchSize)
		if lastID > 0 {
			query = query.Where("id > ?", lastID)
		}
		if err := query.Find(&channels).Error; err != nil {
			common.SysLog(fmt.Sprintf("model missing recheck task query failed: %v", err))
			break
		}
		if len(channels) == 0 {
			break
		}
		lastID = channels[len(channels)-1].Id

		for _, channel := range channels {
			if ctx.Err() != nil {
				break
			}
			if channel == nil {
				continue
			}
			settings := channel.GetOtherSettings()
			if settings.ModelMissingRecheckAt <= 0 || settings.ModelMissingRecheckAt > now || len(settings.ModelMissingRemovedModels) == 0 {
				continue
			}
			recheckChannelModelMissingModels(ctx, channel)
		}

		if len(channels) < modelMissingRecheckTaskBatchSize {
			break
		}
	}
}

func StartChannelModelMissingRecheckTask() error {
	var startErr error
	modelMissingRecheckTaskOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		if !common.GetEnvOrDefaultBool("MODEL_MISSING_RECHECK_TASK_ENABLED", true) {
			common.SysLog("model missing recheck task disabled by MODEL_MISSING_RECHECK_TASK_ENABLED")
			return
		}
		intervalSeconds := common.GetEnvOrDefault("MODEL_MISSING_RECHECK_TASK_TICK_SECONDS", modelMissingRecheckTaskDefaultIntervalSec)
		if intervalSeconds < 10 {
			intervalSeconds = modelMissingRecheckTaskDefaultIntervalSec
		}
		interval := time.Duration(intervalSeconds) * time.Second

		startErr = backgroundtask.Start("channel-model-missing-recheck", func(ctx context.Context) {
			common.SysLog(fmt.Sprintf("model missing recheck task started: interval=%s", interval))
			backgroundtask.RunPeriodic(ctx, interval, true, func() {
				runModelMissingRecheckTaskOnce(ctx)
			})
		})
	})
	return startErr
}
