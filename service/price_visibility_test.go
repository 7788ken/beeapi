package service

import "testing"

// filterPriceItemsForViewer 的可见性契约单测（防跨组泄露回归）。
// go test ./service/ -run TestFilterPriceItemsForViewer -v

func visibleSetOf(groups ...string) map[string]bool {
	set := make(map[string]bool, len(groups))
	for _, g := range groups {
		set[g] = true
	}
	return set
}

// A1: enable_groups 含 "all" 时，affected_groups 必须收敛到可见集，不能把 svip 之类原样吐回。
func TestFilterPriceItemsForViewerAllGroupNarrowsAffectedGroups(t *testing.T) {
	items := []PriceDiffItem{{
		Scope:          PriceScopeModel,
		ModelName:      "gpt-x",
		PriceType:      PriceTypeModelRatio,
		OldValue:       1,
		NewValue:       2,
		Direction:      PriceDirectionUp,
		AffectedGroups: []string{"all", "svip", "vip"},
	}}
	modelGroups := map[string][]string{"gpt-x": {"all"}}

	out := filterPriceItemsForViewer(items, "default", visibleSetOf("default", "vip"), modelGroups)
	if len(out) != 1 {
		t.Fatalf("expected 1 visible item, got %d", len(out))
	}
	assertStringsEqual(t, out[0].AffectedGroups, []string{"default", "vip"})
	if _, leaked := out[0].Display["svip"]; leaked {
		t.Fatal("display leaked invisible group svip")
	}
}

// A2: 过滤用当前 pricing 的 enable_groups，历史快照里的分组不再兜底放行。
func TestFilterPriceItemsForViewerUsesCurrentEnableGroups(t *testing.T) {
	items := []PriceDiffItem{{
		Scope:          PriceScopeModel,
		ModelName:      "gpt-x",
		PriceType:      PriceTypeModelRatio,
		AffectedGroups: []string{"default"}, // 发布时的历史快照
	}}

	// 模型现已只对 svip 开放：default 用户不该再看到这条历史变动
	movedOut := filterPriceItemsForViewer(items, "default", visibleSetOf("default"),
		map[string][]string{"gpt-x": {"svip"}})
	if len(movedOut) != 0 {
		t.Fatalf("expected item hidden after model left viewer's groups, got %+v", movedOut)
	}

	// 模型已完全下架（不在 pricing 中）：同样不展示
	delisted := filterPriceItemsForViewer(items, "default", visibleSetOf("default"),
		map[string][]string{})
	if len(delisted) != 0 {
		t.Fatalf("expected item hidden for delisted model, got %+v", delisted)
	}
}

// group_group_ratio：只有本组可见，且目标组必须在可见集内（复合名会渲染目标组）。
func TestFilterPriceItemsForViewerGroupGroupRatio(t *testing.T) {
	items := []PriceDiffItem{{
		Scope:          PriceScopeGroup,
		GroupName:      "vip->svip",
		PriceType:      PriceTypeGroupGroupRatio,
		AffectedGroups: []string{"vip"},
	}}

	if out := filterPriceItemsForViewer(items, "vip", visibleSetOf("vip"), nil); len(out) != 0 {
		t.Fatalf("expected hidden when target group svip invisible, got %+v", out)
	}
	if out := filterPriceItemsForViewer(items, "vip", visibleSetOf("vip", "svip"), nil); len(out) != 1 {
		t.Fatalf("expected visible when both groups visible, got %+v", out)
	}
	if out := filterPriceItemsForViewer(items, "", visibleSetOf("vip", "svip"), nil); len(out) != 0 {
		t.Fatalf("expected hidden for anonymous viewer, got %+v", out)
	}
	if out := filterPriceItemsForViewer(items, "default", visibleSetOf("vip", "svip"), nil); len(out) != 0 {
		t.Fatalf("expected hidden for other group's viewer, got %+v", out)
	}
}

// group_ratio：分组必须在可见集内。
func TestFilterPriceItemsForViewerGroupRatio(t *testing.T) {
	items := []PriceDiffItem{
		{Scope: PriceScopeGroup, GroupName: "default", PriceType: PriceTypeGroupRatio},
		{Scope: PriceScopeGroup, GroupName: "svip", PriceType: PriceTypeGroupRatio},
	}
	out := filterPriceItemsForViewer(items, "default", visibleSetOf("default"), nil)
	if len(out) != 1 || out[0].GroupName != "default" {
		t.Fatalf("expected only default group ratio, got %+v", out)
	}
}
