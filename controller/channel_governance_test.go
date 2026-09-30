package controller

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ── 档位切分 ──

// 档位边界必须跟着 channel_health_setting.max_degrade_level 走。写死成 L1-2/L3-5/L6-9/L10
// 的话，管理员把封顶级别调到 5 之后链路图上会出现永远为 0 的空档，而真实降级渠道被归错档。
func TestBuildDegradeBuckets_BoundariesFollowMaxDegradeLevel(t *testing.T) {
	type band struct {
		key      string
		min, max int
	}
	cases := []struct {
		name     string
		maxLevel int
		want     []band
	}{
		{
			name:     "默认封顶 10",
			maxLevel: 10,
			want: []band{
				{"healthy", 0, 0}, {"light", 1, 3}, {"moderate", 4, 6}, {"deep", 7, 9}, {"max", 10, 10},
			},
		},
		{
			name:     "封顶 5：中间区间 1..4 摊成 2/1/1",
			maxLevel: 5,
			want: []band{
				{"healthy", 0, 0}, {"light", 1, 2}, {"moderate", 3, 3}, {"deep", 4, 4}, {"max", 5, 5},
			},
		},
		{
			name:     "封顶 3：中间只够两档，deep 不出现",
			maxLevel: 3,
			want: []band{
				{"healthy", 0, 0}, {"light", 1, 1}, {"moderate", 2, 2}, {"max", 3, 3},
			},
		},
		{
			name:     "封顶 1：中间区间为空，只剩健康与触顶",
			maxLevel: 1,
			want: []band{
				{"healthy", 0, 0}, {"max", 1, 1},
			},
		},
		{
			name:     "封顶 0（未 Normalize）：回落到 10，与 ChannelHealthConfig.Normalize 一致",
			maxLevel: 0,
			want: []band{
				{"healthy", 0, 0}, {"light", 1, 3}, {"moderate", 4, 6}, {"deep", 7, 9}, {"max", 10, 10},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildDegradeBuckets(tc.maxLevel)
			require.Len(t, got, len(tc.want))
			for i, want := range tc.want {
				require.Equal(t, want.key, got[i].Key, "档位 %d 的 key", i)
				require.Equal(t, want.min, got[i].MinLevel, "档位 %s 的下界", want.key)
				require.Equal(t, want.max, got[i].MaxLevel, "档位 %s 的上界", want.key)
			}
			// 各档必须首尾相接、无空洞无重叠，否则渠道会被漏计或双计。
			for i := 1; i < len(got); i++ {
				require.Equal(t, got[i-1].MaxLevel+1, got[i].MinLevel,
					"档位 %s 与 %s 之间不连续", got[i-1].Key, got[i].Key)
			}
		})
	}
}

// 封顶级别被调小后，库里残留的高等级行不能被静默丢掉——丢掉则各档之和小于渠道总数，
// 链路图上的节点计数就不守恒了。
func TestAssignDegradeBucket_ClampsLevelsAboveMax(t *testing.T) {
	buckets := buildDegradeBuckets(3)
	require.Equal(t, len(buckets)-1, assignDegradeBucket(buckets, 9), "超出封顶的等级应归入触顶档")
	require.Equal(t, 0, assignDegradeBucket(buckets, 0))
	require.Equal(t, 0, assignDegradeBucket(buckets, -1), "负值等级按健康处理")
	require.Equal(t, len(buckets)-1, assignDegradeBucket(buckets, 3))
}

// ── 分布接口 ──

func intPtr(v int) *int { return &v }

type governanceDistributionEnvelope struct {
	Success bool                          `json:"success"`
	Message string                        `json:"message"`
	Data    ChannelGovernanceDistribution `json:"data"`
}

func callGovernanceDistribution(t *testing.T) ChannelGovernanceDistribution {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/governance/distribution", nil)
	GetChannelGovernanceDistribution(c)
	require.Equal(t, http.StatusOK, w.Code)

	var envelope governanceDistributionEnvelope
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &envelope), "响应体: %s", w.Body.String())
	require.True(t, envelope.Success, "响应体: %s", w.Body.String())
	return envelope.Data
}

