package model

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var group2model2channels map[string]map[string][]int // enabled channel
var channelsIDM map[int]*Channel                     // all channels include disabled
var channel2advancedCustomConfig map[int]*dto.AdvancedCustomConfig
var channelSyncLock sync.RWMutex

func InitChannelCache() {
	if !common.MemoryCacheEnabled {
		return
	}
	newChannelId2channel := make(map[int]*Channel)
	newChannel2advancedCustomConfig := make(map[int]*dto.AdvancedCustomConfig)
	var channels []*Channel
	if err := DB.Find(&channels).Error; err != nil {
		common.SysError("InitChannelCache: failed to load channels, keeping existing cache: " + err.Error())
		return
	}
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				newChannel2advancedCustomConfig[channel.Id] = config
			}
		}
	}
	var abilities []*Ability
	if err := DB.Find(&abilities).Error; err != nil {
		common.SysError("InitChannelCache: failed to load abilities, keeping existing cache: " + err.Error())
		return
	}
	groups := make(map[string]bool)
	for _, ability := range abilities {
		groups[ability.Group] = true
	}
	newGroup2model2channels := make(map[string]map[string][]int)
	for group := range groups {
		newGroup2model2channels[group] = make(map[string][]int)
	}
	for _, channel := range channels {
		if channel.Status != common.ChannelStatusEnabled {
			continue // skip disabled channels
		}
		groups := strings.Split(channel.Group, ",")
		for _, group := range groups {
			if _, ok := newGroup2model2channels[group]; !ok {
				// abilities 表查询为空或 channel.Group 不在 abilities 中时，
				// 内层 map 未预建，这里补建以防 nil map 写入 panic
				newGroup2model2channels[group] = make(map[string][]int)
			}
			models := strings.Split(channel.Models, ",")
			for _, model := range models {
				if _, ok := newGroup2model2channels[group][model]; !ok {
					newGroup2model2channels[group][model] = make([]int, 0)
				}
				newGroup2model2channels[group][model] = append(newGroup2model2channels[group][model], channel.Id)
			}
		}
	}

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return newChannelId2channel[channels[i]].GetPriority() > newChannelId2channel[channels[j]].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	//channelsIDM = newChannelId2channel
	for i, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
			if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				if oldChannel, ok := channelsIDM[i]; ok {
					// 存在旧的渠道，如果是多key且轮询，保留轮询索引信息
					if oldChannel.ChannelInfo.IsMultiKey && oldChannel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
						channel.ChannelInfo.MultiKeyPollingIndex = oldChannel.ChannelInfo.MultiKeyPollingIndex
					}
				}
			}
		}
	}
	channelsIDM = newChannelId2channel
	channel2advancedCustomConfig = newChannel2advancedCustomConfig
	channelSyncLock.Unlock()
	common.SysLog("channels synced from database")
}

func SyncChannelCache(ctx context.Context, frequency int) {
	backgroundtask.RunPeriodic(ctx, time.Duration(frequency)*time.Second, false, func() {
		common.SysLog("syncing channels from database")
		InitChannelCache()
	})
}

// cacheDomainFilter 非空时（same_domain 档），只在该缓存域内选渠道，且不做"全排除回退"，
// 域内无可用即返回 nil（不外溢到其它上游账号，避免丢 prompt cache）。
func GetRandomSatisfiedChannel(group string, model string, retry int, excludedIDs []int, cacheDomainFilter string) (*Channel, error) {
	return GetRandomSatisfiedChannelForRequest(group, model, retry, excludedIDs, cacheDomainFilter, "")
}

