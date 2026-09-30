package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenaiHandlerObservesReturnedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-6-astra",
		RelayFormat:     types.RelayFormatOpenAI,
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "gpt-6-astra"},
	}
	body := `{"id":"chat_1","model":"gpt-5.6-luna","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`
	usage, apiErr := OpenaiHandler(c, info, &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	})
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.NotNil(t, info.ResponseModel)
	assert.True(t, info.ResponseModel.Mismatch)
	assert.Equal(t, "gpt-5.6-luna", info.ResponseModel.ReturnedModel)
	assert.Equal(t, 5, usage.TotalTokens)
}

func TestOaiResponsesHandlerObservesReturnedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-6-astra",
		RelayFormat:     types.RelayFormatOpenAIResponses,
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "gpt-6-astra"},
	}
	body := `{"id":"resp_1","model":"gpt-5.6-luna","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`
	usage, apiErr := OaiResponsesHandler(c, info, &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	})
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.NotNil(t, info.ResponseModel)
	assert.True(t, info.ResponseModel.Mismatch)
	assert.Equal(t, "gpt-5.6-luna", info.ResponseModel.ReturnedModel)
}

func TestOaiResponsesToChatHandlerObservesBeforeConversion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-6-astra",
		RelayFormat:     types.RelayFormatOpenAI,
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "gpt-6-astra"},
	}
	body := `{"id":"resp_1","model":"gpt-5.6-luna","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`
	usage, apiErr := OaiResponsesToChatHandler(c, info, &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	})
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.NotNil(t, info.ResponseModel)
	assert.Equal(t, "gpt-5.6-luna", info.ResponseModel.ReturnedModel)
	assert.True(t, info.ResponseModel.Mismatch)
}

func TestShouldNotObserveSynthesizedBufferedFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		OriginModelName: "requested",
		RelayFormat:     types.RelayFormatOpenAI,
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "mapped"},
	}
	_, apiErr := OaiResponsesToChatBufferedStreamHandler(c, info, &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	})
	require.Nil(t, apiErr)
	assert.Nil(t, info.ResponseModel)
}
