package service

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// FU-7 端到端测试 setup：复用 service 包 TestMain 已建好的共享 SQLite DB。
// 仅补 ChannelHealthEvent 表（TestMain 已 migrate Channel + 其他业务表）。
// t.Cleanup 清掉本测试造的 channel 行 + sync.Map fallback，避免影响后续测试。
var setupHealthTestDBOnce sync.Once

func setupChannelHealthTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	require.NotNil(t, model.DB, "TestMain should have initialized model.DB")

	// 全包仅初始化一次 ChannelHealthEvent 表
	setupHealthTestDBOnce.Do(func() {
		require.NoError(t, model.DB.AutoMigrate(&model.ChannelHealthEvent{}))
	})

	t.Cleanup(func() {
		// 清理 fallback 状态机内存（避免 streak/lock/cooldown 跨测试残留）
		fallbackStreaks.Range(func(k, _ interface{}) bool { fallbackStreaks.Delete(k); return true })
		fallbackLocks.Range(func(k, _ interface{}) bool { fallbackLocks.Delete(k); return true })
		fallbackCooldowns.Range(func(k, _ interface{}) bool { fallbackCooldowns.Delete(k); return true })
		// 清掉本测试创造的 channels + audit events，避免污染共享 DB
		// （task_billing_test 等其他测试也用 channel id=1..N，会撞主键）
		model.DB.Exec("DELETE FROM channels")
		model.DB.Exec("DELETE FROM channel_health_events")
	})

	return model.DB
}

// makeTestChannel 在测试 DB 里建一个渠道。
func makeTestChannel(t *testing.T, db *gorm.DB, id int, priority int64, weight uint) *model.Channel {
	t.Helper()
	autoBan := 1
	ch := &model.Channel{
		Id:       id,
		Type:     1,
		Name:     fmt.Sprintf("test-ch-%d", id),
		Status:   common.ChannelStatusEnabled,
		Priority: &priority,
		Weight:   &weight,
		AutoBan:  &autoBan,
		Models:   "gpt-4",
		Group:    "default",
	}
	require.NoError(t, db.Create(ch).Error)
	return ch
}

// reloadChannel 从 DB 重新加载渠道，避免读到内存里的旧副本。
func reloadChannel(t *testing.T, id int) *model.Channel {
	t.Helper()
	var ch model.Channel
	require.NoError(t, model.DB.First(&ch, id).Error)
	return &ch
}

// makeServerError 构造 5xx 渠道级错误。
func makeServerError(statusCode int, msg string) *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeBadResponseStatusCode, statusCode)
}

// withHealthConfig 临时覆盖 ChannelHealthConfig，测试结束自动恢复默认值。
// 默认关闭 DemoteCooldownSec（=0），保持原有"无降级冷却"语义；
// 想验证冷却行为的测试可在 mutate 内显式赋值。
func withHealthConfig(t *testing.T, mutate func(*operation_setting.ChannelHealthConfig)) {
	t.Helper()
	cfg := operation_setting.GetChannelHealthConfig()
	original := *cfg // 浅拷贝
	cfg.DemoteCooldownSec = 0
	mutate(cfg)
	t.Cleanup(func() { *cfg = original })
}

func TestApplyDegrade(t *testing.T) {
	t.Parallel()

	cfg := &operation_setting.ChannelHealthConfig{
		MaxDegradeLevel: 10,
		MinWeightFactor: 0.05,
	}
	cfg.Normalize()

	cases := []struct {
		name      string
		origP     int64
		origW     uint
		level     int
		wantP     int64
		wantW     uint
	}{
		{"L1 with weight=10", 100, 10, 1, 99, 9},
		{"L1 with weight=1 stays at 1", 50, 1, 1, 49, 1},
		{"L1 with weight=0 stays at 0", 50, 0, 1, 49, 0},
		{"L2 with weight=10", 100, 10, 2, 98, 8},
		{"L2 with weight=1 stays at 1", 50, 1, 2, 48, 1},
		{"L2 with weight=4", 50, 4, 2, 48, 3},
		{"L2 with weight=0 stays at 0", 50, 0, 2, 48, 0},
		{"L10 with weight=10 hits min factor", 100, 10, 10, 90, 1},
		{"L0 returns original", 50, 7, 0, 50, 7},
		{"negative priority allowed", 0, 0, 2, -2, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotP, gotW := applyDegrade(tc.origP, tc.origW, tc.level, cfg)
			require.Equal(t, tc.wantP, gotP, "priority")
			require.Equal(t, tc.wantW, gotW, "weight")
		})
	}
}

