package controller

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/pkg/httplifecycle"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// VerifyChannel 触发对某渠道的外部测评（透传测评网关 /api/verify/{protocol} SSE）。
//
// 路由：POST /api/channel/:id/verify   Body: {"model": "claude-sonnet-4-6"}
// 鉴权：AdminAuth（Root + Admin）
//
// 行为：校验入参后构造 SSE hooks，委托同包的 runChannelVerify 执行测评核心。
// runChannelVerify 与 HTTP 解耦（hooks 可为 nil），供后台调度（channel_verify_schedule.go）复用。
func VerifyChannel(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req struct {
		Model string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid body: " + err.Error()})
		return
	}
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "model is required"})
		return
	}

	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "channel not found"})
		return
	}
	protocol, msg := resolveValidatedVerifyTarget(channel, req.Model)
	if msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": msg})
		return
	}
	operatorId := c.GetInt("id")

	// 准备 SSE
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	verifyCtx := httplifecycle.StreamContext(c.Request.Context())
	c.Writer.Flush()

	// clientGone 在前端关闭 Modal / 网络断开时置 true，之后停止往客户端写，
	// 但后台读取上游 SSE + 落库继续，直到上游完成或超时。这样"关 Modal 也能完成评分"。
	clientGone := false
	hooks := verifyHooks{
		sendEvent: func(eventType string, data any) {
			if clientGone {
				return
			}
			payload := gin.H{"type": eventType, "data": data}
			bs, err := common.Marshal(payload)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", string(bs)); err != nil {
				clientGone = true
				return
			}
			c.Writer.Flush()
		},
		rawLine: func(line []byte) {
			// 透传原始行；写失败说明客户端断开，标记 clientGone 后继续读上游 + 落库
			if clientGone {
				return
			}
			if _, werr := c.Writer.Write(line); werr != nil {
				clientGone = true
			} else {
				c.Writer.Flush()
			}
		},
	}

	// verifyCtx 忽略客户端断开，但会在应用强制结束长连接阶段取消。
	runChannelVerifyWithProtocol(verifyCtx, channel, req.Model, protocol, operatorId, hooks)
}

// verifyHooks 把测评过程透传给调用方。HTTP 场景写 SSE；后台调度场景全传 nil（emit/raw 自动空转）。
type verifyHooks struct {
	sendEvent func(eventType string, data any) // 结构化事件（queued/acquired/report_created/error）
	rawLine   func(line []byte)                // 上游 SSE 原始行透传
}

func (h verifyHooks) emit(eventType string, data any) {
	if h.sendEvent != nil {
		h.sendEvent(eventType, data)
	}
}

func (h verifyHooks) raw(line []byte) {
	if h.rawLine != nil {
		h.rawLine(line)
	}
}

type verifyProviderSpec struct {
	protocol    string
	channelType int
	prefixes    []string
}

var verifyProviderWhitelist = []verifyProviderSpec{
	{protocol: "claude", channelType: constant.ChannelTypeAnthropic, prefixes: []string{"claude-"}},
	{protocol: "openai", channelType: constant.ChannelTypeOpenAI, prefixes: []string{"gpt-", "codex-", "o1", "o3", "o4", "openai/"}},
}

func resolveVerifyProtocol(channelType int, modelName string) (string, bool) {
	for _, spec := range verifyProviderWhitelist {
		if channelType != spec.channelType {
			continue
		}
		for _, prefix := range spec.prefixes {
			if strings.HasPrefix(modelName, prefix) {
				return spec.protocol, true
			}
		}
	}
	return "", false
}

// validateVerifyTarget 校验渠道是否可测评（provider 白名单 / model 在 channel.Models 中 / 有 key / 有 base_url）。
// 返回空串表示通过，否则返回错误信息。HTTP 与后台调度共用。
func validateVerifyTarget(channel *model.Channel, modelName string) string {
	_, msg := resolveValidatedVerifyTarget(channel, modelName)
	return msg
}

func resolveValidatedVerifyTarget(channel *model.Channel, modelName string) (string, string) {
	found := false
	for _, m := range channel.GetModels() {
		if strings.TrimSpace(m) == modelName {
			found = true
			break
		}
	}
	if !found {
		return "", "model not in channel.models: " + modelName
	}
	protocol, ok := resolveVerifyProtocol(channel.Type, modelName)
	if !ok {
		return "", "channel type/model is not supported for verify"
	}
	keys := channel.GetKeys()
	if len(keys) == 0 || keys[0] == "" {
		return "", "channel has no api key"
	}
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = constant.ChannelBaseURLs[channel.Type]
	}
	if baseURL == "" {
		return "", "channel has no base_url"
	}
	return protocol, ""
}