// seedGovernanceChannel 只落治理状态相关列，不碰上游/模型，避免与摘除链路测试互相干扰。
func seedGovernanceChannel(t *testing.T, db *gorm.DB, name string, status int, degradeLevel int,
	permanentDisabled int, verifyDisabled int, settings *dto.ChannelOtherSettings) *model.Channel {
	t.Helper()
	ch := &model.Channel{
		Type:              1,
		Name:              name,
		Key:               "sk-test",
		Status:            status,
		Models:            "gpt-4",
		Group:             "default",
		DegradeLevel:      intPtr(degradeLevel),
		PermanentDisabled: intPtr(permanentDisabled),
		VerifyDisabled:    intPtr(verifyDisabled),
	}
	if settings != nil {
		ch.SetOtherSettings(*settings)
	}
	require.NoError(t, db.Create(ch).Error)
	return ch
}

func withMaxDegradeLevel(t *testing.T, level int) {
	t.Helper()
	cfg := operation_setting.GetChannelHealthConfig()
	original := cfg.MaxDegradeLevel
	cfg.MaxDegradeLevel = level
	t.Cleanup(func() { cfg.MaxDegradeLevel = original })
}

func bucketByKey(t *testing.T, buckets []GovernanceDegradeBucket, key string) GovernanceDegradeBucket {
	t.Helper()
	for _, bucket := range buckets {
		if bucket.Key == key {
			return bucket
		}
	}
	t.Fatalf("档位 %s 不存在，实际档位: %+v", key, buckets)
	return GovernanceDegradeBucket{}
}

func TestGovernanceDistribution_AggregatesStatusAndDegradeOnSQLite(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	withMaxDegradeLevel(t, 10)

	seedGovernanceChannel(t, db, "healthy-1", common.ChannelStatusEnabled, 0, 0, 0, nil)
	seedGovernanceChannel(t, db, "healthy-2", common.ChannelStatusEnabled, 0, 0, 0, nil)
	seedGovernanceChannel(t, db, "light", common.ChannelStatusEnabled, 2, 0, 0, nil)
	seedGovernanceChannel(t, db, "moderate", common.ChannelStatusEnabled, 5, 0, 0, nil)
	seedGovernanceChannel(t, db, "deep", common.ChannelStatusEnabled, 8, 0, 0, nil)
	seedGovernanceChannel(t, db, "capped", common.ChannelStatusEnabled, 10, 0, 0, nil)
	// 自动停用两个，其中一个反弹锁死；锁死是自动停用的子集，不能与它相加。
	seedGovernanceChannel(t, db, "auto-off", common.ChannelStatusAutoDisabled, 10, 0, 0, nil)
	seedGovernanceChannel(t, db, "locked", common.ChannelStatusAutoDisabled, 10, 1, 0, nil)
	seedGovernanceChannel(t, db, "manual-off", common.ChannelStatusManuallyDisabled, 0, 0, 0, nil)
	// 测评低分禁用：真实组合是 status=AutoDisabled + verify_disabled=1
	// （SetChannelVerifyDisabled 与 channel_verify_schedule.go:178 判的就是这一对）。
	// testAllChannels 见 verify_disabled=1 直接 continue，所以巡检那条恢复路径对它不通，
	// 只能等测评分数回升。这一位与 status 正交，单独计。
	seedGovernanceChannel(t, db, "verify-off", common.ChannelStatusAutoDisabled, 0, 0, 1, nil)

	got := callGovernanceDistribution(t)

	require.Equal(t, 10, got.TotalChannels)
	require.Equal(t, 6, got.Enabled)
	require.Equal(t, 3, got.AutoDisabled)
	require.Equal(t, 1, got.PermanentDisabled, "锁死是自动停用的子集")
	require.Equal(t, 1, got.ManuallyDisabled)
	require.Equal(t, 1, got.VerifyDisabled, "测评禁用与 status 正交，实践中是自动停用的子集")
	require.Equal(t, 10, got.MaxDegradeLevel)

	require.Equal(t, 1, bucketByKey(t, got.DegradeBuckets, "light").Count, "L1-3 档只有 level=2 那一个渠道")
	require.Equal(t, 1, bucketByKey(t, got.DegradeBuckets, "moderate").Count)
	require.Equal(t, 1, bucketByKey(t, got.DegradeBuckets, "deep").Count)
	require.Equal(t, 3, bucketByKey(t, got.DegradeBuckets, "max").Count, "触顶档含已停用渠道：停用不清 degrade_level")
	require.Equal(t, 4, bucketByKey(t, got.DegradeBuckets, "healthy").Count)
	require.Equal(t, 6, got.DegradedChannels)

	// 守恒：各档之和恒等于渠道总数，否则链路图上的节点计数会对不上。
	total := 0
	for _, bucket := range got.DegradeBuckets {
		total += bucket.Count
	}
	require.Equal(t, got.TotalChannels, total, "各档之和必须等于渠道总数")
}

