package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/pkg/httplifecycle"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func relayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	var err *types.NewAPIError
	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		err = relay.ImageHelper(c, info)
	case relayconstant.RelayModeAudioSpeech:
		fallthrough
	case relayconstant.RelayModeAudioTranslation:
		fallthrough
	case relayconstant.RelayModeAudioTranscription:
		err = relay.AudioHelper(c, info)
	case relayconstant.RelayModeRerank:
		err = relay.RerankHelper(c, info)
	case relayconstant.RelayModeEmbeddings:
		err = relay.EmbeddingHelper(c, info)
	case relayconstant.RelayModeResponses, relayconstant.RelayModeResponsesCompact:
		err = relay.ResponsesHelper(c, info)
	default:
		err = relay.TextHelper(c, info)
	}
	return err
}

func geminiRelayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	var err *types.NewAPIError
	if strings.Contains(c.Request.URL.Path, "embed") {
		err = relay.GeminiEmbeddingHandler(c, info)
	} else {
		err = relay.GeminiHelper(c, info)
	}
	return err
}

// recordURLOutcome 把一次 base_url 尝试结果喂给 URL 健康/延迟模块（service/url_health）：
//
//	成功 → 记 TTFB（首字节优先，回退到本次尝试耗时）；
//	节点无响应 → 记失败（累积触发熔断）；
//	业务错误 / 上游超时 → 不计入（非节点问题，避免误熔断健康节点）。
func recordURLOutcome(channelId int, url string, info *relaycommon.RelayInfo, attemptStart time.Time, apiErr *types.NewAPIError) {
	if url == "" {
		return
	}
	if apiErr == nil {
		latMs := float64(time.Since(attemptStart).Milliseconds())
		if !info.FirstResponseTime.IsZero() && info.FirstResponseTime.After(attemptStart) {
			latMs = float64(info.FirstResponseTime.Sub(attemptStart).Milliseconds())
		}
		service.RecordURLResult(channelId, url, true, latMs)
		return
	}
	if apiErr.GetErrorCode() == types.ErrorCodeUpstreamNoResponse {
		service.RecordURLResult(channelId, url, false, 0)
	}
}