// runChannelVerify 执行一次渠道测评的完整生命周期：
// 信号量 → 建 running 报告 → 发起上游 SSE → 逐事件落库 → 完成回写 Channel 快照 → 裁剪历史。
//
// 与 HTTP 解耦：通过 hooks 把事件/原始行交给调用方（HTTP 写 SSE / 后台 nil）。
// 入参 channel/modelName 假定已通过 validateVerifyTarget 校验。
// 返回最终态（reportId/score/grade/status/errMsg），供后台调度做分数对比、阈值启停。
//
// 关键：HTTP 场景的 parentCtx 忽略客户端断开，但保留应用关闭取消，
// 同时仍受 VERIFY_TIMEOUT_SEC 与上游响应控制；客户端断开由 hooks 内的 clientGone 检测。
func runChannelVerify(parentCtx context.Context, channel *model.Channel, modelName string, operatorId int, hooks verifyHooks) (reportId int64, finalScore int, finalGrade string, finalStatus string, finalErr string) {
	protocol, msg := resolveValidatedVerifyTarget(channel, modelName)
	if msg != "" {
		hooks.emit("error", gin.H{"message": msg})
		return 0, 0, "", model.VerifyStatusFailed, msg
	}
	return runChannelVerifyWithProtocol(parentCtx, channel, modelName, protocol, operatorId, hooks)
}

