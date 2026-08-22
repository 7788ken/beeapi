package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// runtime_metrics 的排序逻辑纯函数测试（不依赖 DB）。
// 关键：sort 是稳定排序；topN 截断；超过 10 个的数据正确截断。

func TestSortTopUsersByRpm(t *testing.T) {
	items := []TopUserItem{
		{UserId: 1, Rpm: 5.0, Cost: 10},
		{UserId: 2, Rpm: 10.0, Cost: 5},
		{UserId: 3, Rpm: 3.0, Cost: 100},
	}
	got := sortTopUsersByRpm(items)
	assert.Len(t, got, 3)
	assert.Equal(t, 2, got[0].UserId, "RPM 最高的应该排第一")
	assert.Equal(t, 1, got[1].UserId)
	assert.Equal(t, 3, got[2].UserId)
}

func TestSortTopUsersByCost(t *testing.T) {
	items := []TopUserItem{
		{UserId: 1, Rpm: 5.0, Cost: 10},
		{UserId: 2, Rpm: 10.0, Cost: 5},
		{UserId: 3, Rpm: 3.0, Cost: 100},
	}
	got := sortTopUsersByCost(items)
	assert.Equal(t, 3, got[0].UserId, "Cost 最高的应该排第一")
	assert.Equal(t, 1, got[1].UserId)
	assert.Equal(t, 2, got[2].UserId)
}

func TestSortTopChannelsTruncateToTopN(t *testing.T) {
	items := make([]TopChannelItem, 30)
	for i := 0; i < 30; i++ {
		items[i] = TopChannelItem{ChannelId: i, Rpm: float64(30 - i)}
	}
	got := sortTopChannelsByRpm(items)
	assert.Equal(t, runtimeMetricsTopN, len(got), "应该截断到 Top 10")
	assert.Equal(t, 0, got[0].ChannelId, "RPM 最高的（30）排第一")
}

func TestSortStableForSameScore(t *testing.T) {
	// 相同 cost 时，sort.SliceStable 应该保持原始顺序
	items := []TopUserItem{
		{UserId: 1, Cost: 100},
		{UserId: 2, Cost: 100},
		{UserId: 3, Cost: 100},
	}
	got := sortTopUsersByCost(items)
	assert.Equal(t, 1, got[0].UserId)
	assert.Equal(t, 2, got[1].UserId)
	assert.Equal(t, 3, got[2].UserId)
}

func TestSortHandlesEmptyInput(t *testing.T) {
	assert.Empty(t, sortTopUsersByRpm(nil))
	assert.Empty(t, sortTopGroupsByCost([]TopGroupItem{}))
	assert.Empty(t, sortTopChannelsByCost(nil))
	assert.Empty(t, sortTopGroupChannelsByRpm(nil))
}

func TestSortDoesNotMutateInput(t *testing.T) {
	// 排序函数应该拷贝再排，避免污染上游传入的切片
	items := []TopUserItem{
		{UserId: 1, Rpm: 1.0},
		{UserId: 2, Rpm: 2.0},
		{UserId: 3, Rpm: 3.0},
	}
	original := []TopUserItem{
		{UserId: 1, Rpm: 1.0},
		{UserId: 2, Rpm: 2.0},
		{UserId: 3, Rpm: 3.0},
	}
	_ = sortTopUsersByRpm(items)
	assert.Equal(t, original, items, "原切片不应被修改")
}