func Relay(c *gin.Context, relayFormat types.RelayFormat) {

	requestId := c.GetString(common.RequestIdKey)
	//group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	//originalModel := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)

	var (
		newAPIError *types.NewAPIError
		ws          *websocket.Conn
	)

	if relayFormat == types.RelayFormatOpenAIRealtime {
		var err error
		ws, err = upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			helper.WssError(c, ws, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry()).ToOpenAIError())
			return
		}
		unregister := httplifecycle.RegisterHijacked(c.Request.Context(), func() {
			deadline := time.Now().Add(time.Second)
			_ = ws.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutdown"),
				deadline,
			)
			_ = ws.Close()
		})
		defer unregister()
		defer ws.Close()
	}

	// 令牌错误率熔断的终态埋点已上移到 relay 链路中间件 middleware.TokenHealthRecord()。
	// 中间件优先读 context key "token_health_status"（下方错误 defer 写入真实 StatusCode），
	// 否则回退 c.Writer.Status()——因为 realtime(WebSocket) 升级后错误经 Wss message 下发、
	// 不写 HTTP 状态码，此时 c.Writer.Status() 不可靠；写 context key 让所有终态都被权威记录。

	defer func() {
		if newAPIError != nil {
			// 供 TokenHealthRecord 中间件读取的权威终态状态码（覆盖 realtime/流式等 writer 状态码不准的场景）。
			c.Set("token_health_status", newAPIError.StatusCode)
			logger.LogError(c, fmt.Sprintf("relay error: %s", common.LocalLogPreview(common.MaskSensitiveInfo(newAPIError.Error()))))
			newAPIError.SetMessage(common.MessageWithRequestId(newAPIError.Error(), requestId))
			switch relayFormat {
			case types.RelayFormatOpenAIRealtime:
				helper.WssError(c, ws, newAPIError.ToOpenAIError())
			case types.RelayFormatClaude:
				c.JSON(newAPIError.StatusCode, gin.H{
					"type":  "error",
					"error": newAPIError.ToClaudeError(),
				})
			default:
				c.JSON(newAPIError.StatusCode, gin.H{
					"error": newAPIError.ToOpenAIError(),
				})
			}
		}
	}()

	request, err := helper.GetAndValidateRequest(c, relayFormat)
	if err != nil {
		// Map "request body too large" to 413 so clients can handle it correctly
		if common.IsRequestBodyTooLargeError(err) || errors.Is(err, common.ErrRequestBodyTooLarge) {
			newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
		} else {
			// 请求体非法/字段缺失属客户端错误，显式 400（默认 NewError 为 500），
			// 既给客户端正确状态码，也让令牌错误率熔断能把这类"坏请求"计入。
			newAPIError = types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithStatusCode(http.StatusBadRequest))
		}
		return
	}

	relayInfo, err := relaycommon.GenRelayInfo(c, relayFormat, request, ws)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeGenRelayInfoFailed)
		return
	}

	// ── 非流式断连重试短路（拦截侧）：同一请求刚在长时间运行后被客户端取消（疑似 SDK 超时后
	// 自动重试）→ 立即 400 拒绝（官方 SDK 对 400 不自动重试），终止烧钱循环。
	// 位置在预扣费/渠道选择之前：命中时零计费、零渠道副作用、不进重试循环。
	// realtime 无请求体、流式请求不受影响，均跳过。
	if relayFormat != types.RelayFormatOpenAIRealtime && !relayInfo.IsStream {
		if hit, canceledAfterSec, retryAfterMin := service.CheckRetryShortCircuit(c, relayInfo.TokenId, relayInfo.OriginModelName); hit {
			msg := fmt.Sprintf(
				"An identical request was canceled by your client %d seconds after start (likely client-side timeout; official SDK default is 600s). A non-streaming request of this size cannot finish in time — use stream=true, or raise your client timeout. If already fixed, retry after %d minutes. | 相同请求刚在运行 %d 秒后被客户端取消（疑似客户端超时,官方 SDK 默认 600 秒）。请改用 stream=true 或调大客户端超时后重试；若已调整,请约 %d 分钟后再试。",
				canceledAfterSec, retryAfterMin, canceledAfterSec, retryAfterMin)
			newAPIError = types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeRetryShortCircuitActive, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			// 保护性拦截 ≠ 令牌不健康：复用计费拒绝同款豁免（见下方 PreConsume 失败处），
			// 避免 TTL 内密集重放的 400 把 token 推进错误率熔断冷却、殃及同 token 正常流量。
			c.Set("token_health_skip", true)
			// 顺路写 type=5 错误日志（error_code=retry_short_circuit_active，便于统计短路拦截量）
			if constant.ErrorLogEnabled {
				other := map[string]interface{}{
					"request_path": c.Request.URL.Path,
					"error_type":   newAPIError.GetErrorType(),
					"error_code":   newAPIError.GetErrorCode(),
					"status_code":  newAPIError.StatusCode,
				}
				model.RecordErrorLog(c, relayInfo.UserId, 0, relayInfo.OriginModelName, c.GetString("token_name"), newAPIError.MaskSensitiveErrorWithStatusCode(), relayInfo.TokenId, 0, false, c.GetString("group"), other)
			}
			return
		}
	}

	needSensitiveCheck := setting.ShouldCheckPromptSensitive()
	needCountToken := constant.CountToken
	// Avoid building huge CombineText (strings.Join) when token counting and sensitive check are both disabled.
	var meta *types.TokenCountMeta
	if needSensitiveCheck || needCountToken {
		meta = request.GetTokenCountMeta()
	} else {
		meta = fastTokenCountMetaForPricing(request)
	}

	if needSensitiveCheck && meta != nil {
		contains, words := service.CheckSensitiveText(meta.CombineText)
		if contains {
			logger.LogWarn(c, fmt.Sprintf("user sensitive words detected: %s", strings.Join(words, ", ")))
			newAPIError = types.NewError(err, types.ErrorCodeSensitiveWordsDetected)
			return
		}
	}

	tokens, err := service.EstimateRequestToken(c, meta, relayInfo)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeCountTokenFailed)
		return
	}

	relayInfo.SetEstimatePromptTokens(tokens)

	priceData, err := helper.ModelPriceHelper(c, relayInfo, tokens, meta)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest))
		return
	}

	// common.SetContextKey(c, constant.ContextKeyTokenCountMeta, meta)

	if priceData.FreeModel {
		logger.LogInfo(c, fmt.Sprintf("模型 %s 免费，跳过预扣费", relayInfo.OriginModelName))
	} else {
		newAPIError = service.PreConsumeBilling(c, priceData.QuotaToPreConsume, relayInfo)
		if newAPIError != nil {
			// 计费阶段失败（余额不足 403 / 计费查询等内部错误）均不计入令牌错误率：
			// 这不代表"该令牌的请求本身有问题"，跳过可避免欠费令牌被误冷却、也不污染统计分母。
			c.Set("token_health_skip", true)
			return
		}
	}

	defer func() {
		// Only return quota if downstream failed and quota was actually pre-consumed
		if newAPIError != nil {
			newAPIError = service.NormalizeViolationFeeError(newAPIError)
			if relayInfo.Billing != nil {
				relayInfo.Billing.Refund(c)
			}
			service.ChargeViolationFeeIfNeeded(c, relayInfo, newAPIError)
		}
	}()

	retryParam := &service.RetryParam{
		Ctx:               c,
		TokenGroup:        relayInfo.TokenGroup,
		ModelName:         relayInfo.OriginModelName,
		RequestPath:       c.Request.URL.Path,
		Retry:             common.GetPointer(0),
		OriginCacheDomain: common.GetContextKeyString(c, constant.ContextKeyOriginCacheDomain),
	}
	relayInfo.RetryIndex = 0
	relayInfo.LastError = nil

	// ── 请求级总 deadline / per-model timeout / 指数退避（由 admin 后台 relay_retry_setting 控制）──
	relayCfg := operation_setting.GetRelayRetryConfig()
	relayStart := time.Now()
	baseReqCtx := c.Request.Context()
	totalCtx := baseReqCtx
	var totalCancel context.CancelFunc
	if relayCfg.TotalTimeoutSeconds > 0 {
		totalCtx, totalCancel = context.WithTimeout(baseReqCtx, time.Duration(relayCfg.TotalTimeoutSeconds)*time.Second)
		defer totalCancel()
		c.Request = c.Request.WithContext(totalCtx)
	}
	modelTimeout := operation_setting.ResolveModelTimeout(relayInfo.OriginModelName)

	for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
		// 每轮渠道重试先清空，避免在进入 HTTP 请求前失败时沿用上一轮上游请求 ID。
		c.Set(common.UpstreamRequestIdKey, "")
		// 上游拒绝标记同理必须按轮清空：渠道A写入 refusal/content_filter 后失败重试，
		// 渠道B的零产出若继承陈旧标记会被错误拒退（免单判定读该键）。
		common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "")

		// 总 deadline 触发：直接退出，返回 504 + relay_deadline_exceeded
		if err := totalCtx.Err(); err != nil {
			elapsed := time.Since(relayStart).Milliseconds()
			logger.LogError(c, fmt.Sprintf("relay: total deadline exceeded after %dms (channels tried: %v)", elapsed, c.GetStringSlice("use_channel")))
			newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeRelayDeadlineExceeded, http.StatusGatewayTimeout, types.ErrOptionWithSkipRetry())
			break
		}

		relayInfo.RetryIndex = retryParam.GetRetry()
		channel, channelErr := getChannel(c, relayInfo, retryParam)
		if channelErr != nil {
			logger.LogError(c, channelErr.Error())
			if errors.Is(channelErr, model.ErrCapacityNeedQueue) {
				newAPIError = relayInfo.LastError
				break
			}
			// 重试轮选不到渠道（如 same_domain 域内候选耗尽）：保留上一轮真实上游错误（429/5xx），
			// 不要用"无可用渠道"500 覆盖客户端本应看到的信息；首轮无前置错误时仍返回选渠道错误。
			if relayInfo.LastError != nil {
				newAPIError = relayInfo.LastError
			} else {
				if errors.As(channelErr, &newAPIError) {
					break
				}
				newAPIError = types.NewError(channelErr, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
			}
			break
		}

		// 指数退避 + jitter（仅重试轮）：放在成功选到渠道之后——same_domain 候选耗尽等选不到
		// 渠道的情况直接快速失败，不再白等一轮。被 totalCtx 取消时立即结束等待并按总 deadline 退出。
		if retryParam.GetRetry() > 0 {
			if backoff := operation_setting.ComputeRetryBackoff(retryParam.GetRetry()-1, relayCfg.BackoffBaseMs, relayCfg.BackoffMaxMs); backoff > 0 {
				timer := time.NewTimer(backoff)
				select {
				case <-timer.C:
				case <-totalCtx.Done():
					timer.Stop()
				}
				if err := totalCtx.Err(); err != nil {
					elapsed := time.Since(relayStart).Milliseconds()
					logger.LogError(c, fmt.Sprintf("relay: total deadline exceeded after %dms (channels tried: %v)", elapsed, c.GetStringSlice("use_channel")))
					newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeRelayDeadlineExceeded, http.StatusGatewayTimeout, types.ErrOptionWithSkipRetry())
					break
				}
			}
		}

		addUsedChannel(c, channel.Id)
		bodyStorage, bodyErr := common.GetBodyStorage(c)
		if bodyErr != nil {
			// Ensure consistent 413 for oversized bodies even when error occurs later (e.g., retry path)
			if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
				newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
			} else {
				newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
			break
		}
		// 每轮重试前把 body storage 重置到起点：retry / PassThrough 会直接把 storage 当 Request.Body 用，
		// 第一轮读到 EOF 后若不 seek，第二轮重发空 body；叠加本次新增的 ContentLength=Size() 会让上游挂起等超时
		if _, seekErr := bodyStorage.Seek(0, io.SeekStart); seekErr != nil {
			newAPIError = types.NewErrorWithStatusCode(seekErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			break
		}
		c.Request.Body = io.NopCloser(bodyStorage)

		// per-model timeout：把更短的 ctx 套到 c.Request，下游 doRequest 会用这个 ctx 调 client.Do
		callCtx := totalCtx
		var callCancel context.CancelFunc
		if modelTimeout > 0 {
			callCtx, callCancel = context.WithTimeout(totalCtx, time.Duration(modelTimeout)*time.Second)
			c.Request = c.Request.WithContext(callCtx)
		}

		callHandler := func() *types.NewAPIError {
			// 多 base_url 会在同一渠道重入 handler；转换/组装阶段也必须从空 ID 开始。
			c.Set(common.UpstreamRequestIdKey, "")
			common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "")
			switch relayFormat {
			case types.RelayFormatOpenAIRealtime:
				return relay.WssHelper(c, relayInfo)
			case types.RelayFormatClaude:
				return relay.ClaudeHelper(c, relayInfo)
			case types.RelayFormatGemini:
				return geminiRelayHandler(c, relayInfo)
			default:
				return relayHandler(c, relayInfo)
			}
		}

		// ── 渠道内多 base_url 故障切换：网络无响应时在同渠道内换 URL 重试（fastest 择优），URL 用尽才升级换渠道 ──
		// 自洽性：仅 ErrorCodeUpstreamNoResponse（连不上/TLS/响应头不回）触发切换，此时必尚未向客户端写出任何字节，
		// 故同渠道换 URL 重发安全；context 超时（上游慢）已被归为 ErrorCodeUpstreamTimeout，不在此切换。
		effectiveURLs := channel.GetEffectiveBaseURLs()
		if len(effectiveURLs) <= 1 {
			// 单 URL（绝大多数渠道）：保持原行为，零侵入；仍记录延迟/健康供 fastest 与观测使用
			attemptStart := time.Now()
			newAPIError = callHandler()
			recordURLOutcome(channel.Id, common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl), relayInfo, attemptStart, newAPIError)
		} else {
			tried := make(map[string]bool, len(effectiveURLs))
			for urlAttempt := 0; urlAttempt < len(effectiveURLs); urlAttempt++ {
				pickedURL := service.PickURLForChannel(channel.Id, effectiveURLs)
				if pickedURL == "" || tried[pickedURL] {
					// PickURL 复选了已试过的（如全熔断兜底）：改挑一个未试过的；都试过则结束子循环
					pickedURL = ""
					for _, u := range effectiveURLs {
						if !tried[u] {
							pickedURL = u
							break
						}
					}
					if pickedURL == "" {
						break
					}
				}
				tried[pickedURL] = true
				// 覆盖 context 的 ChannelBaseUrl，handler 内 InitChannelMeta 会重读 → adaptor 用新 URL 拼出站地址
				common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, pickedURL)
				if urlAttempt > 0 {
					// 换 URL 重发前重置 body（否则第二次发空 body + 错误 ContentLength 致上游挂起）
					if _, seekErr := bodyStorage.Seek(0, io.SeekStart); seekErr != nil {
						newAPIError = types.NewErrorWithStatusCode(seekErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
						break
					}
					c.Request.Body = io.NopCloser(bodyStorage)
				}
				attemptStart := time.Now()
				newAPIError = callHandler()
				recordURLOutcome(channel.Id, pickedURL, relayInfo, attemptStart, newAPIError)

				if newAPIError == nil {
					break // 成功
				}
				// 仅"节点无响应"且尚未向客户端写出任何内容时，才在同渠道内切下一个 URL
				if newAPIError.GetErrorCode() == types.ErrorCodeUpstreamNoResponse && !relayInfo.HasSendResponse() {
					continue
				}
				break // 业务错误 / 已写客户端 / 其他：交给外层渠道级逻辑
			}
		}

		// 释放 per-model ctx；恢复 c.Request 到 totalCtx，避免下一轮 retry 受上一轮 cancel 影响
		if callCancel != nil {
			callCancel()
			c.Request = c.Request.WithContext(totalCtx)
		}

		if newAPIError == nil {
			relayInfo.LastError = nil
			// 渠道健康度：记录成功（被动机制，零额外 token）
			channelId := channel.Id
			usingKey := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
			var ttftMs int64 = -1
			if relayInfo.IsStream && relayInfo.HasSendResponse() {
				ttftMs = relayInfo.FirstResponseTime.Sub(relayInfo.StartTime).Milliseconds()
			}
			_ = backgroundtask.Submit("channel-result-success", func(context.Context) {
				service.RecordChannelResult(channelId, usingKey, nil, ttftMs)
			})
			return
		}

		newAPIError = service.NormalizeViolationFeeError(newAPIError)
		relayInfo.LastError = newAPIError

		processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()), newAPIError)

		// 失败渠道加入排除列表，下次 retry 不再选它（核心：避免同 priority 加权随机回到同一个渠道）
		retryParam.ExcludeChannel(channel.Id)

		if !shouldRetry(c, newAPIError, common.RetryTimes-retryParam.GetRetry()) {
			break
		}
	}

	// ── 非流式断连重试短路（记录侧）：请求以失败收尾且下游连接已断（baseReqCtx 已取消 =
	// 客户端主动断开；网关自身 total/per-model 超时不会取消 baseReqCtx，天然区分）→
	// 记录请求指纹，TTL 内同一请求再来由上方入口直接 400 短路。运行时长门槛在 service 内判定。
	if newAPIError != nil && !relayInfo.IsStream && relayFormat != types.RelayFormatOpenAIRealtime && baseReqCtx.Err() != nil {
		service.RecordRetryShortCircuit(c, relayInfo.TokenId, relayInfo.OriginModelName, relayInfo.StartTime)
	}

	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}
	// 只有上游侧故障才计入可用率：客户端错误（参数非法/余额不足/敏感词）与网关自身故障
	// 不采样，否则会稀释"上游还活着吗"这个语义。
	if newAPIError != nil && types.IsUpstreamAttributedError(newAPIError) {
		_ = backgroundtask.Submit("relay-sample-failure", func(context.Context) {
			perfmetrics.RecordRelaySample(relayInfo, false, 0)
		})
	}
}