func runChannelVerifyWithProtocol(parentCtx context.Context, channel *model.Channel, modelName string, protocol string, operatorId int, hooks verifyHooks) (reportId int64, finalScore int, finalGrade string, finalStatus string, finalErr string) {
	keys := channel.GetKeys()
	if len(keys) == 0 || keys[0] == "" {
		hooks.emit("error", gin.H{"message": "channel has no api key"})
		return 0, 0, "", model.VerifyStatusFailed, "channel has no api key"
	}
	apiKey := keys[0]
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = constant.ChannelBaseURLs[channel.Type]
	}

	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(service.VerifyTimeoutSeconds())*time.Second)
	defer cancel()

	// 信号量：pos==0 立即拿到；pos>0 曾排队过；onWait 在进入阻塞前回调，让前端立刻看到排队进度
	pos := service.VerifyQueueAcquire(ctx, func(position int) {
		hooks.emit("queued", gin.H{"position": position})
	})
	if pos < 0 {
		hooks.emit("error", gin.H{"message": "cancelled while waiting"})
		return 0, 0, "", model.VerifyStatusCancelled, "cancelled while waiting"
	}
	defer service.VerifyQueueRelease()
	hooks.emit("acquired", gin.H{"position": pos})

	// 创建 running 报告
	startedAt := time.Now().Unix()
	report, err := model.CreateRunningReport(channel.Id, operatorId, modelName)
	if err != nil {
		hooks.emit("error", gin.H{"message": "create report failed: " + err.Error()})
		return 0, 0, "", model.VerifyStatusFailed, "create report failed: " + err.Error()
	}
	reportId = report.Id
	hooks.emit("report_created", gin.H{"report_id": report.Id})

	// 注册 cancel 句柄，供 CancelChannelVerifyReport 调用以主动终止
	service.RegisterVerifyCancel(report.Id, cancel)
	defer service.UnregisterVerifyCancel(report.Id)

	// 发起上游 SSE
	// 安全说明：base_url + api_key 会透传到测评网关 测评网关。
	// 这是设计本意（外部测评需要真 key 才能模拟客户端调用），且 AdminAuth 已限权；
	// 但任何对 VerifyGatewayURL 配置项的修改都需经安全 review。
	// extra_headers 来源于渠道的 header_override，主要用于 AWS External Anthropic API 等
	// 需要附加 anthropic-workspace-id 的场景；过滤掉 Authorization / x-api-key 避免污染鉴权。
	extraHeaders := map[string]string{}
	for k, v := range channel.GetHeaderOverride() {
		lk := strings.ToLower(strings.TrimSpace(k))
		if lk == "" || lk == "authorization" || lk == "x-api-key" || lk == "*" {
			continue
		}
		if s, ok := v.(string); ok && s != "" {
			extraHeaders[k] = s
		}
	}
	// 多 base_url 故障切换：把"对单个 base_url 测一次"封装为闭包，外层逐个 base_url 尝试，
	// 任一测评成功即判渠道活；全部失败才算渠道挂。单 base_url 渠道只循环一次，行为同原先。
	// 价值：避免某个坏的主/备 URL 拖低评分而误禁掉其实仍有可用入口的整个渠道。
	var lastScore int
	var lastGrade string
	var donePayload map[string]any

	// attempt 对单个 base_url 走一次测评网关并解析 SSE，结论写入捕获的 finalStatus/finalErr/lastScore 等。
	// 不在此 finalize report / emit error：成败由外层多 URL 循环裁决（中途失败要留给下一个备用 URL）。
	attempt := func(baseURL string) {
		// 重置本轮结论（多 URL 时清掉上一轮的失败态）
		finalStatus = model.VerifyStatusFailed
		finalErr = ""

		// Marshal 错误吞略：gin.H{} 仅含基本类型，json 序列化不会失败。
		upstreamBody, _ := common.Marshal(gin.H{
			"base_url":       baseURL,
			"api_key":        apiKey,
			"model":          modelName,
			"force_continue": false,
			"extra_headers":  extraHeaders,
		})
		gatewayURL := service.VerifyGatewayURL(protocol)
		if gatewayURL == "" {
			finalErr = "external verification gateway is not configured (set VERIFY_GATEWAY_BASE)"
			return
		}
		upstreamReq, err := http.NewRequestWithContext(ctx, "POST", gatewayURL, bytes.NewReader(upstreamBody))
		if err != nil {
			finalErr = "build upstream request: " + err.Error()
			return
		}
		upstreamReq.Header.Set("Content-Type", "application/json")
		upstreamReq.Header.Set("Accept", "text/event-stream")

		httpClient := &http.Client{Timeout: 0} // ctx 控制
		resp, err := httpClient.Do(upstreamReq)
		if err != nil {
			finalErr = "upstream connect: " + err.Error()
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			finalErr = fmt.Sprintf("upstream HTTP %d: %s", resp.StatusCode, string(bodyBytes))
			return
		}

		// 透传上游 SSE 并在关键事件落库
		reader := bufio.NewReaderSize(resp.Body, 16*1024)
		for {
			select {
			case <-ctx.Done():
				// 区分主动取消 vs 超时：DeadlineExceeded=超时，Canceled=运维点了"终止"
				finalStatus = model.VerifyStatusCancelled
				if ctx.Err() == context.Canceled {
					finalErr = "cancelled by operator"
				} else {
					finalErr = fmt.Sprintf("verify timeout (%ds)", service.VerifyTimeoutSeconds())
				}
				return
			default:
			}
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				// 透传原始行（hooks 内部处理客户端断开）
				hooks.raw(line)

				// 解析 type 字段，更新 running score
				trim := bytes.TrimSpace(line)
				if bytes.HasPrefix(trim, []byte("data:")) {
					jsonPart := bytes.TrimSpace(trim[5:])
					if len(jsonPart) > 0 && jsonPart[0] == '{' {
						var evt map[string]any
						if uerr := common.Unmarshal(jsonPart, &evt); uerr == nil {
							etype, _ := evt["type"].(string)
							edata, _ := evt["data"].(map[string]any)
							switch etype {
							case "health_score":
								if edata != nil {
									if s, ok := toInt(edata["total_score"]); ok {
										lastScore = s
									}
									if g, ok := edata["grade"].(string); ok {
										lastGrade = g
									}
									_ = model.UpdateRunningScore(report.Id, lastScore, lastGrade)
								}
							case "early_stop":
								if edata != nil {
									if s, ok := toInt(edata["total_score"]); ok {
										lastScore = s
									}
									if g, ok := edata["grade"].(string); ok {
										lastGrade = g
									}
								}
								donePayload = map[string]any{"health": edata}
								finalStatus = model.VerifyStatusSuccess
							case "done":
								if edata != nil {
									if health, ok := edata["health"].(map[string]any); ok {
										if s, ok := toInt(health["total_score"]); ok {
											lastScore = s
										}
										if g, ok := health["grade"].(string); ok {
											lastGrade = g
										}
									}
									donePayload = edata
								}
								finalStatus = model.VerifyStatusSuccess
							case "error":
								if edata != nil {
									if m, ok := edata["message"].(string); ok {
										finalErr = m
									}
								}
								finalStatus = model.VerifyStatusFailed
							}
						}
					}
				}
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				// ctx 改 Background 派生后，ctx.Err() 来源只剩两种：超时 / 主动取消。
				// 客户端断开走 clientGone 标志，不再影响 ctx。
				if ctx.Err() != nil {
					finalStatus = model.VerifyStatusCancelled
					if finalErr == "" {
						if ctx.Err() == context.Canceled {
							finalErr = "cancelled by operator"
						} else {
							finalErr = fmt.Sprintf("verify timeout (%ds)", service.VerifyTimeoutSeconds())
						}
					}
				} else if finalStatus == model.VerifyStatusFailed && finalErr == "" {
					finalErr = "upstream read: " + err.Error()
				}
				break
			}
		}
	}

	effectiveURLs := channel.GetEffectiveBaseURLs()
	if len(effectiveURLs) == 0 {
		effectiveURLs = []string{baseURL} // 渠道未配 base_url：用类型默认地址兜底
	}
	finalStatus = model.VerifyStatusFailed
	for i, u := range effectiveURLs {
		if i > 0 {
			hooks.emit("url_attempt", gin.H{"url_index": i, "total_urls": len(effectiveURLs), "url": u})
		}
		attempt(u)
		if finalStatus == model.VerifyStatusSuccess || finalStatus == model.VerifyStatusCancelled {
			break // 任一 URL 通即判渠道活；主动取消/超时也不再试其他 URL
		}
		// 失败：finalErr 已记录，若还有备用 URL 继续尝试下一个
	}
	// 所有 URL 用尽仍失败才向前端报错；中途切换已用 url_attempt 提示
	if finalStatus == model.VerifyStatusFailed {
		hooks.emit("error", gin.H{"message": finalErr})
	}

	if finalStatus == model.VerifyStatusSuccess {
		_ = model.FinalizeReport(report.Id, finalStatus, lastScore, lastGrade, donePayload, "", startedAt)
		// 同步回写 Channel 快照
		if err := model.UpdateChannelVerifySnapshot(channel.Id, lastScore, lastGrade, report.Id, time.Now().Unix(), channel.VerifyScore); err != nil {
			common.SysError("UpdateChannelVerifySnapshot failed: " + err.Error())
		}
		// 异步裁剪历史
		_ = backgroundtask.Submit("channel-verify-history-trim", func(context.Context) {
			defer func() { _ = recover() }()
			if err := model.TrimChannelHistory(channel.Id, service.VerifyHistoryLimit()); err != nil {
				common.SysError("TrimChannelHistory failed: " + err.Error())
			}
		})
	} else {
		// 保留实际状态（failed / cancelled）
		_ = model.FinalizeReport(report.Id, finalStatus, 0, "", nil, finalErr, startedAt)
	}
	return report.Id, lastScore, lastGrade, finalStatus, finalErr
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case float32:
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	case string:
		n, err := strconv.Atoi(x)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// ListChannelVerifyReports GET /api/channel/:id/verify/reports?page=1&page_size=20
