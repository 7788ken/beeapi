package common

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

type HasPrompt interface {
	GetPrompt() string
}

type HasImage interface {
	HasImage() bool
}

func GetFullRequestURL(baseURL string, requestURL string, channelType int) string {
	fullRequestURL := fmt.Sprintf("%s%s", baseURL, requestURL)

	if strings.HasPrefix(baseURL, "https://gateway.ai.cloudflare.com") {
		switch channelType {
		case constant.ChannelTypeOpenAI:
			fullRequestURL = fmt.Sprintf("%s%s", baseURL, strings.TrimPrefix(requestURL, "/v1"))
		case constant.ChannelTypeAzure:
			fullRequestURL = fmt.Sprintf("%s%s", baseURL, strings.TrimPrefix(requestURL, "/openai/deployments"))
		}
	}
	return fullRequestURL
}

func GetAPIVersion(c *gin.Context) string {
	query := c.Request.URL.Query()
	apiVersion := query.Get("api-version")
	if apiVersion == "" {
		apiVersion = c.GetString("api_version")
	}
	return apiVersion
}

func createTaskError(err error, code string, statusCode int, localError bool) *dto.TaskError {
	return &dto.TaskError{
		Code:       code,
		Message:    err.Error(),
		StatusCode: statusCode,
		LocalError: localError,
		Error:      err,
	}
}

func storeTaskRequest(c *gin.Context, info *RelayInfo, action string, requestObj TaskSubmitReq) {
	info.Action = action
	c.Set("task_request", requestObj)
}
func GetTaskRequest(c *gin.Context) (TaskSubmitReq, error) {
	v, exists := c.Get("task_request")
	if !exists {
		return TaskSubmitReq{}, fmt.Errorf("request not found in context")
	}
	req, ok := v.(TaskSubmitReq)
	if !ok {
		return TaskSubmitReq{}, fmt.Errorf("invalid task request type")
	}
	return req, nil
}

func validatePrompt(prompt string) *dto.TaskError {
	if strings.TrimSpace(prompt) == "" {
		return createTaskError(fmt.Errorf("prompt is required"), "invalid_request", http.StatusBadRequest, true)
	}
	return nil
}

// MaxTaskDurationSeconds caps user-supplied video duration. Duration is used
// as a billing multiplier (OtherRatio "seconds"); an unbounded value could
// overflow quota calculation into a negative charge.
const MaxTaskDurationSeconds = 3600

// AdaptiveTaskDuration 是 Seedance 系"由模型自适应决定时长"的哨兵值：视频编辑任务
// 上游要求 duration=-1（配合 ratio=adaptive，成片时长跟随被编辑的原视频）。
// 它不是计费乘数（seedance 按输出 token 结算，不用 OtherRatio "seconds"），
// 所以放行 -1 不会把负数带进额度计算。
const AdaptiveTaskDuration = -1

// allowsAdaptiveDuration 判断本次请求能否接受 duration=-1：仅 Seedance 家族放行 ——
// 渠道类型是豆包/sd 网关视频渠道，或模型名含 seedance（子站以 OpenAI/Sora 渠道
// passthrough 转发 seedance 请求时渠道类型不是豆包，只能靠模型名识别）。
// 其它平台（kling/vidu/ali 等）的适配器会把 -1 当作默认值或原样下送，继续按原规则拒绝。
func allowsAdaptiveDuration(modelName string, channelType int) bool {
	switch channelType {
	case constant.ChannelTypeDoubaoVideo, constant.ChannelTypeSdVideo, constant.ChannelTypeSdVideoV2:
		return true
	}
	return strings.Contains(strings.ToLower(modelName), "seedance")
}

func validateTaskDurationBounds(req TaskSubmitReq, info *RelayInfo) *dto.TaskError {
	seconds := req.Duration
	if seconds == 0 && req.Seconds != "" {
		seconds, _ = strconv.Atoi(req.Seconds)
	}
	channelType := 0
	if info != nil && info.ChannelMeta != nil {
		channelType = info.ChannelType
	}
	if seconds == AdaptiveTaskDuration && allowsAdaptiveDuration(req.Model, channelType) {
		// 自适应时长哨兵：由 seedance 适配器按模型决定是否真正支持（见 doubao.isSeedanceDurationAllowed）
	} else if seconds < 0 || seconds > MaxTaskDurationSeconds {
		return createTaskError(fmt.Errorf("seconds must be between 1 and %d", MaxTaskDurationSeconds), "invalid_seconds", http.StatusBadRequest, true)
	}
	// metadata["durationSeconds"] 会绕过上面的标准字段校验（gemini/veo 等走 metadata），
	// 若不在此拒绝，越界值会在计费层被静默钳到上限并按顶格时长扣费。
	if metaSeconds, ok := metadataDurationSeconds(req.Metadata); ok {
		if metaSeconds < 0 || metaSeconds > MaxTaskDurationSeconds {
			return createTaskError(fmt.Errorf("metadata.durationSeconds must be between 1 and %d", MaxTaskDurationSeconds), "invalid_seconds", http.StatusBadRequest, true)
		}
	}
	return nil
}