var upgrader = websocket.Upgrader{
	Subprotocols: []string{"realtime"}, // WS 握手支持的协议，如果有使用 Sec-WebSocket-Protocol，则必须在此声明对应的 Protocol TODO add other protocol
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许跨域
	},
}

func addUsedChannel(c *gin.Context, channelId int) {
	useChannel := c.GetStringSlice("use_channel")
	useChannel = append(useChannel, fmt.Sprintf("%d", channelId))
	c.Set("use_channel", useChannel)
}

func fastTokenCountMetaForPricing(request dto.Request) *types.TokenCountMeta {
	if request == nil {
		return &types.TokenCountMeta{}
	}
	meta := &types.TokenCountMeta{
		TokenType: types.TokenTypeTokenizer,
	}
	switch r := request.(type) {
	case *dto.GeneralOpenAIRequest:
		maxCompletionTokens := lo.FromPtrOr(r.MaxCompletionTokens, uint(0))
		maxTokens := lo.FromPtrOr(r.MaxTokens, uint(0))
		if maxCompletionTokens > maxTokens {
			meta.MaxTokens = int(maxCompletionTokens)
		} else {
			meta.MaxTokens = int(maxTokens)
		}
	case *dto.OpenAIResponsesRequest:
		meta.MaxTokens = int(lo.FromPtrOr(r.MaxOutputTokens, uint(0)))
	case *dto.ClaudeRequest:
		meta.MaxTokens = int(lo.FromPtr(r.MaxTokens))
	case *dto.ImageRequest:
		// Pricing for image requests depends on ImagePriceRatio; safe to compute even when CountToken is disabled.
		return r.GetTokenCountMeta()
	default:
		// Best-effort: leave CombineText empty to avoid large allocations.
	}
	return meta
}

