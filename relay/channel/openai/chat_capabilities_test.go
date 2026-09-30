package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func convertChat(t *testing.T, request *dto.GeneralOpenAIRequest, upstream string) (*dto.GeneralOpenAIRequest, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		OriginModelName: request.Model,
		RelayFormat:     types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: upstream,
		},
	}
	converted, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, request)
	require.NoError(t, err)
	got, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	return got, info
}

func TestConvertOpenAIRequestAppliesChatCapabilities(t *testing.T) {
	sampling := func() *dto.GeneralOpenAIRequest {
		return &dto.GeneralOpenAIRequest{
			Temperature: lo.ToPtr(0.2),
			TopP:        lo.ToPtr(0.8),
			LogProbs:    lo.ToPtr(true),
			TopLogProbs: lo.ToPtr(5),
			Messages: []dto.Message{
				{Role: "system", Content: "first instruction"},
				{Role: "system", Content: "second instruction"},
				{Role: "user", Content: "hi"},
			},
		}
	}

	t.Run("gpt-6-astra migrates tokens and strips sampling", func(t *testing.T) {
		req := sampling()
		req.Model = "gpt-6-astra"
		req.MaxTokens = lo.ToPtr(uint(100))
		got, info := convertChat(t, req, "gpt-6-astra")
		assert.Nil(t, got.MaxTokens)
		assert.Equal(t, uint(100), lo.FromPtr(got.MaxCompletionTokens))
		assert.Nil(t, got.Temperature)
		assert.Nil(t, got.TopP)
		assert.Nil(t, got.LogProbs)
		assert.Nil(t, got.TopLogProbs)
		assert.Equal(t, "developer", got.Messages[0].Role)
		assert.Equal(t, "system", got.Messages[1].Role)
		assert.Equal(t, "gpt-6-astra", info.UpstreamModelName)
	})

	t.Run("gpt-6-astra snapshot", func(t *testing.T) {
		req := sampling()
		req.Model = "gpt-6-astra-2026-09-03"
		req.MaxTokens = lo.ToPtr(uint(16))
		got, _ := convertChat(t, req, "gpt-6-astra-2026-09-03")
		assert.Equal(t, uint(16), lo.FromPtr(got.MaxCompletionTokens))
		assert.Nil(t, got.Temperature)
		assert.Equal(t, "developer", got.Messages[0].Role)
	})

	t.Run("effort suffix is stripped before capability lookup", func(t *testing.T) {
		req := sampling()
		req.Model = "gpt-6-astra-high"
		req.MaxTokens = lo.ToPtr(uint(16))
		got, info := convertChat(t, req, "gpt-6-astra-high")
		assert.Equal(t, "gpt-6-astra", got.Model)
		assert.Equal(t, "gpt-6-astra", info.UpstreamModelName)
		assert.Equal(t, "high", got.ReasoningEffort)
		assert.Equal(t, "high", info.ReasoningEffort)
		assert.Nil(t, got.Temperature)
		assert.Equal(t, uint(16), lo.FromPtr(got.MaxCompletionTokens))
	})

	t.Run("gpt-5.2 none keeps sampling", func(t *testing.T) {
		req := sampling()
		req.Model = "gpt-5.2"
		req.ReasoningEffort = "none"
		req.MaxTokens = lo.ToPtr(uint(16))
		got, _ := convertChat(t, req, "gpt-5.2")
		assert.Equal(t, 0.2, lo.FromPtr(got.Temperature))
		assert.Equal(t, 0.8, lo.FromPtr(got.TopP))
		assert.Equal(t, true, lo.FromPtr(got.LogProbs))
		assert.Equal(t, 5, lo.FromPtr(got.TopLogProbs))
		assert.Equal(t, uint(16), lo.FromPtr(got.MaxCompletionTokens))
		assert.Equal(t, "developer", got.Messages[0].Role)
	})

	t.Run("gpt-5.4 high strips sampling", func(t *testing.T) {
		req := sampling()
		req.Model = "gpt-5.4"
		req.ReasoningEffort = "high"
		got, _ := convertChat(t, req, "gpt-5.4")
		assert.Nil(t, got.Temperature)
		assert.Nil(t, got.TopP)
		assert.Nil(t, got.LogProbs)
	})

	t.Run("gpt-4.1 and future gpt-7 stay unchanged", func(t *testing.T) {
		for _, model := range []string{"gpt-4.1", "gpt-7"} {
			req := sampling()
			req.Model = model
			req.MaxTokens = lo.ToPtr(uint(16))
			got, _ := convertChat(t, req, model)
			assert.Equal(t, uint(16), lo.FromPtr(got.MaxTokens))
			assert.Nil(t, got.MaxCompletionTokens)
			assert.Equal(t, 0.2, lo.FromPtr(got.Temperature))
			assert.Equal(t, "system", got.Messages[0].Role)
		}
	})

	t.Run("alias mapped to astra uses upstream name", func(t *testing.T) {
		req := sampling()
		req.Model = "customer-model"
		req.MaxTokens = lo.ToPtr(uint(16))
		got, _ := convertChat(t, req, "gpt-6-astra")
		assert.Equal(t, uint(16), lo.FromPtr(got.MaxCompletionTokens))
		assert.Nil(t, got.Temperature)
		assert.Equal(t, "developer", got.Messages[0].Role)
	})

	t.Run("o1-mini keeps system and only clears temperature", func(t *testing.T) {
		req := sampling()
		req.Model = "o1-mini"
		req.MaxTokens = lo.ToPtr(uint(16))
		got, _ := convertChat(t, req, "o1-mini")
		assert.Equal(t, uint(16), lo.FromPtr(got.MaxCompletionTokens))
		assert.Nil(t, got.Temperature)
		assert.Equal(t, 0.8, lo.FromPtr(got.TopP))
		assert.Equal(t, "system", got.Messages[0].Role)
	})

	t.Run("gpt-4o-high does not steal an effort suffix", func(t *testing.T) {
		req := sampling()
		req.Model = "gpt-4o-high"
		req.MaxTokens = lo.ToPtr(uint(16))
		got, info := convertChat(t, req, "gpt-4o-high")
		assert.Equal(t, "gpt-4o-high", got.Model)
		assert.Equal(t, "gpt-4o-high", info.UpstreamModelName)
		assert.Empty(t, got.ReasoningEffort)
		assert.Equal(t, uint(16), lo.FromPtr(got.MaxTokens))
	})
}

