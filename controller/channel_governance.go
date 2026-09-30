package controller

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// 渠道治理的两个只读观测接口：
//
//  1. GET  /api/channel/governance/distribution — 治理状态分布（各处置烈度下有多少渠道）
//  2. POST /api/channel/governance/dry_run      — 判定试算（这条错误会触发哪些处置）
//
// 两个接口都不写库、不改渠道状态、不触发任何处置、不发通知。
//
// 与 GetChannelStatistics（controller/channel_statistics.go）刻意分开：那是**用量口径**
// （额度 / 调用量 / RPM），实现要扫 logs 表，prod 9M+ 行 / 139 渠道下超过 24h 的窗口单查询
// 即达分钟级。治理分布只查 channels 表（百行级），复用那条重查询等于给一个开页面就调的
// 接口挂上分钟级成本。

// ── 降级档位 ──

const (
	// governanceDegradeBucketHealthy 等等：档位 key，前端据此取文案，边界值走 min_level/max_level。
	governanceDegradeBucketHealthy  = "healthy"
	governanceDegradeBucketLight    = "light"
	governanceDegradeBucketModerate = "moderate"
	governanceDegradeBucketDeep     = "deep"
	governanceDegradeBucketMax      = "max"

	// governanceDefaultMaxDegradeLevel 与 ChannelHealthConfig.Normalize 的兜底值一致。
	governanceDefaultMaxDegradeLevel = 10
)

// GovernanceDegradeBucket 一个降级档位。
// 边界随 channel_health_setting.max_degrade_level 变，所以必须把 min/max 一起回给前端：
// 前端拿边界渲染标签（"L1–L3"），不自己猜档位宽度，配置一改标签跟着变。
type GovernanceDegradeBucket struct {
	Key      string `json:"key"`
	MinLevel int    `json:"min_level"`
	MaxLevel int    `json:"max_level"`
	Count    int    `json:"count"`
}

// buildDegradeBuckets 按封顶级别切档位。
//
// 切法：L0 单独一档（健康）；封顶级别单独一档（触顶）；中间 1..max-1 均分成轻/中/深三档，
// 余数摊到靠前的档位，保证各档宽度差不超过 1 且恰好覆盖整个区间。
// max <= 1 时中间区间为空，只剩健康与触顶两档。
func buildDegradeBuckets(maxLevel int) []GovernanceDegradeBucket {
	if maxLevel < 1 {
		maxLevel = governanceDefaultMaxDegradeLevel
	}
	buckets := []GovernanceDegradeBucket{
		{Key: governanceDegradeBucketHealthy, MinLevel: 0, MaxLevel: 0},
	}
	span := maxLevel - 1
	if span > 0 {
		bandKeys := []string{
			governanceDegradeBucketLight,
			governanceDegradeBucketModerate,
			governanceDegradeBucketDeep,
		}
		if span < len(bandKeys) {
			bandKeys = bandKeys[:span]
		}
		bands := len(bandKeys)
		start := 1
		for i, key := range bandKeys {
			width := span / bands
			if i < span%bands {
				width++
			}
			buckets = append(buckets, GovernanceDegradeBucket{
				Key:      key,
				MinLevel: start,
				MaxLevel: start + width - 1,
			})
			start += width
		}
	}
	buckets = append(buckets, GovernanceDegradeBucket{
		Key: governanceDegradeBucketMax, MinLevel: maxLevel, MaxLevel: maxLevel,
	})
	return buckets
}

// assignDegradeBucket 把一个 degrade_level 落到档位下标。
// 超出封顶级别的存量行（管理员把 max_degrade_level 调小后留下的）一律归入触顶档，
// 而不是静默丢掉——丢掉会让各档之和小于渠道总数，链路图上的节点计数就不守恒了。
func assignDegradeBucket(buckets []GovernanceDegradeBucket, level int) int {
	if len(buckets) == 0 {
		return -1
	}
	if level <= 0 {
		return 0
	}
	last := len(buckets) - 1
	if level >= buckets[last].MinLevel {
		return last
	}
	for i, bucket := range buckets {
		if level >= bucket.MinLevel && level <= bucket.MaxLevel {
			return i
		}
	}
	return last
}

