package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
)

// 首次抓取（无基线）不得产生任何变更，否则上线当天全站误报涨价。
func TestDiffGroupRatiosFirstSeenProducesNoDiff(t *testing.T) {
	samples := []GroupRatioSample{
		{GroupName: "default", RatioKind: RatioKindGroup, Value: 2},
		{GroupName: "claude", RatioKind: RatioKindGroup, Value: 3.5},
	}
	diffs, upserts := diffGroupRatios(nil, samples)
	if len(diffs) != 0 {
		t.Fatalf("first seen must not produce diffs, got %d", len(diffs))
	}
	if len(upserts) != 2 {
		t.Fatalf("want 2 baselines, got %d", len(upserts))
	}
}

// 新出现的分组只建基线不报警，避免上游新增分组或改名被误判成涨价。
func TestDiffGroupRatiosNewGroupNotReported(t *testing.T) {
	baselines := []model.ChannelGroupRatioBaseline{
		{GroupName: "default", RatioKind: RatioKindGroup, Ratio: 2},
	}
	samples := []GroupRatioSample{
		{GroupName: "default", RatioKind: RatioKindGroup, Value: 2},
		{GroupName: "brand-new", RatioKind: RatioKindGroup, Value: 99},
	}
	diffs, upserts := diffGroupRatios(baselines, samples)
	if len(diffs) != 0 {
		t.Fatalf("new group must not be reported, got %+v", diffs)
	}
	if len(upserts) != 2 {
		t.Fatalf("want 2 baselines, got %d", len(upserts))
	}
}

func TestDiffGroupRatiosDetectsDirection(t *testing.T) {
	baselines := []model.ChannelGroupRatioBaseline{
		{GroupName: "up", RatioKind: RatioKindGroup, Ratio: 2},
		{GroupName: "down", RatioKind: RatioKindGroup, Ratio: 2},
		{GroupName: "same", RatioKind: RatioKindGroup, Ratio: 2},
	}
	samples := []GroupRatioSample{
		{GroupName: "up", RatioKind: RatioKindGroup, Value: 2.5},
		{GroupName: "down", RatioKind: RatioKindGroup, Value: 1.5},
		{GroupName: "same", RatioKind: RatioKindGroup, Value: 2},
	}
	diffs, _ := diffGroupRatios(baselines, samples)
	if len(diffs) != 2 {
		t.Fatalf("want 2 diffs, got %d: %+v", len(diffs), diffs)
	}
	byGroup := map[string]RatioDiff{}
	for _, d := range diffs {
		byGroup[d.GroupName] = d
	}
	if byGroup["up"].Direction != 1 {
		t.Errorf("up direction = %d, want 1", byGroup["up"].Direction)
	}
	if byGroup["down"].Direction != -1 {
		t.Errorf("down direction = %d, want -1", byGroup["down"].Direction)
	}
	if _, ok := byGroup["same"]; ok {
		t.Error("unchanged ratio must not produce a diff")
	}
}

// 浮点抖动不得被当成倍率变化。
func TestDiffGroupRatiosIgnoresFloatNoise(t *testing.T) {
	baselines := []model.ChannelGroupRatioBaseline{
		{GroupName: "g", RatioKind: RatioKindGroup, Ratio: 0.07},
	}
	samples := []GroupRatioSample{
		{GroupName: "g", RatioKind: RatioKindGroup, Value: 0.07 + 1e-12},
	}
	if diffs, _ := diffGroupRatios(baselines, samples); len(diffs) != 0 {
		t.Fatalf("float noise must not be a diff, got %+v", diffs)
	}
}

// effective 含 sub2api 当前时段 peak 系数，进出高峰时段会自然波动，不能计入角标。
func TestCountBadgeDirectionsExcludesEffective(t *testing.T) {
	diffs := []RatioDiff{
		{GroupName: "a", RatioKind: RatioKindEffective, Direction: 1},
		{GroupName: "a", RatioKind: RatioKindAPIRate, Direction: 1},
		{GroupName: "a", RatioKind: RatioKindGroup, Direction: 1},
		{GroupName: "b", RatioKind: RatioKindResolved, Direction: -1},
	}
	up, down := CountBadgeDirections(diffs)
	if up != 1 {
		t.Errorf("up = %d, want 1 (only group counts)", up)
	}
	if down != 1 {
		t.Errorf("down = %d, want 1 (only resolved counts)", down)
	}
}

// 角标回写判据必须与角标口径一致。
// sub2api 每天进出高峰两次会让 effective 变动：若按 len(Diffs)>0 判定"有变化"，
// 就会用 up=down=0 覆盖掉上一轮真实涨价的角标，功能对 sub2api 渠道实质失效。
func TestBadgeWriteBackGuardIgnoresEffectiveOnlyChange(t *testing.T) {
	// 只有 effective 变动（进出高峰时段的正常波动）
	effectiveOnly := []RatioDiff{
		{GroupName: "(key group)", RatioKind: RatioKindEffective, Direction: 1},
	}
	up, down := CountBadgeDirections(effectiveOnly)
	if up != 0 || down != 0 {
		t.Fatalf("effective-only change must not count: up=%d down=%d", up, down)
	}
	// 这就是回写判据；为 true 会抹掉上轮真实涨价角标
	if shouldWriteBadge := up > 0 || down > 0; shouldWriteBadge {
		t.Error("effective-only change must not trigger badge write-back")
	}

	// 真实涨价必须触发回写
	realRise := []RatioDiff{
		{GroupName: "(key group)", RatioKind: RatioKindResolved, Direction: 1},
	}
	up, down = CountBadgeDirections(realRise)
	if shouldWriteBadge := up > 0 || down > 0; !shouldWriteBadge {
		t.Error("resolved change must trigger badge write-back")
	}
}

// sub2api 上游管理员给我们设了专属倍率时，摘要必须取 resolved（专属价）
// 而不是 group（公共挂牌价）。allincoding 实测形态：挂牌 3.46、专属 0.15。
func TestBuildRatioSummaryPrefersResolvedOverGroup(t *testing.T) {
	samples := []GroupRatioSample{
		{GroupName: "(key group)", RatioKind: RatioKindGroup, Value: 3.46},
		{GroupName: "(key group)", RatioKind: RatioKindResolved, Value: 0.15},
		{GroupName: "(key group)", RatioKind: RatioKindEffective, Value: 0.15},
	}
	got := buildRatioSummary(samples, "(key group)")
	if got != `{"n":1,"min":0.15,"max":0.15,"g":"(key group)"}` {
		t.Fatalf("summary = %s, want resolved 0.15 not listed 3.46", got)
	}
}
