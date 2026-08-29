package ali

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayhelper "github.com/QuantumNous/new-api/relay/helper"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestConvertOpenAIRequestFiltersThinkingBudgetByUpstreamModel(t *testing.T) {
	tests := []struct {
		name          string
		requestModel  string
		upstreamModel string
		budget        string
		wantBudget    bool
		wantValue     int64
	}{
		{
			name:          "qwen",
			requestModel:  "qwen-plus",
			upstreamModel: "qwen-plus",
			budget:        "128",
			wantBudget:    true,
			wantValue:     128,
		},
		{
			name:          "qwq explicit zero",
			requestModel:  "qwq-32b",
			upstreamModel: "qwq-32b",
			budget:        "0",
			wantBudget:    true,
			wantValue:     0,
		},
		{
			name:          "unsupported upstream overrides qwen request",
			requestModel:  "qwen-plus",
			upstreamModel: "deepseek-r1",
			budget:        "128",
			wantBudget:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := &dto.GeneralOpenAIRequest{
				Model:          tt.requestModel,
				EnableThinking: json.RawMessage(`true`),
				ThinkingBudget: json.RawMessage(tt.budget),
			}
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: tt.upstreamModel,
				},
			}

			convertedValue, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
			require.NoError(t, err)
			converted, ok := convertedValue.(*dto.GeneralOpenAIRequest)
			require.True(t, ok)

			if tt.wantBudget {
				assert.Equal(t, tt.budget, string(converted.ThinkingBudget))
			} else {
				assert.Nil(t, converted.ThinkingBudget)
			}

			encoded, err := common.Marshal(converted)
			require.NoError(t, err)

			assert.True(t, gjson.GetBytes(encoded, "enable_thinking").Bool())
			value := gjson.GetBytes(encoded, "thinking_budget")
			assert.Equal(t, tt.wantBudget, value.Exists())
			if tt.wantBudget {
				assert.Equal(t, tt.wantValue, value.Int())
			}
		})
	}
}

func TestConvertOpenAIRequestPreservesExplicitZeroForMappedQwenModel(t *testing.T) {
	const (
		clientModel   = "customer-model"
		upstreamModel = "Qwen/Qwen3-235B-A22B-Thinking-2507"
	)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("model_mapping", `{"customer-model":"Qwen/Qwen3-235B-A22B-Thinking-2507"}`)

	request := &dto.GeneralOpenAIRequest{
		Model:          clientModel,
		EnableThinking: json.RawMessage(`true`),
		ThinkingBudget: json.RawMessage(`0`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: clientModel,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: clientModel,
		},
	}

	err := relayhelper.ModelMappedHelper(c, info, request)
	require.NoError(t, err)
	assert.True(t, info.IsModelMapped)
	assert.Equal(t, upstreamModel, info.UpstreamModelName)
	assert.Equal(t, upstreamModel, request.Model)

	convertedValue, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, request)
	require.NoError(t, err)
	converted, ok := convertedValue.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	assert.Equal(t, json.RawMessage(`0`), converted.ThinkingBudget)

	encoded, err := common.Marshal(converted)
	require.NoError(t, err)

	value := gjson.GetBytes(encoded, "thinking_budget")
	assert.True(t, value.Exists())
	assert.Equal(t, int64(0), value.Int())
}

// TestIsQwenThinkingBudgetModel 固定 thinking_budget 的模型判定口径。
// 该用例覆盖的是 dto 层公共函数，因本次改动的边界限定在 ali 目录内，暂放于此。
func TestIsQwenThinkingBudgetModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "qwen-plus", want: true},
		{model: "Qwen/Qwen3-235B-A22B-Thinking-2507", want: true},
		{model: "qwq-32b", want: true},
		{model: "provider/qwen-plus", want: true},
		{model: "provider/qwq-32b", want: true},
		{model: "gpt-4.1", want: false},
		{model: "deepseek-r1", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			assert.Equal(t, tt.want, dto.IsQwenThinkingBudgetModel(tt.model))
		})
	}
}

// TestGeneralOpenAIRequestGatesThinkingBudgetOnSerialize 守住新增字段带来的跨渠道回归：
// thinking_budget 现在能被反序列化保留，但只允许在通义千问系列模型上真正发往上游。
func TestGeneralOpenAIRequestGatesThinkingBudgetOnSerialize(t *testing.T) {
	raw := []byte(`{"model":"gpt-4.1","thinking_budget":128}`)

	var req dto.GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal(raw, &req))
	require.Equal(t, json.RawMessage(`128`), req.ThinkingBudget)

	encoded, err := common.Marshal(req)
	require.NoError(t, err)
	assert.False(t, gjson.GetBytes(encoded, "thinking_budget").Exists())

	req.Model = "qwen-plus"
	encoded, err = common.Marshal(req)
	require.NoError(t, err)
	value := gjson.GetBytes(encoded, "thinking_budget")
	require.True(t, value.Exists())
	assert.Equal(t, int64(128), value.Int())
}