// metadataDurationSeconds 从 metadata 提取 durationSeconds（JSON 数字解析为 float64，
// 也兼容 int）。第二个返回值表示字段是否存在且为数值类型。
func metadataDurationSeconds(metadata map[string]interface{}) (int, bool) {
	if metadata == nil {
		return 0, false
	}
	v, ok := metadata["durationSeconds"]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

func validateMultipartTaskRequest(c *gin.Context, info *RelayInfo, action string) (TaskSubmitReq, error) {
	var req TaskSubmitReq
	if _, err := c.MultipartForm(); err != nil {
		return req, err
	}

	formData := c.Request.PostForm
	req = TaskSubmitReq{
		Prompt:   formData.Get("prompt"),
		Model:    formData.Get("model"),
		Mode:     formData.Get("mode"),
		Image:    formData.Get("image"),
		Size:     formData.Get("size"),
		Metadata: make(map[string]interface{}),
	}

	if durationStr := formData.Get("seconds"); durationStr != "" {
		if duration, err := strconv.Atoi(durationStr); err == nil {
			req.Duration = duration
		}
	}

	if images := formData["images"]; len(images) > 0 {
		req.Images = images
	}

	for key, values := range formData {
		if len(values) > 0 && !isKnownTaskField(key) {
			if intVal, err := strconv.Atoi(values[0]); err == nil {
				req.Metadata[key] = intVal
			} else if floatVal, err := strconv.ParseFloat(values[0], 64); err == nil {
				req.Metadata[key] = floatVal
			} else {
				req.Metadata[key] = values[0]
			}
		}
	}
	return req, nil
}

func ValidateMultipartDirect(c *gin.Context, info *RelayInfo) *dto.TaskError {
	var prompt string
	var model string
	var seconds int
	var size string
	var hasInputReference bool

	var req TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return createTaskError(err, "invalid_json", http.StatusBadRequest, true)
	}

	prompt = req.Prompt
	model = req.Model
	size = req.Size
	seconds, _ = strconv.Atoi(req.Seconds)
	if seconds == 0 {
		seconds = req.Duration
	}
	if req.InputReference != "" {
		req.Images = []string{req.InputReference}
	} else if len(req.Images) == 0 && strings.TrimSpace(req.Image) != "" {
		req.Images = []string{strings.TrimSpace(req.Image)}
	}

	if strings.TrimSpace(req.Model) == "" {
		return createTaskError(fmt.Errorf("model field is required"), "missing_model", http.StatusBadRequest, true)
	}

	if req.HasImage() {
		hasInputReference = true
	}

	// 多模态 content[] 场景：prompt 可写在 content 的 text 项里（对齐上游 seedance 语义），
	// 此时豁免顶层 prompt 必填；非 content 请求仍要求顶层 prompt（与 ValidateBasicTaskRequest 一致）。
	// 博士站以 OpenAI/Sora 渠道类型 passthrough 转发 seedance 请求时依赖此豁免，
	// 否则纯 content[]（无顶层 prompt）的全能参考请求会在本站被 400 拦下、根本到不了上游。
	if len(req.Content) == 0 {
		if taskErr := validatePrompt(prompt); taskErr != nil {
			return taskErr
		}
	}

	if taskErr := validateTaskDurationBounds(req, info); taskErr != nil {
		return taskErr
	}

	action := constant.TaskActionTextGenerate
	if hasInputReference {
		action = constant.TaskActionGenerate
	}
	if strings.HasPrefix(model, "sora-2") {

		if size == "" {
			size = "720x1280"
		}

		if seconds <= 0 {
			seconds = 4
		}

		if model == "sora-2" && !lo.Contains([]string{"720x1280", "1280x720"}, size) {
			return createTaskError(fmt.Errorf("sora-2 size is invalid"), "invalid_size", http.StatusBadRequest, true)
		}
		if model == "sora-2-pro" && !lo.Contains([]string{"720x1280", "1280x720", "1792x1024", "1024x1792"}, size) {
			return createTaskError(fmt.Errorf("sora-2 size is invalid"), "invalid_size", http.StatusBadRequest, true)
		}
		// OtherRatios 已移到 Sora adaptor 的 EstimateBilling 中设置
	}

	storeTaskRequest(c, info, action, req)

	return nil
}

func isKnownTaskField(field string) bool {
	knownFields := map[string]bool{
		"prompt":          true,
		"model":           true,
		"mode":            true,
		"image":           true,
		"images":          true,
		"size":            true,
		"duration":        true,
		"input_reference": true, // Sora 特有字段
	}
	return knownFields[field]
}

func ValidateBasicTaskRequest(c *gin.Context, info *RelayInfo, action string) *dto.TaskError {
	var err error
	contentType := c.GetHeader("Content-Type")
	var req TaskSubmitReq
	if strings.HasPrefix(contentType, "multipart/form-data") {
		req, err = validateMultipartTaskRequest(c, info, action)
		if err != nil {
			return createTaskError(err, "invalid_multipart_form", http.StatusBadRequest, true)
		}
	}
	// 为了metadata字段的兼容性，统一UnmarshalBodyReusable
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return createTaskError(err, "invalid_request", http.StatusBadRequest, true)
	}

	// 多模态 content[] 场景：prompt 可写在 content 的 text 项里（符合上游语义），
	// 此时豁免顶层 prompt 必填校验；非 content 请求仍要求顶层 prompt。
	if len(req.Content) == 0 {
		if taskErr := validatePrompt(req.Prompt); taskErr != nil {
			return taskErr
		}
	}

	if taskErr := validateTaskDurationBounds(req, info); taskErr != nil {
		return taskErr
	}

	if len(req.Images) == 0 && strings.TrimSpace(req.Image) != "" {
		// 兼容单图上传
		req.Images = []string{req.Image}
	}

	storeTaskRequest(c, info, action, req)
	return nil
}
