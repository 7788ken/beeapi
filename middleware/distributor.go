package middleware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type ModelRequest struct {
	Model string `json:"model"`
	Group string `json:"group,omitempty"`
}

func Distribute() func(c *gin.Context) {
	return func(c *gin.Context) {
		var channel *model.Channel
		channelId, ok := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId)
		modelRequest, shouldSelectChannel, err := getModelRequest(c)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": common.MaskSensitiveInfo(err.Error())}))
			return
		}
		if ok {
			id, err := strconv.Atoi(channelId.(string))
			if err != nil {
				abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidChannelId))
				return
			}
			channel, err = model.GetChannelById(id, true)
			if err != nil {
				abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidChannelId))
				return
			}
			if channel.Status != common.ChannelStatusEnabled {
				abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorChannelDisabled))
				return
			}
		} else {
			// Select a channel for the user
			// check token model mapping
			modelLimitEnable := common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled)
			if modelLimitEnable {
				s, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
				if !ok {
					// token model limit is empty, all models are not allowed
					abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorTokenNoModelAccess))
					return
				}
				var tokenModelLimit map[string]bool
				tokenModelLimit, ok = s.(map[string]bool)
				if !ok {
					tokenModelLimit = map[string]bool{}
				}
				matchName := ratio_setting.FormatMatchingModelName(modelRequest.Model) // match gpts & thinking-*
				if _, ok := tokenModelLimit[matchName]; !ok {
					abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorTokenModelForbidden, map[string]any{"Model": modelRequest.Model}))
					return
				}
			}

			if shouldSelectChannel {
				if modelRequest.Model == "" {
					abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorModelNameRequired))
					return
				}
				var selectGroup string
				usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
				// check path is /pg/chat/completions
				if strings.HasPrefix(c.Request.URL.Path, "/pg/chat/completions") {
					playgroundRequest := &dto.PlayGroundRequest{}
					err = common.UnmarshalBodyReusable(c, playgroundRequest)
					if err != nil {
						abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidPlayground, map[string]any{"Error": common.MaskSensitiveInfo(err.Error())}))
						return
					}
					if playgroundRequest.Group != "" {
						if !service.GroupInUserUsableGroups(usingGroup, playgroundRequest.Group) && playgroundRequest.Group != usingGroup {
							abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorGroupAccessDenied))
							return
						}
						usingGroup = playgroundRequest.Group
						common.SetContextKey(c, constant.ContextKeyUsingGroup, usingGroup)
					}
				}

				if preferredChannelID, found := service.GetPreferredChannelByAffinity(c, modelRequest.Model, usingGroup); found {
					preferred, err := model.CacheGetChannel(preferredChannelID)
					if err == nil && preferred != nil &&
						channelSupportsRequestPath(preferred, c.Request.URL.Path, modelRequest.Model) {
						if preferred.Status != common.ChannelStatusEnabled {
							if service.ShouldSkipRetryAfterChannelAffinityFailure(c) {
								abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorAffinityChannelDisabled))
								return
							}
						} else if usingGroup == "auto" {
							userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
							autoGroups := service.GetUserAutoGroup(userGroup)
							for _, g := range autoGroups {
								if model.IsChannelEnabledForGroupModel(g, modelRequest.Model, preferred.Id) {
									selectGroup = g
									common.SetContextKey(c, constant.ContextKeyAutoGroup, g)
									channel = preferred
									service.MarkChannelAffinityUsed(c, g, preferred.Id)
									break
								}
							}
						} else if model.IsChannelEnabledForGroupModel(usingGroup, modelRequest.Model, preferred.Id) {
							channel = preferred
							selectGroup = usingGroup
							service.MarkChannelAffinityUsed(c, usingGroup, preferred.Id)
						}
					}
				}

				if channel == nil {
					channel, selectGroup, err = selectChannelWithQueue(c, modelRequest.Model, usingGroup)
					// 排队超时：所有渠道持续满载超过 QueueMaxWaitMs → 返回 429（让客户端稍后重试）
					if errors.Is(err, model.ErrCapacityNeedQueue) {
						writeSoftRateLimit429(c)
						return
					}
					// 客户端在排队期间断连：静默放弃，无需写响应
					if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
						c.Abort()
						return
					}
					// 硬桶 reject（容量满且无软桶兜底）：限流语义 → 429（带 Retry-After，与 queue 收尾一致），而非 503
					if errors.Is(err, model.ErrAllChannelsCapacityFull) {
						writeSoftRateLimit429(c)
						return
					}
					if err != nil {
						showGroup := usingGroup
						if usingGroup == "auto" {
							showGroup = fmt.Sprintf("auto(%s)", selectGroup)
						}
						message := i18n.T(c, i18n.MsgDistributorGetChannelFailed, map[string]any{"Group": showGroup, "Model": modelRequest.Model, "Error": common.MaskSensitiveInfo(err.Error())})
						// 如果错误，但是渠道不为空，说明是数据库一致性问题
						//if channel != nil {
						//	common.SysError(fmt.Sprintf("渠道不存在：%d", channel.Id))
						//	message = "数据库一致性已被破坏，请联系管理员"
						//}
						abortWithOpenAiMessage(c, http.StatusServiceUnavailable, message, types.ErrorCodeModelNotFound)
						return
					}
					if channel == nil {
						abortWithOpenAiMessage(c, http.StatusServiceUnavailable, i18n.T(c, i18n.MsgDistributorNoAvailableChannel, map[string]any{"Group": usingGroup, "Model": modelRequest.Model}), types.ErrorCodeModelNotFound)
						return
					}
				}
			}
		}
		common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
		SetupContextForSelectedChannel(c, channel, modelRequest.Model)
		// 记录 origin（首选渠道）的重试策略与缓存域，供 relay 重试时决定 roam 范围。
		// 任务查询类端点（MJ/Suno fetch、视频任务轮询等 shouldSelectChannel=false 路径）channel 为 nil：
		// 不写键，下游读到零值 = inherit/无域，等价旧行为。
		if channel != nil {
			common.SetContextKey(c, constant.ContextKeyOriginRetryStrategy, channel.GetRetryStrategy())
			common.SetContextKey(c, constant.ContextKeyOriginCacheDomain, channel.GetCacheDomain())
		}
		// 软 RPM 限流：在选完 channel 后、转发之前；命中 429 时已 c.Abort()，此处短路。
		SoftUserChannelRateLimit(c)
		if c.IsAborted() {
			return
		}
		c.Next()
		if channel != nil && c.Writer != nil && c.Writer.Status() < http.StatusBadRequest {
			service.RecordChannelAffinity(c, channel.Id)
		}
	}
}

