// Package typesafe 中继 TypeSafe 的 System One API（Jev 决策模型）。
//
// 其它渠道说的都是某种聊天方言：messages 进、文本出。TypeSafe 不是：请求是一段 state
// 加一组命名的带类型 questions，响应是每个问题一个类型化答案与校准概率。这个结构就是
// 模型的全部价值，所以两个方向都原样透传，不压成聊天形态——把概率表塞进一条 assistant
// 消息正好丢掉唯一有用的东西。
//
// 唯一会被改写的是模型名，且仅在渠道配置了模型映射时。
//
// https://docs.typesafe.ai/api
package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct{}

func (a *Adaptor) Init(_ *relaycommon.RelayInfo) {}

// GetRequestURL 只拼一次 /v1/systemone：运营把完整端点填进渠道地址时不应变成
// /v1/systemone/v1/systemone。
func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(info.ChannelBaseUrl), "/")
	if base == "" {
		return "", errors.New("typesafe channel has no base url")
	}
	if strings.HasSuffix(base, RequestPath) {
		return base, nil
	}
	return base + RequestPath, nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", "Bearer "+info.ApiKey)
	return nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

// DoResponse 把厂商的答案原样回给客户，并按厂商报的用量结算。输出 token 记录在案但由
// 目录定价为 0——厂商不计输出。
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	if resp == nil {
		return nil, types.NewError(errors.New("typesafe returned no response"), types.ErrorCodeBadResponse)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeReadResponseBodyFailed)
	}
	service.CloseResponseBodyGracefully(resp)

	var parsed dto.SystemOneResponse
	if err := common.Unmarshal(body, &parsed); err != nil {
		return nil, types.NewError(fmt.Errorf("typesafe returned a body that is not System One JSON: %w", err), types.ErrorCodeBadResponseBody)
	}
	if parsed.Usage == nil {
		// 计费不能凭空编。这里拒绝意味着客户不会为一次无法对账的响应付钱，运营能在日志里
		// 看到原因，而不是事后发现一条悄悄免费的路径。
		return nil, types.NewError(errors.New("typesafe response carried no usage; refusing to bill an unmetered call"), types.ErrorCodeBadResponseBody)
	}

	service.IOCopyBytesGracefully(c, resp, body)

	usage := &dto.Usage{
		PromptTokens:     parsed.Usage.InputTokens,
		CompletionTokens: parsed.Usage.OutputTokens,
		TotalTokens:      parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
	}
	return usage, nil
}

// RelayErrorHandler 让厂商的错误细节到达客户。TypeSafe 的校验错误是 FastAPI 风格的
// {"detail": ...}，不是 OpenAI 的 {"error": {...}}，通用处理器只会留下一个状态码；而
// detail 里说的正是客户自己请求哪里写错了。400/422 是请求本身的问题，换渠道重试没有意义。
func RelayErrorHandler(ctx context.Context, resp *http.Response) *types.NewAPIError {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return types.NewOpenAIError(fmt.Errorf("read typesafe error body failed: %w", err), types.ErrorCodeBadResponseStatusCode, resp.StatusCode)
	}
	service.CloseResponseBodyGracefully(resp)

	var payload struct {
		Detail json.RawMessage `json:"detail"`
	}
	if common.Unmarshal(body, &payload) == nil && len(payload.Detail) > 0 && string(payload.Detail) != "null" {
		detail := string(payload.Detail)
		if len(detail) > 2000 {
			detail = detail[:2000] + "…"
		}
		msg := fmt.Sprintf("typesafe upstream returned %d: %s", resp.StatusCode, detail)
		var ops []types.NewAPIErrorOptions
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity {
			ops = append(ops, types.ErrOptionWithSkipRetry())
		}
		return types.NewOpenAIError(errors.New(msg), types.ErrorCodeBadResponseStatusCode, resp.StatusCode, ops...)
	}

	resp.Body = io.NopCloser(bytes.NewReader(body))
	return service.RelayErrorHandler(ctx, resp, false)
}

func (a *Adaptor) GetModelList() []string { return ModelList }

func (a *Adaptor) GetChannelName() string { return ChannelName }

// 下面这些聊天形态的转换对决策模型没有意义：System One 请求需要 state 和带类型的
// questions，聊天请求里没有也猜不出来。拒绝是诚实的回答——凭空发明一套映射等于让客户
// 为一次没人要求的翻译付钱。
var errNotChat = errors.New("typesafe serves System One requests at /v1/systemone; it has no chat, embedding, rerank, audio or image API")

// notChatError 以 400 + SkipRetry 返回。各 helper 用 types.NewError 包装转换错误时会保留内嵌的
// NewAPIError（含状态码），所以客户拿到的是 400 而不是默认的 500；这是客户请求形态的问题，
// 换渠道重试也没有意义。
func notChatError() *types.NewAPIError {
	return types.NewErrorWithStatusCode(errNotChat, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

func (a *Adaptor) ConvertOpenAIRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeneralOpenAIRequest) (any, error) {
	return nil, notChatError()
}

func (a *Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	return nil, notChatError()
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	return nil, notChatError()
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(*gin.Context, *relaycommon.RelayInfo, dto.OpenAIResponsesRequest) (any, error) {
	return nil, notChatError()
}

func (a *Adaptor) ConvertRerankRequest(*gin.Context, int, dto.RerankRequest) (any, error) {
	return nil, notChatError()
}

func (a *Adaptor) ConvertEmbeddingRequest(*gin.Context, *relaycommon.RelayInfo, dto.EmbeddingRequest) (any, error) {
	return nil, notChatError()
}

func (a *Adaptor) ConvertAudioRequest(*gin.Context, *relaycommon.RelayInfo, dto.AudioRequest) (io.Reader, error) {
	return nil, notChatError()
}

func (a *Adaptor) ConvertImageRequest(*gin.Context, *relaycommon.RelayInfo, dto.ImageRequest) (any, error) {
	return nil, notChatError()
}