// 同一批数据，只改封顶级别，同一个渠道必须落到不同档位——这是「边界跟着配置走」的端到端证据。
func TestGovernanceDistribution_BucketsFollowMaxDegradeLevelChange(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	seedGovernanceChannel(t, db, "level-3", common.ChannelStatusEnabled, 3, 0, 0, nil)

	withMaxDegradeLevel(t, 10)
	atTen := callGovernanceDistribution(t)
	require.Equal(t, 10, atTen.MaxDegradeLevel)
	require.Equal(t, 1, bucketByKey(t, atTen.DegradeBuckets, "light").Count,
		"封顶 10 时 L3 属于轻度档（1-3）")
	require.Equal(t, 0, bucketByKey(t, atTen.DegradeBuckets, "max").Count)

	withMaxDegradeLevel(t, 3)
	atThree := callGovernanceDistribution(t)
	require.Equal(t, 3, atThree.MaxDegradeLevel)
	require.Len(t, atThree.DegradeBuckets, 4, "封顶 3 时中间只够两档，deep 不该出现")
	require.Equal(t, 1, bucketByKey(t, atThree.DegradeBuckets, "max").Count,
		"封顶 3 时同一个 L3 渠道变成触顶")
	require.Equal(t, 0, bucketByKey(t, atThree.DegradeBuckets, "light").Count)
}

func TestGovernanceDistribution_RemovalReasonBreakdown(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	withMaxDegradeLevel(t, 10)

	seedGovernanceChannel(t, db, "with-reasons", common.ChannelStatusEnabled, 0, 0, 0,
		&dto.ChannelOtherSettings{
			ModelMissingRemovedModels: []string{"gpt-5", "gpt-4o", "claude-3"},
			ModelRemovalReasons: map[string]string{
				"gpt-5":    string(modelRemovalReasonMissing),
				"gpt-4o":   string(modelRemovalReasonRateLimit),
				"claude-3": string(modelRemovalReasonForbidden),
			},
			ModelMissingRecheckAt: 2_000_000_000,
		})
	// 存量渠道：只记了模型名，没有原因表。必须计入 unknown，不能硬塞一个默认原因。
	seedGovernanceChannel(t, db, "legacy-no-reasons", common.ChannelStatusEnabled, 0, 0, 0,
		&dto.ChannelOtherSettings{
			ModelMissingRemovedModels: []string{"legacy-model"},
			ModelMissingRecheckAt:     1_900_000_000,
		})
	// LIKE 预筛的假阳性：settings 里键在、值是空数组。
	// 必须直写原始 JSON——ModelMissingRemovedModels 带 omitempty，走 SetOtherSettings 传空切片
	// 会把整个键 marshal 掉，LIKE 压根匹配不到，这条防御分支就测不到（第一版就踩了这个坑）。
	cleared := seedGovernanceChannel(t, db, "cleared", common.ChannelStatusEnabled, 0, 0, 0, nil)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", cleared.Id).
		Update("settings", `{"model_missing_removed_models":[]}`).Error)
	seedGovernanceChannel(t, db, "clean", common.ChannelStatusEnabled, 0, 0, 0, nil)

	got := callGovernanceDistribution(t)

	require.Equal(t, 4, got.RemovedModels.TotalModels, "摘除模型总数")
	require.Equal(t, 2, got.RemovedModels.Channels, "只有真的带摘除清单的渠道才计入")
	require.Equal(t, 1, got.RemovedModels.ByReason[string(modelRemovalReasonMissing)])
	require.Equal(t, 1, got.RemovedModels.ByReason[string(modelRemovalReasonRateLimit)])
	require.Equal(t, 1, got.RemovedModels.ByReason[string(modelRemovalReasonForbidden)])
	require.Equal(t, 1, got.RemovedModels.ByReason[governanceRemovalReasonUnknown], "存量记录原因未知")
	require.Equal(t, 1, got.RemovedModels.UnknownReasonModels)
	require.Equal(t, int64(1_900_000_000), got.RemovedModels.NextRecheckAt, "应取所有渠道里最早的复核时间")
}