func TestApplyDegrade_NoCascade(t *testing.T) {
	// L1 → L2 必须基于 originalW 重算，不能在 L1 weight 上再砍（雪崩）。
	t.Parallel()
	cfg := &operation_setting.ChannelHealthConfig{MaxDegradeLevel: 10, MinWeightFactor: 0.05}
	cfg.Normalize()
	_, w := applyDegrade(100, 10, 2, cfg)
	// L2 factor=0.81, weight=floor(10*0.81)=8
	require.Equal(t, uint(8), w, "L2 weight 必须基于 originalW=10 算，不是基于 L1 后的值")
}

func TestApplyDegrade_ZeroMinWeightFactor(t *testing.T) {
	t.Parallel()
	cfg := &operation_setting.ChannelHealthConfig{MaxDegradeLevel: 10, MinWeightFactor: 0}
	cfg.Normalize()

	// L10 with MinWeightFactor=0: weight 应该降到 0（完全排除真实流量）
	_, w := applyDegrade(100, 10, 10, cfg)
	require.Equal(t, uint(0), w, "MinWeightFactor=0 时 L10 weight 应为 0")

	// L1 with MinWeightFactor=0: factor=0.9, weight=floor(10*0.9)=9（仍 > 0）
	_, w1 := applyDegrade(100, 10, 1, cfg)
	require.Equal(t, uint(9), w1, "L1 weight 仍应正常计算")

	// L5 with weight=1, MinWeightFactor=0: factor 很小，weight 应降到 0
	_, w5 := applyDegrade(100, 1, 5, cfg)
	require.Equal(t, uint(0), w5, "MinWeightFactor=0 + 低 weight 应降到 0")
}

func TestIsKeyFatalError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  *types.NewAPIError
		want bool
	}{
		{"nil err", nil, false},
		{"401", types.NewErrorWithStatusCode(errors.New("nope"), types.ErrorCodeBadResponseStatusCode, 401), true},
		{"403", types.NewErrorWithStatusCode(errors.New("forbidden"), types.ErrorCodeBadResponseStatusCode, 403), true},
		{"500 not key", types.NewErrorWithStatusCode(errors.New("server error"), types.ErrorCodeBadResponseStatusCode, 500), false},
		{"invalid api key keyword 200 sc", types.NewErrorWithStatusCode(errors.New("Invalid API Key provided"), types.ErrorCodeBadResponseStatusCode, 200), true},
		{"unauthorized keyword", types.NewErrorWithStatusCode(errors.New("Unauthorized request"), types.ErrorCodeBadResponseStatusCode, 200), true},
		{"plain rate limit", types.NewErrorWithStatusCode(errors.New("Too many requests"), types.ErrorCodeBadResponseStatusCode, 429), false},
		{"incorrect api key", types.NewErrorWithStatusCode(errors.New("Incorrect API key provided: sk-xxx"), types.ErrorCodeBadResponseStatusCode, 400), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isKeyFatalError(tc.err))
		})
	}
}

func TestIsCountableError(t *testing.T) {
	t.Parallel()

	cfg := &operation_setting.ChannelHealthConfig{
		Count429AsError: true,
	}
	cfgNo429 := &operation_setting.ChannelHealthConfig{
		Count429AsError: false,
	}

	cases := []struct {
		name string
		cfg  *operation_setting.ChannelHealthConfig
		err  *types.NewAPIError
		want bool
	}{
		{"nil err", cfg, nil, false},
		{"500", cfg, types.NewErrorWithStatusCode(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, 500), true},
		{"502", cfg, types.NewErrorWithStatusCode(errors.New("bad gateway"), types.ErrorCodeBadResponseStatusCode, 502), true},
		{"401 key fatal", cfg, types.NewErrorWithStatusCode(errors.New("unauthorized"), types.ErrorCodeBadResponseStatusCode, 401), true},
		{"429 count when enabled", cfg, types.NewErrorWithStatusCode(errors.New("rate limited"), types.ErrorCodeBadResponseStatusCode, 429), true},
		{"429 ignored when disabled", cfgNo429, types.NewErrorWithStatusCode(errors.New("rate limited"), types.ErrorCodeBadResponseStatusCode, 429), false},
		{"400 client err", cfg, types.NewErrorWithStatusCode(errors.New("bad param"), types.ErrorCodeBadResponseStatusCode, 400), false},
		{"skip retry", cfg, types.NewErrorWithStatusCode(errors.New("oversized"), types.ErrorCodeBadResponseStatusCode, 413, types.ErrOptionWithSkipRetry()), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isCountableError(tc.err, tc.cfg))
		})
	}
}

