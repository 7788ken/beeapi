package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// token 重算必须沿用提交时快照的分组倍率（BillingContext.GroupRatio）。
// 预扣阶段用的是（用户组 → 调用组）的专属倍率；重算阶段只有 task.Group（调用组），
// 若按调用组的公共倍率重算，结算倍率会与预扣/报价不一致（线上曾出现预扣 ×7、结算 ×8）。
func TestRecalculateByTokens_UsesBillingContextGroupRatio(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 30, 30, 30
	const initQuota, preConsumed, tokenRemain = 100000, 20000, 500000
	const modelName = "test-token-video-model"
	const totalTokens = 1000

	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"`+modelName+`":5}`))
	// 调用组公共倍率 8，与快照里的专属倍率 7 故意不同
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":8}`))
	t.Cleanup(func() {
		_ = ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`)
	})

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-group", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = &model.TaskBillingContext{
		GroupRatio:      7,
		ModelRatio:      5,
		OriginModelName: modelName,
	}

	RecalculateTaskQuotaByTokens(ctx, task, totalTokens)

	// 1000 tokens × modelRatio 5 × 快照分组倍率 7 = 35000（若误用公共倍率 8 则为 40000）
	const want = totalTokens * 5 * 7
	assert.Equal(t, want, task.Quota)
	assert.Equal(t, initQuota-(want-preConsumed), getUserQuota(t, userID))
}

// 老任务没有快照分组倍率时，保持原有回退：按调用组查倍率。
func TestRecalculateByTokens_FallsBackToGroupLookupWithoutSnapshot(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 31, 31, 31
	const initQuota, preConsumed, tokenRemain = 100000, 20000, 500000
	const modelName = "test-token-video-model-legacy"
	const totalTokens = 1000

	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"`+modelName+`":5}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":8}`))
	t.Cleanup(func() {
		_ = ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`)
	})

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-legacy", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Properties.OriginModelName = modelName
	task.PrivateData.BillingContext = nil

	RecalculateTaskQuotaByTokens(ctx, task, totalTokens)

	assert.Equal(t, totalTokens*5*8, task.Quota)
}
