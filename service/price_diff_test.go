package service

import (
	"math"
	"testing"
)

// 纯函数单测：diff 引擎（增/删/改/净零回改/GroupRatio/GroupGroupRatio 两层/ModelPrice/混合/影响面并集/浮点容差）。
// go test ./service/ -run TestPriceDiff -v

func mustComputePriceDiff(t *testing.T, oldSnap, newSnap map[string]string, modelGroups map[string][]string) *PriceDiffResult {
	t.Helper()
	result, err := ComputePriceDiff(oldSnap, newSnap, modelGroups)
	if err != nil {
		t.Fatalf("ComputePriceDiff error: %v", err)
	}
	return result
}

func findPriceItem(t *testing.T, items []PriceDiffItem, priceType, name string) *PriceDiffItem {
	t.Helper()
	for i := range items {
		item := &items[i]
		if item.PriceType != priceType {
			continue
		}
		if item.Scope == PriceScopeModel && item.ModelName == name {
			return item
		}
		if item.Scope == PriceScopeGroup && item.GroupName == name {
			return item
		}
	}
	t.Fatalf("item not found: type=%s name=%s in %+v", priceType, name, items)
	return nil
}

func assertStringsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice mismatch: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slice mismatch at %d: got %v want %v", i, got, want)
		}
	}
}

func TestPriceDiffModelRatioUpDown(t *testing.T) {
	oldSnap := map[string]string{"ModelRatio": `{"gpt-4o":2.5,"gpt-4o-mini":0.15,"stable":1}`}
	newSnap := map[string]string{"ModelRatio": `{"gpt-4o":3.0,"gpt-4o-mini":0.075,"stable":1}`}
	groups := map[string][]string{"gpt-4o": {"vip", "default"}, "gpt-4o-mini": {"default"}}

	result := mustComputePriceDiff(t, oldSnap, newSnap, groups)
	if len(result.Items) != 2 {
		t.Fatalf("expect 2 items, got %d: %+v", len(result.Items), result.Items)
	}
	up := findPriceItem(t, result.Items, PriceTypeModelRatio, "gpt-4o")
	if up.Direction != PriceDirectionUp || up.OldValue != 2.5 || up.NewValue != 3.0 || up.Scope != PriceScopeModel {
		t.Fatalf("unexpected up item: %+v", up)
	}
	assertStringsEqual(t, up.AffectedGroups, []string{"default", "vip"}) // 注入影响面已排序
	down := findPriceItem(t, result.Items, PriceTypeModelRatio, "gpt-4o-mini")
	if down.Direction != PriceDirectionDown {
		t.Fatalf("unexpected down item: %+v", down)
	}
	if result.Summary.Up != 1 || result.Summary.Down != 1 || result.Summary.Added != 0 || result.Summary.Removed != 0 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
	assertStringsEqual(t, result.AffectedGroups, []string{"default", "vip"})
}

func TestPriceDiffModelPriceAddedRemoved(t *testing.T) {
	oldSnap := map[string]string{"ModelPrice": `{"dall-e-3":0.04}`}
	newSnap := map[string]string{"ModelPrice": `{"sora-2":0.3}`}
	groups := map[string][]string{"sora-2": {"default"}}

	result := mustComputePriceDiff(t, oldSnap, newSnap, groups)
	if len(result.Items) != 2 {
		t.Fatalf("expect 2 items, got %d", len(result.Items))
	}
	removed := findPriceItem(t, result.Items, PriceTypeModelPrice, "dall-e-3")
	if removed.Direction != PriceDirectionRemoved || removed.OldValue != 0.04 || removed.NewValue != 0 {
		t.Fatalf("unexpected removed item: %+v", removed)
	}
	// dall-e-3 已不在 pricing（渠道下线），影响面为空 → 管理员可见、用户不可见
	if len(removed.AffectedGroups) != 0 {
		t.Fatalf("removed model should have empty affected groups, got %v", removed.AffectedGroups)
	}
	added := findPriceItem(t, result.Items, PriceTypeModelPrice, "sora-2")
	if added.Direction != PriceDirectionAdded || added.OldValue != 0 || added.NewValue != 0.3 {
		t.Fatalf("unexpected added item: %+v", added)
	}
	if result.Summary.Added != 1 || result.Summary.Removed != 1 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
}

func TestPriceDiffNetZeroRevert(t *testing.T) {
	// 管理员改 2.5→3.0 又改回 2.5：与上次发布快照逐字节一致，净零不出明细
	snapshot := map[string]string{
		"ModelRatio": `{"gpt-4o":2.5}`,
		"GroupRatio": `{"default":1,"vip":0.9}`,
	}
	result := mustComputePriceDiff(t, snapshot, map[string]string{
		"ModelRatio": `{"gpt-4o":2.5}`,
		"GroupRatio": `{"default":1,"vip":0.9}`,
	}, nil)
	if len(result.Items) != 0 || len(result.AffectedGroups) != 0 {
		t.Fatalf("expect no items on net-zero revert, got %+v", result.Items)
	}
}

