package service

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

// 统一重试范围（渠道级 RetryStrategy 与 Key 级 relay_retry_policy 归一后的有效档）。
// 取两轴最保守（min）；任一为 inherit/system 表示该轴不约束。
const (
	retryScopeUnconstrained  = -1 // inherit/system：不约束，跟随全局/另一轴
	RetryScopeNoCrossChannel = 0  // cost_guard / disabled：失败不跨渠道
	RetryScopeSameDomain     = 1  // same_domain / cache_domain_only：只在同 cache_domain 内
	RetryScopeCrossChannel   = 2  // cross_channel / allow_cross_channel：任意渠道
)

func channelStrategyToScope(s int) int {
	switch s {
	case model.RetryStrategyCostGuard:
		return RetryScopeNoCrossChannel
	case model.RetryStrategySameDomain:
		return RetryScopeSameDomain
	case model.RetryStrategyCrossChannel:
		return RetryScopeCrossChannel
	default: // RetryStrategyInherit
		return retryScopeUnconstrained
	}
}

func tokenPolicyToScope(policy string) int {
	switch strings.TrimSpace(policy) {
	case model.TokenRelayRetryPolicyDisabled:
		return RetryScopeNoCrossChannel
	case model.TokenRelayRetryPolicyCacheDomainOnly:
		return RetryScopeSameDomain
	case model.TokenRelayRetryPolicyAllowCrossChannel:
		return RetryScopeCrossChannel
	default: // system
		return retryScopeUnconstrained
	}
}

// EffectiveRetryScope 合并渠道级策略（origin）与 Key 级策略，取最保守者（min）。
// 二者皆 inherit/system 时返回 unconstrained（维持全局默认行为）。
// 显式的更宽松档不能放开另一轴的收紧（最保守者胜）。
func EffectiveRetryScope(c *gin.Context) int {
	ch := channelStrategyToScope(common.GetContextKeyInt(c, constant.ContextKeyOriginRetryStrategy))
	tk := tokenPolicyToScope(common.GetContextKeyString(c, constant.ContextKeyTokenRelayRetryPolicy))
	switch {
	case ch == retryScopeUnconstrained:
		return tk
	case tk == retryScopeUnconstrained:
		return ch
	case ch < tk:
		return ch
	default:
		return tk
	}
}

type RetryParam struct {
	Ctx                *gin.Context
	TokenGroup         string
	ModelName          string
	RequestPath        string
	Retry              *int
	resetNextTry       bool
	ExcludedChannelIDs []int // 本次 relay 已失败的 channel id，retry 选渠道时排除
	// origin（retry=0 渠道）的缓存域：有效档为 same_domain 时 relay 选渠道只在该域内。
	// 重试档位本身由 EffectiveRetryScope 直接读 context（ContextKeyOriginRetryStrategy），不在本结构体上。
	OriginCacheDomain string
}

// cacheDomainFilter 返回有效档为 same_domain 时用于过滤候选渠道的缓存域；其它档返回空（不过滤）。
// 有效档由渠道级 RetryStrategy 与 Key 级 relay_retry_policy 合并得到，故 cache_domain_only 的 Key 也会触发过滤。
func (p *RetryParam) cacheDomainFilter() string {
	if EffectiveRetryScope(p.Ctx) == RetryScopeSameDomain {
		return p.OriginCacheDomain
	}
	return ""
}

func (p *RetryParam) ExcludeChannel(channelID int) {
	for _, id := range p.ExcludedChannelIDs {
		if id == channelID {
			return
		}
	}
	p.ExcludedChannelIDs = append(p.ExcludedChannelIDs, channelID)
}

func (p *RetryParam) IsChannelExcluded(channelID int) bool {
	for _, id := range p.ExcludedChannelIDs {
		if id == channelID {
			return true
		}
	}
	return false
}

func (p *RetryParam) GetRetry() int {
	if p.Retry == nil {
		return 0
	}
	return *p.Retry
}

func (p *RetryParam) SetRetry(retry int) {
	p.Retry = &retry
}

func (p *RetryParam) IncreaseRetry() {
	if p.resetNextTry {
		p.resetNextTry = false
		return
	}
	if p.Retry == nil {
		p.Retry = new(int)
	}
	*p.Retry++
}

func (p *RetryParam) ResetRetryNextTry() {
	p.resetNextTry = true
}