func TestConvertOpenAIRequestTokenPriority(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "omitted", input: `{}`, want: `{}`},
		{name: "legacy only", input: `{"max_tokens":100}`, want: `{"max_completion_tokens":100}`},
		{name: "completion only", input: `{"max_completion_tokens":50}`, want: `{"max_completion_tokens":50}`},
		{name: "both positive stay", input: `{"max_tokens":100,"max_completion_tokens":50}`, want: `{"max_tokens":100,"max_completion_tokens":50}`},
		{name: "zero completion falls back", input: `{"max_tokens":100,"max_completion_tokens":0}`, want: `{"max_completion_tokens":100}`},
		{name: "legacy zero stays", input: `{"max_tokens":0}`, want: `{"max_tokens":0}`},
		{name: "completion zero stays", input: `{"max_completion_tokens":0}`, want: `{"max_completion_tokens":0}`},
		{name: "both zero stay", input: `{"max_tokens":0,"max_completion_tokens":0}`, want: `{"max_tokens":0,"max_completion_tokens":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &dto.GeneralOpenAIRequest{
				Model:    "gpt-6-astra",
				Messages: []dto.Message{{Role: "user", Content: "hi"}},
			}
			require.NoError(t, common.UnmarshalJsonStr(tc.input, req))
			got, _ := convertChat(t, req, "gpt-6-astra")
			want := dto.GeneralOpenAIRequest{
				Model:    "gpt-6-astra",
				Messages: []dto.Message{{Role: "user", Content: "hi"}},
			}
			require.NoError(t, common.UnmarshalJsonStr(tc.want, &want))
			gotJSON, err := common.Marshal(got)
			require.NoError(t, err)
			wantJSON, err := common.Marshal(&want)
			require.NoError(t, err)
			assert.JSONEq(t, string(wantJSON), string(gotJSON))
		})
	}
}

func TestConvertOpenAIResponsesRequestKeepsExistingParameters(t *testing.T) {
	const body = `{"model":"gpt-6-astra","input":"hi","max_output_tokens":100,"temperature":0.2,"top_p":0.8,"top_logprobs":5,"include":["message.output_text.logprobs"],"reasoning":{"effort":"high"}}`
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.UnmarshalJsonStr(body, &request))
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-6-astra",
		RelayFormat:     types.RelayFormatOpenAIResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gpt-6-astra",
		},
	}
	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)
	require.NoError(t, err)
	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.JSONEq(t, body, string(encoded))
}

func TestConvertOpenAIRequestJSONDoesNotInventFields(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model:     "gpt-5.2",
		MaxTokens: lo.ToPtr(uint(16)),
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, _ := convertChat(t, req, "gpt-5.2")
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), `"max_tokens"`)
	assert.Contains(t, string(encoded), `"max_completion_tokens"`)
}