// ── 试算接口 ──

type governanceDryRunEnvelope struct {
	Success bool                   `json:"success"`
	Message string                 `json:"message"`
	Data    GovernanceDryRunResult `json:"data"`
}

func callGovernanceDryRun(t *testing.T, req GovernanceDryRunRequest) GovernanceDryRunResult {
	t.Helper()
	body, err := common.Marshal(req)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/governance/dry_run", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	PostChannelGovernanceDryRun(c)
	require.Equal(t, http.StatusOK, w.Code)

	var envelope governanceDryRunEnvelope
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &envelope), "响应体: %s", w.Body.String())
	require.True(t, envelope.Success, "响应体: %s", w.Body.String())
	return envelope.Data
}

// 试算必须与真实处置同源：同一输入下，接口返回的判定与直接调用真实判定函数逐字段一致。
// 一旦有人在接口里另写一套规则（哪怕只是抄一遍再改一个阈值），这个测试立刻红。
func TestGovernanceDryRun_MatchesRealDecisionFunctions(t *testing.T) {
	setupModelRemovalTestDB(t)

	originalMissing := operation_setting.ModelMissingRemovalEnabled
	originalRateLimit := operation_setting.ModelRateLimitRemovalEnabled
	originalForbidden := operation_setting.ModelForbiddenRemovalEnabled
	originalAutoDisable := common.AutomaticDisableChannelEnabled
	t.Cleanup(func() {
		operation_setting.ModelMissingRemovalEnabled = originalMissing
		operation_setting.ModelRateLimitRemovalEnabled = originalRateLimit
		operation_setting.ModelForbiddenRemovalEnabled = originalForbidden
		common.AutomaticDisableChannelEnabled = originalAutoDisable
	})
	// 三条摘除路径全开 + 自动停用总开关打开，才能让各判定的差异真的显现出来。
	operation_setting.ModelMissingRemovalEnabled = true
	operation_setting.ModelRateLimitRemovalEnabled = true
	operation_setting.ModelForbiddenRemovalEnabled = true
	common.AutomaticDisableChannelEnabled = true

	cases := []GovernanceDryRunRequest{
		{StatusCode: 404, ErrorMessage: "The model `gpt-5` does not exist", Model: "gpt-5"},
		{StatusCode: 404, ErrorMessage: "The model `gpt-5` does not exist", Model: ""},
		{StatusCode: 429, ErrorMessage: "rate limit exceeded", Model: "gpt-5"},
		{StatusCode: 403, ErrorMessage: "Permission denied for this model", Model: "gpt-5"},
		{StatusCode: 400, ErrorMessage: "unsupported model", Model: "gpt-5"},
		{StatusCode: 401, ErrorMessage: "invalid api key", Model: "gpt-5"},
		{StatusCode: 500, ErrorMessage: "internal server error", Model: "gpt-5"},
		{StatusCode: 503, ErrorMessage: "no available channel for model gpt-5", Model: "gpt-5"},
		{StatusCode: 200, ErrorMessage: "", Model: "gpt-5"},
	}

	for _, req := range cases {
		t.Run(fmt.Sprintf("%d/%s", req.StatusCode, req.ErrorMessage), func(t *testing.T) {
			got := callGovernanceDryRun(t, req)

			// 真实链路构造同形错误对象的方式（relay 上游非 2xx 走 ErrorCodeBadResponseStatusCode）
			apiError := types.NewErrorWithStatusCode(
				fmt.Errorf("%s", req.ErrorMessage), types.ErrorCodeBadResponseStatusCode, req.StatusCode)
			wantReason, wantMatched := classifyModelRemovalReason(apiError, req.Model)

			require.Equal(t, isUpstreamModelMissingError(apiError, req.Model), got.Verdicts.ModelMissing)
			require.Equal(t, isUpstreamForbiddenError(apiError), got.Verdicts.Forbidden)
			require.Equal(t, isUpstreamRateLimitError(apiError), got.Verdicts.RateLimit)
			require.Equal(t, wantMatched, got.Removal.Matched)
			require.Equal(t, string(wantReason), got.Removal.Reason)
			require.Equal(t, service.ShouldDisableChannel(apiError), got.ChannelDisable.WouldDisableChannel)
			require.Equal(t, service.ShouldCountTowardChannelStreak(apiError), got.DegradeStreak.Countable)
			require.Equal(t, operation_setting.ShouldRetryByStatusCode(req.StatusCode), got.Retry.StatusCodeRetryable)
			if wantMatched {
				require.Equal(t, recheckIntervalSecondsFor(wantReason), got.Removal.RecheckIntervalSeconds)
				require.Equal(t, removalCapApplies(wantReason), got.Removal.CapApplies)
			}
			// 摘除接管成功即短路停渠道，这条不对称是整个低烈度优先设计的落点。
			require.Equal(t, wantMatched, got.ChannelDisable.ShortCircuitedByRemoval)
		})
	}
}