// selectChannelWithQueue 选渠道；当 full_strategy=queue 且所有优先级渠道均满时，
// 在 QueueMaxWaitMs 内轮询重试（对应需求"优先转移到其他渠道 → 排队等待 → 超时 429"中的排队阶段；
// "转移"已由 model 层 capacity 过滤 + fallback 跨层完成，本函数只负责全满后的排队）。
//   - 选到渠道：立即返回 (channel, group, nil)。
//   - 排队超时：返回 model.ErrCapacityNeedQueue，由调用方转 429。
//   - 客户端断连：返回 ctx error，由调用方静默放弃。
//
// 排队期间不触发容量计数（IncrChannelCapacity 仅在选中后的 SetupContextForSelectedChannel 发生），
// 故多轮重选不会重复计数。
func selectChannelWithQueue(c *gin.Context, modelName, usingGroup string) (*model.Channel, string, error) {
	param := &service.RetryParam{
		Ctx:         c,
		ModelName:   modelName,
		TokenGroup:  usingGroup,
		RequestPath: c.Request.URL.Path,
		Retry:       common.GetPointer(0),
	}
	channel, selectGroup, err := service.CacheGetRandomSatisfiedChannel(param)
	// 成功 / 其它错误 / 非 queue 策略：直接返回，零额外开销
	if !errors.Is(err, model.ErrCapacityNeedQueue) {
		return channel, selectGroup, err
	}

	maxWait := time.Duration(operation_setting.GetQueueMaxWaitMs()) * time.Millisecond
	pollBase := operation_setting.GetQueuePollIntervalMs()
	if pollBase <= 0 {
		pollBase = 500
	}
	deadline := time.Now().Add(maxWait)
	reqCtx := c.Request.Context()
	waited := 0

	for {
		// 轮询间隔叠加 [0.5x, 1.5x] 抖动，避免多请求同时唤醒抢同一释放名额（惊群）
		sleepMs := pollBase/2 + rand.Intn(pollBase+1)
		sleep := time.Duration(sleepMs) * time.Millisecond
		if remain := time.Until(deadline); remain < sleep {
			sleep = remain
		}
		if sleep > 0 {
			select {
			case <-reqCtx.Done():
				return nil, selectGroup, reqCtx.Err() // 客户端断连
			case <-time.After(sleep):
				waited += int(sleep / time.Millisecond)
			}
		}

		// 重新选渠道：容量可能已释放，或其它渠道腾出名额
		param.SetRetry(0)
		param.ExcludedChannelIDs = nil
		common.SetContextKey(c, constant.ContextKeyAutoGroupIndex, 0) // reset cursor; CacheGetRandomSatisfiedChannel overwrites AutoGroupIndex on each call
		channel, selectGroup, err = service.CacheGetRandomSatisfiedChannel(param)
		if !errors.Is(err, model.ErrCapacityNeedQueue) {
			if channel != nil {
				common.SysLog(fmt.Sprintf("capacity queue: satisfied after ~%dms group=%s model=%s", waited, usingGroup, modelName))
			}
			return channel, selectGroup, err
		}
		if !time.Now().Before(deadline) {
			common.SysLog(fmt.Sprintf("capacity queue: timeout after ~%dms group=%s model=%s", waited, usingGroup, modelName))
			return nil, selectGroup, model.ErrCapacityNeedQueue
		}
	}
}