func GetRandomSatisfiedChannelForRequest(group string, model string, retry int, excludedIDs []int, cacheDomainFilter string, requestPath string) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannelForRequest(group, model, retry, excludedIDs, cacheDomainFilter, requestPath)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	// First, try to find channels with the exact model name.
	channels := filterChannelsByRequestPathAndModel(group2model2channels[group][model], requestPath, model)

	// If no channels found, try to find channels with the normalized model name.
	if len(channels) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(model)
		channels = filterChannelsByRequestPathAndModel(group2model2channels[group][normalizedModel], requestPath, model)
	}

	if len(channels) == 0 {
		return nil, nil
	}

	if len(channels) == 1 {
		channel, ok := channelsIDM[channels[0]]
		if !ok {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channels[0])
		}
		// same_domain：单渠道不在目标缓存域 → 不返回（不外溢到其它账号）
		if cacheDomainFilter != "" && channel.GetCacheDomain() != cacheDomainFilter {
			return nil, nil
		}
		if isChannelExcluded(channels[0], excludedIDs) {
			// same_domain：域内唯一候选已失败 → 与多渠道分支一致 fail-fast（nil），
			// 由 relay 层把上一轮真实上游错误返回客户端，不再原地重试坏点。
			if cacheDomainFilter != "" {
				return nil, nil
			}
			// 单渠道且被排除（retry > 0 时必然触发）→ 仍返回它，避免"无可用渠道"瞬间失败；
			// 上层 retry 次数有上限，不会死循环。
			common.SysLog(fmt.Sprintf("retry: single channel %d excluded for group %s model %s, fallback to retry it", channels[0], group, model))
			// 被排除的单渠道是 relay 重试兜底，不做容量拦截，直接返回避免"无渠道可用"。
			return channel, nil
		}
		// 单渠道容量检查：capacity 模式且已满时无其它渠道可转移，按该渠道「有效全满策略」处理
		//（渠道级 capacity_full_strategy > 全局：reject→拒绝；queue→排队信号让 distributor 等待；
		// 软档 fallback/degraded→无次优先级可降，挤自己放行而非 503——修复单渠道桶满误报）。
		// 不接入此检查时，单渠道会在上面直接 return，绕过容量过滤 → 单渠道限速失效。
		if cfg := operation_setting.GetChannelRoutingConfig(); !cfg.DryRun &&
			ResolveRoutingMode(channel, cfg.Mode) == constant.RoutingModeCapacity {
			survivors, capChans, counts := filterCapacityChannels([]*Channel{channel}, cfg.Mode)
			if len(survivors) == 0 {
				switch ResolveFullStrategy(channel, cfg.FullStrategy) {
				case constant.CapacityFullStrategyReject:
					return nil, ErrAllChannelsCapacityFull
				case constant.CapacityFullStrategyQueue:
					return nil, ErrCapacityNeedQueue
				default: // 软档 fallback/degraded：无处可降 → 放行该渠道本身（限速退化为软提示）
					if ch := pickLeastOverloadedChannel(capChans, counts); ch != nil {
						return ch, nil
					}
					return nil, ErrAllChannelsCapacityFull // limit<=0=显式禁用，才真拒
				}
			}
		}
		return channel, nil
	}

	uniquePriorities := make(map[int]bool)
	availableChannels := make([]int, 0, len(channels))
	for _, channelId := range channels {
		if isChannelExcluded(channelId, excludedIDs) {
			continue
		}
		if channel, ok := channelsIDM[channelId]; ok {
			// same_domain：跳过不在目标缓存域的渠道
			if cacheDomainFilter != "" && channel.GetCacheDomain() != cacheDomainFilter {
				continue
			}
			uniquePriorities[int(channel.GetPriority())] = true
			availableChannels = append(availableChannels, channelId)
		} else {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
		}
	}
	// FALLBACK：所有候选都已 excluded（说明 RetryTimes > 实际可用渠道数）。
	// 清空排除集重新选——瞬时错误（429/5xx）通常已恢复，宁可重试坏点也不要
	// "无可用渠道"瞬间失败。RetryTimes 已限制总次数，不会死循环。
	// 注意：same_domain（cacheDomainFilter != ""）时不回退，域内无可用即失败，避免外溢丢缓存。
	if len(availableChannels) == 0 && len(excludedIDs) > 0 && cacheDomainFilter == "" {
		common.SysLog(fmt.Sprintf("retry: all %d channels excluded for group %s model %s, fallback to retry full candidate set", len(channels), group, model))
		uniquePriorities = make(map[int]bool)
		for _, channelId := range channels {
			channel, ok := channelsIDM[channelId]
			if !ok {
				return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
			}
			uniquePriorities[int(channel.GetPriority())] = true
			availableChannels = append(availableChannels, channelId)
		}
	}
	if len(availableChannels) == 0 {
		return nil, nil
	}
	var sortedUniquePriorities []int
	for priority := range uniquePriorities {
		sortedUniquePriorities = append(sortedUniquePriorities, priority)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sortedUniquePriorities)))

	if retry >= len(uniquePriorities) {
		retry = len(uniquePriorities) - 1
	}
	targetPriority := int64(sortedUniquePriorities[retry])

	// get the priority for the given retry number
	var targetChannels []*Channel
	for _, channelId := range availableChannels {
		if channel, ok := channelsIDM[channelId]; ok {
			if channel.GetPriority() == targetPriority {
				targetChannels = append(targetChannels, channel)
			}
		} else {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
		}
	}

	if len(targetChannels) == 0 {
		return nil, errors.New(fmt.Sprintf("no channel found, group: %s, model: %s, priority: %d", group, model, targetPriority))
	}

	// ── 渠道路由模式（P2 接入）──
	// 当 capacity 模式生效时，先做容量过滤；过滤后仍按 weight 加权随机。
	// docs/2026-05-26-channel-routing-mode-switchable.md
	return selectFromPriorityLayer(targetChannels, availableChannels, sortedUniquePriorities, retry, group, model)
}

