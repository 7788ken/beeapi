package doubao

import (
	"bytes"
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
	"github.com/samber/lo"
)

// getMetaString 从 metadata 中提取字符串字段，兼容 multipart 反序列化把数字写成
// int/float64 的情形：用 fmt.Sprintf("%v",v) 兜底转字符串，再做白名单匹配。
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

// resolveSeedanceDuration 取客户端实际传的时长（秒），优先 Seconds 字符串，
// 回退 Duration int。返回 (seconds, hasValue, parseErr)。
func resolveSeedanceDuration(req *relaycommon.TaskSubmitReq) (int, bool, error) {
	if req.Seconds != "" {
		sec, err := strconv.Atoi(req.Seconds)
		if err != nil {
			return 0, true, fmt.Errorf("seconds must be an integer, got %q", req.Seconds)
		}
		return sec, true, nil
	}
	if req.Duration > 0 {
		return req.Duration, true, nil
	}
	return 0, false, nil
}

// ============================
// Request / Response structures
// ============================

type ContentItem struct {
	Type     string    `json:"type,omitempty"`
	Text     string    `json:"text,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`
	AudioURL *MediaURL `json:"audio_url,omitempty"`
	Role     string    `json:"role,omitempty"`
}

type MediaURL struct {
	URL string `json:"url,omitempty"`
}

type requestPayload struct {
	Model                 string         `json:"model"`
	Content               []ContentItem  `json:"content,omitempty"`
	CallbackURL           string         `json:"callback_url,omitempty"`
	ReturnLastFrame       *dto.BoolValue `json:"return_last_frame,omitempty"`
	ServiceTier           string         `json:"service_tier,omitempty"`
	ExecutionExpiresAfter *dto.IntValue  `json:"execution_expires_after,omitempty"`
	GenerateAudio         *dto.BoolValue `json:"generate_audio,omitempty"`
	Draft                 *dto.BoolValue `json:"draft,omitempty"`
	Tools                 []struct {
		Type string `json:"type,omitempty"`
	} `json:"tools,omitempty"`
	SafetyIdentifier string         `json:"safety_identifier,omitempty"`
	Priority         *dto.IntValue  `json:"priority,omitempty"`
	Resolution       string         `json:"resolution,omitempty"`
	Ratio            string         `json:"ratio,omitempty"`
	Duration         *dto.IntValue  `json:"duration,omitempty"`
	Frames           *dto.IntValue  `json:"frames,omitempty"`
	Seed             *dto.IntValue  `json:"seed,omitempty"`
	CameraFixed      *dto.BoolValue `json:"camera_fixed,omitempty"`
	Watermark        *dto.BoolValue `json:"watermark,omitempty"`
}

type responsePayload struct {
	ID string `json:"id"` // task_id
}

type responseTask struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Status  string `json:"status"`
	Content struct {
		VideoURL string `json:"video_url"`
	} `json:"content"`
	Seed            int    `json:"seed"`
	Resolution      string `json:"resolution"`
	Duration        int    `json:"duration"`
	Ratio           string `json:"ratio"`
	FramesPerSecond int    `json:"framespersecond"`
	ServiceTier     string `json:"service_tier"`
	Tools           []struct {
		Type string `json:"type"`
	} `json:"tools"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		ToolUsage        struct {
			WebSearch int `json:"web_search"`
		} `json:"tool_usage"`
	} `json:"usage"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	// UpstreamFlavor 上游线协议风格：空/UpstreamFlavorArk = 火山方舟原生；
	// UpstreamFlavorSd = sd 网关风格（/v1/video/generate + /v1/video/tasks，见 sd_flavor.go）。
	// 轮询链路不经过 Init，由 GetTaskAdaptor 按渠道类型注入；提交链路在 Init 中按 ChannelType 兜底。
	UpstreamFlavor string
	apiKey         string
	baseURL        string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
	if info.ChannelType == constant.ChannelTypeSdVideo {
		a.UpstreamFlavor = UpstreamFlavorSd
	}
}