// 摘除路径的开关关闭时，原始分类照旧命中，但最终判定必须不摘——试算要能把"规则匹配了但
// 开关没开"这种状态如实呈现，否则管理员会以为已经生效。
func TestGovernanceDryRun_ReflectsRemovalSwitchesOff(t *testing.T) {
	setupModelRemovalTestDB(t)

	original := operation_setting.ModelRateLimitRemovalEnabled
	t.Cleanup(func() { operation_setting.ModelRateLimitRemovalEnabled = original })

	req := GovernanceDryRunRequest{StatusCode: 429, ErrorMessage: "rate limit exceeded", Model: "gpt-5"}

	operation_setting.ModelRateLimitRemovalEnabled = false
	off := callGovernanceDryRun(t, req)
	require.True(t, off.Verdicts.RateLimit, "原始分类应命中限流")
	require.False(t, off.Removal.Matched, "开关关闭时不该判定为摘除")
	require.Empty(t, off.Removal.Reason)

	operation_setting.ModelRateLimitRemovalEnabled = true
	on := callGovernanceDryRun(t, req)
	require.True(t, on.Removal.Matched)
	require.Equal(t, string(modelRemovalReasonRateLimit), on.Removal.Reason)
	require.Greater(t, on.Removal.RecheckIntervalSeconds, int64(0))
}

// 试算必须零副作用：不写库、不改渠道状态、不占用摘除防抖冷却窗口。
func TestGovernanceDryRun_HasNoSideEffects(t *testing.T) {
	db := setupModelRemovalTestDB(t)

	originalMissing := operation_setting.ModelMissingRemovalEnabled
	originalAutoDisable := common.AutomaticDisableChannelEnabled
	t.Cleanup(func() {
		operation_setting.ModelMissingRemovalEnabled = originalMissing
		common.AutomaticDisableChannelEnabled = originalAutoDisable
	})
	// 开到"最容易产生副作用"的档位：摘除会命中，停渠道判定也会命中。
	operation_setting.ModelMissingRemovalEnabled = true
	common.AutomaticDisableChannelEnabled = true

	ch := seedGovernanceChannel(t, db, "dry-run-target", common.ChannelStatusEnabled, 0, 0, 0, nil)
	before := reloadChannel(t, db, ch.Id)

	var channelCountBefore, abilityCountBefore int64
	require.NoError(t, db.Model(&model.Channel{}).Count(&channelCountBefore).Error)
	require.NoError(t, db.Model(&model.Ability{}).Count(&abilityCountBefore).Error)

	// 冷却窗口：真实链路里 tryHandleChannelModelMissing 会写 lastTrigger。试算调完之后，
	// 冷却必须仍然可用——否则一次试算就会吞掉紧随其后的真实摘除。
	req := GovernanceDryRunRequest{
		StatusCode:   404,
		ErrorMessage: "The model `gpt-4` does not exist",
		Model:        "gpt-4",
	}
	got := callGovernanceDryRun(t, req)
	require.True(t, got.Removal.Matched, "前提：本用例必须命中摘除，否则测不到副作用")

	after := reloadChannel(t, db, ch.Id)
	require.Equal(t, before.Status, after.Status, "渠道状态不应变化")
	require.Equal(t, before.Models, after.Models, "渠道模型清单不应变化")
	require.Equal(t, before.OtherSettings, after.OtherSettings, "渠道 settings 不应变化")

	var channelCountAfter, abilityCountAfter int64
	require.NoError(t, db.Model(&model.Channel{}).Count(&channelCountAfter).Error)
	require.NoError(t, db.Model(&model.Ability{}).Count(&abilityCountAfter).Error)
	require.Equal(t, channelCountBefore, channelCountAfter, "渠道行数不应变化")
	require.Equal(t, abilityCountBefore, abilityCountAfter, "abilities 行数不应变化")

	require.True(t, modelMissingRemovalCooldownOk(ch.Id, "gpt-4"),
		"试算不该占用摘除防抖冷却窗口")
}

