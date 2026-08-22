package controller

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

var imageGenerationModelPrefixes = []string{
	"dall-e", "gpt-image", "chatgpt-image",
}

func isImageGenerationModel(model string) bool {
	lower := strings.ToLower(model)
	for _, prefix := range imageGenerationModelPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// extractPromptFromMessages 从 chat messages 末尾向前找最后一条 user 消息，提取其文本作为 image prompt。
// 支持 string content 与 array-of-{type,text} 多模态结构（StringContent 已统一处理）。
func extractPromptFromMessages(messages []dto.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		if text := strings.TrimSpace(messages[i].StringContent()); text != "" {
			return text
		}
	}
	return ""
}

func Playground(c *gin.Context) {
	var newAPIError *types.NewAPIError

	defer func() {
		if newAPIError != nil {
			c.JSON(newAPIError.StatusCode, gin.H{
				"error": newAPIError.ToOpenAIError(),
			})
		}
	}()

	useAccessToken := c.GetBool("use_access_token")
	if useAccessToken {
		newAPIError = types.NewError(errors.New("暂不支持使用 access token"), types.ErrorCodeAccessDenied, types.ErrOptionWithSkipRetry())
		return
	}

	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAI, nil, nil)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		return
	}

	userId := c.GetInt("id")

	// Write user context to ensure acceptUnsetRatio is available
	userCache, err := model.GetUserCache(userId)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		return
	}
	userCache.WriteContext(c)

	tempToken := &model.Token{
		UserId: userId,
		Name:   fmt.Sprintf("playground-%s", relayInfo.UsingGroup),
		Group:  relayInfo.UsingGroup,
	}
	_ = middleware.SetupContextForToken(c, tempToken)

	// Detect image generation models and rewrite both path AND body for correct relay mode.
	// Chat tab 发来的是 {model, messages, stream, temperature, ...}，但图像端点 image_handler
	// 期望 dto.ImageRequest {model, prompt, ...}；只改 path 不改 body 会被类型断言挡掉。
	relayFormat := types.RelayFormatOpenAI
	var chatReq dto.GeneralOpenAIRequest
	_ = common.UnmarshalBodyReusable(c, &chatReq)
	if isImageGenerationModel(chatReq.Model) {
		prompt := extractPromptFromMessages(chatReq.Messages)
		if prompt == "" {
			newAPIError = types.NewErrorWithStatusCode(
				errors.New("无法从对话内容中提取生图提示词，请确保最后一条用户消息包含文本"),
				types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			return
		}

		imgReq := dto.ImageRequest{Model: chatReq.Model, Prompt: prompt}
		jsonData, err := common.Marshal(&imgReq)
		if err != nil {
			newAPIError = types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			return
		}

		c.Request.Body = io.NopCloser(bytes.NewBuffer(jsonData))
		c.Request.ContentLength = int64(len(jsonData))
		// 关键：清掉 distributor 中间件已缓存的 BodyStorage（指向老 chat body）。
		// 否则下游 UnmarshalBodyReusable → GetRequestBody (common/gin.go:38)
		// 优先返回老 storage，导致 GetAndValidOpenAIImageRequest 拿到的是 chat body，
		// prompt 字段缺失 → 上游报 "Invalid 'prompt': empty string"。
		common.CleanupBodyStorage(c)
		c.Set(common.KeyRequestBody, jsonData)
		// 保留 /pg 前缀，让 GenRelayInfo (relay_info.go:496) 识别 IsPlayground=true，
		// 从而在 PreConsumeTokenQuota (service/quota.go:387) 跳过 token 查询（tempToken 没 key）。
		c.Request.URL.Path = "/pg/images/generations"
		// format 必须切到 OpenAIImage，否则 controller.GetAndValidateRequest 会按 chat 格式
		// 解析新 body，prompt 字段被丢弃 → 上游收到空 prompt。
		relayFormat = types.RelayFormatOpenAIImage
		// relay_mode 也必须同步覆盖。distributor 中间件已按原 /pg/chat/completions 推断为
		// ChatCompletions，导致 relayHandler (controller/relay.go:37) 走 default TextHelper 分支；
		// 改为 ImagesGenerations 才会进入 relay.ImageHelper。
		c.Set("relay_mode", relayconstant.RelayModeImagesGenerations)
	}

	Relay(c, relayFormat)
}