func TestPriceDiffFloatTolerance(t *testing.T) {
	oldSnap := map[string]string{"ModelRatio": `{"m1":1.0,"m2":1.0}`}
	newSnap := map[string]string{"ModelRatio": `{"m1":1.0000000000001,"m2":1.000001}`}
	result := mustComputePriceDiff(t, oldSnap, newSnap, nil)
	// m1 差值 1e-13 在 1e-9 容差内不出项；m2 差值 1e-6 应出项
	if len(result.Items) != 1 {
		t.Fatalf("expect exactly 1 item beyond tolerance, got %d: %+v", len(result.Items), result.Items)
	}
	if result.Items[0].ModelName != "m2" || result.Items[0].Direction != PriceDirectionUp {
		t.Fatalf("unexpected item: %+v", result.Items[0])
	}
}

func TestPriceDiffGroupRatio(t *testing.T) {
	oldSnap := map[string]string{"GroupRatio": `{"default":1,"vip":0.9}`}
	newSnap := map[string]string{"GroupRatio": `{"default":1,"vip":0.85,"svip":0.8}`}
	result := mustComputePriceDiff(t, oldSnap, newSnap, nil)
	if len(result.Items) != 2 {
		t.Fatalf("expect 2 items, got %d", len(result.Items))
	}
	vip := findPriceItem(t, result.Items, PriceTypeGroupRatio, "vip")
	if vip.Scope != PriceScopeGroup || vip.Direction != PriceDirectionDown || vip.OldValue != 0.9 || vip.NewValue != 0.85 {
		t.Fatalf("unexpected vip item: %+v", vip)
	}
	assertStringsEqual(t, vip.AffectedGroups, []string{"vip"})
	svip := findPriceItem(t, result.Items, PriceTypeGroupRatio, "svip")
	if svip.Direction != PriceDirectionAdded {
		t.Fatalf("unexpected svip item: %+v", svip)
	}
	assertStringsEqual(t, result.AffectedGroups, []string{"svip", "vip"})
}

func TestPriceDiffGroupGroupRatioNested(t *testing.T) {
	oldSnap := map[string]string{"GroupGroupRatio": `{"vip":{"default":0.9}}`}
	newSnap := map[string]string{"GroupGroupRatio": `{"vip":{"default":0.8},"svip":{"default":0.7}}`}
	result := mustComputePriceDiff(t, oldSnap, newSnap, nil)
	if len(result.Items) != 2 {
		t.Fatalf("expect 2 items, got %d: %+v", len(result.Items), result.Items)
	}
	changed := findPriceItem(t, result.Items, PriceTypeGroupGroupRatio, "vip->default")
	if changed.Direction != PriceDirectionDown || changed.OldValue != 0.9 || changed.NewValue != 0.8 {
		t.Fatalf("unexpected changed item: %+v", changed)
	}
	// 专属倍率只影响该用户组
	assertStringsEqual(t, changed.AffectedGroups, []string{"vip"})
	ug, target, ok := changed.GroupGroupRatioParts()
	if !ok || ug != "vip" || target != "default" {
		t.Fatalf("GroupGroupRatioParts failed: %s %s %v", ug, target, ok)
	}
	added := findPriceItem(t, result.Items, PriceTypeGroupGroupRatio, "svip->default")
	if added.Direction != PriceDirectionAdded {
		t.Fatalf("unexpected added item: %+v", added)
	}
	assertStringsEqual(t, added.AffectedGroups, []string{"svip"})
	assertStringsEqual(t, result.AffectedGroups, []string{"svip", "vip"})
}

func TestPriceDiffMixedScenarioUnionAndOrder(t *testing.T) {
	oldSnap := map[string]string{
		"GroupRatio":      `{"default":1,"vip":0.9}`,
		"ModelRatio":      `{"gpt-4o":2.5}`,
		"CompletionRatio": `{"orphan-model":4}`,
		"ModelPrice":      `{"dall-e-3":0.04}`,
	}
	newSnap := map[string]string{
		"GroupRatio":      `{"default":1,"vip":0.85}`,
		"ModelRatio":      `{"gpt-4o":3.0}`,
		"CompletionRatio": `{"orphan-model":8}`,
		"ModelPrice":      `{"dall-e-3":0.05}`,
	}
	groups := map[string][]string{
		"gpt-4o":   {"default", "vip"},
		"dall-e-3": {"svip"},
		// orphan-model 不在 pricing → 影响面空
	}
	result := mustComputePriceDiff(t, oldSnap, newSnap, groups)
	if len(result.Items) != 4 {
		t.Fatalf("expect 4 items, got %d: %+v", len(result.Items), result.Items)
	}
	// 分组级明细排前
	if result.Items[0].Scope != PriceScopeGroup || result.Items[0].PriceType != PriceTypeGroupRatio {
		t.Fatalf("group item should sort first, got %+v", result.Items[0])
	}
	orphan := findPriceItem(t, result.Items, PriceTypeCompletionRatio, "orphan-model")
	if len(orphan.AffectedGroups) != 0 {
		t.Fatalf("orphan model should have empty affected groups: %+v", orphan)
	}
	// 影响面并集 = vip(分组) ∪ default,vip(gpt-4o) ∪ svip(dall-e-3) ∪ ∅(orphan)
	assertStringsEqual(t, result.AffectedGroups, []string{"default", "svip", "vip"})
	if result.Summary.Up != 3 || result.Summary.Down != 1 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
}