func TestIsTransientOverloadError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  *types.NewAPIError
		want bool
	}{
		{"nil", nil, false},
		{"503 cpu overloaded", makeServerError(503, "system cpu overloaded (current: 92.2%, threshold: 90%)"), true},
		{"503 memory overloaded", makeServerError(503, "system memory overloaded (current: 85%, threshold: 80%)"), true},
		{"503 no available channel", makeServerError(503, "No available channel for model claude-opus-4-7 under group claude-aws (distributor)"), true},
		{"503 generic", makeServerError(503, "Service Unavailable"), false},
		{"500 cpu keyword but wrong status", makeServerError(500, "system cpu overloaded"), false},
		{"502 no available channel wrong status", makeServerError(502, "No available channel"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isTransientOverloadError(tc.err))
		})
	}
}

func TestIsCountableError_TransientOverloadExcluded(t *testing.T) {
	t.Parallel()

	cfg := &operation_setting.ChannelHealthConfig{Count429AsError: true}

	require.False(t, isCountableError(makeServerError(503, "system cpu overloaded (current: 94%)"), cfg),
		"503 cpu overloaded should NOT be countable")
	require.False(t, isCountableError(makeServerError(503, "No available channel for model gpt-4"), cfg),
		"503 no available channel should NOT be countable")
	require.True(t, isCountableError(makeServerError(500, "Internal server error"), cfg),
		"500 should still be countable")
	require.True(t, isCountableError(makeServerError(503, "some other 503 error"), cfg),
		"generic 503 should still be countable in fallback mode")
}

func TestFallbackIncr_Streak(t *testing.T) {
	t.Parallel()
	// 验证进程内 fallback 计数行为：连续 INCR 单调递增，DEL 后归零
	key := "test:fallback:streak:1"
	defer fallbackDel(key)

	ttl := 10 * time.Second
	for i := int64(1); i <= 5; i++ {
		got := fallbackIncr(key, ttl)
		require.Equal(t, i, got)
	}
	fallbackDel(key)
	require.Equal(t, int64(1), fallbackIncr(key, ttl))
}

func TestFallbackTryLock(t *testing.T) {
	t.Parallel()
	key := "test:fallback:lock:1"
	defer fallbackDel(key)

	require.True(t, fallbackTryLock(key, 50*time.Millisecond), "first acquire should succeed")
	require.False(t, fallbackTryLock(key, 50*time.Millisecond), "second acquire while held should fail")

	time.Sleep(60 * time.Millisecond)
	require.True(t, fallbackTryLock(key, 50*time.Millisecond), "after expiry should re-acquire")
}

// ─────────────────────────────────────────────────────────────────────────────
// FU-7: 状态机端到端测试（DB-backed）
// ─────────────────────────────────────────────────────────────────────────────

// TestStateMachine_DemoteToL1 验证连续 N 次 5xx 错误后渠道从 L0 进入 L1
// （priority -1, weight×0.5）。
func TestStateMachine_DemoteToL1(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.DegradeThreshold = 2
		cfg.BaseDegradeThreshold = 2
		cfg.LevelStepThreshold = 3
		cfg.L2Threshold = 5
		cfg.DisableThreshold = 10
		cfg.UpgradeThreshold = 20
		cfg.Count429AsError = true
	})
	common.AutomaticDisableChannelEnabled = true // ShouldDisableChannel 入口需要

	makeTestChannel(t, model.DB, 1, 10, 20)

	// 2 次 5xx → L1
	for i := 0; i < 2; i++ {
		RecordChannelResult(1, "", makeServerError(500, "upstream"), -1)
	}

	ch := reloadChannel(t, 1)
	require.Equal(t, 1, common.DerefIntOr(ch.DegradeLevel, 0), "degrade_level should be 1")
	require.Equal(t, int64(9), common.DerefInt64Or(ch.Priority, 0), "priority should be 10-1=9")
	require.Equal(t, uint(18), common.DerefUintOr(ch.Weight, 0), "weight should be floor(20*0.905)=18")
	require.Equal(t, int64(10), common.DerefInt64Or(ch.OriginalPriority, 0), "original_priority snapshot")
	require.Equal(t, uint(20), common.DerefUintOr(ch.OriginalWeight, 0), "original_weight snapshot")
}

