package model

import (
	"testing"
)

// mkCapChannel 构造带 capacity_limit 的测试渠道（与现有 mkChannel 兼容）
func mkCapChannel(id int, weight uint, capacityLimit *int) *Channel {
	w := weight
	ch := &Channel{Id: id, Weight: &w}
	ch.CapacityLimit = capacityLimit
	return ch
}

func intPtr(v int) *int { return &v }

func TestWeightedRandomByCapacity_RespectsLimit(t *testing.T) {
	// 大桶 limit=100，小桶 limit=10：应明显偏向大桶
	big := mkCapChannel(1, 1, intPtr(100))
	small := mkCapChannel(2, 1, intPtr(10))
	channels := []*Channel{big, small}

	bigCount := 0
	trials := 10000
	for i := 0; i < trials; i++ {
		// 用固定种子序列模拟均匀随机
		rnd := func(n int) int { return i % n }
		picked, err := weightedRandomByCapacity(channels, rnd)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if picked.Id == 1 {
			bigCount++
		}
	}
	// 期望 big 占 100/110 ≈ 90.9%；允许 ±5% 波动
	ratio := float64(bigCount) / float64(trials)
	if ratio < 0.85 || ratio > 0.95 {
		t.Errorf("big channel selection ratio = %.3f, want ≈ 0.909 (±0.05)", ratio)
	}
}

func TestWeightedRandomByCapacity_FallbackToWeight(t *testing.T) {
	// capacity_limit NULL → GetCapacityLimit 回落 weight；行为应等同于按 weight 加权
	a := mkCapChannel(1, 50, nil) // limit=NULL → 用 weight=50
	b := mkCapChannel(2, 10, nil) // limit=NULL → 用 weight=10
	channels := []*Channel{a, b}

	aCount := 0
	trials := 10000
	for i := 0; i < trials; i++ {
		rnd := func(n int) int { return i % n }
		picked, _ := weightedRandomByCapacity(channels, rnd)
		if picked.Id == 1 {
			aCount++
		}
	}
	// 期望 a 占 50/60 ≈ 83.3%；±5%
	ratio := float64(aCount) / float64(trials)
	if ratio < 0.78 || ratio > 0.88 {
		t.Errorf("a channel selection ratio = %.3f, want ≈ 0.833 (±0.05)", ratio)
	}
}

func TestWeightedRandomByCapacity_AllZero(t *testing.T) {
	// 全部 limit/weight=0 → 退化为均匀分布
	a := mkCapChannel(1, 0, intPtr(0))
	b := mkCapChannel(2, 0, intPtr(0))
	channels := []*Channel{a, b}

	aCount := 0
	trials := 10000
	for i := 0; i < trials; i++ {
		rnd := func(n int) int { return i % n }
		picked, _ := weightedRandomByCapacity(channels, rnd)
		if picked.Id == 1 {
			aCount++
		}
	}
	// 期望 ~50%；±5%
	ratio := float64(aCount) / float64(trials)
	if ratio < 0.45 || ratio > 0.55 {
		t.Errorf("uniform fallback ratio = %.3f, want ≈ 0.5 (±0.05)", ratio)
	}
}

func TestWeightedRandomByCapacity_Empty(t *testing.T) {
	if _, err := weightedRandomByCapacity(nil, nil); err == nil {
		t.Error("expected error on empty channels")
	}
}
