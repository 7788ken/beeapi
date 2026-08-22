package service

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// 价格变动 diff 引擎（发布制对账）。设计见 Docs/price-change-notify-design.md 第 6 节。
// 本文件保持纯函数：影响面（模型→分组）通过参数注入，不直接依赖 DB。

const (
	PriceScopeGroup = "group"
	PriceScopeModel = "model"

	PriceTypeGroupRatio       = "group_ratio"
	PriceTypeGroupGroupRatio  = "group_group_ratio"
	PriceTypeModelRatio       = "model_ratio"
	PriceTypeCompletionRatio  = "completion_ratio"
	PriceTypeCacheRatio       = "cache_ratio"
	PriceTypeCreateCacheRatio = "create_cache_ratio"
	PriceTypeModelPrice       = "model_price"

	PriceDirectionUp      = "up"
	PriceDirectionDown    = "down"
	PriceDirectionAdded   = "added"
	PriceDirectionRemoved = "removed"

	PriceDisplayUnitPer1M   = "per_1m"
	PriceDisplayUnitPerCall = "per_call"

	// GroupGroupRatio 明细的 GroupName 组合格式："用户组->目标组"
	priceGroupGroupSeparator = "->"

	// 浮点比较容差，净零回改不出明细
	priceFloatTolerance = 1e-9
)

// PriceMonitoredOptionKeys 监控的定价 option keys 白名单（与 model/option.go:182-192 同名）。
var PriceMonitoredOptionKeys = []string{
	"ModelRatio",
	"CompletionRatio",
	"CacheRatio",
	"CreateCacheRatio",
	"ModelPrice",
	"GroupRatio",
	"GroupGroupRatio",
}

// 模型级 option key → price_type
var priceModelKeyTypes = map[string]string{
	"ModelRatio":       PriceTypeModelRatio,
	"CompletionRatio":  PriceTypeCompletionRatio,
	"CacheRatio":       PriceTypeCacheRatio,
	"CreateCacheRatio": PriceTypeCreateCacheRatio,
	"ModelPrice":       PriceTypeModelPrice,
}

// price_type 稳定排序权重（输出与测试的确定性）
var priceTypeOrder = map[string]int{
	PriceTypeGroupRatio:       0,
	PriceTypeGroupGroupRatio:  1,
	PriceTypeModelRatio:       2,
	PriceTypeCompletionRatio:  3,
	PriceTypeCacheRatio:       4,
	PriceTypeCreateCacheRatio: 5,
	PriceTypeModelPrice:       6,
}

type PriceDisplayEntry struct {
	OldUsd float64 `json:"old_usd"`
	NewUsd float64 `json:"new_usd"`
	Unit   string  `json:"unit"` // per_1m | per_call
}

type PriceDiffItem struct {
	Scope          string                       `json:"scope"`      // group | model
	GroupName      string                       `json:"group_name"` // scope=group；group_group_ratio 为 "用户组->目标组"
	ModelName      string                       `json:"model_name"` // scope=model
	PriceType      string                       `json:"price_type"`
	OldValue       float64                      `json:"old_value"`
	NewValue       float64                      `json:"new_value"`
	Direction      string                       `json:"direction"` // up/down/added/removed
	AffectedGroups []string                     `json:"affected_groups"`
	Display        map[string]PriceDisplayEntry `json:"display"`
}

