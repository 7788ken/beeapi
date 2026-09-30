package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestModelRemovalStreakIncrResetIsolation 验证「连续 N 次才摘」门槛底层的计数封装：
// 累加返回 1,2,3…；成功清零后重新从 1 起算；不同 (渠道,模型) 互相隔离；非法入参不计数。
// Redis 未启用时走 channel_health 同款进程内 sync.Map 降级，结果确定。
func TestModelRemovalStreakIncrResetIsolation(t *testing.T) {
	const ch = 910001
	model := "streak-test-model"
	ttl := 600 * time.Duration(time.Second)
	ResetModelRemovalStreak(ch, model)
	t.Cleanup(func() { ResetModelRemovalStreak(ch, model) })

	require.Equal(t, int64(1), IncrModelRemovalStreak(ch, model, ttl))
	require.Equal(t, int64(2), IncrModelRemovalStreak(ch, model, ttl))
	require.Equal(t, int64(3), IncrModelRemovalStreak(ch, model, ttl))

	// 清零后重新从 1 起算（成功请求打断连击的落点）
	ResetModelRemovalStreak(ch, model)
	require.Equal(t, int64(1), IncrModelRemovalStreak(ch, model, ttl))

	// 不同渠道隔离
	const chB = 910002
	ResetModelRemovalStreak(chB, model)
	t.Cleanup(func() { ResetModelRemovalStreak(chB, model) })
	require.Equal(t, int64(1), IncrModelRemovalStreak(chB, model, ttl), "另一渠道独立从 1 起算")
	require.Equal(t, int64(2), IncrModelRemovalStreak(ch, model, ttl), "原渠道继续累加，未受 chB 影响")

	// 非法入参：channelId<=0 或 model 为空都不计数、返回 0，且清零不 panic
	require.Equal(t, int64(0), IncrModelRemovalStreak(0, model, ttl))
	require.Equal(t, int64(0), IncrModelRemovalStreak(ch, "", ttl))
	ResetModelRemovalStreak(0, "")
}

// TestClearModelRemovalStreaks 验证渠道恢复时按前缀清掉该渠道**所有模型**的连击计数，且不误伤别的渠道。
func TestClearModelRemovalStreaks(t *testing.T) {
	const ch = 920001
	const otherCh = 920002
	ttl := 600 * time.Duration(time.Second)
	t.Cleanup(func() {
		ClearModelRemovalStreaks(ch)
		ClearModelRemovalStreaks(otherCh)
	})
	ClearModelRemovalStreaks(ch)
	ClearModelRemovalStreaks(otherCh)

	require.Equal(t, int64(1), IncrModelRemovalStreak(ch, "modelA", ttl))
	require.Equal(t, int64(2), IncrModelRemovalStreak(ch, "modelA", ttl))
	require.Equal(t, int64(1), IncrModelRemovalStreak(ch, "modelB", ttl))
	require.Equal(t, int64(1), IncrModelRemovalStreak(otherCh, "modelA", ttl))
	require.Equal(t, int64(2), IncrModelRemovalStreak(otherCh, "modelA", ttl))

	ClearModelRemovalStreaks(ch)

	// 本渠道两个模型都重新从 1 起算
	require.Equal(t, int64(1), IncrModelRemovalStreak(ch, "modelA", ttl), "modelA 应被清")
	require.Equal(t, int64(1), IncrModelRemovalStreak(ch, "modelB", ttl), "modelB 应被清")
	// 另一渠道不受影响：继续从 3 累加
	require.Equal(t, int64(3), IncrModelRemovalStreak(otherCh, "modelA", ttl), "另一渠道不得被误清")
}

// TestClearChannelHealthRuntimeClearsModelStreaks 验证渠道恢复/手动启用走的 ClearChannelHealthRuntime
// 会把模型连击计数一并清掉，避免“刚启用又被摘”。
func TestClearChannelHealthRuntimeClearsModelStreaks(t *testing.T) {
	const ch = 920003
	model := "claude-opus-4-6"
	ttl := 600 * time.Duration(time.Second)
	t.Cleanup(func() { ClearModelRemovalStreaks(ch) })
	ClearModelRemovalStreaks(ch)

	require.Equal(t, int64(1), IncrModelRemovalStreak(ch, model, ttl))
	require.Equal(t, int64(2), IncrModelRemovalStreak(ch, model, ttl))

	ClearChannelHealthRuntime(ch)

	require.Equal(t, int64(1), IncrModelRemovalStreak(ch, model, ttl), "恢复后计数应清零，避免刚启用又被摘")
}