func getChannel(c *gin.Context, info *relaycommon.RelayInfo, retryParam *service.RetryParam) (*model.Channel, error) {
	if info.ChannelMeta == nil {
		autoBan := c.GetBool("auto_ban")
		autoBanInt := 1
		if !autoBan {
			autoBanInt = 0
		}
		ch := &model.Channel{
			Id:      c.GetInt("channel_id"),
			Type:    c.GetInt("channel_type"),
			Name:    c.GetString("channel_name"),
			AutoBan: &autoBanInt,
		}
		// 补全 base_url 与 setting（distributor 的 SetupContextForSelectedChannel 已存入 context）。
		// 首次请求时 info.ChannelMeta 尚未初始化，若不补全，channel.GetEffectiveBaseURLs() 拿不到
		// backup_base_urls，会让多 base_url failover 退化成"要等渠道级 retry 才切换"。
		if bu := common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl); bu != "" {
			ch.BaseURL = &bu
		}
		if v, ok := common.GetContextKey(c, constant.ContextKeyChannelSetting); ok {
			if setting, ok2 := v.(dto.ChannelSettings); ok2 {
				ch.SetSetting(setting)
			}
		}
		return ch, nil
	}
	channel, selectGroup, err := service.CacheGetRandomSatisfiedChannel(retryParam)

	info.PriceData.GroupRatioInfo = helper.HandleGroupRatio(c, info)
	// 重试可能落到与首轮不同的分组，分层计价快照冻结的是首轮倍率，
	// 不同步会导致用首组倍率结算最终组的用量。
	if snap := info.TieredBillingSnapshot; snap != nil {
		snap.GroupRatio = info.PriceData.GroupRatioInfo.GroupRatio
	}

	if err != nil {
		if errors.Is(err, model.ErrCapacityNeedQueue) {
			return nil, err
		}
		return nil, types.NewError(fmt.Errorf("获取分组 %s 下模型 %s 的可用渠道失败（retry）: %s", selectGroup, info.OriginModelName, err.Error()), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if channel == nil {
		return nil, types.NewError(fmt.Errorf("分组 %s 下模型 %s 的可用渠道不存在（retry）", selectGroup, info.OriginModelName), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}

	newAPIError := middleware.SetupContextForSelectedChannel(c, channel, info.OriginModelName)
	if newAPIError != nil {
		return nil, newAPIError
	}
	return channel, nil
}

func shouldRetry(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	if openaiErr == nil {
		return false
	}
	if service.ShouldSkipRetryAfterChannelAffinityFailure(c) {
		return false
	}
	// 统一重试范围（渠道级 RetryStrategy 与 Key 级 relay_retry_policy 取最保守）：
	// NoCrossChannel（cost_guard / disabled）→ 网关侧完全停止重试（含同渠道换 key），
	// 快速失败交客户端重试（命中亲和回到同一热渠道，保住 prompt cache，避免换账号重算大额输入）。
	if service.EffectiveRetryScope(c) == service.RetryScopeNoCrossChannel {
		return false
	}
	if types.IsChannelError(openaiErr) {
		return true
	}
	if types.IsSkipRetryError(openaiErr) {
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	code := openaiErr.StatusCode
	if code >= 200 && code < 300 {
		return false
	}
	if code < 100 || code > 599 {
		return true
	}
	if operation_setting.IsAlwaysSkipRetryCode(openaiErr.GetErrorCode()) {
		return false
	}
	return operation_setting.ShouldRetryByStatusCode(code)
}

func processChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError) {
	logger.LogError(c, fmt.Sprintf("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(common.MaskSensitiveInfo(err.Error()))))
	// 不要使用context获取渠道信息，异步处理时可能会出现渠道信息不一致的情况
	// do not use context to get channel info, there may be inconsistent channel info when processing asynchronously

	// 渠道健康度：记录错误（被动机制，零额外 token）。
	// 在 ShouldDisableChannel 之前调用：状态机内部按阈值决定降级 / 禁用 / 单 key 失效。
	chId := channelError.ChannelId
	chKey := channelError.UsingKey
	chErr := err
	_ = backgroundtask.Submit("channel-result-failure", func(context.Context) {
		service.RecordChannelResult(chId, chKey, chErr, -1)
	})

	if service.ShouldDisableChannel(err) && channelError.AutoBan {
		_ = backgroundtask.Submit("disable-channel", func(context.Context) {
			service.DisableChannel(channelError, err.ErrorWithStatusCode())
		})
	}

	if constant.ErrorLogEnabled && types.IsRecordErrorLog(err) {
		// 保存错误日志到mysql中
		userId := c.GetInt("id")
		tokenName := c.GetString("token_name")
		modelName := c.GetString("original_model")
		tokenId := c.GetInt("token_id")
		userGroup := c.GetString("group")
		channelId := c.GetInt("channel_id")
		other := make(map[string]interface{})
		if c.Request != nil && c.Request.URL != nil {
			other["request_path"] = c.Request.URL.Path
		}
		other["error_type"] = err.GetErrorType()
		other["error_code"] = err.GetErrorCode()
		other["status_code"] = err.StatusCode
		other["channel_id"] = channelId
		other["channel_name"] = c.GetString("channel_name")
		other["channel_type"] = c.GetInt("channel_type")
		adminInfo := make(map[string]interface{})
		adminInfo["use_channel"] = c.GetStringSlice("use_channel")
		isMultiKey := common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey)
		if isMultiKey {
			adminInfo["is_multi_key"] = true
			adminInfo["multi_key_index"] = common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
		}
		service.AppendChannelAffinityAdminInfo(c, adminInfo)
		other["admin_info"] = adminInfo
		startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
		if startTime.IsZero() {
			startTime = time.Now()
		}
		useTimeSeconds := int(time.Since(startTime).Seconds())
		model.RecordErrorLog(c, userId, channelId, modelName, tokenName, err.MaskSensitiveErrorWithStatusCode(), tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
	}

}

func RelayMidjourney(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatMjProxy, nil, nil)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"description": fmt.Sprintf("failed to generate relay info: %s", err.Error()),
			"type":        "upstream_error",
			"code":        4,
		})
		return
	}

	var mjErr *dto.MidjourneyResponse
	switch relayInfo.RelayMode {
	case relayconstant.RelayModeMidjourneyNotify:
		mjErr = relay.RelayMidjourneyNotify(c)
	case relayconstant.RelayModeMidjourneyTaskFetch, relayconstant.RelayModeMidjourneyTaskFetchByCondition:
		mjErr = relay.RelayMidjourneyTask(c, relayInfo.RelayMode)
	case relayconstant.RelayModeMidjourneyTaskImageSeed:
		mjErr = relay.RelayMidjourneyTaskImageSeed(c)
	case relayconstant.RelayModeSwapFace:
		mjErr = relay.RelaySwapFace(c, relayInfo)
	default:
		mjErr = relay.RelayMidjourneySubmit(c, relayInfo)
	}
	//err = relayMidjourneySubmit(c, relayMode)
	log.Println(mjErr)
	if mjErr != nil {
		statusCode := http.StatusBadRequest
		if mjErr.Code == 30 {
			mjErr.Result = "当前分组负载已饱和，请稍后再试，或升级账户以提升服务质量。"
			statusCode = http.StatusTooManyRequests
		}
		c.JSON(statusCode, gin.H{
			"description": fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result),
			"type":        "upstream_error",
			"code":        mjErr.Code,
		})
		channelId := c.GetInt("channel_id")
		logger.LogError(c, fmt.Sprintf("relay error (channel #%d, status code %d): %s", channelId, statusCode, fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result)))
	}
}