// TestStateMachine_DemoteL1ToL2 验证 L1→L2 时 weight 基于 originalW 重算（不级联）。
// 关键约束：weight 公式必须基于 originalW=20，得 floor(20*0.81)=16，不是基于 L1 后的 weight=18 再算。
func TestStateMachine_DemoteL1ToL2(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.DegradeThreshold = 2
		cfg.BaseDegradeThreshold = 2
		cfg.LevelStepThreshold = 3
		cfg.L2Threshold = 5
		cfg.DisableThreshold = 10
		cfg.UpgradeThreshold = 20
	})
	common.AutomaticDisableChannelEnabled = true

	makeTestChannel(t, model.DB, 2, 10, 20)

	// 5 次 5xx → L2
	for i := 0; i < 5; i++ {
		RecordChannelResult(2, "", makeServerError(503, "service unavailable"), -1)
	}

	ch := reloadChannel(t, 2)
	require.Equal(t, 2, common.DerefIntOr(ch.DegradeLevel, 0))
	require.Equal(t, int64(8), common.DerefInt64Or(ch.Priority, 0), "priority should be 10-2=8")
	require.Equal(t, uint(16), common.DerefUintOr(ch.Weight, 0), "weight should be floor(20*0.81)=16 (NOT based on L1 weight)")
	// snapshot 不变（首次降级时已写）
	require.Equal(t, int64(10), common.DerefInt64Or(ch.OriginalPriority, 0))
	require.Equal(t, uint(20), common.DerefUintOr(ch.OriginalWeight, 0))
}

// TestStateMachine_DemoteCooldownBlocksCascade 验证 DemoteCooldownSec > 0 时
// 窗口内只允许 1 次降级，瞬时风暴不会把 L0 直接打到 L2。
// 但禁用阈值（DisableThreshold）仍然按原逻辑生效，不受 cooldown 约束。
func TestStateMachine_DemoteCooldownBlocksCascade(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.DegradeThreshold = 2
		cfg.BaseDegradeThreshold = 2
		cfg.LevelStepThreshold = 3
		cfg.L2Threshold = 5
		cfg.DisableThreshold = 100 // 提高，避免覆盖到禁用路径
		cfg.UpgradeThreshold = 20
		cfg.DemoteCooldownSec = 60 // 启用 60s 降级冷却
	})
	common.AutomaticDisableChannelEnabled = true

	makeTestChannel(t, model.DB, 11, 10, 20)

	// 5 次连续 5xx：原本应该一路走到 L2，但被 cooldown 拦在 L1
	for i := 0; i < 5; i++ {
		RecordChannelResult(11, "", makeServerError(500, "burst"), -1)
	}

	ch := reloadChannel(t, 11)
	require.Equal(t, 1, common.DerefIntOr(ch.DegradeLevel, 0), "cooldown 应只允许降到 L1")
	require.Equal(t, int64(9), common.DerefInt64Or(ch.Priority, 0), "priority 仍是 L1 公式 10-1=9")
	require.Equal(t, uint(18), common.DerefUintOr(ch.Weight, 0), "weight 仍是 L1 公式 floor(20*0.905)=18")
}

// TestStateMachine_DemoteCooldownStreakAccumulates 验证 cooldown 窗口内被拦下的 demote
// 不会清空 err_streak，下一窗口（或窗口结束后）的错误能直接命中更高阈值——
// 这是 cooldown 的设计约束（KISS：节流而非"渐进降级"）。
//
// 同时验证：cooldown 期间继续累计的错误不会异常 panic、不会重复触发降级。
func TestStateMachine_DemoteCooldownStreakAccumulates(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.DegradeThreshold = 2
		cfg.BaseDegradeThreshold = 2
		cfg.LevelStepThreshold = 3
		cfg.L2Threshold = 5
		cfg.DisableThreshold = 100
		cfg.UpgradeThreshold = 20
		cfg.DemoteCooldownSec = 60
	})
	common.AutomaticDisableChannelEnabled = true

	makeTestChannel(t, model.DB, 13, 10, 20)

	// 触发 L1 + 继续累积到 L2 阈值之上
	for i := 0; i < 8; i++ {
		RecordChannelResult(13, "", makeServerError(500, "burst"), -1)
	}

	// 当前应停在 L1（cooldown 拦下了 L2 case）
	ch := reloadChannel(t, 13)
	require.Equal(t, 1, common.DerefIntOr(ch.DegradeLevel, 0), "cooldown 应只允许降到 L1")

	// 验证 err_streak 仍在累积——直接读 fallback（测试环境 Redis 关闭）
	rawStreak, ok := fallbackStreaks.Load(keyErrStreak(13))
	require.True(t, ok, "err_streak 键应存在")
	require.GreaterOrEqual(t, rawStreak.(*fallbackEntry).val, int64(8), "err_streak 应继续累积，不被 cooldown 截断")
}

