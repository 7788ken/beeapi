package minimax_inf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

// ============================
// 请求 / 响应结构
// ============================

// ContentItem 上游 content[] 元素：文本，或图片/视频/音频三类素材之一。
//
// ⚠ 文档的「content 元素」章节只写了文本与参考图两种，但同一份文档的「输入素材
// 计费」章节列了音频（免费）与视频（按输入时长计费）；2026-09-12 实测也确认三类
// 素材都被上游原生解析（失败时报错会精确指向 content[i].video_url / content[i].audio_url）。
// 三个指针字段互斥，按 Type 只填对应的那一个。
type ContentItem struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`
	AudioURL *MediaURL `json:"audio_url,omitempty"`
	Role     string    `json:"role,omitempty"`
}

type MediaURL struct {
	URL string `json:"url"`
}

// videoRequest 提交体。文档字段：model（必填）、content（必填）、
// resolution / duration（选填）、ratio（文生视频必填）。
// 网关不接受 callback_url，故不设该字段。
type videoRequest struct {
	Model      string        `json:"model"`
	Content    []ContentItem `json:"content"`
	Resolution string        `json:"resolution"`
	Duration   int           `json:"duration"`
	Ratio      string        `json:"ratio,omitempty"`
}

// gatewayTask 网关任务对象，提交响应与轮询响应共用同一形状。
type gatewayTask struct {
	ID              string          `json:"id"`
	Status          string          `json:"status"`
	Model           string          `json:"model"`
	DurationSeconds int             `json:"duration_seconds"`
	Outputs         []string        `json:"outputs"`
	Error           json.RawMessage `json:"error"`      // null / 字符串 / {code,message}
	CreatedAt       string          `json:"created_at"` // RFC3339 字符串，非 int64
	CompletedAt     string          `json:"completed_at"`
	Metadata        struct {
		Model      string `json:"model"`
		Status     string `json:"status"` // MiniMax 原生状态（如 succeeded），仅日志参考
		Resolution string `json:"resolution"`
		Duration   int    `json:"duration"`
		Usage      struct {
			TotalSeconds    int `json:"total_seconds"`
			InputSeconds    int `json:"input_seconds"`
			OutputSeconds   int `json:"output_seconds"`
			InputImageCount int `json:"input_image_count"`
		} `json:"usage"`
		Ratio    string `json:"ratio"`
		TaskType string `json:"task_type"`
		Content  struct {
			URL string `json:"url"`
		} `json:"content"`
	} `json:"metadata"`
}

type taskEnvelope struct {
	Task *gatewayTask `json:"task"`
}

// resultURL 取视频下载地址：outputs[0] 优先，回退 metadata.content.url（文档载明两者一致）。
func (t *gatewayTask) resultURL() string {
	for _, u := range t.Outputs {
		if strings.TrimSpace(u) != "" {
			return u
		}
	}
	return strings.TrimSpace(t.Metadata.Content.URL)
}

// gatewayError 网关错误体，形如
// {"error":{"message":"...","type":"...","code":"ERR_AUTH_001","param":null},"request_id":"..."}。
type gatewayError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

// ============================
// 适配器实现
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	apiKey  string
	baseURL string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	if taskErr = relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); taskErr != nil {
		return taskErr
	}

	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}

	canonicalModel, ok := resolveUpstreamModel(pickModelName(info, &req))
	if !ok {
		return service.TaskErrorWrapperLocal(
			fmt.Errorf("model %q is not supported by this channel (available: %s)",
				pickModelName(info, &req), strings.Join(ModelList, ", ")),
			"invalid_model", http.StatusBadRequest)
	}
	spec, ok := getSpec(canonicalModel)
	if !ok {
		return service.TaskErrorWrapperLocal(
			fmt.Errorf("model %q has no capability spec registered", canonicalModel),
			"invalid_model", http.StatusBadRequest)
	}

	// 顶层字段校验：resolution / ratio 必须进 metadata；duration 类型必须能被解析
	if taskErr := validateTopLevelFields(c); taskErr != nil {
		return taskErr
	}

	// 时长：按模型（h3 4–15s，h3-max 5–15s）
	if sec, hasValue, parseErr := resolveDuration(&req); parseErr != nil {
		return service.TaskErrorWrapperLocal(parseErr, "invalid_duration", http.StatusBadRequest)
	} else if hasValue && (sec < spec.minDuration || sec > spec.maxDuration) {
		return service.TaskErrorWrapperLocal(
			fmt.Errorf("duration must be between %d and %d seconds for model %q, got %d",
				spec.minDuration, spec.maxDuration, canonicalModel, sec),
			"invalid_duration", http.StatusBadRequest)
	}

	// 分辨率：按模型白名单（h3 480P/768P/2K，h3-max 480P/768P）
	resolution := canonicalResolution(metaString(req.Metadata, "resolution"))
	if resolution != "" {
		if _, valid := spec.resolutions[resolution]; !valid {
			return service.TaskErrorWrapperLocal(
				fmt.Errorf("resolution must be one of %s for model %q, got %q",
					strings.Join(supportedResolutions(spec), "/"), canonicalModel, resolution),
				"invalid_resolution", http.StatusBadRequest)
		}
	}

	// ratio：白名单（场景相关规则在 content 解析后校验）
	ratio, hasRatio := getMetaString(req.Metadata, "ratio")
	if hasRatio {
		if _, valid := validRatios[ratio]; !valid {
			return service.TaskErrorWrapperLocal(
				fmt.Errorf("ratio must be one of 16:9/4:3/1:1/3:4/9:16/21:9/adaptive, got %q", ratio),
				"invalid_ratio", http.StatusBadRequest)
		}
	}

	// content 组装校验（类型/角色白名单、素材数量上限、帧模式与参考模式互斥、音频不可单独）
	_, hasVisual, taskErr := buildContentItems(&req, canonicalModel)
	if taskErr != nil {
		return taskErr
	}

	// 文生视频（无任何视觉素材）：ratio 必填且不能为 adaptive
	if taskErr := validateRatio(ratio, hasRatio, hasVisual); taskErr != nil {
		return taskErr
	}

	return nil
}

// validateRatio 校验画面比例的场景规则（白名单已在调用方查过）。
//
// 上游硬约束，前置拦截避免白烧一次上游往返（上游错误原文：
// "ratio is required for t2va (text-only) and cannot be 'adaptive'"）。
// 闸门是「有无视觉素材」而不是「有无参考图」：纯参考视频同样可以配 adaptive
// （2026-09-12 实测 video_url + ratio=adaptive 提交通过）。
func validateRatio(ratio string, hasRatio, hasVisual bool) *dto.TaskError {
	if hasVisual {
		return nil
	}
	if !hasRatio {
		return service.TaskErrorWrapperLocal(
			fmt.Errorf("ratio is required for text-to-video (one of 16:9/4:3/1:1/3:4/9:16/21:9)"),
			"invalid_ratio", http.StatusBadRequest)
	}
	if ratio == ratioAdaptive {
		return service.TaskErrorWrapperLocal(
			fmt.Errorf("ratio cannot be %q for text-to-video; adaptive is only allowed with an image or video input", ratioAdaptive),
			"invalid_ratio", http.StatusBadRequest)
	}
	return nil
}

// pickModelName 取客户侧模型名：渠道映射名优先（映射发生在 ValidateRequestAndSetAction 之后，
// 故此处映射名可能尚未就绪，逐级回退）。
func pickModelName(info *relaycommon.RelayInfo, req *relaycommon.TaskSubmitReq) string {
	if info.IsModelMapped && info.UpstreamModelName != "" {
		return info.UpstreamModelName
	}
	if req.Model != "" {
		return req.Model
	}
	return info.OriginModelName
}

// topLevelMustGoToMetadata 放在顶层不会被适配器消费的字段，命中即 400，避免静默丢字段。
var topLevelMustGoToMetadata = []string{
	"resolution",
	"ratio",
}

// validateTopLevelFields 校验顶层字段，两类问题都在这里拦：
//
//  1. resolution / ratio 放在顶层不会被消费（本适配器只从 metadata 读），命中即 400；
//  2. duration 的原始 JSON 类型必须能被 TaskSubmitReq 解析。
//     relay_info.go 的 UnmarshalJSON 对非数字 duration（如 "abc"、true、数组）是静默置 0，
//     于是客户会拿到一个与 duration 毫不相干的报错（比如 ratio is required），
//     或者默默按默认 5 秒出片。doubao 适配器已有同样防护，此处口径对齐，
//     并额外收紧两点：字符串必须真能解析成整数（doubao 只放行 string 类型不校内容），
//     JSON 数字必须是整数值。
func validateTopLevelFields(c *gin.Context) *dto.TaskError {
	var raw map[string]interface{}
	if err := common.UnmarshalBodyReusable(c, &raw); err != nil {
		return nil
	}
	for _, field := range topLevelMustGoToMetadata {
		if _, has := raw[field]; has {
			return service.TaskErrorWrapperLocal(
				fmt.Errorf("top-level %q is ignored by the MiniMax adapter; please move it into the metadata object (e.g. {\"metadata\": {%q: ...}})", field, field),
				"unsupported_top_level_field", http.StatusBadRequest)
		}
	}
	if rawDur, has := raw["duration"]; has {
		if taskErr := validateRawDuration(rawDur); taskErr != nil {
			return taskErr
		}
	}
	return nil
}

// validateRawDuration 校验顶层 duration 的原始 JSON 值。标准库把 JSON 数字反序列化成
// float64，所以合法形态只有三种：nil 与空字符串（等价于未传，不得报错——部分客户端
// 会用空字符串表示未设置）、整数值 float64、可解析为整数的字符串。
func validateRawDuration(rawDur interface{}) *dto.TaskError {
	const want = "top-level duration must be an integer or numeric string (e.g. 15 or \"15\")"
	invalid := func(got string) *dto.TaskError {
		return service.TaskErrorWrapperLocal(
			fmt.Errorf("%s; got %s", want, got),
			"invalid_duration", http.StatusBadRequest)
	}
	switch v := rawDur.(type) {
	case nil:
		return nil
	case float64:
		if v != float64(int64(v)) {
			return invalid(fmt.Sprintf("non-integer number %v", v))
		}
		return nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		if _, err := strconv.Atoi(strings.TrimSpace(v)); err != nil {
			return invalid(fmt.Sprintf("%q", v))
		}
		return nil
	default:
		return invalid(fmt.Sprintf("%T", v))
	}
}

func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf(GenerateEndpointFmt, a.baseURL), nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

// EstimateBilling 预扣费倍率：秒数 × 分辨率倍率（基准档 768P）。
// 基础价应按 768P 每秒单价配置（两个模型同为 $0.0624），
// h3 的 2K 自动乘 1.625、h3-max 的 480P 自动乘 0.625。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	canonicalModel, ok := resolveUpstreamModel(pickModelName(info, &req))
	if !ok {
		return nil
	}
	seconds, _, _ := resolveDuration(&req)
	if seconds <= 0 {
		seconds = defaultDuration
	}
	ratios := map[string]float64{"seconds": float64(seconds)}

	resolution := taskcommon.DefaultString(canonicalResolution(metaString(req.Metadata, "resolution")), defaultResolution)
	if r, ok := getResolutionBillingRatio(canonicalModel, resolution); ok && r != 1 {
		ratios["resolution"] = r
	}
	return ratios
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	upstreamModel, ok := resolveUpstreamModel(pickModelName(info, &req))
	if !ok {
		return nil, fmt.Errorf("model %q is not supported by this channel", pickModelName(info, &req))
	}

	items, hasVisual, taskErr := buildContentItems(&req, upstreamModel)
	if taskErr != nil {
		return nil, errors.New(taskErr.Message)
	}

	seconds, _, _ := resolveDuration(&req)
	if seconds <= 0 {
		seconds = defaultDuration
	}
	resolution := taskcommon.DefaultString(canonicalResolution(metaString(req.Metadata, "resolution")), defaultResolution)

	// ratio：文生视频必填（校验层已拦）；带视觉素材时客户未指定则默认 adaptive
	ratio, _ := getMetaString(req.Metadata, "ratio")
	if ratio == "" && hasVisual {
		ratio = ratioAdaptive
	}

	body := videoRequest{
		Model:      upstreamModel,
		Content:    items,
		Resolution: resolution,
		Duration:   seconds,
		Ratio:      ratio,
	}

	info.UpstreamModelName = body.Model
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse 解析提交响应。上游返回 {"task":{"id":"mvt-...", ...}} 信封，
// 任务号取 task.id（不是顶层 task_id）。
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	if task, parseErr := parseTaskEnvelope(responseBody); parseErr == nil && task.ID != "" {
		ov := dto.NewOpenAIVideo()
		ov.ID = info.PublicTaskID
		ov.TaskID = info.PublicTaskID
		ov.CreatedAt = time.Now().Unix()
		ov.Model = info.OriginModelName
		c.JSON(http.StatusOK, ov)
		return task.ID, responseBody, nil
	}

	// 拿不到 task.id：提交失败
	taskErr = submitFailTaskError(resp.StatusCode, responseBody)
	return
}

// submitFailTaskError 把非成功的提交响应转成带上游信息的错误。
// 4xx 属客户侧/上游业务拒绝，用 Local 包装避免触发重试白烧配额；5xx 保持可重试。
func submitFailTaskError(status int, body []byte) *dto.TaskError {
	var ge gatewayError
	if err := common.Unmarshal(body, &ge); err == nil && ge.Error.Message != "" {
		code := ge.Error.Code
		if code == "" {
			code = ge.Error.Type
		}
		if code == "" {
			code = "upstream_error"
		}
		msg := fmt.Errorf("minimax api error: %s", ge.Error.Message)
		if status >= 400 && status < 500 {
			return service.TaskErrorWrapperLocal(msg, code, status)
		}
		return service.TaskErrorWrapper(msg, code, status)
	}
	return service.TaskErrorWrapper(
		fmt.Errorf("minimax submit failed: status=%d body=%s", status, truncate(string(body), 512)),
		"invalid_response", status)
}

// FetchTask 轮询任务：GET {base}/v1/video/tasks/{task_id}。
// 注意必须是 GET，POST /v1/video/tasks 在上游不存在（404）。
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf(QueryEndpointFmt, baseUrl, taskID)
	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

// parseTaskEnvelope 解析 {"task":{...}} 信封。
// 网关个别路径可能直接返回裸任务对象，此处一并容忍。
func parseTaskEnvelope(body []byte) (*gatewayTask, error) {
	var envelope taskEnvelope
	if err := common.Unmarshal(body, &envelope); err == nil && envelope.Task != nil {
		return envelope.Task, nil
	}
	var bare gatewayTask
	if err := common.Unmarshal(body, &bare); err != nil {
		return nil, errors.Wrap(err, "unmarshal minimax task response failed")
	}
	if bare.ID == "" && bare.Status == "" {
		return nil, fmt.Errorf("minimax task response has neither task envelope nor task fields")
	}
	return &bare, nil
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	task, err := parseTaskEnvelope(respBody)
	if err != nil {
		return nil, err
	}

	taskResult := relaycommon.TaskInfo{Code: 0}

	// 失败优先判定：文档载明"失败时 status 异常且 error 非空"，status 未必是 failed，
	// 故只要未 completed 且 error 非空即判失败。
	if task.Status != statusCompleted && errorPresent(task.Error) {
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = failReason(task)
		return &taskResult, nil
	}

	switch task.Status {
	case statusPending:
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case statusProcessing:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case statusCompleted:
		url := task.resultURL()
		if url == "" {
			// 上游报完成却没给地址：按失败处理，让零产出退款生效，
			// 否则会出现"扣了钱拿不到视频"。
			taskResult.Status = model.TaskStatusFailure
			taskResult.Progress = "100%"
			taskResult.Reason = "upstream reported completed but returned no video URL"
			return &taskResult, nil
		}
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = url
		// 文档的 task.metadata.usage 只有秒数（total/input/output_seconds、input_image_count），
		// 没有 token 字段；计费已在 EstimateBilling 按「秒数 × 分辨率」预扣，故此处不回填 token。
	case statusFailed:
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = failReason(task)
	default:
		// 空字符串过渡态或网关新增的未知状态：按未完成继续轮询
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}
	return &taskResult, nil
}

// errorPresent 判断 task.error 是否携带失败信息（null / 空串 / 空对象均视为无）。
func errorPresent(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != `""` && s != "{}"
}

// failReason 从 task.error（null / 字符串 / {code,message}）提取失败原因。
func failReason(task *gatewayTask) string {
	raw := task.Error
	if !errorPresent(raw) {
		return fmt.Sprintf("task %s without details", task.Status)
	}

	var s string
	if err := common.Unmarshal(raw, &s); err == nil && strings.TrimSpace(s) != "" {
		return s
	}

	var obj struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := common.Unmarshal(raw, &obj); err == nil {
		code, msg := strings.TrimSpace(obj.Code), strings.TrimSpace(obj.Message)
		switch {
		case code != "" && msg != "":
			return fmt.Sprintf("code=%s: %s", code, msg)
		case code != "":
			return fmt.Sprintf("code=%s", code)
		case msg != "":
			return msg
		}
	}
	return string(raw)
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	openAIVideo := originTask.ToOpenAIVideo()
	if task, err := parseTaskEnvelope(originTask.Data); err == nil {
		if url := task.resultURL(); url != "" {
			openAIVideo.SetMetadata("url", url)
		}
		if task.Status == statusFailed || (task.Status != statusCompleted && errorPresent(task.Error)) {
			openAIVideo.Error = &dto.OpenAIVideoError{
				Message: failReason(task),
			}
		}
	}

	jsonData, err := common.Marshal(openAIVideo)
	if err != nil {
		return nil, errors.Wrap(err, "marshal openai video failed")
	}
	return jsonData, nil
}

// ============================
// content[] 组装
// ============================

// maxContentItems content[] 元素数量粗上限，只用于挡住病态请求；真正的素材约束是
// 按类型的 9 图 / 3 视频 / 3 音频（见 constants.go），那才是上游的提交期强校验规则。
const maxContentItems = 32

// buildContentItems 把客户请求转成上游 content[]，并返回是否含视觉素材
// （参考图 / 参考视频 / 首尾帧）——它决定 ratio 规则。注意音频不算视觉素材：
// 上游的 ratio=adaptive 只认图片与视频输入（同上游 new-api 的 h3HasVisualContent）。
//
// 顶层 content[] 优先（受控透传）；否则走便捷字段 prompt + images[]/input_reference
// + videos[] + metadata 里的首尾帧与参考音视频。
func buildContentItems(req *relaycommon.TaskSubmitReq, modelName string) ([]ContentItem, bool, *dto.TaskError) {
	if len(req.Content) > 0 {
		return buildFromTopLevelContent(req, modelName)
	}
	return buildFromConvenienceFields(req, modelName)
}

// buildFromConvenienceFields 便捷字段路径。
//
// ⚠ images[] / input_reference 映射为 reference_image（参考模式），与上游 new-api 的
// hailuo 插件不同——那边把 images[] 当首尾帧。本站线上一直是参考图语义，改动会
// 静默改变既有客户的生成效果，故保持。要用帧模式请传 metadata.first_frame_image /
// metadata.last_frame_image，或直接用顶层 content[] 指定 role。
func buildFromConvenienceFields(req *relaycommon.TaskSubmitReq, modelName string) ([]ContentItem, bool, *dto.TaskError) {
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, false, service.TaskErrorWrapperLocal(
			fmt.Errorf("prompt is required (content must include a non-empty text item)"),
			"invalid_request", http.StatusBadRequest)
	}
	items := []ContentItem{{Type: contentTypeText, Text: req.Prompt}}
	// locators 与 items 一一对应，记录每个元素在客户请求里的出处。
	// 便捷路径的组装顺序（帧→图→视频→音频）与客户书写顺序无关，而且客户根本
	// 没写 content[]，所以这里必须报字段名（images[0] / metadata.reference_video[1]）；
	// 报 content[N] 会指到客户眼里完全不同的位置，把「提交就报准确原因」这个优点抵消掉。
	locators := []string{"prompt"}

	// 帧模式：metadata.first_frame_image / last_frame_image（键名对齐上游 hailuo 插件）
	for _, f := range []struct{ key, role string }{
		{metaFirstFrameImage, roleFirstFrame},
		{metaLastFrameImage, roleLastFrame},
	} {
		for _, l := range metaMediaLabeled(req.Metadata, f.key) {
			items = append(items, mediaItem(contentTypeImageURL, f.role, l.url))
			locators = append(locators, l.label)
		}
	}

	// 参考模式：images[] + input_reference → 参考图；videos[] + metadata.reference_video → 参考视频
	for _, l := range collectLabeledImages(req) {
		items = append(items, mediaItem(contentTypeImageURL, roleReferenceImage, l.url))
		locators = append(locators, l.label)
	}
	videos := append(labeledList(req.Videos, "videos"), metaMediaLabeled(req.Metadata, metaReferenceVideo)...)
	for _, l := range dedupeLabeled(videos) {
		items = append(items, mediaItem(contentTypeVideoURL, roleReferenceVideo, l.url))
		locators = append(locators, l.label)
	}
	for _, l := range metaMediaLabeled(req.Metadata, metaReferenceAudio) {
		items = append(items, mediaItem(contentTypeAudioURL, roleReferenceAudio, l.url))
		locators = append(locators, l.label)
	}

	if taskErr := validateContentItems(items, locators, modelName); taskErr != nil {
		return nil, false, taskErr
	}
	return items, hasVisualMedia(items), nil
}

// mediaItem 按类型组装素材元素，只填对应的那一个 URL 字段。
func mediaItem(typ, role, url string) ContentItem {
	item := ContentItem{Type: typ, Role: role}
	media := &MediaURL{URL: url}
	switch typ {
	case contentTypeImageURL:
		item.ImageURL = media
	case contentTypeVideoURL:
		item.VideoURL = media
	case contentTypeAudioURL:
		item.AudioURL = media
	}
	return item
}

// labeledURL 素材地址 + 它在客户请求里的出处。出处只用于报错定位，不进上游请求体。
type labeledURL struct {
	url   string
	label string
}

// labeledList 给一组地址打上「字段名[下标]」的出处标签，丢空值。
// 下标用的是客户原始数组里的位置（跳过的空值不占位），这样报错里的 images[2]
// 就是客户写的第三个元素，而不是去重后的第三个。
func labeledList(urls []string, field string) []labeledURL {
	out := make([]labeledURL, 0, len(urls))
	for i, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		out = append(out, labeledURL{url: u, label: fmt.Sprintf("%s[%d]", field, i)})
	}
	return out
}

// collectLabeledImages 顺序收集 images[] 与 input_reference，带出处标签，丢空去重。
func collectLabeledImages(req *relaycommon.TaskSubmitReq) []labeledURL {
	in := labeledList(req.Images, "images")
	if u := strings.TrimSpace(req.InputReference); u != "" {
		in = append(in, labeledURL{url: u, label: "input_reference"})
	}
	return dedupeLabeled(in)
}

// dedupeLabeled 按地址去重，保留首次出现的出处标签。
func dedupeLabeled(in []labeledURL) []labeledURL {
	seen := make(map[string]struct{}, len(in))
	out := make([]labeledURL, 0, len(in))
	for _, l := range in {
		if _, ok := seen[l.url]; ok {
			continue
		}
		seen[l.url] = struct{}{}
		out = append(out, l)
	}
	return out
}

// metaMediaLabeled 从 metadata 取素材地址并打上出处标签，兼容单值字符串与数组
// （JSON 反序列化后数组一律是 []interface{}），丢空去重、保持顺序。
// 对应上游 hailuo 插件的 h3MediaList，额外多一层出处以便报错定位。
func metaMediaLabeled(m map[string]interface{}, key string) []labeledURL {
	if m == nil {
		return nil
	}
	v, exists := m[key]
	if !exists || v == nil {
		return nil
	}
	field := "metadata." + key
	switch val := v.(type) {
	case string:
		if s := strings.TrimSpace(val); s != "" {
			return []labeledURL{{url: s, label: field}}
		}
		return nil
	case []string:
		return dedupeLabeled(labeledList(val, field))
	case []interface{}:
		// 逐元素保留原始下标，非字符串项直接跳过（不估猜、也不静默当空字符串）
		out := make([]labeledURL, 0, len(val))
		for i, e := range val {
			s, ok := e.(string)
			if !ok {
				continue
			}
			if s = strings.TrimSpace(s); s == "" {
				continue
			}
			out = append(out, labeledURL{url: s, label: fmt.Sprintf("%s[%d]", field, i)})
		}
		return dedupeLabeled(out)
	default:
		return nil
	}
}

// metaMediaList 同 metaMediaLabeled，只取地址（给不关心出处的调用方与单测用）。
func metaMediaList(m map[string]interface{}, key string) []string {
	labeled := metaMediaLabeled(m, key)
	urls := make([]string, 0, len(labeled))
	for _, l := range labeled {
		urls = append(urls, l.url)
	}
	return urls
}

// buildFromTopLevelContent 受控透传客户自带的 content[]：放行 text 与
// image_url / video_url / audio_url 三类素材；role 按类型白名单校验（可省略，
// 省略时取该类型的默认角色）。逐项错误带 content[i] 下标，便于客户定位。
func buildFromTopLevelContent(req *relaycommon.TaskSubmitReq, modelName string) ([]ContentItem, bool, *dto.TaskError) {
	if len(req.Content) > maxContentItems {
		return nil, false, service.TaskErrorWrapperLocal(
			fmt.Errorf("content has %d items, at most %d are allowed", len(req.Content), maxContentItems),
			"invalid_content", http.StatusBadRequest)
	}

	items := make([]ContentItem, 0, len(req.Content))
	// 顶层 content[] 路径：元素顺序与客户写的完全一致，出处就是 content[i]
	// （上游自己的报错也用 content[i]，两边对得上）。
	locators := make([]string, 0, len(req.Content))
	for i, raw := range req.Content {
		typ, _ := raw["type"].(string)
		switch typ {
		case contentTypeText:
			text, _ := raw["text"].(string)
			items = append(items, ContentItem{Type: contentTypeText, Text: text})
			locators = append(locators, fmt.Sprintf("content[%d]", i))
		case contentTypeImageURL, contentTypeVideoURL, contentTypeAudioURL:
			url := extractMediaURL(raw, typ)
			if url == "" {
				return nil, false, contentErr(i, fmt.Sprintf("%s.url is required", typ))
			}
			role, _ := raw["role"].(string)
			role = strings.TrimSpace(role)
			if role == "" {
				role = defaultRoleForType(typ)
			} else if !isAllowedRole(typ, role) {
				return nil, false, contentErr(i, fmt.Sprintf("role %q is not valid for %s; allowed: %s",
					role, typ, strings.Join(rolesForType(typ), "/")))
			}
			items = append(items, mediaItem(typ, role, url))
			locators = append(locators, fmt.Sprintf("content[%d]", i))
		default:
			// 上游对不认识的 type 是静默丢弃，会让请求退化成纯文本并抛出与 type 无关的
			// ratio 报错，故本地必须拦下来给出准确原因。
			return nil, false, contentErr(i, fmt.Sprintf(
				"type must be one of %s/%s/%s/%s, got %q (unknown types are silently dropped by the upstream gateway)",
				contentTypeText, contentTypeImageURL, contentTypeVideoURL, contentTypeAudioURL, typ))
		}
	}

	if taskErr := validateContentItems(items, locators, modelName); taskErr != nil {
		return nil, false, taskErr
	}
	return items, hasVisualMedia(items), nil
}

// hasVisualMedia 是否含视觉素材（图片或视频）。音频不算——上游的 ratio=adaptive
// 只认图片与视频输入。
func hasVisualMedia(items []ContentItem) bool {
	for _, it := range items {
		if it.Type == contentTypeImageURL || it.Type == contentTypeVideoURL {
			return true
		}
	}
	return false
}

// validateContentItems 实施上游的提交期强校验规则，让客户在本地就拿到准确 400，
// 不白烧一次上游往返。两条路径（顶层 content[] 与便捷字段）共用。
//
// 规则来源：2026-09-12 零成本实测的 400 报错原文，与上游 new-api hailuo 插件的
// validateH3Content 交叉验证一致：
//
//	at most 9 reference images allowed
//	at most 3 reference videos allowed
//	at most 3 reference audios allowed
//	reference mode cannot be mixed with first_frame/middle_frame/last_frame; choose one
//	audio cannot be the only reference; include at least one reference image or video
//
// locators 与 items 一一对应，是逐项报错时指向客户请求的位置描述：顶层 content[]
// 路径传 content[i]，便捷字段路径传客户真写的字段名（images[0] / metadata.xxx）。
// 缺失或长度不齐时退回 content[i]，保证不会因定位信息缺失而 panic。
func validateContentItems(items []ContentItem, locators []string, modelName string) *dto.TaskError {
	// 未知模型不会走到这里（ValidateRequestAndSetAction 已先拦），故忽略 ok；
	// 取不到 spec 时等于不施加任何按模型分档的限制。
	spec, _ := getSpec(modelName)

	locator := func(i int) string {
		if i < len(locators) && locators[i] != "" {
			return locators[i]
		}
		return fmt.Sprintf("content[%d]", i)
	}

	var hasText bool
	var firstFrames, middleFrames, lastFrames, frameImages int
	var refImages, refVideos, refAudios, totalImages int

	for i, it := range items {
		switch it.Type {
		case contentTypeText:
			if strings.TrimSpace(it.Text) != "" {
				hasText = true
			}
		case contentTypeImageURL:
			if taskErr := validateMediaURL(locator(i), it); taskErr != nil {
				return taskErr
			}
			totalImages++
			switch it.Role {
			case roleFirstFrame:
				firstFrames++
				frameImages++
			case roleMiddleFrame:
				middleFrames++
				frameImages++
			case roleLastFrame:
				lastFrames++
				frameImages++
			default:
				refImages++
			}
		case contentTypeVideoURL, contentTypeAudioURL:
			if taskErr := validateMediaURL(locator(i), it); taskErr != nil {
				return taskErr
			}
			if it.Type == contentTypeVideoURL {
				refVideos++
			} else {
				refAudios++
			}
		}
	}

	invalid := func(err error) *dto.TaskError {
		return service.TaskErrorWrapperLocal(err, "invalid_content", http.StatusBadRequest)
	}

	if !hasText {
		return invalid(fmt.Errorf("content must include a non-empty text item (prompt is required)"))
	}
	for _, f := range []struct {
		name string
		n    int
	}{{roleFirstFrame, firstFrames}, {roleMiddleFrame, middleFrames}, {roleLastFrame, lastFrames}} {
		if f.n > 1 {
			return invalid(fmt.Errorf("model %q accepts at most one %s image, got %d", modelName, f.name, f.n))
		}
	}
	if frameImages > maxFrameImages {
		return invalid(fmt.Errorf("model %q accepts at most %d frame images (%s/%s/%s), got %d",
			modelName, maxFrameImages, roleFirstFrame, roleMiddleFrame, roleLastFrame, frameImages))
	}
	if refImages > maxReferenceImages {
		return invalid(fmt.Errorf("model %q accepts at most %d reference images, got %d",
			modelName, maxReferenceImages, refImages))
	}
	if totalImages > maxReferenceImages {
		return invalid(fmt.Errorf("model %q accepts at most %d images in total, got %d",
			modelName, maxReferenceImages, totalImages))
	}
	if refVideos > maxReferenceVideos {
		return invalid(fmt.Errorf("model %q accepts at most %d reference videos, got %d",
			modelName, maxReferenceVideos, refVideos))
	}
	if refAudios > maxReferenceAudios {
		return invalid(fmt.Errorf("model %q accepts at most %d reference audios, got %d",
			modelName, maxReferenceAudios, refAudios))
	}
	// 帧模式与参考模式二选一（上游报错原文：reference mode cannot be mixed with
	// first_frame/middle_frame/last_frame; choose one）
	if frameImages > 0 && refImages+refVideos+refAudios > 0 {
		return invalid(fmt.Errorf(
			"model %q cannot mix frame images (%s/%s/%s) with reference media (%s/%s/%s); choose one mode",
			modelName, roleFirstFrame, roleMiddleFrame, roleLastFrame,
			roleReferenceImage, roleReferenceVideo, roleReferenceAudio))
	}
	// 音频不能作为唯一参考素材（上游报错原文：audio cannot be the only reference;
	// include at least one reference image or video）。只在已实测确认的模型上本地拦，
	// 详见 modelSpec.audioNeedsVisualCompanion 的注释——未证实的模型交由上游判定，
	// 避免重蹈「照文档收紧白名单、拦掉上游实际支持的能力」的覆辙。
	if spec.audioNeedsVisualCompanion && refAudios > 0 && refImages == 0 && refVideos == 0 && frameImages == 0 {
		return invalid(fmt.Errorf(
			"model %q: audio cannot be the only reference; include at least one reference image or video",
			modelName))
	}
	return nil
}

// itemURL 取素材元素的地址（三个指针字段按 Type 互斥）。
func itemURL(it ContentItem) string {
	switch it.Type {
	case contentTypeImageURL:
		if it.ImageURL != nil {
			return it.ImageURL.URL
		}
	case contentTypeVideoURL:
		if it.VideoURL != nil {
			return it.VideoURL.URL
		}
	case contentTypeAudioURL:
		if it.AudioURL != nil {
			return it.AudioURL.URL
		}
	}
	return ""
}

// isAllowedMediaURL 上游只接受 http(s):// 与 data:...;base64 两种形态。实测报错原文：
//
//	content[1].video_url: invalid param: video url must be http(s):// or data:...;base64
//	content[1].video_url: disallowed url: https://不可解析的域名/v.mp4
//
// 本地前置拦截有两个好处：一是客户立即拿到准确 400，而不是提交成功后要到轮询里
// 才发现任务 failed；二是挡掉 asset://... 这类只在渠道类型 58/60 上有效的素材库引用
// （类型 61 未接入素材库，见 controller/sd_asset.go），否则上游只会回一句含义不明的
// disallowed url。注意不在本地做可达性探测：内网地址/防盗链等属于上游的 SSRF 判定范围。
func isAllowedMediaURL(u string) bool {
	u = strings.TrimSpace(u)
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "data:")
}

// validateMediaURL 校验单个素材元素的地址形态；locator 是该元素在客户请求里的出处。
func validateMediaURL(locator string, it ContentItem) *dto.TaskError {
	url := itemURL(it)
	if isAllowedMediaURL(url) {
		return nil
	}
	hint := ""
	if strings.HasPrefix(strings.TrimSpace(url), "asset://") {
		hint = " (asset:// references belong to the sd asset library, which this channel type does not use; pass a public http(s) URL or an inline data: URL instead)"
	}
	return service.TaskErrorWrapperLocal(
		fmt.Errorf("%s: %s url must start with http://, https:// or data:<mime>;base64, got %q%s",
			locator, it.Type, truncate(url, 120), hint),
		"invalid_content", http.StatusBadRequest)
}

// extractMediaURL 取 content 项里 {type}_url.url。
func extractMediaURL(raw map[string]interface{}, typ string) string {
	mu, ok := raw[typ].(map[string]interface{})
	if !ok {
		return ""
	}
	u, _ := mu["url"].(string)
	return strings.TrimSpace(u)
}

func contentErr(index int, msg string) *dto.TaskError {
	return service.TaskErrorWrapperLocal(
		fmt.Errorf("content[%d]: %s", index, msg),
		"invalid_content", http.StatusBadRequest)
}

// ============================
// 小工具
// ============================

// resolveDuration 取客户实际传的时长（秒）。三个来源：顶层 seconds（字符串）、
// 顶层 duration（整数）、metadata.duration。
//
// 冲突口径与 resolution / ratio 保持一致（见 topLevelMustGoToMetadata：写错位置就 400）：
// 多源同时出现且取值不一致时直接报错，不静默按优先级取一——客户在 resolution 上
// 踩过一次「写了但没生效」，不该在 duration 上再踩一次。取值一致时不算冲突，照常放行。
//
// metadata.duration 是补上的兜底：resolution / ratio 被要求必须放在 metadata 里，
// 客户很自然会照同样写法把 duration 也放进去，而此前它会被静默忽略、退回默认 5 秒——
// 既不符合客户预期，也让预扣费与实际生成时长脱节。
func resolveDuration(req *relaycommon.TaskSubmitReq) (int, bool, error) {
	type durationSource struct {
		name string
		secs int
	}
	var found []durationSource

	if s := strings.TrimSpace(req.Seconds); s != "" {
		sec, err := strconv.Atoi(s)
		if err != nil {
			return 0, true, fmt.Errorf("seconds must be an integer, got %q", req.Seconds)
		}
		found = append(found, durationSource{"seconds", sec})
	}
	if req.Duration > 0 {
		found = append(found, durationSource{"duration", req.Duration})
	}
	if v, ok := getMetaString(req.Metadata, "duration"); ok {
		sec, err := strconv.Atoi(v)
		if err != nil {
			return 0, true, fmt.Errorf("metadata.duration must be an integer, got %q", v)
		}
		found = append(found, durationSource{"metadata.duration", sec})
	}

	if len(found) == 0 {
		return 0, false, nil
	}
	parts := make([]string, 0, len(found))
	for _, src := range found {
		parts = append(parts, fmt.Sprintf("%s=%d", src.name, src.secs))
	}
	for _, src := range found[1:] {
		if src.secs != found[0].secs {
			return 0, true, fmt.Errorf(
				"conflicting duration values (%s); keep exactly one of seconds / duration / metadata.duration",
				strings.Join(parts, ", "))
		}
	}
	return found[0].secs, true, nil
}

// getMetaString 从 metadata 提取字符串字段，兼容数字被写成 int/float64 的情形。
func getMetaString(m map[string]interface{}, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, exists := m[key]
	if !exists || v == nil {
		return "", false
	}
	switch s := v.(type) {
	case string:
		s = strings.TrimSpace(s)
		if s == "" {
			return "", false
		}
		return s, true
	case int, int32, int64, float32, float64, bool:
		return fmt.Sprintf("%v", s), true
	default:
		return "", false
	}
}

// metaString 同 getMetaString，缺失时返回空串。
func metaString(m map[string]interface{}, key string) string {
	v, _ := getMetaString(m, key)
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
