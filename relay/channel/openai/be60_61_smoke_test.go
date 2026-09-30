package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSmokeBE60ConvertGeminiRequestIncludesStreamUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	info := &relaycommon.RelayInfo{
		IsStream: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          constant.ChannelTypeOpenAI,
			SupportStreamOptions: true,
			UpstreamModelName:    "gpt-4o-mini",
		},
	}
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{
			Role:  "user",
			Parts: []dto.GeminiPart{{Text: "hi"}},
		}},
	}

	got, err := (&Adaptor{}).ConvertGeminiRequest(nil, info, req)
	require.NoError(t, err)
	converted, ok := got.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, converted.StreamOptions)
	require.True(t, converted.StreamOptions.IncludeUsage)

	raw, err := json.Marshal(converted)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"include_usage":true`)
}

func TestSmokeBE60ConvertClaudeRequestIncludesStreamUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stream := true
	maxTokens := uint(64)
	info := &relaycommon.RelayInfo{
		IsStream: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          constant.ChannelTypeOpenAI,
			SupportStreamOptions: true,
			UpstreamModelName:    "gpt-4o-mini",
		},
	}
	req := &dto.ClaudeRequest{
		Model:     "claude-test",
		MaxTokens: &maxTokens,
		Stream:    &stream,
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
	}

	got, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, req)
	require.NoError(t, err)
	converted, ok := got.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, converted.StreamOptions)
	require.True(t, converted.StreamOptions.IncludeUsage)
}

func TestSmokeBE60SkipsWhenChannelHasNoStreamOptions(t *testing.T) {
	info := &relaycommon.RelayInfo{
		IsStream: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gpt-4o-mini",
		},
	}
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{
			Role:  "user",
			Parts: []dto.GeminiPart{{Text: "hi"}},
		}},
	}

	got, err := (&Adaptor{}).ConvertGeminiRequest(nil, info, req)
	require.NoError(t, err)
	converted, ok := got.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.True(t, converted.StreamOptions == nil || !converted.StreamOptions.IncludeUsage)
}

func TestSmokeBE61ImageHandlerMapsOutputTokensDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"created":1710000000,"data":[{"b64_json":"image"}],"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7,"input_tokens_details":{"image_tokens":2,"text_tokens":1},"output_tokens_details":{"image_tokens":1120,"text_tokens":4}}}`
	c, _, resp, info := newImageTestContext(t, body, "application/json", false)
	info.RelayMode = relayconstant.RelayModeImagesGenerations

	usage, apiErr := OpenaiImageHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.Equal(t, 1120, usage.CompletionTokenDetails.ImageTokens)
	require.Equal(t, 2, usage.PromptTokensDetails.ImageTokens)
}

func TestSmokeBE61ChatUsageMapsOutputTokensDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	body := `{"id":"chatcmpl-smoke","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7,"output_tokens_details":{"image_tokens":1120,"text_tokens":4}}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	usage, apiErr := OpenaiHandlerWithUsage(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, resp)
	require.Nil(t, apiErr)
	require.Equal(t, 1120, usage.CompletionTokenDetails.ImageTokens)
	require.Equal(t, 4, usage.CompletionTokenDetails.TextTokens)
}

func TestSmokeBE61ChatUsageDoesNotDoubleCountOutputDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	body := `{"id":"chatcmpl-smoke","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"completion_tokens_details":{"image_tokens":1120,"text_tokens":4},"output_tokens_details":{"image_tokens":1120,"text_tokens":4}}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	usage, apiErr := OpenaiHandlerWithUsage(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, resp)
	require.Nil(t, apiErr)
	require.Equal(t, 1120, usage.CompletionTokenDetails.ImageTokens)
	require.Equal(t, 4, usage.CompletionTokenDetails.TextTokens)
}