func (a *TaskAdaptor) isSdFlavor() bool {
	return a.UpstreamFlavor == UpstreamFlavorSd
}

// ValidateRequestAndSetAction parses body, validates fields and sets default action.
// 上游 seedance 对 duration<4 / 非法 resolution / 非法 ratio 会回 500 internal_error，
// 触发 newapi 重试浪费配额；在此前置校验，命中即返回 400 LocalError 终止流程。
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	if taskErr = relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); taskErr != nil {
		return taskErr
	}

	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}

	modelName := req.Model
	if modelName == "" {
		modelName = info.OriginModelName
	}

	if sec, hasValue, parseErr := resolveSeedanceDuration(&req); parseErr != nil {
		return service.TaskErrorWrapperLocal(parseErr, "invalid_duration", http.StatusBadRequest)
	} else if hasValue && (sec < seedanceDurationMin || sec > seedanceDurationMax) {
		return service.TaskErrorWrapperLocal(
			fmt.Errorf("duration must be between %d and %d seconds, got %d", seedanceDurationMin, seedanceDurationMax, sec),
			"invalid_duration", http.StatusBadRequest)
	}

	if v, ok := getMetaString(req.Metadata, "resolution"); ok {
		if _, valid := validSeedanceResolutions[strings.ToLower(v)]; !valid {
			return service.TaskErrorWrapperLocal(
				fmt.Errorf("resolution must be one of 480p/720p/1080p/4k, got %q", v),
				"invalid_resolution", http.StatusBadRequest)
		}
		if _, isFast := fastSeedanceModels[modelName]; isFast &&
			(strings.EqualFold(v, "1080p") || strings.EqualFold(v, "4k")) {
			return service.TaskErrorWrapperLocal(
				fmt.Errorf("%s is not supported on fast model %q", v, modelName),
				"invalid_resolution", http.StatusBadRequest)
		}
	}

	if v, ok := getMetaString(req.Metadata, "ratio"); ok {
		if _, valid := validSeedanceRatios[v]; !valid {
			return service.TaskErrorWrapperLocal(
				fmt.Errorf("ratio must be one of 16:9/9:16/1:1/4:3/3:4/21:9/adaptive, got %q", v),
				"invalid_ratio", http.StatusBadRequest)
		}
	}

	// 拒绝顶层放高级参数（resolution/ratio/... 应放 metadata）等"静默丢失"字段。
	// 注：content[] 已支持顶层多模态透传，不在拒绝之列。
	if taskErr := validateNoForbiddenTopLevel(c); taskErr != nil {
		return taskErr
	}

	// 多模态 content[] 白名单校验（type/role 合法性、mini 模型不支持 audio），
	// 前置拦截避免非法组合触发上游 5xx + 无谓重试。
	if len(req.Content) > 0 {
		if taskErr := validateContentItems(req.Content, modelName); taskErr != nil {
			return taskErr
		}
	}

	return nil
}

// validateNoForbiddenTopLevel 检查请求体里有没有放到顶层但 adapter 不会消费的字段。
// 命中即返回 400，提示客户挪到 metadata 或换字段名，避免静默丢字段难排查。
// 复用 UnmarshalBodyReusable，不会破坏后续 BuildRequestBody 重新读 body。
func validateNoForbiddenTopLevel(c *gin.Context) *dto.TaskError {
	var raw map[string]interface{}
	if err := common.UnmarshalBodyReusable(c, &raw); err != nil {
		// 已经走过一次 unmarshal 仍失败，说明不是 JSON，跳过该校验由后续流程兜底
		return nil
	}

	for _, field := range topLevelMustGoToMetadata {
		if _, has := raw[field]; has {
			return service.TaskErrorWrapperLocal(
				fmt.Errorf("top-level %q is ignored by the seedance adapter; please move it into the metadata object (e.g. {\"metadata\": {%q: ...}})", field, field),
				"unsupported_top_level_field", http.StatusBadRequest)
		}
	}

	// 顶层 content[] 现已支持多模态受控透传（见 validateContentItems / convertToRequestPayload），此处不再拒绝。

	// duration 顶层允许 int / numeric string / null。其它类型（bool、array、object）兜底 400，
	// 因为 TaskSubmitReq.UnmarshalJSON 会静默置 0，与"按 15s 计费却出 5s"同样难排查。
	// 标准库 json.Unmarshal 把 JSON 数字反序列化为 float64，所以只列 nil/string/float64 三类。
	if rawDur, has := raw["duration"]; has {
		switch rawDur.(type) {
		case nil, string, float64:
			// ok, TaskSubmitReq 已正确解析
		default:
			return service.TaskErrorWrapperLocal(
				fmt.Errorf("top-level duration must be an integer or numeric string (e.g. 15 or \"15\"); got %T", rawDur),
				"invalid_duration", http.StatusBadRequest)
		}
	}

	return nil
}

