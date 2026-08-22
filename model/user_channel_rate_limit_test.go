package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ensureUserChannelRateLimitTable 在 task_cas_test.go 的 TestMain 没列出该表时按需建表。
// 走独立 helper 让本测试文件自管 schema，不污染其它测试。
func ensureUserChannelRateLimitTable(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&UserChannelRateLimit{}))
	// 同时确保 user/channel 表存在（外键引用预检需要）
	require.NoError(t, DB.AutoMigrate(&User{}, &Channel{}))
}

func insertTestUser(t *testing.T, id int) {
	t.Helper()
	u := &User{Id: id, Username: "u" + itoa(id), Status: common.UserStatusEnabled, AffCode: "aff_" + itoa(id)}
	require.NoError(t, DB.Create(u).Error)
}

func insertTestChannel(t *testing.T, id int) {
	t.Helper()
	ch := &Channel{Id: id, Name: "c" + itoa(id), Status: common.ChannelStatusEnabled}
	require.NoError(t, DB.Create(ch).Error)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func cleanupRpmTables(t *testing.T) {
	t.Helper()
	DB.Exec("DELETE FROM user_channel_rate_limit")
	DB.Exec("DELETE FROM users")
	DB.Exec("DELETE FROM channels")
}

func TestUserChannelRateLimit_CreateAndGet(t *testing.T) {
	ensureUserChannelRateLimitTable(t)
	cleanupRpmTables(t)
	insertTestUser(t, 101)
	insertTestChannel(t, 201)

	rule := &UserChannelRateLimit{
		UserId:     101,
		ChannelId:  201,
		BaseRpm:    10,
		JitterPct:  0.3,
		MinDelayMs: 100,
		MaxDelayMs: 400,
		Enabled:    true,
	}
	require.NoError(t, CreateUserChannelRateLimit(rule))
	assert.True(t, rule.Id > 0)

	got, err := GetUserChannelRateLimit(101, 201)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 10, got.BaseRpm)
	assert.InDelta(t, 0.3, got.JitterPct, 0.001)
	assert.Equal(t, 100, got.MinDelayMs)
	assert.Equal(t, 400, got.MaxDelayMs)
	assert.True(t, got.Enabled)
}

func TestUserChannelRateLimit_GetMissing(t *testing.T) {
	ensureUserChannelRateLimitTable(t)
	cleanupRpmTables(t)

	got, err := GetUserChannelRateLimit(999, 888)
	require.NoError(t, err)
	assert.Nil(t, got, "missing rule should return (nil, nil), not error")
}

func TestUserChannelRateLimit_Validation(t *testing.T) {
	ensureUserChannelRateLimitTable(t)
	cleanupRpmTables(t)
	insertTestUser(t, 102)
	insertTestChannel(t, 202)

	cases := []struct {
		name string
		rule UserChannelRateLimit
		want string
	}{
		{"base_rpm zero", UserChannelRateLimit{UserId: 102, ChannelId: 202, BaseRpm: 0}, "base_rpm"},
		{"jitter > 1", UserChannelRateLimit{UserId: 102, ChannelId: 202, BaseRpm: 5, JitterPct: 1.5}, "jitter_pct"},
		{"jitter < 0", UserChannelRateLimit{UserId: 102, ChannelId: 202, BaseRpm: 5, JitterPct: -0.1}, "jitter_pct"},
		{"min > max", UserChannelRateLimit{UserId: 102, ChannelId: 202, BaseRpm: 5, MinDelayMs: 600, MaxDelayMs: 100}, "min_delay_ms"},
		{"user missing", UserChannelRateLimit{UserId: 9999, ChannelId: 202, BaseRpm: 5}, "user"},
		{"channel missing", UserChannelRateLimit{UserId: 102, ChannelId: 9999, BaseRpm: 5}, "channel"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CreateUserChannelRateLimit(&tc.rule)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestUserChannelRateLimit_UpdatePartial(t *testing.T) {
	ensureUserChannelRateLimitTable(t)
	cleanupRpmTables(t)
	insertTestUser(t, 103)
	insertTestChannel(t, 203)

	rule := &UserChannelRateLimit{UserId: 103, ChannelId: 203, BaseRpm: 5, JitterPct: 0.2, MinDelayMs: 50, MaxDelayMs: 500, Enabled: true}
	require.NoError(t, CreateUserChannelRateLimit(rule))

	// 只更新 base_rpm
	require.NoError(t, UpdateUserChannelRateLimit(rule.Id, 203, map[string]any{"base_rpm": 20}))
	got, err := GetUserChannelRateLimit(103, 203)
	require.NoError(t, err)
	assert.Equal(t, 20, got.BaseRpm)
	assert.InDelta(t, 0.2, got.JitterPct, 0.001, "jitter 没传不应被改")

	// 无效更新：jitter > 1
	err = UpdateUserChannelRateLimit(rule.Id, 203, map[string]any{"jitter_pct": float32(2.0)})
	require.Error(t, err)

	// 跨渠道更新应该 404
	err = UpdateUserChannelRateLimit(rule.Id, 999, map[string]any{"base_rpm": 50})
	require.Error(t, err)
}

func TestUserChannelRateLimit_Delete(t *testing.T) {
	ensureUserChannelRateLimitTable(t)
	cleanupRpmTables(t)
	insertTestUser(t, 104)
	insertTestChannel(t, 204)

	rule := &UserChannelRateLimit{UserId: 104, ChannelId: 204, BaseRpm: 7}
	require.NoError(t, CreateUserChannelRateLimit(rule))

	require.NoError(t, DeleteUserChannelRateLimit(rule.Id, 204))
	got, err := GetUserChannelRateLimit(104, 204)
	require.NoError(t, err)
	assert.Nil(t, got)

	// 二次删除应报 not found
	err = DeleteUserChannelRateLimit(rule.Id, 204)
	require.Error(t, err)
}

func TestUserChannelRateLimit_ListByChannel(t *testing.T) {
	ensureUserChannelRateLimitTable(t)
	cleanupRpmTables(t)
	insertTestUser(t, 201)
	insertTestUser(t, 202)
	insertTestChannel(t, 301)
	insertTestChannel(t, 302)

	require.NoError(t, CreateUserChannelRateLimit(&UserChannelRateLimit{UserId: 201, ChannelId: 301, BaseRpm: 5}))
	require.NoError(t, CreateUserChannelRateLimit(&UserChannelRateLimit{UserId: 202, ChannelId: 301, BaseRpm: 8}))
	require.NoError(t, CreateUserChannelRateLimit(&UserChannelRateLimit{UserId: 201, ChannelId: 302, BaseRpm: 3}))

	items, err := ListUserChannelRateLimitsByChannel(301)
	require.NoError(t, err)
	assert.Len(t, items, 2)
	for _, it := range items {
		assert.EqualValues(t, 301, it.ChannelId)
	}
}

func TestUserChannelRateLimit_UniqueConstraint(t *testing.T) {
	ensureUserChannelRateLimitTable(t)
	cleanupRpmTables(t)
	insertTestUser(t, 401)
	insertTestChannel(t, 501)

	require.NoError(t, CreateUserChannelRateLimit(&UserChannelRateLimit{UserId: 401, ChannelId: 501, BaseRpm: 5}))
	// 第二条 (user_id=401, channel_id=501) 必须撞唯一索引
	err := CreateUserChannelRateLimit(&UserChannelRateLimit{UserId: 401, ChannelId: 501, BaseRpm: 10})
	require.Error(t, err)
}