// TestStateMachine_DemoteCooldownExpires 验证 cooldown 过期后下一次降级能正常触发。
// 用 1s cooldown + 短暂 sleep（测试容忍度），避免引入 clock 注入侵入业务代码。
func TestStateMachine_DemoteCooldownExpires(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.DegradeThreshold = 2
		cfg.BaseDegradeThreshold = 2
		cfg.LevelStepThreshold = 3
		cfg.L2Threshold = 5
		cfg.DisableThreshold = 100
		cfg.UpgradeThreshold = 20
		cfg.DemoteCooldownSec = 1 // 1s 短窗口便于测试
	})
	common.AutomaticDisableChannelEnabled = true

	makeTestChannel(t, model.DB, 14, 10, 20)

	// 第一波：5 次错误 → L1（L2 被 cooldown 拦）
	for i := 0; i < 5; i++ {
		RecordChannelResult(14, "", makeServerError(500, "first wave"), -1)
	}
	ch := reloadChannel(t, 14)
	require.Equal(t, 1, common.DerefIntOr(ch.DegradeLevel, 0), "第一波后应停在 L1")

	// 等 cooldown 过期
	time.Sleep(1100 * time.Millisecond)

	// 再来一次错误，streak 已 ≥5，cooldown 已过，命中 L2 case
	RecordChannelResult(14, "", makeServerError(500, "second wave"), -1)
	ch = reloadChannel(t, 14)
	require.Equal(t, 2, common.DerefIntOr(ch.DegradeLevel, 0), "cooldown 过期后应能继续降级到 L2")
}

// TestIsCooldownActive_RedisErrorFailsClosed 验证 P0-3 修复：
// Redis 启用但 EXISTS 调用失败时，isCooldownActive 必须 fail-closed 返回 true，
// 避免冷却失效让降级风暴穿透击穿渠道。
//
// 实现：临时把 common.RDB 替换为指向不可达端口的 client，
// EXISTS 调用必失败（连接拒绝 / dial timeout），观察返回值。
func TestIsCooldownActive_RedisErrorFailsClosed(t *testing.T) {
	// 备份并恢复全局 Redis 状态
	originalRDB := common.RDB
	originalEnabled := common.RedisEnabled
	defer func() {
		common.RDB = originalRDB
		common.RedisEnabled = originalEnabled
	}()

	// 指向 127.0.0.1:1（reserved port，必拒绝连接），DialTimeout 短一点加速测试
	badClient := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 100 * time.Millisecond,
		ReadTimeout: 100 * time.Millisecond,
		MaxRetries:  -1, // 关掉自动重试，直接报错
	})
	defer badClient.Close()

	common.RDB = badClient
	common.RedisEnabled = true

	// 调用必走 Redis 路径（RedisEnabled=true 且 RDB 非 nil），EXISTS 必失败
	// → fail-closed 返回 true（保守认为冷却中）
	got := isCooldownActive(keyDemoteCooldown(99999))
	require.True(t, got, "Redis EXISTS 失败时应 fail-closed 返回 true，否则冷却会穿透")
}

// TestStateMachine_DemoteCooldownDoesNotBlockDisable 验证 cooldown 不影响禁用动作：
// 即便降级被冷却拦下，连续错误打满 DisableThreshold 仍会立即禁用渠道。
func TestStateMachine_DemoteCooldownDoesNotBlockDisable(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.DegradeThreshold = 2
		cfg.BaseDegradeThreshold = 2
		cfg.LevelStepThreshold = 3
		cfg.L2Threshold = 5
		cfg.DisableThreshold = 10
		cfg.UpgradeThreshold = 20
		cfg.DemoteCooldownSec = 60
	})
	common.AutomaticDisableChannelEnabled = true

	makeTestChannel(t, model.DB, 12, 10, 20)

	// 10 次连续 5xx：L2 升级被 cooldown 拦下，但 disable 阈值仍然照常生效
	for i := 0; i < 10; i++ {
		RecordChannelResult(12, "", makeServerError(503, "down"), -1)
	}

	ch := reloadChannel(t, 12)
	require.Equal(t, common.ChannelStatusAutoDisabled, ch.Status, "DisableThreshold 不受 cooldown 限制")
}