// contentRoleWhitelist 每种 content type 允许的 role 取值（空 role 一律允许）。
var contentRoleWhitelist = map[string][]string{
	"image_url": {"reference_image", "first_frame", "last_frame"},
	"video_url": {"reference_video"},
	"audio_url": {"reference_audio"},
}

// validateContentItems 校验顶层多模态 content[]：
// - type 必须 ∈ {text,image_url,video_url,audio_url}
// - role（若提供）必须与 type 匹配（见 contentRoleWhitelist）
// - audio_url 在 mini 模型上不支持，直接 400（上游必拒）
func validateContentItems(content []map[string]interface{}, modelName string) *dto.TaskError {
	isMini := isMiniSeedanceModel(modelName)
	for i, item := range content {
		typ, _ := item["type"].(string)
		switch typ {
		case "text", "image_url", "video_url", "audio_url":
		default:
			return service.TaskErrorWrapperLocal(
				fmt.Errorf("content[%d].type must be one of text/image_url/video_url/audio_url, got %q", i, typ),
				"invalid_content", http.StatusBadRequest)
		}
		if role, _ := item["role"].(string); role != "" {
			allowed, ok := contentRoleWhitelist[typ]
			if !ok || !lo.Contains(allowed, role) {
				return service.TaskErrorWrapperLocal(
					fmt.Errorf("content[%d] role %q is not valid for type %q", i, role, typ),
					"invalid_content", http.StatusBadRequest)
			}
		}
		if typ == "audio_url" && isMini {
			return service.TaskErrorWrapperLocal(
				fmt.Errorf("audio reference is not supported on mini model %q", modelName),
				"unsupported_audio", http.StatusBadRequest)
		}
	}
	return nil
}

// isMiniSeedanceModel 判断是否为 mini 变体（不支持音频参考）。
func isMiniSeedanceModel(name string) bool {
	return strings.Contains(strings.ToLower(name), "mini")
}

// normalizeAssetURL 把素材库引用 Asset://... 归一化为小写 asset://...（其余原样返回）。
func normalizeAssetURL(u string) string {
	const prefix = "asset://"
	if len(u) >= len(prefix) && strings.EqualFold(u[:len(prefix)], prefix) {
		return prefix + u[len(prefix):]
	}
	return u
}

// BuildRequestURL constructs the upstream URL.
func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	if a.isSdFlavor() {
		return fmt.Sprintf("%s/v1/video/generate", a.baseURL), nil
	}
	return fmt.Sprintf("%s/api/v3/contents/generations/tasks", a.baseURL), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

// EstimateBilling uses the two-dimensional resolution × video-input price table.
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	resolution, _ := getMetaString(req.Metadata, "resolution")
	ratio, ok := GetSeedanceBillingRatio(info.OriginModelName, resolution, hasVideoInput(&req))
	if !ok || ratio == 1 {
		return nil
	}
	return map[string]float64{"seedance_resolution_video_input": ratio}
}