// filterChannelsByRequestPathAndModel only filters Advanced Custom channels.
// Other channel types retain the existing routing, capacity and retry behavior.
// The caller holds channelSyncLock.RLock.
func filterChannelsByRequestPathAndModel(channels []int, requestPath string, model string) []int {
	if requestPath == "" || len(channels) == 0 {
		return channels
	}
	filtered := make([]int, 0, len(channels))
	for _, channelID := range channels {
		channel, ok := channelsIDM[channelID]
		if !ok {
			filtered = append(filtered, channelID)
			continue
		}
		if channel.Type != constant.ChannelTypeAdvancedCustom {
			filtered = append(filtered, channelID)
			continue
		}
		if config := channel2advancedCustomConfig[channelID]; config != nil &&
			config.SupportsPathForModel(requestPath, model) {
			filtered = append(filtered, channelID)
		}
	}
	return filtered
}

// selectFromPriorityLayer 从某 priority 层选 1 个渠道。
// 含容量过滤分支 + 全满策略 fallback / reject / degraded。
//
// 参数：
//   - targetChannels: 当前 priority 的全部候选
//   - availableChannels: 全集（用于 fallback 时翻找下一 priority）
//   - sortedUniquePriorities: 降序唯一优先级数组
//   - currentRetryIdx: 当前 priority 在 sortedUniquePriorities 中的索引
func selectFromPriorityLayer(targetChannels []*Channel, availableChannels []int, sortedUniquePriorities []int, currentRetryIdx int, group, model string) (*Channel, error) {
	routingCfg := operation_setting.GetChannelRoutingConfig()

	// dry-run 模式：跳过容量过滤直接走加权随机；distributor 仍会写计数器，
	// 用于评估"如果真切到 capacity 模式会拒掉多少请求"。
	if routingCfg.DryRun {
		return weightedRandomFromChannels(targetChannels, rand.Intn)
	}

	// 快速路径：没有任何 capacity 渠道 → 直接走原始加权随机
	if !hasAnyCapacityChannel(targetChannels, routingCfg.Mode) {
		return weightedRandomFromChannels(targetChannels, rand.Intn)
	}

	// 容量过滤（一次 MGET 拿到所有 capacity 渠道的当前用量，degraded 分支复用 counts）
	survivors, capChans, counts := filterCapacityChannels(targetChannels, routingCfg.Mode)
	if len(survivors) > 0 {
		// capacity 模式：按 capacity_limit（水桶大小）加权分配——大桶多得、小桶少得；
		// GetCapacityLimit NULL 时自动回落 weight，对混合 capacity/probabilistic 候选兼容。
		return weightedRandomByCapacity(survivors, rand.Intn)
	}

	// 全满 → 按该 priority 层各渠道的「有效全满策略」合成处理（渠道级 capacity_full_strategy > 全局）。
	// 软档 fallback/degraded：不拒，挤/降级继续服务；硬档 reject/queue：退出候选、拒/排队。
	// 「最宽松优先」：一层混合策略时只要存在软桶就不整层 503；fallback/queue 仍先跨层转移。
	var softChans []*Channel
	hasFallback, hasQueue := false, false
	for _, ch := range capChans {
		switch ResolveFullStrategy(ch, routingCfg.FullStrategy) {
		case constant.CapacityFullStrategyFallback:
			hasFallback = true
			softChans = append(softChans, ch)
		case constant.CapacityFullStrategyDegraded:
			softChans = append(softChans, ch)
		case constant.CapacityFullStrategyQueue:
			hasQueue = true
		case constant.CapacityFullStrategyReject:
			// 硬拒：满了退出候选，不挤、不跨层
		}
	}

	// fallback / queue 含跨层语义：先尝试转移到更低优先级层的未满渠道（"优先转移到其他渠道"）。
	if hasFallback || hasQueue {
		if ch := tryLowerPriorityLayers(availableChannels, sortedUniquePriorities, currentRetryIdx, routingCfg.Mode, group, model); ch != nil {
			return ch, nil
		}
	}
	// 跨层无果（或本就不跨层）：软档兜底——挤当前层最不超载的软桶，继续服务（含 fallback 无处可降时）。
	if len(softChans) > 0 {
		if ch := pickLeastOverloadedChannel(softChans, counts); ch != nil {
			common.SysLog(fmt.Sprintf("capacity soft: channel=%d picked (least overloaded soft) at priority idx=%d for group=%s model=%s",
				ch.Id, currentRetryIdx, group, model))
			return ch, nil
		}
	}
	// 无软桶兜底：queue→排队信号；否则（全 reject，或软桶 limit<=0 全禁用）→ 容量耗尽拒绝
	if hasQueue {
		common.SysLog(fmt.Sprintf("capacity queue: all priorities full from idx=%d for group=%s model=%s, signal caller to wait",
			currentRetryIdx, group, model))
		return nil, ErrCapacityNeedQueue
	}
	common.SysLog(fmt.Sprintf("capacity full (hard reject): all channels full from idx=%d for group=%s model=%s",
		currentRetryIdx, group, model))
	return nil, ErrAllChannelsCapacityFull
}