func RelayNotImplemented(c *gin.Context) {
	err := types.OpenAIError{
		Message: "API not implemented",
		Type:    "new_api_error",
		Param:   "",
		Code:    "api_not_implemented",
	}
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": err,
	})
}

func RelayNotFound(c *gin.Context) {
	err := types.OpenAIError{
		Message: fmt.Sprintf("Invalid URL (%s %s)", c.Request.Method, c.Request.URL.Path),
		Type:    "invalid_request_error",
		Param:   "",
		Code:    "",
	}
	c.JSON(http.StatusNotFound, gin.H{
		"error": err,
	})
}

func RelayTaskFetch(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, &dto.TaskError{
			Code:       "gen_relay_info_failed",
			Message:    err.Error(),
			StatusCode: http.StatusInternalServerError,
		})
		return
	}
	if taskErr := relay.RelayTaskFetch(c, relayInfo.RelayMode); taskErr != nil {
		respondTaskError(c, taskErr)
	}
}

func RelayTask(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, &dto.TaskError{
			Code:       "gen_relay_info_failed",
			Message:    err.Error(),
			StatusCode: http.StatusInternalServerError,
		})
		return
	}

	if taskErr := relay.ResolveOriginTask(c, relayInfo); taskErr != nil {
		respondTaskError(c, taskErr)
		return
	}

	var result *relay.TaskSubmitResult
	var taskErr *dto.TaskError
	defer func() {
		if taskErr != nil && relayInfo.Billing != nil {
			relayInfo.Billing.Refund(c)
		}
	}()

	retryParam := &service.RetryParam{
		Ctx:               c,
		TokenGroup:        relayInfo.TokenGroup,
		ModelName:         relayInfo.OriginModelName,
		RequestPath:       c.Request.URL.Path,
		Retry:             common.GetPointer(0),
		OriginCacheDomain: common.GetContextKeyString(c, constant.ContextKeyOriginCacheDomain),
	}

	// ── 与普通 relay 同款：总 deadline / per-model timeout / backoff ──
	relayCfg := operation_setting.GetRelayRetryConfig()
	taskStart := time.Now()
	totalCtx := c.Request.Context()
	var totalCancel context.CancelFunc
	if relayCfg.TotalTimeoutSeconds > 0 {
		totalCtx, totalCancel = context.WithTimeout(c.Request.Context(), time.Duration(relayCfg.TotalTimeoutSeconds)*time.Second)
		defer totalCancel()
		c.Request = c.Request.WithContext(totalCtx)
	}
	modelTimeout := operation_setting.ResolveModelTimeout(relayInfo.OriginModelName)

	for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
		c.Set(common.UpstreamRequestIdKey, "")

		if err := totalCtx.Err(); err != nil {
			elapsed := time.Since(taskStart).Milliseconds()
			logger.LogError(c, fmt.Sprintf("task relay: total deadline exceeded after %dms (channels tried: %v)", elapsed, c.GetStringSlice("use_channel")))
			taskErr = service.TaskErrorWrapperLocal(err, string(types.ErrorCodeRelayDeadlineExceeded), http.StatusGatewayTimeout)
			break
		}

		var channel *model.Channel

		if lockedCh, ok := relayInfo.LockedChannel.(*model.Channel); ok && lockedCh != nil {
			channel = lockedCh
			if retryParam.GetRetry() > 0 {
				if setupErr := middleware.SetupContextForSelectedChannel(c, channel, relayInfo.OriginModelName); setupErr != nil {
					taskErr = service.TaskErrorWrapperLocal(setupErr.Err, "setup_locked_channel_failed", http.StatusInternalServerError)
					break
				}
			}
		} else {
			var channelErr error
			channel, channelErr = getChannel(c, relayInfo, retryParam)
			if channelErr != nil {
				logger.LogError(c, channelErr.Error())
				if errors.Is(channelErr, model.ErrCapacityNeedQueue) {
					break
				}
				// 同主 relay：重试轮候选耗尽时保留上一轮真实上游错误，不被"无可用渠道"500 覆盖
				if taskErr == nil {
					var newAPIError *types.NewAPIError
					if errors.As(channelErr, &newAPIError) {
						channelErr = newAPIError.Err
					}
					taskErr = service.TaskErrorWrapperLocal(channelErr, "get_channel_failed", http.StatusInternalServerError)
				}
				break
			}
		}

		// 同主 relay：退避移到成功选到渠道之后，候选耗尽时不再白等一轮
		if retryParam.GetRetry() > 0 {
			if backoff := operation_setting.ComputeRetryBackoff(retryParam.GetRetry()-1, relayCfg.BackoffBaseMs, relayCfg.BackoffMaxMs); backoff > 0 {
				timer := time.NewTimer(backoff)
				select {
				case <-timer.C:
				case <-totalCtx.Done():
					timer.Stop()
				}
				if err := totalCtx.Err(); err != nil {
					elapsed := time.Since(taskStart).Milliseconds()
					logger.LogError(c, fmt.Sprintf("task relay: total deadline exceeded after %dms (channels tried: %v)", elapsed, c.GetStringSlice("use_channel")))
					taskErr = service.TaskErrorWrapperLocal(err, string(types.ErrorCodeRelayDeadlineExceeded), http.StatusGatewayTimeout)
					break
				}
			}
		}

		addUsedChannel(c, channel.Id)
		bodyStorage, bodyErr := common.GetBodyStorage(c)
		if bodyErr != nil {
			if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
				taskErr = service.TaskErrorWrapperLocal(bodyErr, "read_request_body_failed", http.StatusRequestEntityTooLarge)
			} else {
				taskErr = service.TaskErrorWrapperLocal(bodyErr, "read_request_body_failed", http.StatusBadRequest)
			}
			break
		}
		// 同上：task relay 重试路径也需把 body storage 重置到起点，避免重发空 body
		if _, seekErr := bodyStorage.Seek(0, io.SeekStart); seekErr != nil {
			taskErr = service.TaskErrorWrapperLocal(seekErr, "read_request_body_failed", http.StatusBadRequest)
			break
		}
		c.Request.Body = io.NopCloser(bodyStorage)

		var callCancel context.CancelFunc
		if modelTimeout > 0 {
			var callCtx context.Context
			callCtx, callCancel = context.WithTimeout(totalCtx, time.Duration(modelTimeout)*time.Second)
			c.Request = c.Request.WithContext(callCtx)
		}

		result, taskErr = relay.RelayTaskSubmit(c, relayInfo)

		if callCancel != nil {
			callCancel()
			c.Request = c.Request.WithContext(totalCtx)
		}

		if taskErr == nil {
			break
		}

		if !taskErr.LocalError {
			processChannelError(c,
				*types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey,
					common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()),
				types.NewOpenAIError(taskErr.Error, types.ErrorCodeBadResponseStatusCode, taskErr.StatusCode))
		}

		// Task relay 同样需要排除已失败渠道（LockedChannel 路径不走 distributor，对其无影响）
		retryParam.ExcludeChannel(channel.Id)

		if !shouldRetryTaskRelay(c, channel.Id, taskErr, common.RetryTimes-retryParam.GetRetry()) {
			break
		}
	}

	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}

	// ── 成功：结算 + 日志 + 插入任务 ──
	if taskErr == nil {
		if settleErr := service.SettleBilling(c, relayInfo, result.Quota); settleErr != nil {
			common.SysError("settle task billing error: " + settleErr.Error())
		}
		service.LogTaskConsumption(c, relayInfo)

		task := model.InitTask(result.Platform, relayInfo)
		task.PrivateData.UpstreamTaskID = result.UpstreamTaskID
		task.PrivateData.BillingSource = relayInfo.BillingSource
		task.PrivateData.SubscriptionId = relayInfo.SubscriptionId
		task.PrivateData.TokenId = relayInfo.TokenId
		task.PrivateData.BillingContext = &model.TaskBillingContext{
			ModelPrice:      relayInfo.PriceData.ModelPrice,
			GroupRatio:      relayInfo.PriceData.GroupRatioInfo.GroupRatio,
			ModelRatio:      relayInfo.PriceData.ModelRatio,
			OtherRatios:     relayInfo.PriceData.OtherRatios,
			OriginModelName: relayInfo.OriginModelName,
			PerCallBilling:  common.StringsContains(constant.TaskPricePatches, relayInfo.OriginModelName) || relayInfo.PriceData.UsePrice,
		}
		task.Quota = result.Quota
		task.Data = result.TaskData
		task.Action = relayInfo.Action
		if insertErr := task.Insert(); insertErr != nil {
			common.SysError("insert task error: " + insertErr.Error())
		}
	}

	if taskErr != nil {
		respondTaskError(c, taskErr)
	}
}