// TestStateMachine_HitDisableThreshold 验证连续 N 次错后整渠道 AutoDisabled。
func TestStateMachine_HitDisableThreshold(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.DegradeThreshold = 2
		cfg.BaseDegradeThreshold = 2
		cfg.LevelStepThreshold = 3
		cfg.L2Threshold = 5
		cfg.DisableThreshold = 10
		cfg.UpgradeThreshold = 20
		cfg.RebounceProtectionMinutes = 30
	})
	common.AutomaticDisableChannelEnabled = true

	makeTestChannel(t, model.DB, 3, 10, 20)

	// 10 次 5xx → AutoDisabled
	for i := 0; i < 10; i++ {
		RecordChannelResult(3, "", makeServerError(502, "bad gateway"), -1)
	}

	ch := reloadChannel(t, 3)
	require.Equal(t, common.ChannelStatusAutoDisabled, ch.Status, "status should be AutoDisabled")
	// 反弹保护写了 LastDisabledAt
	require.NotZero(t, common.DerefInt64Or(ch.LastDisabledAt, 0))
	require.Equal(t, 1, common.DerefIntOr(ch.RebounceCount, 0))
}

// TestStateMachine_SuccessClearsErrStreak 验证一次成功清掉 err_streak，连续不会触发降级。
func TestStateMachine_SuccessClearsErrStreak(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.DegradeThreshold = 2
		cfg.BaseDegradeThreshold = 2
		cfg.LevelStepThreshold = 3
		cfg.L2Threshold = 5
		cfg.DisableThreshold = 10
		cfg.UpgradeThreshold = 20
	})
	common.AutomaticDisableChannelEnabled = true

	makeTestChannel(t, model.DB, 4, 10, 20)

	// 错-错-成功-错（间断错误，不应触发 L1）
	RecordChannelResult(4, "", makeServerError(500, "x"), -1)
	RecordChannelResult(4, "", nil, -1) // 成功，清 err_streak
	RecordChannelResult(4, "", makeServerError(500, "x"), -1)

	ch := reloadChannel(t, 4)
	require.Equal(t, 0, common.DerefIntOr(ch.DegradeLevel, 0), "should still be L0 (intermittent error)")
}

// TestStateMachine_KeyFatalErrorDoesNotCountToChannelStreak 验证多 key 渠道下
// 401/403 错误只标 key disabled，不计入渠道 streak。
func TestStateMachine_KeyFatalErrorDoesNotCountToChannelStreak(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.DegradeThreshold = 2
		cfg.BaseDegradeThreshold = 2
		cfg.LevelStepThreshold = 3
		cfg.L2Threshold = 5
		cfg.DisableThreshold = 10
		cfg.UpgradeThreshold = 20
	})
	common.AutomaticDisableChannelEnabled = true

	ch := makeTestChannel(t, model.DB, 5, 10, 20)
	ch.ChannelInfo.IsMultiKey = true
	require.NoError(t, model.DB.Save(ch).Error)

	// 5 次 401（如果计入会触发 L2）—— 应仅触发 key disable，不动渠道
	for i := 0; i < 5; i++ {
		RecordChannelResult(5, "key-1", makeServerError(401, "unauthorized"), -1)
	}

	got := reloadChannel(t, 5)
	require.Equal(t, 0, common.DerefIntOr(got.DegradeLevel, 0), "channel should still be L0")
}

// TestStateMachine_4xxIgnored 验证 4xx 客户端错误（参数错误等）不计入 streak。
func TestStateMachine_4xxIgnored(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.DegradeThreshold = 2
		cfg.BaseDegradeThreshold = 2
		cfg.LevelStepThreshold = 3
		cfg.L2Threshold = 5
		cfg.DisableThreshold = 10
		cfg.UpgradeThreshold = 20
	})
	common.AutomaticDisableChannelEnabled = true

	makeTestChannel(t, model.DB, 6, 10, 20)

	// 5 次 400 / 422 都不计入
	for i := 0; i < 5; i++ {
		RecordChannelResult(6, "", makeServerError(400, "bad request"), -1)
	}

	ch := reloadChannel(t, 6)
	require.Equal(t, 0, common.DerefIntOr(ch.DegradeLevel, 0), "4xx should not trigger demote")
}