func channelSupportsRequestPath(channel *model.Channel, requestPath string, requestModel string) bool {
	if channel == nil {
		return false
	}
	if channel.Type != constant.ChannelTypeAdvancedCustom {
		return true
	}
	config := channel.GetOtherSettings().AdvancedCustom
	return config != nil && config.SupportsPathForModel(requestPath, requestModel)
}

// getModelFromRequest 从请求中读取模型信息
// 根据 Content-Type 自动处理：
// - application/json
// - application/x-www-form-urlencoded
// - multipart/form-data
func getModelFromRequest(c *gin.Context) (*ModelRequest, error) {
	if strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/json") {
		return getModelFromJSONRequest(c)
	}

	var modelRequest ModelRequest
	err := common.UnmarshalBodyReusable(c, &modelRequest)
	if err != nil {
		return nil, errors.New(i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": common.MaskSensitiveInfo(err.Error())}))
	}
	return &modelRequest, nil
}

func getModelFromJSONRequest(c *gin.Context) (*ModelRequest, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, errors.New(i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": common.MaskSensitiveInfo(err.Error())}))
	}
	if _, seekErr := storage.Seek(0, io.SeekStart); seekErr != nil {
		return nil, errors.New(i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": common.MaskSensitiveInfo(seekErr.Error())}))
	}
	if storage.IsDisk() {
		var modelRequest ModelRequest
		if err := common.DecodeJson(storage, &modelRequest); err != nil {
			return nil, errors.New(i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": common.MaskSensitiveInfo(err.Error())}))
		}
		if _, seekErr := storage.Seek(0, io.SeekStart); seekErr != nil {
			return nil, errors.New(i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": common.MaskSensitiveInfo(seekErr.Error())}))
		}
		c.Request.Body = io.NopCloser(storage)
		return &modelRequest, nil
	}

	requestBody, err := storage.Bytes()
	if err != nil {
		return nil, errors.New(i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": common.MaskSensitiveInfo(err.Error())}))
	}
	if !gjson.ValidBytes(requestBody) {
		return nil, errors.New(i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": common.MaskSensitiveInfo("invalid json")}))
	}

	modelResult := gjson.GetBytes(requestBody, "model")
	groupResult := gjson.GetBytes(requestBody, "group")
	modelRequest := &ModelRequest{}
	if modelResult.Exists() {
		if modelResult.Type != gjson.String {
			return nil, errors.New(i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": common.MaskSensitiveInfo("model must be a string")}))
		}
		modelRequest.Model = modelResult.String()
	}
	if groupResult.Exists() {
		if groupResult.Type != gjson.String {
			return nil, errors.New(i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": common.MaskSensitiveInfo("group must be a string")}))
		}
		modelRequest.Group = groupResult.String()
	}
	if _, seekErr := storage.Seek(0, io.SeekStart); seekErr != nil {
		return nil, errors.New(i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": common.MaskSensitiveInfo(seekErr.Error())}))
	}
	c.Request.Body = io.NopCloser(storage)
	return modelRequest, nil
}

func getModelRequest(c *gin.Context) (*ModelRequest, bool, error) {
	var modelRequest ModelRequest
	shouldSelectChannel := true
	var err error
	if strings.Contains(c.Request.URL.Path, "/mj/") {
		relayMode := relayconstant.Path2RelayModeMidjourney(c.Request.URL.Path)
		if relayMode == relayconstant.RelayModeMidjourneyTaskFetch ||
			relayMode == relayconstant.RelayModeMidjourneyTaskFetchByCondition ||
			relayMode == relayconstant.RelayModeMidjourneyNotify ||
			relayMode == relayconstant.RelayModeMidjourneyTaskImageSeed {
			shouldSelectChannel = false
		} else {
			midjourneyRequest := dto.MidjourneyRequest{}
			err = common.UnmarshalBodyReusable(c, &midjourneyRequest)
			if err != nil {
				return nil, false, errors.New(i18n.T(c, i18n.MsgDistributorInvalidMidjourney, map[string]any{"Error": common.MaskSensitiveInfo(err.Error())}))
			}
			midjourneyModel, mjErr, success := service.GetMjRequestModel(relayMode, &midjourneyRequest)
			if mjErr != nil {
				return nil, false, fmt.Errorf("%s", mjErr.Description)
			}
			if midjourneyModel == "" {
				if !success {
					return nil, false, fmt.Errorf("%s", i18n.T(c, i18n.MsgDistributorInvalidParseModel))
				} else {
					// task fetch, task fetch by condition, notify
					shouldSelectChannel = false
				}
			}
			modelRequest.Model = midjourneyModel
		}
		c.Set("relay_mode", relayMode)
	} else if strings.Contains(c.Request.URL.Path, "/suno/") {
		relayMode := relayconstant.Path2RelaySuno(c.Request.Method, c.Request.URL.Path)
		if relayMode == relayconstant.RelayModeSunoFetch ||
			relayMode == relayconstant.RelayModeSunoFetchByID {
			shouldSelectChannel = false
		} else {
			modelName := service.CoverTaskActionToModelName(constant.TaskPlatformSuno, c.Param("action"))
			modelRequest.Model = modelName
		}
		c.Set("platform", string(constant.TaskPlatformSuno))
		c.Set("relay_mode", relayMode)
	} else if strings.Contains(c.Request.URL.Path, "/v1/videos/") && strings.HasSuffix(c.Request.URL.Path, "/remix") {
		relayMode := relayconstant.RelayModeVideoSubmit
		c.Set("relay_mode", relayMode)
		shouldSelectChannel = false
	} else if strings.Contains(c.Request.URL.Path, "/v1/videos") {
		//curl https://api.openai.com/v1/videos \
		//  -H "Authorization: Bearer $OPENAI_API_KEY" \
		//  -F "model=sora-2" \
		//  -F "prompt=A calico cat playing a piano on stage"
		//	-F input_reference="@image.jpg"
		relayMode := relayconstant.RelayModeUnknown
		if c.Request.Method == http.MethodPost {
			relayMode = relayconstant.RelayModeVideoSubmit
			req, err := getModelFromRequest(c)
			if err != nil {
				return nil, false, err
			}
			if req != nil {
				modelRequest.Model = req.Model
			}
		} else if c.Request.Method == http.MethodGet {
			relayMode = relayconstant.RelayModeVideoFetchByID
			shouldSelectChannel = false
		}
		c.Set("relay_mode", relayMode)
	} else if strings.Contains(c.Request.URL.Path, "/v1/video/generations") {
		relayMode := relayconstant.RelayModeUnknown
		if c.Request.Method == http.MethodPost {
			req, err := getModelFromRequest(c)
			if err != nil {
				return nil, false, err
			}
			modelRequest.Model = req.Model
			relayMode = relayconstant.RelayModeVideoSubmit
		} else if c.Request.Method == http.MethodGet {
			relayMode = relayconstant.RelayModeVideoFetchByID
			shouldSelectChannel = false
		}
		if _, ok := c.Get("relay_mode"); !ok {
			c.Set("relay_mode", relayMode)
		}
	} else if strings.HasPrefix(c.Request.URL.Path, "/v1beta/models/") || strings.HasPrefix(c.Request.URL.Path, "/v1/models/") {
		// Gemini API 路径处理: /v1beta/models/gemini-2.0-flash:generateContent
		relayMode := relayconstant.RelayModeGemini
		modelName := extractModelNameFromGeminiPath(c.Request.URL.Path)
		if modelName != "" {
			modelRequest.Model = modelName
		}
		c.Set("relay_mode", relayMode)
	} else if !strings.HasPrefix(c.Request.URL.Path, "/v1/audio/transcriptions") && !strings.Contains(c.Request.Header.Get("Content-Type"), "multipart/form-data") {
		req, err := getModelFromRequest(c)
		if err != nil {
			return nil, false, err
		}
		modelRequest.Model = req.Model
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/realtime") {
		//wss://api.openai.com/v1/realtime?model=gpt-4o-realtime-preview-2024-10-01
		modelRequest.Model = c.Query("model")
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/moderations") {
		if modelRequest.Model == "" {
			modelRequest.Model = "text-moderation-stable"
		}
	}
	if strings.HasSuffix(c.Request.URL.Path, "embeddings") {
		if modelRequest.Model == "" {
			modelRequest.Model = c.Param("model")
		}
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/images/generations") {
		modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "dall-e")
	} else if strings.HasPrefix(c.Request.URL.Path, "/v1/images/edits") {
		//modelRequest.Model = common.GetStringIfEmpty(c.PostForm("model"), "gpt-image-1")
		contentType := c.ContentType()
		if slices.Contains([]string{gin.MIMEPOSTForm, gin.MIMEMultipartPOSTForm}, contentType) {
			req, err := getModelFromRequest(c)
			if err == nil && req.Model != "" {
				modelRequest.Model = req.Model
			}
		}
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/audio") {
		relayMode := relayconstant.RelayModeAudioSpeech
		if strings.HasPrefix(c.Request.URL.Path, "/v1/audio/speech") {

			modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "tts-1")
		} else if strings.HasPrefix(c.Request.URL.Path, "/v1/audio/translations") {
			// 先尝试从请求读取
			if req, err := getModelFromRequest(c); err == nil && req.Model != "" {
				modelRequest.Model = req.Model
			}
			modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "whisper-1")
			relayMode = relayconstant.RelayModeAudioTranslation
		} else if strings.HasPrefix(c.Request.URL.Path, "/v1/audio/transcriptions") {
			// 先尝试从请求读取
			if req, err := getModelFromRequest(c); err == nil && req.Model != "" {
				modelRequest.Model = req.Model
			}
			modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "whisper-1")
			relayMode = relayconstant.RelayModeAudioTranscription
		}
		c.Set("relay_mode", relayMode)
	}
	if strings.HasPrefix(c.Request.URL.Path, "/pg/chat/completions") {
		// playground chat completions
		req, err := getModelFromRequest(c)
		if err != nil {
			return nil, false, err
		}
		modelRequest.Model = req.Model
		modelRequest.Group = req.Group
		common.SetContextKey(c, constant.ContextKeyTokenGroup, modelRequest.Group)
	}

	if strings.HasPrefix(c.Request.URL.Path, "/v1/responses/compact") && modelRequest.Model != "" {
		modelRequest.Model = ratio_setting.WithCompactModelSuffix(modelRequest.Model)
	}
	return &modelRequest, shouldSelectChannel, nil
}

func SetupContextForSelectedChannel(c *gin.Context, channel *model.Channel, modelName string) *types.NewAPIError {
	c.Set("original_model", modelName) // for retry
	if channel == nil {
		return types.NewError(errors.New("channel is nil"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	common.SetContextKey(c, constant.ContextKeyChannelId, channel.Id)
	common.SetContextKey(c, constant.ContextKeyChannelName, channel.Name)
	common.SetContextKey(c, constant.ContextKeyChannelType, channel.Type)
	common.SetContextKey(c, constant.ContextKeyChannelCreateTime, channel.CreatedTime)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, channel.GetSetting())
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, channel.GetOtherSettings())
	paramOverride := channel.GetParamOverride()
	headerOverride := channel.GetHeaderOverride()
	if mergedParam, applied := service.ApplyChannelAffinityOverrideTemplate(c, paramOverride); applied {
		paramOverride = mergedParam
	}
	common.SetContextKey(c, constant.ContextKeyChannelParamOverride, paramOverride)
	common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, headerOverride)
	if nil != channel.OpenAIOrganization && *channel.OpenAIOrganization != "" {
		common.SetContextKey(c, constant.ContextKeyChannelOrganization, *channel.OpenAIOrganization)
	}
	common.SetContextKey(c, constant.ContextKeyChannelAutoBan, channel.GetAutoBan())
	common.SetContextKey(c, constant.ContextKeyChannelModelMapping, channel.GetModelMapping())
	common.SetContextKey(c, constant.ContextKeyChannelStatusCodeMapping, channel.GetStatusCodeMapping())

	key, index, newAPIError := channel.GetNextEnabledKey()
	if newAPIError != nil {
		return newAPIError
	}
	if channel.ChannelInfo.IsMultiKey {
		common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, true)
		common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, index)
	} else {
		// 必须设置为 false，否则在重试到单个 key 的时候会导致日志显示错误
		common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, false)
	}
	// c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	common.SetContextKey(c, constant.ContextKeyChannelKey, key)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, channel.GetBaseURL())

	common.SetContextKey(c, constant.ContextKeySystemPromptOverride, false)

	// TODO: api_version统一
	switch channel.Type {
	case constant.ChannelTypeAzure:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeVertexAi:
		c.Set("region", channel.Other)
	case constant.ChannelTypeXunfei:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeGemini:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeAli:
		c.Set("plugin", channel.Other)
	case constant.ChannelCloudflare:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeMokaAI:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeCoze:
		c.Set("bot_id", channel.Other)
	}

	// 渠道路由模式：选中渠道后异步 INCR 容量计数器
	// 触发条件（任一）：
	//   1) 全局 dry-run on（评估真实流量分布）
	//   2) 渠道实际处于 capacity 模式（真实计数被主链路 filterByCapacity 消费）
	// docs/2026-05-26-channel-routing-mode-switchable.md §5.3
	recordChannelCapacity(channel)

	return nil
}

// recordChannelCapacity 选中渠道后异步 INCR 容量计数器。
// dry-run on：必记
// capacity 模式（全局或渠道级）：必记，让 P2 主链路的 filterByCapacity 能读到
// 二者都关：零开销直接 return。
//
// 采样日志（仅 dry-run 时启用）：默认 1/100，避免高 QPS 下日志爆炸。
func recordChannelCapacity(channel *model.Channel) {
	cfg := operation_setting.GetChannelRoutingConfig()
	dryRun := cfg.DryRun
	realCapacity := model.ResolveRoutingMode(channel, cfg.Mode) == constant.RoutingModeCapacity

	if !dryRun && !realCapacity {
		return
	}

	channelID := channel.Id
	effectiveWindow := operation_setting.GetEffectiveWindowSec(channel.GetCapacityWindowSec())
	_ = backgroundtask.Submit("channel-capacity-record", func(context.Context) {
		defer func() {
			if r := recover(); r != nil {
				common.SysLog(fmt.Sprintf("recordChannelCapacity panic: %v", r))
			}
		}()
		service.IncrChannelCapacity(channelID, effectiveWindow)
		// dry-run 走采样日志（仅评估用）；真实 capacity 模式不需要每次都打日志
		if dryRun && cfg.DryRunSampleRate > 0 && rand.Float64() < cfg.DryRunSampleRate {
			count := service.GetChannelCapacityCount(channelID, cfg.CapacityWindowSec)
			common.SysLog(fmt.Sprintf("[channel-routing dry-run] channel=%d window=%ds count=%.1f",
				channelID, cfg.CapacityWindowSec, count))
		}
	})
}

// extractModelNameFromGeminiPath 从 Gemini API URL 路径中提取模型名
// 输入格式: /v1beta/models/gemini-2.0-flash:generateContent
// 输出: gemini-2.0-flash
func extractModelNameFromGeminiPath(path string) string {
	// 查找 "/models/" 的位置
	modelsPrefix := "/models/"
	modelsIndex := strings.Index(path, modelsPrefix)
	if modelsIndex == -1 {
		return ""
	}

	// 从 "/models/" 之后开始提取
	startIndex := modelsIndex + len(modelsPrefix)
	if startIndex >= len(path) {
		return ""
	}

	// 查找 ":" 的位置，模型名在 ":" 之前
	colonIndex := strings.Index(path[startIndex:], ":")
	if colonIndex == -1 {
		// 如果没有找到 ":"，返回从 "/models/" 到路径结尾的部分
		return path[startIndex:]
	}

	// 返回模型名部分
	return path[startIndex : startIndex+colonIndex]
}