// ── 分布响应 ──

// GovernanceRemovalStats 模型级摘除的聚合。
type GovernanceRemovalStats struct {
	// TotalModels 当前被摘除的模型总数（跨渠道累加）。
	TotalModels int `json:"total_models"`
	// Channels 有摘除记录的渠道数。
	Channels int `json:"channels"`
	// ByReason 按触发原因分：model_missing / rate_limit / forbidden / unknown。
	// unknown = 存量记录：原因表（settings.model_removal_reasons）是本次新增的，
	// 之前摘掉的模型只记了模型名，原因无从追溯，只能计入 unknown 而不是硬塞一个默认值。
	ByReason map[string]int `json:"by_reason"`
	// UnknownReasonModels 原因未知的模型数（= ByReason["unknown"]，单独抬出来便于前端提示）。
	UnknownReasonModels int `json:"unknown_reason_models"`
	// NextRecheckAt 所有渠道里最早的一次复核时间（unix 秒）；0=当前无待复核。
	// 复核时间是渠道级字段（settings.model_missing_recheck_at），不是每模型一个。
	NextRecheckAt int64 `json:"next_recheck_at"`
}

// ChannelGovernanceDistribution 治理状态分布。
type ChannelGovernanceDistribution struct {
	GeneratedAt   int64 `json:"generated_at"`
	TotalChannels int   `json:"total_channels"`
	// Enabled status = ChannelStatusEnabled 的渠道数。
	Enabled int `json:"enabled"`
	// MaxDegradeLevel 当前生效的封顶级别，前端用它判断档位标签。
	MaxDegradeLevel int `json:"max_degrade_level"`
	// DegradeBuckets 按 degrade_level 分档。统计对象是**全部**渠道（含已停用的：
	// 停用不会清掉 degrade_level，降级快照是持久化的），各档之和恒等于 TotalChannels。
	DegradeBuckets []GovernanceDegradeBucket `json:"degrade_buckets"`
	// DegradedChannels degrade_level >= 1 的渠道数。
	DegradedChannels int `json:"degraded_channels"`
	// AutoDisabled status = ChannelStatusAutoDisabled 的渠道数。
	AutoDisabled int `json:"auto_disabled"`
	// PermanentDisabled 上面这批里 permanent_disabled = 1 的（反弹触顶锁死，恢复探活跳过）。
	// 是 AutoDisabled 的**子集**，不要与它相加。
	PermanentDisabled int `json:"permanent_disabled"`
	// ManuallyDisabled status = ChannelStatusManuallyDisabled 的渠道数，与自动停用分开计。
	ManuallyDisabled int `json:"manually_disabled"`
	// VerifyDisabled verify_disabled = 1 的渠道数：被测评低分禁用。
	// testAllChannels 见这一位为 1 直接 continue，所以「巡检测通启用」那条恢复路径对它不通，
	// 恢复只能等测评分数回升。这一位与 status 正交地统计，但实践中它必然伴随
	// status = AutoDisabled（见 controller/channel_verify_schedule.go 的置位与复位判定），
	// 因此它是 AutoDisabled 的子集，不要与它相加。
	VerifyDisabled int `json:"verify_disabled"`
	// RemovedModels 模型级摘除聚合。
	RemovedModels GovernanceRemovalStats `json:"removed_models"`
}

