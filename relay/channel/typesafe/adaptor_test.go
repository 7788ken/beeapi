package typesafe

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newInfo(base string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: base, ApiKey: "secret-key"}}
}

func newCtx() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/systemone", nil)
	return c, rec
}

// 无论运营把地址填到哪一层，/v1/systemone 只拼一次；空地址报错而不是发相对路径。
func TestGetRequestURLAppendsPathOnce(t *testing.T) {
	a := &Adaptor{}
	cases := map[string]string{
		"https://api.typesafe.ai":           "https://api.typesafe.ai/v1/systemone",
		"https://upstream.example/":             "https://upstream.example/v1/systemone",
		"https://upstream.example/v1/systemone": "https://upstream.example/v1/systemone",
	}
	for base, want := range cases {
		got, err := a.GetRequestURL(newInfo(base))
		require.NoError(t, err, base)
		require.Equal(t, want, got, base)
	}
	_, err := a.GetRequestURL(newInfo(""))
	require.Error(t, err)
}

func TestSetupRequestHeaderUsesBearerKey(t *testing.T) {
	c, _ := newCtx()
	h := http.Header{}
	require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &h, newInfo("https://api.typesafe.ai")))
	require.Equal(t, "Bearer secret-key", h.Get("Authorization"))
}

// 类型化答案逐字节回到客户手上，计费数字来自厂商 usage，不是我们自己数的。
func TestDoResponseForwardsBodyVerbatimAndReportsUsage(t *testing.T) {
	c, rec := newCtx()
	body := `{"model":"jev-1.13.0","answers":{"refund_requested":{"type":"noul","noul":0.99}},"usage":{"input_tokens":328,"output_tokens":21}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
	usageAny, apiErr := (&Adaptor{}).DoResponse(c, resp, newInfo("https://api.typesafe.ai"))
	require.Nil(t, apiErr)
	usage, ok := usageAny.(*dto.Usage)
	require.True(t, ok)
	require.Equal(t, 328, usage.PromptTokens)
	require.Equal(t, 21, usage.CompletionTokens)
	require.Equal(t, 349, usage.TotalTokens)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, body, rec.Body.String())
}

// 响应没有 usage 一律拒绝，不按 0 计费，也不给客户写任何字节。
func TestDoResponseRefusesUnmeteredBody(t *testing.T) {
	c, rec := newCtx()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewBufferString(`{"model":"jev-1.13.0","answers":{}}`)),
	}
	_, apiErr := (&Adaptor{}).DoResponse(c, resp, newInfo("https://api.typesafe.ai"))
	require.NotNil(t, apiErr)
	require.Contains(t, apiErr.Error(), "usage")
	require.Empty(t, rec.Body.String())
}

func TestDoResponseRejectsNonJSON(t *testing.T) {
	c, _ := newCtx()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewBufferString("<html>cf</html>"))}
	_, apiErr := (&Adaptor{}).DoResponse(c, resp, newInfo("https://api.typesafe.ai"))
	require.NotNil(t, apiErr)
}

// 聊天类转换全部拒绝，错误信息指向 /v1/systemone，状态码 400 且不重试；
// 经过 helper 的 types.NewError 包装后状态码仍是 400。
func TestChatShapedConversionsAreRefusedWith400(t *testing.T) {
	a := &Adaptor{}
	var errs []error
	_, err := a.ConvertOpenAIRequest(nil, nil, &dto.GeneralOpenAIRequest{})
	errs = append(errs, err)
	_, err = a.ConvertClaudeRequest(nil, nil, &dto.ClaudeRequest{})
	errs = append(errs, err)
	_, err = a.ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{})
	errs = append(errs, err)
	_, err = a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	errs = append(errs, err)
	_, err = a.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{})
	errs = append(errs, err)
	for _, err := range errs {
		require.ErrorContains(t, err, "/v1/systemone")
		wrapped := types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		require.Equal(t, http.StatusBadRequest, wrapped.StatusCode)
		require.True(t, types.IsSkipRetryError(wrapped))
	}
}

// 厂商的 {"detail": ...} 校验错误要带给客户，而且 422 不换渠道重试。
func TestRelayErrorHandlerSurfacesDetail(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusUnprocessableEntity,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewBufferString(`{"detail":[{"type":"union_tag_not_found","loc":["body","questions","q"],"msg":"Unable to extract tag using discriminator 'type'"}]}`)),
	}
	apiErr := RelayErrorHandler(context.Background(), resp)
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusUnprocessableEntity, apiErr.StatusCode)
	require.Contains(t, apiErr.Error(), "discriminator")
	require.True(t, types.IsSkipRetryError(apiErr), "422 是客户请求本身的问题，不该换渠道重试")
}

// 非 detail 形态（例如上游 New API 自己的 {"error":{...}}）走通用处理器。
func TestRelayErrorHandlerFallsBackToGeneric(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewBufferString(`{"error":{"message":"no available channel","type":"new_api_error"}}`)),
	}
	apiErr := RelayErrorHandler(context.Background(), resp)
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
	require.Contains(t, apiErr.Error(), "no available channel")
	require.False(t, types.IsSkipRetryError(apiErr), "上游 503 仍按状态码白名单决定是否换渠道")
}