// respondTaskError 统一输出 Task 错误响应（含 429 限流提示改写）
func respondTaskError(c *gin.Context, taskErr *dto.TaskError) {
	if taskErr.StatusCode == http.StatusTooManyRequests {
		taskErr.Message = "当前分组上游负载已饱和，请稍后再试"
	}
	c.JSON(taskErr.StatusCode, taskErr)
}

func shouldRetryTaskRelay(c *gin.Context, channelId int, taskErr *dto.TaskError, retryTimes int) bool {
	if taskErr == nil {
		return false
	}
	if service.ShouldSkipRetryAfterChannelAffinityFailure(c) {
		return false
	}
	// 统一重试范围：NoCrossChannel（cost_guard / disabled）→ 不跨渠道重试
	if service.EffectiveRetryScope(c) == service.RetryScopeNoCrossChannel {
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	if taskErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if taskErr.StatusCode == 307 {
		return true
	}
	if taskErr.StatusCode/100 == 5 {
		// 超时不重试
		if operation_setting.IsAlwaysSkipRetryStatusCode(taskErr.StatusCode) {
			return false
		}
		return true
	}
	if taskErr.StatusCode == http.StatusBadRequest {
		return false
	}
	if taskErr.StatusCode == 408 {
		// azure处理超时不重试
		return false
	}
	if taskErr.LocalError {
		return false
	}
	if taskErr.StatusCode/100 == 2 {
		return false
	}
	return true
}