// GetChannelGovernanceDistribution 治理状态分布（只读）。
func GetChannelGovernanceDistribution(c *gin.Context) {
	ctx := c.Request.Context()

	rows, err := model.GetChannelGovernanceRows(ctx)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	cfg := operation_setting.GetChannelHealthConfig()
	buckets := buildDegradeBuckets(cfg.MaxDegradeLevel)

	result := ChannelGovernanceDistribution{
		GeneratedAt:     common.GetTimestamp(),
		TotalChannels:   len(rows),
		MaxDegradeLevel: cfg.MaxDegradeLevel,
		DegradeBuckets:  buckets,
	}
	if result.MaxDegradeLevel < 1 {
		result.MaxDegradeLevel = governanceDefaultMaxDegradeLevel
	}

	for _, row := range rows {
		level := common.DerefIntOr(row.DegradeLevel, 0)
		if idx := assignDegradeBucket(result.DegradeBuckets, level); idx >= 0 {
			result.DegradeBuckets[idx].Count++
		}
		if level >= 1 {
			result.DegradedChannels++
		}
		switch row.Status {
		case common.ChannelStatusEnabled:
			result.Enabled++
		case common.ChannelStatusAutoDisabled:
			result.AutoDisabled++
			if common.DerefIntOr(row.PermanentDisabled, 0) == 1 {
				result.PermanentDisabled++
			}
		case common.ChannelStatusManuallyDisabled:
			result.ManuallyDisabled++
		}
		if common.DerefIntOr(row.VerifyDisabled, 0) == 1 {
			result.VerifyDisabled++
		}
	}

	removalStats, err := collectRemovalStats(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result.RemovedModels = removalStats

	common.ApiSuccess(c, result)
}

// collectRemovalStats 聚合模型摘除清单。
func collectRemovalStats(c *gin.Context) (GovernanceRemovalStats, error) {
	stats := GovernanceRemovalStats{
		ByReason: map[string]int{
			string(modelRemovalReasonMissing):   0,
			string(modelRemovalReasonForbidden): 0,
			string(modelRemovalReasonRateLimit): 0,
			governanceRemovalReasonUnknown:      0,
		},
	}
	rows, err := model.GetChannelsWithRemovedModels(c.Request.Context())
	if err != nil {
		return stats, err
	}
	for _, row := range rows {
		settings := dto.ChannelOtherSettings{}
		if row.Settings != "" {
			if unmarshalErr := common.UnmarshalJsonStr(row.Settings, &settings); unmarshalErr != nil {
				// 单个渠道 settings 坏了不该让整张分布图 500：记日志跳过，其余渠道照常统计。
				common.SysLog(fmt.Sprintf("governance distribution: failed to parse channel settings, channel_id=%d err=%v",
					row.Id, unmarshalErr))
				continue
			}
		}
		removed := settings.ModelMissingRemovedModels
		if len(removed) == 0 {
			// LIKE 预筛只保证 settings 里出现过这个键，键值可能已被清空成 []。
			continue
		}
		stats.Channels++
		stats.TotalModels += len(removed)
		for _, modelName := range removed {
			reason := settings.ModelRemovalReasons[modelName]
			if reason == "" {
				reason = governanceRemovalReasonUnknown
			}
			stats.ByReason[reason]++
		}
		if settings.ModelMissingRecheckAt > 0 &&
			(stats.NextRecheckAt == 0 || settings.ModelMissingRecheckAt < stats.NextRecheckAt) {
			stats.NextRecheckAt = settings.ModelMissingRecheckAt
		}
	}
	stats.UnknownReasonModels = stats.ByReason[governanceRemovalReasonUnknown]
	return stats, nil
}

const governanceRemovalReasonUnknown = "unknown"

// ── 判定试算 ──

// GovernanceDryRunRequest 试算入参：一次上游错误的状态码 + 错误文案（+ 可选模型名）。
type GovernanceDryRunRequest struct {
	StatusCode   int    `json:"status_code"`
	ErrorMessage string `json:"error_message"`
	Model        string `json:"model"`
}

// GovernanceDryRunVerdicts 三条模型级摘除路径的**原始分类**结果，不含各自的开关。
type GovernanceDryRunVerdicts struct {
	ModelMissing bool `json:"model_missing"`
	Forbidden    bool `json:"forbidden"`
	RateLimit    bool `json:"rate_limit"`
}

// GovernanceDryRunRemoval 模型级摘除的最终判定（含开关）。
type GovernanceDryRunRemoval struct {
	// Matched 是否会走模型级摘除（= classifyModelRemovalReason 的第二个返回值）。
	Matched bool `json:"matched"`
	// Reason 命中的路径，未命中为空串。
	Reason string `json:"reason"`
	// RequiresUpstreamVerify 摘除前是否还要做「模型是否真的不在上游列表」核实。
	// 只有缺失路径要；限流/权限路径模型必然仍被上游列出，走核实就永远摘不掉。
	RequiresUpstreamVerify bool `json:"requires_upstream_verify"`
	// RecheckIntervalSeconds 摘除后的复核间隔（秒），随命中路径不同。
	RecheckIntervalSeconds int64 `json:"recheck_interval_seconds"`
	// CapApplies 该路径是否受「同渠道累计摘除上限」约束。
	CapApplies bool `json:"cap_applies"`
	// CapValue 当前生效的上限值。
	CapValue int `json:"cap_value"`
	// CapAction 触顶动作：alert_only / disable_channel。
	CapAction string `json:"cap_action"`
	// ModelProvided 有没有给模型名。缺失路径要求错误文案里能对上模型名，
	// 不给模型名时该路径必然判不出来——不提示的话界面上像是规则没配对。
	ModelProvided bool `json:"model_provided"`
}

// GovernanceDryRunDisable 停整渠道的判定。
type GovernanceDryRunDisable struct {
	// WouldDisableChannel service.ShouldDisableChannel 的原始判定。
	WouldDisableChannel bool `json:"would_disable_channel"`
	// AutomaticDisableChannelEnabled 全局「自动禁用渠道」总开关（关掉时上一项恒为 false）。
	AutomaticDisableChannelEnabled bool `json:"automatic_disable_channel_enabled"`
	// ShortCircuitedByRemoval 摘除是否短路了停渠道。真实链路是
	// `if !handledModelMissing && ShouldDisableChannel(err) && AutoBan { DisableChannel }`，
	// 所以摘除接管成功时这次错误不会立刻停渠道；但摘除最终没能落地（核实不通过 / 只剩最后
	// 一个模型 / 事务失败）时会回退到同一个判定，WouldDisableChannel 那时才生效。
	ShortCircuitedByRemoval bool `json:"short_circuited_by_removal"`
	// AutoBanNotEvaluated 恒为 true：渠道级 auto_ban 是渠道属性，试算只有状态码和文案，
	// 没有具体渠道可查，因此这一道闸门未纳入判定。
	AutoBanNotEvaluated bool `json:"auto_ban_not_evaluated"`
}

// GovernanceDryRunStreak 降级连击的判定。
type GovernanceDryRunStreak struct {
	// Countable 这条错误的分类是否计入渠道错误连击。
	Countable bool `json:"countable"`
	// ChannelHealthEnabled 健康度总开关。关闭时 RecordChannelResult 开头就 return，
	// 连成功都不再累计，所以 Countable 为真也不会真的推进连击。
	ChannelHealthEnabled bool `json:"channel_health_enabled"`
	// Effective Countable && ChannelHealthEnabled，即此刻是否真的会推进连击。
	Effective bool `json:"effective"`
}

// GovernanceDryRunRetry 重试判定。
type GovernanceDryRunRetry struct {
	// StatusCodeRetryable 只覆盖状态码这道闸门（operation_setting.ShouldRetryByStatusCode）。
	// 真实 shouldRetry 还会看重试次数余量、亲和性失败标记、重试作用域、是否指定渠道等
	// 请求上下文，这些不由状态码和文案决定，因此不在试算范围内。
	StatusCodeRetryable bool `json:"status_code_retryable"`
}

// GovernanceDryRunResult 试算结果。
type GovernanceDryRunResult struct {
	StatusCode     int                      `json:"status_code"`
	Model          string                   `json:"model"`
	Verdicts       GovernanceDryRunVerdicts `json:"verdicts"`
	Removal        GovernanceDryRunRemoval  `json:"removal"`
	ChannelDisable GovernanceDryRunDisable  `json:"channel_disable"`
	DegradeStreak  GovernanceDryRunStreak   `json:"degrade_streak"`
	Retry          GovernanceDryRunRetry    `json:"retry"`
}

// PostChannelGovernanceDryRun 判定试算（只读）。
//
// 零副作用，逐条说明：
//   - 不写库：全程没有任何 DB 语句，连读都没有（判定只依赖内存里的配置）。
//   - 不改渠道状态：不调 DisableChannel / UpdateChannelStatus / demoteTo。
//   - 不触发处置：**不调** tryHandleChannelModelMissing——那个函数会占用防抖冷却窗口
//     （modelMissingRemovalCooldownOk 会写 lastTrigger map）并提交后台摘除任务；
//     这里只调它内部那几个纯判定函数。
//   - 不推进连击：不调 RecordChannelResult（会写 Redis streak），只调
//     service.ShouldCountTowardChannelStreak，那是 isCountableError 的纯判定转调。
//   - 不发通知：不调 NotifyRootUser。
//
// 判定一律复用真实处置路径调用的同一批函数，不在此处另写一套规则：判定语义一旦分裂，
// 界面说「会摘模型」而实际停了整渠道，比没有这个功能更糟。
func PostChannelGovernanceDryRun(c *gin.Context) {
	var req GovernanceDryRunRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiErrorMsg(c, "invalid request body: "+err.Error())
		return
	}
	if req.StatusCode < 100 || req.StatusCode > 599 {
		common.ApiErrorMsg(c, "status_code must be between 100 and 599")
		return
	}

	originModel := strings.TrimSpace(req.Model)
	// 构造与 relay 报错路径同形的错误对象：上游非 2xx 走的就是 ErrorCodeBadResponseStatusCode。
	// 不用结构体字面量——errorCode/skipRetry 在 types 包外不可设置，字面量会静默绕开
	// IsChannelError / ErrorCodeModelNotFound 这两道判定前置。
	apiError := types.NewErrorWithStatusCode(
		errors.New(req.ErrorMessage), types.ErrorCodeBadResponseStatusCode, req.StatusCode)

	reason, matched := classifyModelRemovalReason(apiError, originModel)
	healthCfg := operation_setting.GetChannelHealthConfig()
	countable := service.ShouldCountTowardChannelStreak(apiError)

	result := GovernanceDryRunResult{
		StatusCode: req.StatusCode,
		Model:      originModel,
		Verdicts: GovernanceDryRunVerdicts{
			ModelMissing: isUpstreamModelMissingError(apiError, originModel),
			Forbidden:    isUpstreamForbiddenError(apiError),
			RateLimit:    isUpstreamRateLimitError(apiError),
		},
		Removal: GovernanceDryRunRemoval{
			Matched:                matched,
			Reason:                 string(reason),
			RequiresUpstreamVerify: matched && reason == modelRemovalReasonMissing,
			CapValue:               modelRemovalMaxRemovedPerChannel(),
			CapAction:              operation_setting.ModelRemovalCapAction,
			ModelProvided:          originModel != "",
		},
		ChannelDisable: GovernanceDryRunDisable{
			WouldDisableChannel:            service.ShouldDisableChannel(apiError),
			AutomaticDisableChannelEnabled: common.AutomaticDisableChannelEnabled,
			ShortCircuitedByRemoval:        matched,
			AutoBanNotEvaluated:            true,
		},
		DegradeStreak: GovernanceDryRunStreak{
			Countable:            countable,
			ChannelHealthEnabled: healthCfg.Enabled,
			Effective:            countable && healthCfg.Enabled,
		},
		Retry: GovernanceDryRunRetry{
			StatusCodeRetryable: operation_setting.ShouldRetryByStatusCode(req.StatusCode),
		},
	}
	if matched {
		result.Removal.RecheckIntervalSeconds = recheckIntervalSecondsFor(reason)
		result.Removal.CapApplies = removalCapApplies(reason)
	}

	common.ApiSuccess(c, result)
}