func ListChannelVerifyReports(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	items, total, err := model.ListReportsByChannel(channelId, page, pageSize)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     items,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// CancelChannelVerifyReport POST /api/channel/verify/report/:report_id/cancel
//
// 主动终止一个 running 的测评：
//  1. 查 DB 报告存在且 status=running；非 running 直接返回 success（幂等）
//  2. 调 service.CancelVerify 触发 ctx 取消；runChannelVerify 的 select<-ctx.Done()
//     会走 FINALIZE 把状态写成 cancelled（reason: cancelled by operator）
//  3. 兜底：若 service 中找不到 cancel 函数（进程重启或已结束），直接把 DB
//     status=running 改写成 cancelled，避免"幽灵 running"
//
// AdminAuth 已经管住权限。
func CancelChannelVerifyReport(c *gin.Context) {
	reportId, err := strconv.ParseInt(c.Param("report_id"), 10, 64)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	r, err := model.GetReportById(reportId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "report not found"})
		return
	}
	if r.Status != model.VerifyStatusRunning {
		// 已经是终态，直接 success（幂等）
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"already_done": true, "status": r.Status}})
		return
	}
	cancelled := service.CancelVerify(reportId)
	if !cancelled {
		// 进程内找不到 cancel 句柄（重启 / 已 FINALIZE 但 DB 还没刷）→ DB 兜底
		startedAt := r.TestedAt
		if startedAt == 0 {
			startedAt = time.Now().Unix()
		}
		_ = model.FinalizeReport(reportId, model.VerifyStatusCancelled, 0, "", nil, "cancelled by operator (no live ctx)", startedAt)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"cancelled_inflight": cancelled}})
}

// GetChannelVerifyReport GET /api/channel/verify/report/:report_id
func GetChannelVerifyReport(c *gin.Context) {
	reportId, err := strconv.ParseInt(c.Param("report_id"), 10, 64)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	r, err := model.GetReportById(reportId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "report not found"})
		return
	}
	// 解析 ReportJson
	var report map[string]any
	if r.ReportJson != "" {
		_ = common.UnmarshalJsonStr(r.ReportJson, &report)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id":          r.Id,
			"channel_id":  r.ChannelId,
			"model":       r.Model,
			"score":       r.Score,
			"grade":       r.Grade,
			"status":      r.Status,
			"duration_ms": r.DurationMs,
			"error_msg":   r.ErrorMsg,
			"tested_at":   r.TestedAt,
			"operator_id": r.OperatorId,
			"report":      report,
		},
	})
}