// hasVideoInput 检测请求是否含视频输入（触发视频输入折扣），覆盖三个入口：
// 顶层 content[]（type=video_url）、顶层便捷字段 videos[]、metadata.content。
func hasVideoInput(req *relaycommon.TaskSubmitReq) bool {
	if len(req.Videos) > 0 {
		return true
	}
	for _, item := range req.Content {
		if item["type"] == "video_url" {
			return true
		}
		if _, has := item["video_url"]; has {
			return true
		}
	}
	return metadataContentHasVideo(req.Metadata)
}

// metadataContentHasVideo 检查 metadata.content 数组是否包含 video_url 条目。
func metadataContentHasVideo(metadata map[string]interface{}) bool {
	if metadata == nil {
		return false
	}
	contentSlice, ok := metadata["content"].([]interface{})
	if !ok {
		return false
	}
	for _, item := range contentSlice {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if itemMap["type"] == "video_url" {
			return true
		}
		if _, has := itemMap["video_url"]; has {
			return true
		}
	}
	return false
}

// BuildRequestBody converts request into Doubao specific format.
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	body, err := a.convertToRequestPayload(&req)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}
	if info.IsModelMapped {
		body.Model = info.UpstreamModelName
	} else {
		info.UpstreamModelName = body.Model
	}
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// DoRequest delegates to common helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse handles upstream response, returns taskID etc.
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	var upstreamTaskID string
	if a.isSdFlavor() {
		// sd 网关：{"task":{"id":"mvt-...","status":"pending",...}}
		task, err := parseSdTaskEnvelope(responseBody)
		if err != nil {
			taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
			return
		}
		upstreamTaskID = task.ID
	} else {
		// Parse Doubao response
		var dResp responsePayload
		if err := common.Unmarshal(responseBody, &dResp); err != nil {
			taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
			return
		}
		upstreamTaskID = dResp.ID
	}

	if upstreamTaskID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return upstreamTaskID, responseBody, nil
}

// FetchTask fetch task status
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s/api/v3/contents/generations/tasks/%s", baseUrl, taskID)
	if a.isSdFlavor() {
		uri = fmt.Sprintf("%s/v1/video/tasks/%s", baseUrl, taskID)
	}

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
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

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq) (*requestPayload, error) {
	r := requestPayload{
		Model:   req.Model,
		Content: []ContentItem{},
	}

	usingTopLevelContent := len(req.Content) > 0

	if !usingTopLevelContent {
		// 简易路径：参考图（images[]/input_reference）+ 便捷视频参考（videos[]），自动打 role + asset:// 规范化
		for _, imgURL := range collectImageURLs(req) {
			r.Content = append(r.Content, ContentItem{
				Type:     "image_url",
				Role:     "reference_image",
				ImageURL: &MediaURL{URL: normalizeAssetURL(imgURL)},
			})
		}
		for _, vidURL := range collectVideoURLs(req) {
			r.Content = append(r.Content, ContentItem{
				Type:     "video_url",
				Role:     "reference_video",
				VideoURL: &MediaURL{URL: normalizeAssetURL(vidURL)},
			})
		}
	}

	metadata := req.Metadata
	if err := taskcommon.UnmarshalMetadata(metadata, &r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}

	// 顶层 content[] 优先：覆盖 metadata.content（若有），asset:// 规范化后原样透传。
	if usingTopLevelContent {
		items, err := buildContentItems(req.Content)
		if err != nil {
			return nil, err
		}
		r.Content = items
	}

	// 时长优先级：顶层 seconds(string) > 顶层 duration(int) > metadata.duration
	if sec, _ := strconv.Atoi(req.Seconds); sec > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(sec))
	} else if req.Duration > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(req.Duration))
	}

	if !usingTopLevelContent {
		// 简易路径：prompt 作为唯一 text 项（先剔除既有 text 再追加）
		r.Content = lo.Reject(r.Content, func(c ContentItem, _ int) bool { return c.Type == "text" })
		r.Content = append(r.Content, ContentItem{
			Type: "text",
			Text: req.Prompt,
		})
	}
	// content[] 透传模式：text 由客户端在 content 中提供，不追加顶层 prompt，避免重复 text 项

	return &r, nil
}