// tryLowerPriorityLayers 从 currentRetryIdx 的下一层起逐层找未满渠道（fallback/queue 跨层转移）。
// 找到则返回该层加权随机选中的渠道；所有更低优先级层都满返回 nil。
// 抽自原 selectFromPriorityLayer 内联跨层遍历，供「最宽松优先」合成逻辑复用。
func tryLowerPriorityLayers(availableChannels []int, sortedUniquePriorities []int, currentRetryIdx int, globalMode, group, model string) *Channel {
	for nextIdx := currentRetryIdx + 1; nextIdx < len(sortedUniquePriorities); nextIdx++ {
		nextPriority := int64(sortedUniquePriorities[nextIdx])
		nextChannels := make([]*Channel, 0, 4)
		for _, channelId := range availableChannels {
			if ch, ok := channelsIDM[channelId]; ok && ch.GetPriority() == nextPriority {
				nextChannels = append(nextChannels, ch)
			}
		}
		if len(nextChannels) == 0 {
			continue
		}
		if !hasAnyCapacityChannel(nextChannels, globalMode) {
			common.SysLog(fmt.Sprintf("capacity fallback: priority idx=%d→%d (no capacity at next layer) group=%s model=%s",
				currentRetryIdx, nextIdx, group, model))
			if ch, err := weightedRandomFromChannels(nextChannels, rand.Intn); err == nil {
				return ch
			}
			return nil
		}
		nextSurvivors, _, _ := filterCapacityChannels(nextChannels, globalMode)
		if len(nextSurvivors) > 0 {
			common.SysLog(fmt.Sprintf("capacity fallback: priority idx=%d→%d (survivors=%d) group=%s model=%s",
				currentRetryIdx, nextIdx, len(nextSurvivors), group, model))
			if ch, err := weightedRandomByCapacity(nextSurvivors, rand.Intn); err == nil {
				return ch
			}
			return nil
		}
	}
	return nil
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	return c, nil
}

func CacheGetChannelInfo(id int) (*ChannelInfo, error) {
	if !common.MemoryCacheEnabled {
		channel, err := GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		return &channel.ChannelInfo, nil
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	return &c.ChannelInfo, nil
}

func CacheUpdateChannelStatus(id int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channel, ok := channelsIDM[id]; ok {
		channel.Status = status
	}
	if status != common.ChannelStatusEnabled {
		// delete the channel from group2model2channels
		for group, model2channels := range group2model2channels {
			for model, channels := range model2channels {
				for i, channelId := range channels {
					if channelId == id {
						// remove the channel from the slice
						group2model2channels[group][model] = append(channels[:i], channels[i+1:]...)
						break
					}
				}
			}
		}
	}
}

func CacheUpdateChannel(channel *Channel) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channel == nil {
		return
	}

	println("CacheUpdateChannel:", channel.Id, channel.Name, channel.Status, channel.ChannelInfo.MultiKeyPollingIndex)

	println("before:", channelsIDM[channel.Id].ChannelInfo.MultiKeyPollingIndex)
	channelsIDM[channel.Id] = channel
	if channel2advancedCustomConfig == nil {
		channel2advancedCustomConfig = make(map[int]*dto.AdvancedCustomConfig)
	}
	delete(channel2advancedCustomConfig, channel.Id)
	if channel.Type == constant.ChannelTypeAdvancedCustom {
		if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
			channel2advancedCustomConfig[channel.Id] = config
		}
	}
	println("after :", channelsIDM[channel.Id].ChannelInfo.MultiKeyPollingIndex)
}

// isChannelExcluded 判断 channelID 是否在 excluded 列表中
func isChannelExcluded(channelID int, excludedIDs []int) bool {
	for _, id := range excludedIDs {
		if id == channelID {
			return true
		}
	}
	return false
}