// CacheGetRandomSatisfiedChannel tries to get a random channel that satisfies the requirements.
// 尝试获取一个满足要求的随机渠道。
//
// For "auto" tokenGroup with cross-group Retry enabled:
// 对于启用了跨分组重试的 "auto" tokenGroup：
//
//   - Each group will exhaust all its priorities before moving to the next group.
//     每个分组会用完所有优先级后才会切换到下一个分组。
//
//   - Uses ContextKeyAutoGroupIndex to track current group index.
//     使用 ContextKeyAutoGroupIndex 跟踪当前分组索引。
//
//   - Uses ContextKeyAutoGroupRetryIndex to track the global Retry count when current group started.
//     使用 ContextKeyAutoGroupRetryIndex 跟踪当前分组开始时的全局重试次数。
//
//   - priorityRetry = Retry - startRetryIndex, represents the priority level within current group.
//     priorityRetry = Retry - startRetryIndex，表示当前分组内的优先级级别。
//
//   - When GetRandomSatisfiedChannel returns nil (priorities exhausted), moves to next group.
//     当 GetRandomSatisfiedChannel 返回 nil（优先级用完）时，切换到下一个分组。
//
// Example flow (2 groups, each with 2 priorities, RetryTimes=3):
// 示例流程（2个分组，每个有2个优先级，RetryTimes=3）：
//
//	Retry=0: GroupA, priority0 (startRetryIndex=0, priorityRetry=0)
//	         分组A, 优先级0
//
//	Retry=1: GroupA, priority1 (startRetryIndex=0, priorityRetry=1)
//	         分组A, 优先级1
//
//	Retry=2: GroupA exhausted → GroupB, priority0 (startRetryIndex=2, priorityRetry=0)
//	         分组A用完 → 分组B, 优先级0
//
//	Retry=3: GroupB, priority1 (startRetryIndex=2, priorityRetry=1)
//	         分组B, 优先级1
func CacheGetRandomSatisfiedChannel(param *RetryParam) (*model.Channel, string, error) {
	var channel *model.Channel
	var err error
	selectGroup := param.TokenGroup
	sawCapacityQueue := false // auto 分组遍历期间是否遇到过 capacity queue 信号
	userGroup := common.GetContextKeyString(param.Ctx, constant.ContextKeyUserGroup)

	if param.TokenGroup == "auto" {
		if len(setting.GetAutoGroups()) == 0 {
			return nil, selectGroup, errors.New("auto groups is not enabled")
		}
		autoGroups := GetUserAutoGroup(userGroup)

		// startGroupIndex: the group index to start searching from
		// startGroupIndex: 开始搜索的分组索引
		startGroupIndex := 0
		crossGroupRetry := common.GetContextKeyBool(param.Ctx, constant.ContextKeyTokenCrossGroupRetry)

		if lastGroupIndex, exists := common.GetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex); exists {
			if idx, ok := lastGroupIndex.(int); ok {
				startGroupIndex = idx
			}
		}

		for i := startGroupIndex; i < len(autoGroups); i++ {
			autoGroup := autoGroups[i]
			// Calculate priorityRetry for current group
			// 计算当前分组的 priorityRetry
			priorityRetry := param.GetRetry()
			// If moved to a new group, reset priorityRetry and update startRetryIndex
			// 如果切换到新分组，重置 priorityRetry 并更新 startRetryIndex
			if i > startGroupIndex {
				priorityRetry = 0
			}
			logger.LogDebug(param.Ctx, "Auto selecting group: %s, priorityRetry: %d", autoGroup, priorityRetry)

			var selErr error
			channel, selErr = model.GetRandomSatisfiedChannelForRequest(autoGroup, param.ModelName, priorityRetry, param.ExcludedChannelIDs, param.cacheDomainFilter(), param.RequestPath)
			if errors.Is(selErr, model.ErrCapacityNeedQueue) {
				sawCapacityQueue = true
			}
			if channel == nil {
				// Current group has no available channel for this model, try next group
				// 当前分组没有该模型的可用渠道，尝试下一个分组
				logger.LogDebug(param.Ctx, "No available channel in group %s for model %s at priorityRetry %d, trying next group", autoGroup, param.ModelName, priorityRetry)
				// 重置状态以尝试下一个分组
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupRetryIndex, 0)
				// Reset retry counter so outer loop can continue for next group
				// 重置重试计数器，以便外层循环可以为下一个分组继续
				param.SetRetry(0)
				// 切到新分组：清空已失败渠道列表，避免污染新分组的选择
				param.ExcludedChannelIDs = nil
				continue
			}
			common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroup, autoGroup)
			selectGroup = autoGroup
			logger.LogDebug(param.Ctx, "Auto selected group: %s", autoGroup)

			// Prepare state for next retry
			// 为下一次重试准备状态
			if crossGroupRetry && priorityRetry >= common.RetryTimes {
				// Current group has exhausted all retries, prepare to switch to next group
				// This request still uses current group, but next retry will use next group
				// 当前分组已用完所有重试次数，准备切换到下一个分组
				// 本次请求仍使用当前分组，但下次重试将使用下一个分组
				logger.LogDebug(param.Ctx, "Current group %s retries exhausted (priorityRetry=%d >= RetryTimes=%d), preparing switch to next group for next retry", autoGroup, priorityRetry, common.RetryTimes)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
				// Reset retry counter so outer loop can continue for next group
				// 重置重试计数器，以便外层循环可以为下一个分组继续
				param.SetRetry(0)
				// 准备切到下一个分组：同步清空 excluded，避免上一分组污染下一分组的渠道选择
				param.ExcludedChannelIDs = nil
				param.ResetRetryNextTry()
			} else {
				// Stay in current group, save current state
				// 保持在当前分组，保存当前状态
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i)
			}
			break
		}
		// auto 分组遍历完所有组仍无可用渠道，但途中遇到 capacity queue 信号 →
		// 透出排队信号，由 distributor 排队等待后重试（重试时重新走完整 auto 选择）。
		if channel == nil && sawCapacityQueue {
			return nil, selectGroup, model.ErrCapacityNeedQueue
		}
	} else {
		channel, err = model.GetRandomSatisfiedChannelForRequest(param.TokenGroup, param.ModelName, param.GetRetry(), param.ExcludedChannelIDs, param.cacheDomainFilter(), param.RequestPath)
		if err != nil {
			return nil, param.TokenGroup, err
		}
	}
	return channel, selectGroup, nil
}