// TestStateMachine_UpgradePath 验证 L2→L1 升级（连续 ok_streak 达 upgrade_threshold）
// 且 weight 基于 originalW 重算（不是从 L2 翻倍）。
func TestStateMachine_UpgradePath(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.DegradeThreshold = 2
		cfg.BaseDegradeThreshold = 2
		cfg.LevelStepThreshold = 3
		cfg.L2Threshold = 5
		cfg.DisableThreshold = 100 // 防止意外触发 disable
		cfg.UpgradeThreshold = 3   // 简化测试，3 次成功即升一级
	})
	common.AutomaticDisableChannelEnabled = true

	makeTestChannel(t, model.DB, 7, 10, 20)

	// 5 次错 → L2
	for i := 0; i < 5; i++ {
		RecordChannelResult(7, "", makeServerError(500, "x"), -1)
	}
	ch := reloadChannel(t, 7)
	require.Equal(t, 2, common.DerefIntOr(ch.DegradeLevel, 0))
	require.Equal(t, int64(8), common.DerefInt64Or(ch.Priority, 0), "L2 priority = original-2 = 8")
	require.Equal(t, uint(16), common.DerefUintOr(ch.Weight, 0), "L2 weight = floor(20*0.81) = 16")

	// 3 次成功 → L2 → L1
	for i := 0; i < 3; i++ {
		RecordChannelResult(7, "", nil, -1)
	}
	ch = reloadChannel(t, 7)
	require.Equal(t, 1, common.DerefIntOr(ch.DegradeLevel, 0))
	require.Equal(t, int64(9), common.DerefInt64Or(ch.Priority, 0), "L1 priority = original-1 = 9")
	require.Equal(t, uint(18), common.DerefUintOr(ch.Weight, 0), "L1 weight = floor(20*0.905) = 18")

	// 又 3 次成功 → L1 → L0
	for i := 0; i < 3; i++ {
		RecordChannelResult(7, "", nil, -1)
	}
	ch = reloadChannel(t, 7)
	require.Equal(t, 0, common.DerefIntOr(ch.DegradeLevel, 0))
	require.Equal(t, int64(10), common.DerefInt64Or(ch.Priority, 0), "L0 priority = original = 10")
	require.Equal(t, uint(20), common.DerefUintOr(ch.Weight, 0), "L0 weight = original = 20")
	// snapshot 已清
	require.Equal(t, int64(0), common.DerefInt64Or(ch.OriginalPriority, 0))
}

// TestStateMachine_AutoBanFalseSkipped 验证 AutoBan=false 渠道完全跳过状态机。
func TestStateMachine_AutoBanFalseSkipped(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.DegradeThreshold = 2
		cfg.BaseDegradeThreshold = 2
	})
	common.AutomaticDisableChannelEnabled = true

	noBan := 0
	priority := int64(10)
	weight := uint(20)
	ch := &model.Channel{
		Id:       8,
		Type:     1,
		Name:     "no-ban-ch",
		Status:   common.ChannelStatusEnabled,
		Priority: &priority,
		Weight:   &weight,
		AutoBan:  &noBan,
		Models:   "gpt-4",
		Group:    "default",
	}
	require.NoError(t, model.DB.Create(ch).Error)

	for i := 0; i < 10; i++ {
		RecordChannelResult(8, "", makeServerError(500, "x"), -1)
	}

	got := reloadChannel(t, 8)
	require.Equal(t, 0, common.DerefIntOr(got.DegradeLevel, 0), "AutoBan=false should never demote")
	require.Equal(t, common.ChannelStatusEnabled, got.Status, "AutoBan=false should never disable")
}

