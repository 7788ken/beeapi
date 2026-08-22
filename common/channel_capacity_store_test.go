package common

import (
	"testing"
	"time"
)

func TestNormalizeCapacityWindowSec(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, 60},
		{1, 60},
		{30, 60},
		{60, 60},
		{61, 120},
		{90, 120},
		{120, 120},
		{121, 180},
		{3599, 3600},
		{3600, 3600},
		{4000, 3600},
	}
	for _, c := range cases {
		if got := NormalizeCapacityWindowSec(c.in); got != c.want {
			t.Errorf("NormalizeCapacityWindowSec(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestCapacityTTLForWindow(t *testing.T) {
	cases := []struct {
		window int
		want   time.Duration
	}{
		{60, 130 * time.Second},
		{120, 190 * time.Second},
		{300, 370 * time.Second},
		{3600, 3670 * time.Second},
		{30, 130 * time.Second}, // 规整到 60 → ttl=130（即默认）
	}
	for _, c := range cases {
		if got := CapacityTTLForWindow(c.window); got != c.want {
			t.Errorf("CapacityTTLForWindow(%d) = %v, want %v", c.window, got, c.want)
		}
	}
}

func TestBucketCount(t *testing.T) {
	cases := []struct {
		window int
		want   int
	}{
		{60, 2},   // 1 整桶 + 1 边缘
		{120, 3},  // 2 整桶 + 1 边缘
		{180, 4},  // 3 整桶 + 1 边缘
		{3600, 61},
	}
	for _, c := range cases {
		if got := bucketCount(c.window); got != c.want {
			t.Errorf("bucketCount(%d) = %d, want %d", c.window, got, c.want)
		}
	}
}

func TestCapacityWeightedMulti_Window60(t *testing.T) {
	// 60s 窗口 = 1 整桶 cur + 1 边缘桶 prev
	// 与旧 capacityWeighted(cur, prev) = cur + prev * (60-elapsed)/60 等价
	buckets := []int64{100, 50}
	got := capacityWeightedMulti(buckets, 60)
	elapsed := float64(time.Now().Unix() % 60)
	want := 100.0 + 50.0*(60.0-elapsed)/60.0
	// 浮点 epsilon
	if got < want-0.5 || got > want+0.5 {
		t.Errorf("capacityWeightedMulti(60s) = %f, want ≈ %f (elapsed=%f)", got, want, elapsed)
	}
}

func TestCapacityWeightedMulti_Window120(t *testing.T) {
	// 120s 窗口 = 2 整桶 + 1 边缘桶
	buckets := []int64{100, 200, 50}
	got := capacityWeightedMulti(buckets, 120)
	elapsed := float64(time.Now().Unix() % 60)
	want := 100.0 + 200.0 + 50.0*(60.0-elapsed)/60.0
	if got < want-0.5 || got > want+0.5 {
		t.Errorf("capacityWeightedMulti(120s) = %f, want ≈ %f", got, want)
	}
}

func TestCapacityWeightedMulti_NotEnoughBuckets(t *testing.T) {
	// 桶数少于 N+1 时不 panic，按可用桶聚合
	buckets := []int64{10}
	got := capacityWeightedMulti(buckets, 120)
	if got != 10.0 {
		t.Errorf("capacityWeightedMulti(short) = %f, want 10.0", got)
	}
}

func TestBatchGetChannelCapacityCountsStore_OrderingMemoryFallback(t *testing.T) {
	// Redis 不可用时走 mem fallback；验证 channel × bucket 二维映射顺序正确（防 off-by-one）
	// 三个 channel，每个写入不同的 cur/prev 计数
	channelIDs := []int{30001, 30002, 30003}
	minute := currentCapacityMinute()

	memCapacity.mu.Lock()
	for _, id := range channelIDs {
		delete(memCapacity.buckets, id)
	}
	memCapacity.mu.Unlock()

	// 30001: cur=5, prev=3 → 60s 窗口 ≈ 5 + 3*((60-elapsed)/60)
	for i := 0; i < 5; i++ {
		memCapacity.incr(30001, minute)
	}
	for i := 0; i < 3; i++ {
		memCapacity.incr(30001, minute-1)
	}
	// 30002: cur=10, prev=0
	for i := 0; i < 10; i++ {
		memCapacity.incr(30002, minute)
	}
	// 30003: cur=0, prev=7
	for i := 0; i < 7; i++ {
		memCapacity.incr(30003, minute-1)
	}

	counts := BatchGetChannelCapacityCountsStore(channelIDs, 60, CapacityFailOpen)

	if len(counts) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(counts))
	}

	elapsed := float64(time.Now().Unix() % 60)
	weightPrev := (60.0 - elapsed) / 60.0

	check := func(id int, wantCur, wantPrev float64) {
		want := wantCur + wantPrev*weightPrev
		got := counts[id]
		if got < want-0.5 || got > want+0.5 {
			t.Errorf("channel %d count = %f, want ≈ %f", id, got, want)
		}
	}
	check(30001, 5, 3)
	check(30002, 10, 0)
	check(30003, 0, 7)
}

func TestBatchGetChannelCapacityCountsStore_MultiBucket(t *testing.T) {
	// window=180 → 应聚合 4 个桶（3 整桶 + 1 边缘）
	channelID := 30004
	minute := currentCapacityMinute()

	memCapacity.mu.Lock()
	delete(memCapacity.buckets, channelID)
	memCapacity.mu.Unlock()

	// cur=10, prev=20, prev-1=30, prev-2=5（边缘桶，按 elapsed 加权）
	for i := 0; i < 10; i++ {
		memCapacity.incr(channelID, minute)
	}
	for i := 0; i < 20; i++ {
		memCapacity.incr(channelID, minute-1)
	}
	for i := 0; i < 30; i++ {
		memCapacity.incr(channelID, minute-2)
	}
	for i := 0; i < 5; i++ {
		memCapacity.incr(channelID, minute-3)
	}

	counts := BatchGetChannelCapacityCountsStore([]int{channelID}, 180, CapacityFailOpen)

	elapsed := float64(time.Now().Unix() % 60)
	weightOldest := (60.0 - elapsed) / 60.0
	want := 10.0 + 20.0 + 30.0 + 5.0*weightOldest

	got := counts[channelID]
	if got < want-0.5 || got > want+0.5 {
		t.Errorf("multi-bucket aggregation = %f, want ≈ %f (elapsed=%f)", got, want, elapsed)
	}
}

func TestMemCapacityIncrAndGetN(t *testing.T) {
	channelID := 99999 // 测试专用 ID，避免冲突
	minute := currentCapacityMinute()
	// 清掉历史
	memCapacity.mu.Lock()
	delete(memCapacity.buckets, channelID)
	memCapacity.mu.Unlock()

	memCapacity.incr(channelID, minute)
	memCapacity.incr(channelID, minute)
	memCapacity.incr(channelID, minute-1)

	buckets := memCapacityGetN(channelID, minute, 3)
	if buckets[0] != 2 {
		t.Errorf("buckets[0] = %d, want 2", buckets[0])
	}
	if buckets[1] != 1 {
		t.Errorf("buckets[1] = %d, want 1", buckets[1])
	}
	if buckets[2] != 0 {
		t.Errorf("buckets[2] = %d, want 0", buckets[2])
	}
}
