package controller

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSubscriptionPlanPayload_FallbackAndAllowedGroupsRoundTrip 验证兜底组 + 允许购买分组
// 在 payload<->model 之间的往返映射，以及对外的 JSON 字段名 / omitempty / 空值形态。
// 纯函数测试，不依赖 DB。
func TestSubscriptionPlanPayload_FallbackAndAllowedGroupsRoundTrip(t *testing.T) {
	// ── model -> payload ──
	plan := model.SubscriptionPlan{
		Id:            7,
		Title:         "rt",
		BoundGroup:    "vip",
		FallbackGroup: "basic",
	}
	plan.SetAllowedUserGroups([]string{"vip", "svip", "  ", ""}) // 含空项，应被清洗

	payload := subscriptionPlanPayloadFromModel(plan)
	require.NotNil(t, payload.FallbackGroup)
	assert.Equal(t, "basic", *payload.FallbackGroup)
	assert.Equal(t, []string{"vip", "svip"}, payload.AllowedUserGroups, "空项应被丢弃")

	// ── payload -> model（ToModel 应 trim FallbackGroup 并重新序列化 allowed）──
	fb := "  basic  "
	payload.FallbackGroup = &fb
	payload.AllowedUserGroups = []string{"vip", "svip"}
	back := payload.ToModel()
	assert.Equal(t, "basic", back.FallbackGroup, "FallbackGroup 应被 trim")
	assert.Equal(t, []string{"vip", "svip"}, back.GetAllowedUserGroups())

	// ── 空值形态：allowed 应为非 nil 空切片（JSON []），fallback 应为 nil 指针（omitempty 省略）──
	emptyPlan := model.SubscriptionPlan{Id: 8, Title: "e"}
	emptyPayload := subscriptionPlanPayloadFromModel(emptyPlan)
	assert.NotNil(t, emptyPayload.AllowedUserGroups)
	assert.Len(t, emptyPayload.AllowedUserGroups, 0)
	assert.Nil(t, emptyPayload.FallbackGroup, "空 FallbackGroup -> nil 指针")

	// ── JSON 序列化：字段名 + omitempty + 空数组 ──
	rawEmpty, err := json.Marshal(newSubscriptionPlanDTO(emptyPlan))
	require.NoError(t, err)
	assert.Contains(t, string(rawEmpty), `"allowed_user_groups":[]`, "空 allowed 应序列化为 [] 而非 null")
	assert.NotContains(t, string(rawEmpty), `"fallback_group"`, "空 fallback_group 应被 omitempty 省略")

	rawSet, err := json.Marshal(newSubscriptionPlanDTO(plan))
	require.NoError(t, err)
	assert.Contains(t, string(rawSet), `"fallback_group":"basic"`)
	assert.Contains(t, string(rawSet), `"allowed_user_groups":["vip","svip"]`)

	// ── JSON 反序列化：外部 payload 能正确解析回 model ──
	var inbound SubscriptionPlanDTO
	require.NoError(t, json.Unmarshal(rawSet, &inbound))
	require.NotNil(t, inbound.Plan.FallbackGroup)
	assert.Equal(t, "basic", *inbound.Plan.FallbackGroup)
	assert.Equal(t, []string{"vip", "svip"}, inbound.Plan.AllowedUserGroups)
	inboundModel := inbound.Plan.ToModel()
	assert.Equal(t, []string{"vip", "svip"}, inboundModel.GetAllowedUserGroups())
}