// GroupGroupRatioParts 拆解 group_group_ratio 明细的复合 GroupName，返回（用户组, 目标组, ok）。
func (item *PriceDiffItem) GroupGroupRatioParts() (string, string, bool) {
	if item.PriceType != PriceTypeGroupGroupRatio {
		return "", "", false
	}
	parts := strings.SplitN(item.GroupName, priceGroupGroupSeparator, 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

type PriceDiffSummary struct {
	Up      int `json:"up"`
	Down    int `json:"down"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

type PriceDiffResult struct {
	Items          []PriceDiffItem
	AffectedGroups []string // 全部明细受影响分组并集（排序）
	Summary        PriceDiffSummary
}

// CollectCurrentPriceSnapshot 从 ratio_setting 内存状态导出全部监控 key 的当前 JSON 值。
func CollectCurrentPriceSnapshot() map[string]string {
	return map[string]string{
		"ModelRatio":       ratio_setting.ModelRatio2JSONString(),
		"CompletionRatio":  ratio_setting.CompletionRatio2JSONString(),
		"CacheRatio":       ratio_setting.CacheRatio2JSONString(),
		"CreateCacheRatio": ratio_setting.CreateCacheRatio2JSONString(),
		"ModelPrice":       ratio_setting.ModelPrice2JSONString(),
		"GroupRatio":       ratio_setting.GroupRatio2JSONString(),
		"GroupGroupRatio":  ratio_setting.GroupGroupRatio2JSONString(),
	}
}

// ComputePriceDiff 计算 旧快照 vs 新快照 的价格变动明细。
// modelGroups：模型 → enable_groups（影响面注入）；模型不在其中则 AffectedGroups 为空（管理员可见、用户不可见）。
func ComputePriceDiff(oldSnapshot, newSnapshot map[string]string, modelGroups map[string][]string) (*PriceDiffResult, error) {
	result := &PriceDiffResult{Items: make([]PriceDiffItem, 0)}
	groupSet := make(map[string]bool)

	for _, key := range PriceMonitoredOptionKeys {
		oldJSON := snapshotValue(oldSnapshot, key)
		newJSON := snapshotValue(newSnapshot, key)
		if oldJSON == newJSON {
			continue
		}
		var items []PriceDiffItem
		var err error
		switch key {
		case "GroupRatio":
			items, err = diffGroupRatio(oldJSON, newJSON)
		case "GroupGroupRatio":
			items, err = diffGroupGroupRatio(oldJSON, newJSON)
		default:
			items, err = diffModelLevelKey(priceModelKeyTypes[key], oldJSON, newJSON, modelGroups)
		}
		if err != nil {
			return nil, fmt.Errorf("diff price key %s: %w", key, err)
		}
		result.Items = append(result.Items, items...)
	}

	sortPriceDiffItems(result.Items)
	for i := range result.Items {
		for _, g := range result.Items[i].AffectedGroups {
			groupSet[g] = true
		}
		switch result.Items[i].Direction {
		case PriceDirectionUp:
			result.Summary.Up++
		case PriceDirectionDown:
			result.Summary.Down++
		case PriceDirectionAdded:
			result.Summary.Added++
		case PriceDirectionRemoved:
			result.Summary.Removed++
		}
	}
	result.AffectedGroups = sortedKeys(groupSet)
	return result, nil
}

func snapshotValue(snapshot map[string]string, key string) string {
	if snapshot == nil {
		return "{}"
	}
	v, ok := snapshot[key]
	if !ok || strings.TrimSpace(v) == "" {
		return "{}"
	}
	return v
}

func diffGroupRatio(oldJSON, newJSON string) ([]PriceDiffItem, error) {
	oldMap, err := parsePriceFloatMap(oldJSON)
	if err != nil {
		return nil, err
	}
	newMap, err := parsePriceFloatMap(newJSON)
	if err != nil {
		return nil, err
	}
	items := make([]PriceDiffItem, 0)
	for _, g := range unionKeys(oldMap, newMap) {
		item, changed := buildDiffValueItem(oldMap, newMap, g)
		if !changed {
			continue
		}
		item.Scope = PriceScopeGroup
		item.GroupName = g
		item.PriceType = PriceTypeGroupRatio
		item.AffectedGroups = []string{g}
		items = append(items, item)
	}
	return items, nil
}

func diffGroupGroupRatio(oldJSON, newJSON string) ([]PriceDiffItem, error) {
	oldNested, err := parsePriceNestedMap(oldJSON)
	if err != nil {
		return nil, err
	}
	newNested, err := parsePriceNestedMap(newJSON)
	if err != nil {
		return nil, err
	}
	userGroupSet := make(map[string]bool)
	for ug := range oldNested {
		userGroupSet[ug] = true
	}
	for ug := range newNested {
		userGroupSet[ug] = true
	}
	items := make([]PriceDiffItem, 0)
	for _, ug := range sortedKeys(userGroupSet) {
		oldInner := oldNested[ug]
		newInner := newNested[ug]
		for _, g := range unionKeys(oldInner, newInner) {
			item, changed := buildDiffValueItem(oldInner, newInner, g)
			if !changed {
				continue
			}
			item.Scope = PriceScopeGroup
			item.GroupName = ug + priceGroupGroupSeparator + g
			item.PriceType = PriceTypeGroupGroupRatio
			item.AffectedGroups = []string{ug} // 专属倍率只影响该用户组
			items = append(items, item)
		}
	}
	return items, nil
}

func diffModelLevelKey(priceType string, oldJSON, newJSON string, modelGroups map[string][]string) ([]PriceDiffItem, error) {
	oldMap, err := parsePriceFloatMap(oldJSON)
	if err != nil {
		return nil, err
	}
	newMap, err := parsePriceFloatMap(newJSON)
	if err != nil {
		return nil, err
	}
	items := make([]PriceDiffItem, 0)
	for _, m := range unionKeys(oldMap, newMap) {
		item, changed := buildDiffValueItem(oldMap, newMap, m)
		if !changed {
			continue
		}
		item.Scope = PriceScopeModel
		item.ModelName = m
		item.PriceType = priceType
		item.AffectedGroups = copySortedGroups(modelGroups[m])
		items = append(items, item)
	}
	return items, nil
}

// buildDiffValueItem 处理单 key 的 增/删/改 判定；changed=false 表示净零无变化。
func buildDiffValueItem(oldMap, newMap map[string]float64, key string) (PriceDiffItem, bool) {
	oldVal, oldOk := oldMap[key]
	newVal, newOk := newMap[key]
	item := PriceDiffItem{
		OldValue: oldVal,
		NewValue: newVal,
		Display:  map[string]PriceDisplayEntry{},
	}
	switch {
	case oldOk && !newOk:
		item.Direction = PriceDirectionRemoved
		item.NewValue = 0
	case !oldOk && newOk:
		item.Direction = PriceDirectionAdded
		item.OldValue = 0
	case math.Abs(newVal-oldVal) <= priceFloatTolerance:
		return item, false
	case newVal > oldVal:
		item.Direction = PriceDirectionUp
	default:
		item.Direction = PriceDirectionDown
	}
	return item, true
}

func parsePriceFloatMap(jsonStr string) (map[string]float64, error) {
	m := make(map[string]float64)
	if err := common.UnmarshalJsonStr(jsonStr, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func parsePriceNestedMap(jsonStr string) (map[string]map[string]float64, error) {
	m := make(map[string]map[string]float64)
	if err := common.UnmarshalJsonStr(jsonStr, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func unionKeys(oldMap, newMap map[string]float64) []string {
	set := make(map[string]bool, len(oldMap)+len(newMap))
	for k := range oldMap {
		set[k] = true
	}
	for k := range newMap {
		set[k] = true
	}
	return sortedKeys(set)
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func copySortedGroups(groups []string) []string {
	out := make([]string, 0, len(groups))
	out = append(out, groups...)
	sort.Strings(out)
	return out
}

// sortPriceDiffItems 稳定排序：分组级在前，其后按 price_type、名称。
func sortPriceDiffItems(items []PriceDiffItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Scope != b.Scope {
			return a.Scope == PriceScopeGroup
		}
		if priceTypeOrder[a.PriceType] != priceTypeOrder[b.PriceType] {
			return priceTypeOrder[a.PriceType] < priceTypeOrder[b.PriceType]
		}
		if a.Scope == PriceScopeGroup {
			return a.GroupName < b.GroupName
		}
		return a.ModelName < b.ModelName
	})
}

// usdPerMillionFactor 1 倍率对应的 $/1M tokens（QuotaPerUnit=500000 → 2）。
func usdPerMillionFactor() float64 {
	return 1000000 / common.QuotaPerUnit
}

// BuildPriceItemDisplay 为模型级明细按分组生成有效美元价（分组级明细 display 恒为空）。
// groups：需要展示的分组；groupRatioFor：分组→请求者视角有效倍率；modelRatioFor：模型→当前 model_ratio。
// 换算规则（契约冻结）：
//   - model_ratio 项：$/1M = ratio × (1e6/QuotaPerUnit) × 分组倍率
//   - completion/cache/create_cache 项：$/1M = 当前 model_ratio × 该倍率 × (1e6/QuotaPerUnit) × 分组倍率
//   - model_price 项：$ = price × 分组倍率，unit=per_call
func BuildPriceItemDisplay(item *PriceDiffItem, groups []string, groupRatioFor func(string) float64, modelRatioFor func(string) float64) {
	item.Display = map[string]PriceDisplayEntry{}
	if item.Scope != PriceScopeModel {
		return
	}
	factor := usdPerMillionFactor()
	for _, g := range groups {
		groupRatio := groupRatioFor(g)
		var entry PriceDisplayEntry
		switch item.PriceType {
		case PriceTypeModelRatio:
			entry = PriceDisplayEntry{
				OldUsd: roundPriceUsd(item.OldValue * factor * groupRatio),
				NewUsd: roundPriceUsd(item.NewValue * factor * groupRatio),
				Unit:   PriceDisplayUnitPer1M,
			}
		case PriceTypeCompletionRatio, PriceTypeCacheRatio, PriceTypeCreateCacheRatio:
			modelRatio := modelRatioFor(item.ModelName)
			entry = PriceDisplayEntry{
				OldUsd: roundPriceUsd(modelRatio * item.OldValue * factor * groupRatio),
				NewUsd: roundPriceUsd(modelRatio * item.NewValue * factor * groupRatio),
				Unit:   PriceDisplayUnitPer1M,
			}
		case PriceTypeModelPrice:
			entry = PriceDisplayEntry{
				OldUsd: roundPriceUsd(item.OldValue * groupRatio),
				NewUsd: roundPriceUsd(item.NewValue * groupRatio),
				Unit:   PriceDisplayUnitPerCall,
			}
		default:
			continue
		}
		item.Display[g] = entry
	}
}

func roundPriceUsd(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}