// collectImageURLs 收集所有参考图 URL，顺序保留 images[]，input_reference 追加在末尾，自动去重。
func collectImageURLs(req *relaycommon.TaskSubmitReq) []string {
	seen := make(map[string]struct{}, len(req.Images)+1)
	out := make([]string, 0, len(req.Images)+1)
	add := func(u string) {
		if u == "" {
			return
		}
		if _, ok := seen[u]; ok {
			return
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	for _, u := range req.Images {
		add(u)
	}
	add(req.InputReference)
	return out
}

// collectVideoURLs 收集便捷视频参考 URL（顶层 videos[]），保序去重、丢空。
func collectVideoURLs(req *relaycommon.TaskSubmitReq) []string {
	seen := make(map[string]struct{}, len(req.Videos))
	out := make([]string, 0, len(req.Videos))
	for _, u := range req.Videos {
		if u == "" {
			continue
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

// buildContentItems 把客户端顶层 content[]（原始 map）asset:// 规范化后转成上游 ContentItem 列表。
func buildContentItems(raw []map[string]interface{}) ([]ContentItem, error) {
	normalizeContentAssetURLs(raw)
	data, err := common.Marshal(raw)
	if err != nil {
		return nil, errors.Wrap(err, "marshal content failed")
	}
	var items []ContentItem
	if err := common.Unmarshal(data, &items); err != nil {
		return nil, errors.Wrap(err, "unmarshal content failed")
	}
	return items, nil
}

// normalizeContentAssetURLs 就地把 content[] 各项 image_url/video_url/audio_url 的 url 做 asset:// 归一化。
func normalizeContentAssetURLs(raw []map[string]interface{}) {
	for _, item := range raw {
		for _, key := range []string{"image_url", "video_url", "audio_url"} {
			mu, ok := item[key].(map[string]interface{})
			if !ok {
				continue
			}
			if u, ok := mu["url"].(string); ok {
				mu["url"] = normalizeAssetURL(u)
			}
		}
	}
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	if a.isSdFlavor() {
		return parseSdTaskResult(respBody)
	}
	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}

	// Map Doubao status to internal status
	switch resTask.Status {
	case "pending", "queued":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "processing", "running":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case "succeeded":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = resTask.Content.VideoURL
		// 解析 usage 信息用于按倍率计费
		taskResult.CompletionTokens = resTask.Usage.CompletionTokens
		taskResult.TotalTokens = resTask.Usage.TotalTokens
	case "failed":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = formatDoubaoFailReason(resTask)
	default:
		// Unknown status, treat as processing
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

// formatDoubaoFailReason 把 doubao 失败响应的 code 与 message 合成易于排障的 fail_reason。
// 仅 message:           "<message>"
// 仅 code:              "code=<code>"
// 同时存在:             "code=<code>: <message>"
// 两者都为空:           "upstream returned failed status without details"
func formatDoubaoFailReason(t responseTask) string {
	code := strings.TrimSpace(t.Error.Code)
	msg := strings.TrimSpace(t.Error.Message)
	switch {
	case code != "" && msg != "":
		return fmt.Sprintf("code=%s: %s", code, msg)
	case code != "":
		return fmt.Sprintf("code=%s", code)
	case msg != "":
		return msg
	default:
		return "upstream returned failed status without details"
	}
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	if a.isSdFlavor() {
		return convertSdTaskToOpenAIVideo(originTask)
	}
	var dResp responseTask
	if err := common.Unmarshal(originTask.Data, &dResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal doubao task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.SetMetadata("url", dResp.Content.VideoURL)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName

	if dResp.Status == "failed" {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: dResp.Error.Message,
			Code:    dResp.Error.Code,
		}
	}

	return common.Marshal(openAIVideo)
}