// ── 摘除通知 ──

// captureUsersTableQueries 装一个 GORM 查询回调，记录是否查过 users 表。
// NotifyRootUser 第一步就是 model.GetRootUser()（查 users），所以"有没有查 users"
// 就是"有没有真的走进发通知流程"的可观测信号。
func captureUsersTableQueries(t *testing.T, db *gorm.DB) *atomic.Bool {
	t.Helper()
	queried := &atomic.Bool{}
	const callbackName = "test:capture_users_query"
	require.NoError(t, db.Callback().Query().After("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement != nil && tx.Statement.Table == "users" {
				queried.Store(true)
			}
		}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })
	return queried
}

func TestNotifyModelRemoval_DefaultOffSendsNothing(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	queried := captureUsersTableQueries(t, db)

	require.False(t, operation_setting.ModelRemovalNotifyEnabled,
		"默认必须关：改造前摘除路径从来不发通知，默认开就是行为变更")

	notifyModelRemoval(1, "ch-1", "gpt-5", modelRemovalReasonMissing, 3, common.GetTimestamp()+600)
	require.False(t, queried.Load(), "开关默认关时不该走进发通知流程")
}

func TestNotifyModelRemoval_OnActuallySends(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	queried := captureUsersTableQueries(t, db)

	original := operation_setting.ModelRemovalNotifyEnabled
	t.Cleanup(func() { operation_setting.ModelRemovalNotifyEnabled = original })
	operation_setting.ModelRemovalNotifyEnabled = true

	notifyModelRemoval(1, "ch-1", "gpt-5", modelRemovalReasonMissing, 3, common.GetTimestamp()+600)
	require.True(t, queried.Load(), "开关打开时应走进发通知流程（对照组，证明上一个用例不是假绿）")
}

// 摘除时必须把原因落库，否则分布接口只能报 unknown，Task A 的按原因分档等于永远空着。
func TestRunModelRemoval_RecordsRemovalReason(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	srv := upstreamModelsServer(t, []string{"gpt-5", "gpt-4"})
	ch := seedRemovalTestChannel(t, db, srv.URL, []string{"gpt-5", "gpt-4"})

	runModelMissingRemoval(context.Background(), ch.Id, ch.Name, "gpt-5",
		types.ChannelError{ChannelId: ch.Id, ChannelName: ch.Name}, rateLimitError(), modelRemovalReasonRateLimit)

	settings := reloadChannel(t, db, ch.Id).GetOtherSettings()
	require.Equal(t, []string{"gpt-5"}, settings.ModelMissingRemovedModels)
	require.Equal(t, map[string]string{"gpt-5": string(modelRemovalReasonRateLimit)},
		settings.ModelRemovalReasons, "原因表应记录该模型的触发原因")
}

// 原因表与追踪清单同生命周期：模型加回后原因也必须消失，否则会留下永久涨的僵尸原因。
func TestPruneModelRemovalReasons_DropsRestoredModels(t *testing.T) {
	reasons := map[string]string{
		"gpt-5":  string(modelRemovalReasonMissing),
		"gpt-4o": string(modelRemovalReasonRateLimit),
	}
	require.Equal(t, map[string]string{"gpt-4o": string(modelRemovalReasonRateLimit)},
		pruneModelRemovalReasons(reasons, []string{"gpt-4o"}))
	require.Nil(t, pruneModelRemovalReasons(reasons, nil), "追踪清空后原因表应为 nil 而不是空 map")
	require.Nil(t, pruneModelRemovalReasons(nil, []string{"gpt-5"}))
}