// TestStateMachine_DisabledFlagOff 验证总开关 OFF 时所有调用立即 return（无副作用）。
func TestStateMachine_DisabledFlagOff(t *testing.T) {
	setupChannelHealthTestDB(t)
	// 不开 cfg.Enabled
	common.AutomaticDisableChannelEnabled = true

	makeTestChannel(t, model.DB, 9, 10, 20)

	for i := 0; i < 10; i++ {
		RecordChannelResult(9, "", makeServerError(500, "x"), -1)
	}

	ch := reloadChannel(t, 9)
	require.Equal(t, 0, common.DerefIntOr(ch.DegradeLevel, 0))
	require.Equal(t, common.ChannelStatusEnabled, ch.Status)
	// 也没写审计事件
	var count int64
	require.NoError(t, model.DB.Model(&model.ChannelHealthEvent{}).Where("channel_id = ?", 9).Count(&count).Error)
	require.Equal(t, int64(0), count)
}

// ─────────────────────────────────────────────────────────────────────────────
// FU-7-2 反弹保护单测：postChannelDisableHook（service 层）
// ─────────────────────────────────────────────────────────────────────────────

// TestPostChannelDisableHook_FirstDisable 第一次禁用：rebounce_count=1，未锁死
func TestPostChannelDisableHook_FirstDisable(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.RebounceProtectionMinutes = 30
		cfg.RebounceProtectionThreshold = 1
	})

	makeTestChannel(t, model.DB, 10, 10, 20)
	postChannelDisableHook(10, "test-ch-10", "first error")

	got := reloadChannel(t, 10)
	require.NotZero(t, common.DerefInt64Or(got.LastDisabledAt, 0), "last_disabled_at written")
	require.Equal(t, 1, common.DerefIntOr(got.RebounceCount, 0), "rebounce_count=1")
	require.Equal(t, 0, common.DerefIntOr(got.PermanentDisabled, 0), "NOT locked yet")
}

// TestPostChannelDisableHook_RebounceLock 30min 内第二次 disable → permanent_disabled=1
func TestPostChannelDisableHook_RebounceLock(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.RebounceProtectionMinutes = 30
		cfg.RebounceProtectionThreshold = 1
	})

	// 渠道有"刚刚 disable"的历史
	makeTestChannel(t, model.DB, 11, 10, 20)
	now := time.Now().Unix()
	require.NoError(t, model.UpdateChannelHealthFields(11, map[string]interface{}{
		"last_disabled_at": now - 60, // 1 分钟前刚 disable
		"rebounce_count":   1,
	}))

	// 现在又 disable
	postChannelDisableHook(11, "test-ch-11", "second error in window")

	got := reloadChannel(t, 11)
	require.Equal(t, 1, common.DerefIntOr(got.PermanentDisabled, 0), "永久锁死")
	require.Equal(t, 2, common.DerefIntOr(got.RebounceCount, 0), "rebounce_count++")
}

// TestPostChannelDisableHook_OutsideWindow 超出 30min 窗口的二次 disable 不锁死
func TestPostChannelDisableHook_OutsideWindow(t *testing.T) {
	setupChannelHealthTestDB(t)
	withHealthConfig(t, func(cfg *operation_setting.ChannelHealthConfig) {
		cfg.Enabled = true
		cfg.RebounceProtectionMinutes = 30
		cfg.RebounceProtectionThreshold = 1
	})

	makeTestChannel(t, model.DB, 12, 10, 20)
	now := time.Now().Unix()
	// 4 小时前的旧 disable 历史
	require.NoError(t, model.UpdateChannelHealthFields(12, map[string]interface{}{
		"last_disabled_at": now - 4*3600,
		"rebounce_count":   1,
	}))

	postChannelDisableHook(12, "test-ch-12", "after window")

	got := reloadChannel(t, 12)
	require.Equal(t, 0, common.DerefIntOr(got.PermanentDisabled, 0), "out of window: NOT locked")
	// 24h 滚动衰减：超过窗口 4 倍（120min）→ rebounce_count 归零再 +1 = 1
	require.Equal(t, 1, common.DerefIntOr(got.RebounceCount, 0), "rebounce_count reset to 1")
}

// TestPostChannelDisableHook_SkipWhenDisabled 总开关 OFF 时跳过
func TestPostChannelDisableHook_SkipWhenDisabled(t *testing.T) {
	setupChannelHealthTestDB(t)
	// 不开 cfg.Enabled

	makeTestChannel(t, model.DB, 13, 10, 20)
	postChannelDisableHook(13, "test-ch-13", "should be ignored")

	got := reloadChannel(t, 13)
	require.Equal(t, int64(0), common.DerefInt64Or(got.LastDisabledAt, 0), "should not write")
	require.Equal(t, 0, common.DerefIntOr(got.RebounceCount, 0))
}