func TestPriceDiffMissingKeyTreatedAsEmpty(t *testing.T) {
	// 旧快照缺 CacheRatio key（历史 baseline 未含该 key）→ 视为 {}，新增键全部按 added 出项
	oldSnap := map[string]string{}
	newSnap := map[string]string{"CacheRatio": `{"claude-opus-4-7":0.1}`}
	result := mustComputePriceDiff(t, oldSnap, newSnap, nil)
	if len(result.Items) != 1 || result.Items[0].Direction != PriceDirectionAdded {
		t.Fatalf("expect single added item, got %+v", result.Items)
	}
	if result.Items[0].PriceType != PriceTypeCacheRatio {
		t.Fatalf("unexpected price type: %+v", result.Items[0])
	}
}

func TestPriceDiffDisplayConversion(t *testing.T) {
	groupRatios := map[string]float64{"default": 1, "vip": 0.9}
	ratioFor := func(g string) float64 { return groupRatios[g] }
	modelRatioFor := func(string) float64 { return 2.5 }

	// model_ratio：$/1M = ratio × 2 × 分组倍率（QuotaPerUnit=500000）
	item := PriceDiffItem{
		Scope: PriceScopeModel, ModelName: "gpt-4o", PriceType: PriceTypeModelRatio,
		OldValue: 2.5, NewValue: 3.0, Direction: PriceDirectionUp,
		AffectedGroups: []string{"default", "vip"},
	}
	BuildPriceItemDisplay(&item, item.AffectedGroups, ratioFor, modelRatioFor)
	def := item.Display["default"]
	if def.OldUsd != 5.0 || def.NewUsd != 6.0 || def.Unit != PriceDisplayUnitPer1M {
		t.Fatalf("unexpected default display: %+v", def)
	}
	vip := item.Display["vip"]
	if math.Abs(vip.OldUsd-4.5) > 1e-9 || math.Abs(vip.NewUsd-5.4) > 1e-9 {
		t.Fatalf("unexpected vip display: %+v", vip)
	}

	// completion_ratio：输出价 = 当前 model_ratio × completion_ratio × 2 × 分组倍率
	completion := PriceDiffItem{
		Scope: PriceScopeModel, ModelName: "gpt-4o", PriceType: PriceTypeCompletionRatio,
		OldValue: 4, NewValue: 8, Direction: PriceDirectionUp,
		AffectedGroups: []string{"default"},
	}
	BuildPriceItemDisplay(&completion, completion.AffectedGroups, ratioFor, modelRatioFor)
	compDef := completion.Display["default"]
	if compDef.OldUsd != 20.0 || compDef.NewUsd != 40.0 || compDef.Unit != PriceDisplayUnitPer1M {
		t.Fatalf("unexpected completion display: %+v", compDef)
	}

	// model_price：按次 $ = price × 分组倍率
	price := PriceDiffItem{
		Scope: PriceScopeModel, ModelName: "dall-e-3", PriceType: PriceTypeModelPrice,
		OldValue: 0.04, NewValue: 0.05, Direction: PriceDirectionUp,
		AffectedGroups: []string{"vip"},
	}
	BuildPriceItemDisplay(&price, price.AffectedGroups, ratioFor, modelRatioFor)
	priceVip := price.Display["vip"]
	if math.Abs(priceVip.OldUsd-0.036) > 1e-9 || math.Abs(priceVip.NewUsd-0.045) > 1e-9 || priceVip.Unit != PriceDisplayUnitPerCall {
		t.Fatalf("unexpected model_price display: %+v", priceVip)
	}

	// 分组级明细 display 恒为空
	groupItem := PriceDiffItem{Scope: PriceScopeGroup, GroupName: "vip", PriceType: PriceTypeGroupRatio}
	BuildPriceItemDisplay(&groupItem, []string{"vip"}, ratioFor, modelRatioFor)
	if len(groupItem.Display) != 0 {
		t.Fatalf("group item display should be empty, got %+v", groupItem.Display)
	}
}
