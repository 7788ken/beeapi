package minimax_inf

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
)

// 默认出站 client 由 main 的 InitHttpClient 初始化，单测须自行初始化
func TestMain(m *testing.M) {
	service.InitHttpClient()
	os.Exit(m.Run())
}

// h3Spec / h3MaxSpec 取能力表，避免每个用例重复写 modelSpecs[...] 并丢失缺失时的报错。
func h3Spec(t *testing.T) modelSpec {
	t.Helper()
	spec, ok := getSpec(modelMinimaxH3)
	if !ok {
		t.Fatalf("spec for %q must be registered", modelMinimaxH3)
	}
	return spec
}

func h3MaxSpec(t *testing.T) modelSpec {
	t.Helper()
	spec, ok := getSpec(modelMinimaxH3Max)
	if !ok {
		t.Fatalf("spec for %q must be registered", modelMinimaxH3Max)
	}
	return spec
}

// 文档「示例：文生视频」的提交响应原文
const docSubmitResp = `{
    "task": {
        "id": "mvt-3bc4bfbb75dd456a",
        "status": "pending",
        "model": "minimax-h3",
        "duration_seconds": 5,
        "outputs": [],
        "error": null,
        "created_at": "2026-08-13T08:57:18.571Z",
        "completed_at": null
    }
}`

// 文档「响应（完成态）」原文
const docCompletedResp = `{
    "task": {
        "id": "mvt-3bc4bfbb75dd456a",
        "status": "completed",
        "model": "minimax-h3",
        "duration_seconds": 5,
        "outputs": [
            "https://video-product.cdn.minimax.io/inference_output/rollout/2026-08-13/67f317db-2d81-4202-a8cf-fcd503e722e7/output.mp4"
        ],
        "error": null,
        "created_at": "2026-08-13T08:57:18.571Z",
        "completed_at": "2026-08-13T09:00:36.177Z",
        "metadata": {
            "model": "minimax-h3",
            "status": "succeeded",
            "resolution": "2K",
            "duration": 5,
            "usage": {
                "total_seconds": 5,
                "input_seconds": 0,
                "output_seconds": 5,
                "input_image_count": 0
            },
            "ratio": "16:9",
            "task_type": "generation",
            "content": {
                "url": "https://video-product.cdn.minimax.io/.../output.mp4"
            }
        }
    }
}`

// 端点必须是网关 v1 线协议：提交 /v1/video/generate、轮询 GET /v1/video/tasks/{id}
func TestBuildAndFetchURLs(t *testing.T) {
	var gotPath, gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_, _ = w.Write([]byte(docCompletedResp))
	}))
	defer server.Close()

	a := &TaskAdaptor{baseURL: "https://model.service-inference.ai"}
	u, err := a.BuildRequestURL(nil)
	if err != nil || u != "https://model.service-inference.ai/v1/video/generate" {
		t.Fatalf("unexpected submit url: %q err=%v", u, err)
	}

	resp, err := a.FetchTask(server.URL, "k", map[string]any{"task_id": "mvt-3bc4bfbb75dd456a"}, "")
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	resp.Body.Close()
	if gotPath != "/v1/video/tasks/mvt-3bc4bfbb75dd456a" {
		t.Fatalf("unexpected query path: %q", gotPath)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("query must use GET (POST /v1/video/tasks is 404 upstream), got %q", gotMethod)
	}
}

// 提交响应是 {"task":{"id":...}} 信封，不是裸 task_id
func TestParseSubmitEnvelope(t *testing.T) {
	task, err := parseTaskEnvelope([]byte(docSubmitResp))
	if err != nil {
		t.Fatalf("parse submit response failed: %v", err)
	}
	if task.ID != "mvt-3bc4bfbb75dd456a" {
		t.Fatalf("task.id = %q", task.ID)
	}
	if task.Status != statusPending {
		t.Fatalf("status = %q, want pending", task.Status)
	}
	if task.DurationSeconds != 5 {
		t.Fatalf("duration_seconds = %d, want 5", task.DurationSeconds)
	}
	if task.CreatedAt != "2026-08-13T08:57:18.571Z" {
		t.Fatalf("created_at must be RFC3339 string, got %q", task.CreatedAt)
	}
	if len(task.Outputs) != 0 {
		t.Fatalf("outputs should be empty while pending, got %v", task.Outputs)
	}
}

func TestParseTaskResultStatuses(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{statusPending, string(model.TaskStatusQueued)},
		{statusProcessing, string(model.TaskStatusInProgress)},
		{statusCompleted, string(model.TaskStatusSuccess)},
		{statusFailed, string(model.TaskStatusFailure)},
		// 文档：中间可能短暂出现 status 为空字符串的过渡态，继续轮询
		{"", string(model.TaskStatusInProgress)},
		{"weird", string(model.TaskStatusInProgress)},
	}
	a := &TaskAdaptor{}
	for _, tc := range cases {
		body := `{"task":{"id":"mvt-1","status":"` + tc.status + `"`
		if tc.status == statusCompleted {
			body += `,"outputs":["https://cdn/output.mp4"]`
		}
		body += `}}`
		ti, err := a.ParseTaskResult([]byte(body))
		if err != nil || string(ti.Status) != tc.want {
			t.Fatalf("status %q → %v (want %v), err=%v", tc.status, ti.Status, tc.want, err)
		}
	}
}

// 完成态：视频地址取 outputs[0]
func TestParseTaskResultCompleted(t *testing.T) {
	a := &TaskAdaptor{}
	ti, err := a.ParseTaskResult([]byte(docCompletedResp))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if ti.Status != model.TaskStatusSuccess {
		t.Fatalf("status = %v, want SUCCESS", ti.Status)
	}
	want := "https://video-product.cdn.minimax.io/inference_output/rollout/2026-08-13/67f317db-2d81-4202-a8cf-fcd503e722e7/output.mp4"
	if ti.Url != want {
		t.Fatalf("url = %q, want %q", ti.Url, want)
	}
}

// outputs 为空时回退 metadata.content.url
func TestResultURLFallsBackToMetadata(t *testing.T) {
	body := `{"task":{"id":"mvt-1","status":"completed","outputs":[],
		"metadata":{"content":{"url":"https://cdn/fallback.mp4"}}}}`
	a := &TaskAdaptor{}
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if ti.Url != "https://cdn/fallback.mp4" {
		t.Fatalf("expected metadata fallback url, got %q", ti.Url)
	}
}

// completed 但拿不到地址：必须判失败，避免扣费却无产出
func TestCompletedWithoutURLIsFailure(t *testing.T) {
	a := &TaskAdaptor{}
	ti, err := a.ParseTaskResult([]byte(`{"task":{"id":"mvt-1","status":"completed","outputs":[]}}`))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if ti.Status != model.TaskStatusFailure {
		t.Fatalf("want FAILURE for completed-without-url, got %v", ti.Status)
	}
	if !strings.Contains(ti.Reason, "no video URL") {
		t.Fatalf("reason = %q", ti.Reason)
	}
}

// 文档：失败时 status 异常且 error 非空 —— status 不是 failed 也要判失败
func TestFailureDetectedByErrorField(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"task":{"id":"mvt-1","status":"processing","error":{"code":"ERR_MODEL_003","message":"Model not available to your account"}}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if ti.Status != model.TaskStatusFailure {
		t.Fatalf("want FAILURE when error present, got %v", ti.Status)
	}
	if !strings.Contains(ti.Reason, "ERR_MODEL_003") || !strings.Contains(ti.Reason, "Model not available") {
		t.Fatalf("reason missing details: %q", ti.Reason)
	}
}

// error 为字符串形态时也要能取到原因
func TestFailReasonStringShape(t *testing.T) {
	task := &gatewayTask{Status: statusFailed}
	_ = task.Error.UnmarshalJSON([]byte(`"upstream rejected the prompt"`))
	if got := failReason(task); got != "upstream rejected the prompt" {
		t.Fatalf("failReason = %q", got)
	}
}

func TestErrorPresent(t *testing.T) {
	cases := map[string]bool{
		``:                                   false,
		`null`:                               false,
		`""`:                                 false,
		`{}`:                                 false,
		`"boom"`:                             true,
		`{"code":"ERR_X_001","message":"m"}`: true,
	}
	for raw, want := range cases {
		var task gatewayTask
		if raw != "" {
			_ = task.Error.UnmarshalJSON([]byte(raw))
		}
		if got := errorPresent(task.Error); got != want {
			t.Fatalf("errorPresent(%s) = %v, want %v", raw, got, want)
		}
	}
}

// 网关错误体：{"error":{message,type,code,param},"request_id"}
func TestSubmitFailTaskErrorShapes(t *testing.T) {
	body := `{"error":{"message":"Model not available to your account","type":"permission_error","code":"ERR_MODEL_003","param":null},"request_id":"01a06daf"}`

	// 4xx：Local 包装，不触发重试
	te := submitFailTaskError(403, []byte(body))
	if te == nil || !strings.Contains(te.Message, "Model not available") || te.Code != "ERR_MODEL_003" {
		t.Fatalf("unexpected task error: %+v", te)
	}
	if !te.LocalError {
		t.Fatalf("4xx must be LocalError to avoid burning quota on retry: %+v", te)
	}

	// 5xx：保持可重试
	te2 := submitFailTaskError(502, []byte(body))
	if te2 == nil || te2.LocalError {
		t.Fatalf("5xx must stay retryable: %+v", te2)
	}

	// 非 JSON 兜底：截断，不透传长响应体原文
	te3 := submitFailTaskError(502, []byte(strings.Repeat("x", 1000)))
	if te3 == nil || !strings.Contains(te3.Message, "...") {
		t.Fatalf("expected truncated fallback error, got %+v", te3)
	}
}

func TestBuildContentConvenienceT2V(t *testing.T) {
	req := &relaycommon.TaskSubmitReq{Prompt: "一只橘猫在沙滩上奔跑，海浪拍打岸边，电影感光影"}
	items, hasVisual, taskErr := buildContentItems(req, modelMinimaxH3)
	if taskErr != nil {
		t.Fatalf("unexpected error: %v", taskErr.Message)
	}
	if hasVisual {
		t.Fatalf("text-only must not be flagged as having visual media")
	}
	if len(items) != 1 || items[0].Type != contentTypeText || items[0].Text != req.Prompt {
		t.Fatalf("unexpected items: %+v", items)
	}
}

// 便捷字段 images[] → role reference_image（文档「示例：图生视频」的形状）
func TestBuildContentConvenienceReferenceImage(t *testing.T) {
	req := &relaycommon.TaskSubmitReq{
		Prompt: "让人物转头微笑，保持背景不变",
		Images: []string{"https://example.com/reference.jpg"},
	}
	items, hasVisual, taskErr := buildContentItems(req, modelMinimaxH3)
	if taskErr != nil {
		t.Fatalf("unexpected error: %v", taskErr.Message)
	}
	if !hasVisual {
		t.Fatalf("reference image must count as visual media")
	}
	if len(items) != 2 {
		t.Fatalf("want text + image, got %+v", items)
	}
	img := items[1]
	if img.Type != contentTypeImageURL || img.Role != roleReferenceImage {
		t.Fatalf("unexpected image item: %+v", img)
	}
	if img.ImageURL == nil || img.ImageURL.URL != "https://example.com/reference.jpg" {
		t.Fatalf("image url lost: %+v", img)
	}
}

// 便捷字段 videos[] 与 metadata.reference_video 合并去重 → role reference_video
func TestBuildContentConvenienceVideos(t *testing.T) {
	req := &relaycommon.TaskSubmitReq{
		Prompt: "x",
		Videos: []string{"https://e.com/v1.mp4", " https://e.com/v1.mp4 ", "  "},
		Metadata: map[string]interface{}{
			metaReferenceVideo: []interface{}{"https://e.com/v2.mp4"},
		},
	}
	items, hasVisual, taskErr := buildContentItems(req, modelMinimaxH3)
	if taskErr != nil {
		t.Fatalf("videos[] 必须被接受（以前会被硬拒）: %v", taskErr.Message)
	}
	if !hasVisual {
		t.Fatalf("参考视频也算视觉素材（ratio=adaptive 可用）")
	}
	if len(items) != 3 {
		t.Fatalf("want text + 2 videos (去重后), got %+v", items)
	}
	for i, want := range []string{"https://e.com/v1.mp4", "https://e.com/v2.mp4"} {
		v := items[i+1]
		if v.Type != contentTypeVideoURL || v.Role != roleReferenceVideo {
			t.Fatalf("item %d 类型/角色错误: %+v", i+1, v)
		}
		if v.VideoURL == nil || v.VideoURL.URL != want {
			t.Fatalf("item %d url = %+v, want %q", i+1, v.VideoURL, want)
		}
		if v.ImageURL != nil || v.AudioURL != nil {
			t.Fatalf("item %d 只应填 VideoURL: %+v", i+1, v)
		}
	}
}

// metadata.first_frame_image / last_frame_image → 帧模式；reference_audio → 参考音频
func TestBuildContentConvenienceMetadataFramesAndAudio(t *testing.T) {
	req := &relaycommon.TaskSubmitReq{
		Prompt: "x",
		Metadata: map[string]interface{}{
			metaFirstFrameImage: "https://e.com/f.jpg",
			metaLastFrameImage:  "https://e.com/l.jpg",
		},
	}
	items, hasVisual, taskErr := buildContentItems(req, modelMinimaxH3)
	if taskErr != nil {
		t.Fatalf("unexpected error: %v", taskErr.Message)
	}
	if !hasVisual || len(items) != 3 {
		t.Fatalf("want text + 首帧 + 尾帧, got %+v hasVisual=%v", items, hasVisual)
	}
	if items[1].Role != roleFirstFrame || items[2].Role != roleLastFrame {
		t.Fatalf("帧角色错误: %+v", items)
	}

	// 音频不能单独使用（h3-max 已实测），必须配图片或视频
	_, _, taskErr = buildContentItems(&relaycommon.TaskSubmitReq{
		Prompt:   "x",
		Metadata: map[string]interface{}{metaReferenceAudio: "https://e.com/a.mp3"},
	}, modelMinimaxH3Max)
	if taskErr == nil || !strings.Contains(taskErr.Message, "audio cannot be the only reference") {
		t.Fatalf("h3-max 音频单独使用必须被拦, got %+v", taskErr)
	}

	// 音频 + 参考图 → 通过，且音频不算视觉素材
	items, hasVisual, taskErr = buildContentItems(&relaycommon.TaskSubmitReq{
		Prompt: "x",
		Images: []string{"https://e.com/1.jpg"},
		Metadata: map[string]interface{}{
			metaReferenceAudio: []interface{}{"https://e.com/a.mp3"},
		},
	}, modelMinimaxH3)
	if taskErr != nil {
		t.Fatalf("unexpected error: %v", taskErr.Message)
	}
	if !hasVisual || len(items) != 3 {
		t.Fatalf("want text + image + audio, got %+v hasVisual=%v", items, hasVisual)
	}
	audio := items[2]
	if audio.Type != contentTypeAudioURL || audio.Role != roleReferenceAudio {
		t.Fatalf("unexpected audio item: %+v", audio)
	}
	if audio.AudioURL == nil || audio.AudioURL.URL != "https://e.com/a.mp3" || audio.ImageURL != nil {
		t.Fatalf("audio url 字段错误: %+v", audio)
	}
}

// 帧模式与参考模式互斥：metadata 首帧 + images[] 参考图 必须被拦
func TestBuildContentConvenienceRejectsFrameReferenceMix(t *testing.T) {
	_, _, taskErr := buildContentItems(&relaycommon.TaskSubmitReq{
		Prompt:   "x",
		Images:   []string{"https://e.com/ref.jpg"},
		Metadata: map[string]interface{}{metaFirstFrameImage: "https://e.com/f.jpg"},
	}, modelMinimaxH3)
	if taskErr == nil || taskErr.Code != "invalid_content" || !strings.Contains(taskErr.Message, "cannot mix frame images") {
		t.Fatalf("expected mixing rejection, got %+v", taskErr)
	}
}

func TestBuildContentConvenienceRequiresPrompt(t *testing.T) {
	_, _, taskErr := buildContentItems(&relaycommon.TaskSubmitReq{Prompt: "   "}, modelMinimaxH3)
	if taskErr == nil || !strings.Contains(taskErr.Message, "prompt is required") {
		t.Fatalf("expected prompt requirement error, got %+v", taskErr)
	}
}

// 顶层 content[] 透传：文本 + 图/视频/音频三类素材全部放行
// （2026-09-12 实测上游原生解析这三类，文档「content 元素」章节漏写）
func TestBuildContentTopLevelPassthrough(t *testing.T) {
	req := &relaycommon.TaskSubmitReq{
		Content: []map[string]interface{}{
			{"type": "text", "text": "让人物转头微笑"},
			{"type": "image_url", "image_url": map[string]interface{}{"url": "https://example.com/reference.jpg"}, "role": "reference_image"},
			{"type": "video_url", "video_url": map[string]interface{}{"url": "https://example.com/ref.mp4"}, "role": "reference_video"},
			{"type": "audio_url", "audio_url": map[string]interface{}{"url": "https://example.com/ref.mp3"}, "role": "reference_audio"},
		},
	}
	items, hasVisual, taskErr := buildContentItems(req, modelMinimaxH3)
	if taskErr != nil {
		t.Fatalf("unexpected error: %v", taskErr.Message)
	}
	if !hasVisual || len(items) != 4 {
		t.Fatalf("hasVisual=%v items=%+v", hasVisual, items)
	}
	if items[1].ImageURL == nil || items[1].ImageURL.URL != "https://example.com/reference.jpg" {
		t.Fatalf("image url lost: %+v", items[1])
	}
	if items[2].VideoURL == nil || items[2].VideoURL.URL != "https://example.com/ref.mp4" {
		t.Fatalf("video url lost: %+v", items[2])
	}
	if items[3].AudioURL == nil || items[3].AudioURL.URL != "https://example.com/ref.mp3" {
		t.Fatalf("audio url lost: %+v", items[3])
	}
}

// 序列化形状必须与上游一致：按 type 只出现对应的 {type}_url 字段
func TestContentItemJSONShape(t *testing.T) {
	items := []ContentItem{
		{Type: contentTypeText, Text: "x"},
		mediaItem(contentTypeImageURL, roleFirstFrame, "https://e.com/f.jpg"),
		mediaItem(contentTypeVideoURL, roleReferenceVideo, "https://e.com/v.mp4"),
		mediaItem(contentTypeAudioURL, roleReferenceAudio, "https://e.com/a.mp3"),
	}
	data, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		`{"type":"text","text":"x"}`,
		`{"type":"image_url","image_url":{"url":"https://e.com/f.jpg"},"role":"first_frame"}`,
		`{"type":"video_url","video_url":{"url":"https://e.com/v.mp4"},"role":"reference_video"}`,
		`{"type":"audio_url","audio_url":{"url":"https://e.com/a.mp3"},"role":"reference_audio"}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少 %s\n实际: %s", want, got)
		}
	}
}

// 省略 role 时按类型取默认角色（图片默认 reference_image，与上游 hailuo 插件不同，
// 详见 defaultRoleForType 的注释）
func TestBuildContentDefaultsRolePerType(t *testing.T) {
	req := &relaycommon.TaskSubmitReq{
		Content: []map[string]interface{}{
			{"type": "text", "text": "x"},
			{"type": "image_url", "image_url": map[string]interface{}{"url": "https://e.com/1.jpg"}},
			{"type": "video_url", "video_url": map[string]interface{}{"url": "https://e.com/1.mp4"}},
			{"type": "audio_url", "audio_url": map[string]interface{}{"url": "https://e.com/1.mp3"}},
		},
	}
	items, hasVisual, taskErr := buildContentItems(req, modelMinimaxH3)
	if taskErr != nil {
		t.Fatalf("unexpected error: %v", taskErr.Message)
	}
	if !hasVisual {
		t.Fatalf("必须识别为含视觉素材")
	}
	for i, want := range []string{roleReferenceImage, roleReferenceVideo, roleReferenceAudio} {
		if items[i+1].Role != want {
			t.Fatalf("items[%d].Role = %q, want %q", i+1, items[i+1].Role, want)
		}
	}
}

// 非法形状：未知类型、缺 url、role 与 type 不匹配
// （未知类型必须本地拦下：上游会静默丢弃它，客户只会看到一条莫名其妙的 ratio 报错）
func TestBuildContentRejectsInvalidShapes(t *testing.T) {
	cases := []struct {
		name    string
		model   string
		content []map[string]interface{}
		want    string
	}{
		{
			name: "未知类型",
			content: []map[string]interface{}{
				{"type": "text", "text": "x"},
				{"type": "file", "file": map[string]interface{}{"url": "https://e.com/f.bin"}},
			},
			want: "type must be one of text/image_url/video_url/audio_url",
		},
		{
			name: "image_url 缺 url",
			content: []map[string]interface{}{
				{"type": "text", "text": "x"},
				{"type": "image_url", "image_url": map[string]interface{}{}},
			},
			want: "image_url.url is required",
		},
		{
			name: "video_url 的 url 全是空白",
			content: []map[string]interface{}{
				{"type": "text", "text": "x"},
				{"type": "video_url", "video_url": map[string]interface{}{"url": "   "}},
			},
			want: "video_url.url is required",
		},
		{
			name: "image_url 配视频角色",
			content: []map[string]interface{}{
				{"type": "text", "text": "x"},
				{"type": "image_url", "image_url": map[string]interface{}{"url": "https://e.com/1.jpg"}, "role": "reference_video"},
			},
			want: `role "reference_video" is not valid for image_url`,
		},
		{
			name: "video_url 配帧角色",
			content: []map[string]interface{}{
				{"type": "text", "text": "x"},
				{"type": "video_url", "video_url": map[string]interface{}{"url": "https://e.com/v.mp4"}, "role": "first_frame"},
			},
			want: `role "first_frame" is not valid for video_url`,
		},
		{
			name: "乱写角色",
			content: []map[string]interface{}{
				{"type": "text", "text": "x"},
				{"type": "image_url", "image_url": map[string]interface{}{"url": "https://e.com/1.jpg"}, "role": "totally_bogus"},
			},
			want: `role "totally_bogus" is not valid`,
		},
		{
			name: "没有文本项",
			content: []map[string]interface{}{
				{"type": "image_url", "image_url": map[string]interface{}{"url": "https://e.com/1.jpg"}, "role": "reference_image"},
			},
			want: "non-empty text",
		},
		{
			// 该规则仅在 h3-max 上实测确认，故按模型分档（见 audioNeedsVisualCompanion）
			name:  "音频作为唯一参考素材",
			model: modelMinimaxH3Max,
			content: []map[string]interface{}{
				{"type": "text", "text": "x"},
				{"type": "audio_url", "audio_url": map[string]interface{}{"url": "https://e.com/a.mp3"}, "role": "reference_audio"},
			},
			want: "audio cannot be the only reference",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.model
			if m == "" {
				m = modelMinimaxH3
			}
			_, _, taskErr := buildContentItems(&relaycommon.TaskSubmitReq{Content: tc.content}, m)
			if taskErr == nil {
				t.Fatalf("expected rejection")
			}
			if !strings.Contains(taskErr.Message, tc.want) {
				t.Fatalf("message %q does not contain %q", taskErr.Message, tc.want)
			}
		})
	}
}

// 音频单独使用：h3-max 本地拦（已实测）；h3 不在本地拦，交由上游判定。
// 未实测确认的规则不得用来收紧能力，否则会重蹈「拦掉上游实际支持的能力」的覆辙。
func TestAudioOnlyReferenceIsModelSpecific(t *testing.T) {
	audioOnly := func(model string) *dto.TaskError {
		_, _, taskErr := buildContentItems(&relaycommon.TaskSubmitReq{
			Content: []map[string]interface{}{
				{"type": "text", "text": "x"},
				{"type": "audio_url", "audio_url": map[string]interface{}{"url": "https://e.com/a.mp3"}, "role": "reference_audio"},
			},
		}, model)
		return taskErr
	}
	if taskErr := audioOnly(modelMinimaxH3Max); taskErr == nil ||
		!strings.Contains(taskErr.Message, "audio cannot be the only reference") {
		t.Fatalf("h3-max 应本地拦下音频单独使用, got %+v", taskErr)
	}
	if taskErr := audioOnly(modelMinimaxH3); taskErr != nil {
		t.Fatalf("h3 不得本地拦（未实测确认），应透到上游判定, got %v", taskErr.Message)
	}

	// 能力表必须反映这一分档
	if !h3MaxSpec(t).audioNeedsVisualCompanion {
		t.Fatalf("h3-max 必须标记 audioNeedsVisualCompanion")
	}
	if h3Spec(t).audioNeedsVisualCompanion {
		t.Fatalf("h3 未实测确认，不得标记 audioNeedsVisualCompanion")
	}
}

// 素材数量上限：上游提交期强校验（错误码 2013），实测与上游 new-api 常量一致
func TestBuildContentMediaLimits(t *testing.T) {
	img := func(n int) []map[string]interface{} {
		out := make([]map[string]interface{}, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, map[string]interface{}{
				"type": "image_url", "role": "reference_image",
				"image_url": map[string]interface{}{"url": fmt.Sprintf("https://e.com/%d.jpg", i)},
			})
		}
		return out
	}
	media := func(typ, role string, n int) []map[string]interface{} {
		out := make([]map[string]interface{}, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, map[string]interface{}{
				"type": typ, "role": role,
				typ: map[string]interface{}{"url": fmt.Sprintf("https://e.com/%d.bin", i)},
			})
		}
		return out
	}
	text := map[string]interface{}{"type": "text", "text": "x"}
	join := func(parts ...[]map[string]interface{}) []map[string]interface{} {
		out := []map[string]interface{}{text}
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}

	for _, tc := range []struct {
		name    string
		content []map[string]interface{}
	}{
		{"九张参考图", join(img(maxReferenceImages))},
		{"三条参考视频", join(media(contentTypeVideoURL, roleReferenceVideo, maxReferenceVideos))},
		{"三条参考音频配一张图", join(img(1), media(contentTypeAudioURL, roleReferenceAudio, maxReferenceAudios))},
	} {
		t.Run("上限内_"+tc.name, func(t *testing.T) {
			if _, _, taskErr := buildContentItems(&relaycommon.TaskSubmitReq{Content: tc.content}, modelMinimaxH3); taskErr != nil {
				t.Fatalf("应通过，却报错: %v", taskErr.Message)
			}
		})
	}

	for _, tc := range []struct {
		name    string
		content []map[string]interface{}
		want    string
	}{
		{"十张参考图", join(img(maxReferenceImages + 1)), "at most 9 reference images"},
		{"四条参考视频", join(media(contentTypeVideoURL, roleReferenceVideo, maxReferenceVideos+1)), "at most 3 reference videos"},
		{"四条参考音频", join(img(1), media(contentTypeAudioURL, roleReferenceAudio, maxReferenceAudios+1)), "at most 3 reference audios"},
	} {
		t.Run("超限_"+tc.name, func(t *testing.T) {
			_, _, taskErr := buildContentItems(&relaycommon.TaskSubmitReq{Content: tc.content}, modelMinimaxH3)
			if taskErr == nil || !strings.Contains(taskErr.Message, tc.want) {
				t.Fatalf("应报 %q，实际 %+v", tc.want, taskErr)
			}
		})
	}
}

// 帧模式：首尾帧可组合；三帧同传、重复首帧、与参考模式混用均必须本地拦下
func TestBuildContentFrameMode(t *testing.T) {
	frame := func(role string) map[string]interface{} {
		return map[string]interface{}{
			"type": "image_url", "role": role,
			"image_url": map[string]interface{}{"url": "https://e.com/" + role + ".jpg"},
		}
	}
	text := map[string]interface{}{"type": "text", "text": "x"}
	refVideo := map[string]interface{}{
		"type": "video_url", "role": "reference_video",
		"video_url": map[string]interface{}{"url": "https://e.com/v.mp4"},
	}

	// 首帧 + 尾帧 → 通过（实测 h3 与 h3-max 都提交通过）
	for _, m := range []string{modelMinimaxH3, modelMinimaxH3Max} {
		items, hasVisual, taskErr := buildContentItems(&relaycommon.TaskSubmitReq{
			Content: []map[string]interface{}{text, frame(roleFirstFrame), frame(roleLastFrame)},
		}, m)
		if taskErr != nil {
			t.Fatalf("%s 首尾帧组合应通过: %v", m, taskErr.Message)
		}
		if !hasVisual || len(items) != 3 {
			t.Fatalf("%s items=%+v hasVisual=%v", m, items, hasVisual)
		}
	}

	for _, tc := range []struct {
		name    string
		content []map[string]interface{}
		want    string
	}{
		{
			"首中尾三帧同传",
			[]map[string]interface{}{text, frame(roleFirstFrame), frame(roleMiddleFrame), frame(roleLastFrame)},
			"at most 2 frame images",
		},
		{
			"两个首帧",
			[]map[string]interface{}{text, frame(roleFirstFrame), frame(roleFirstFrame)},
			"at most one first_frame image",
		},
		{
			"首尾帧配参考视频",
			[]map[string]interface{}{text, frame(roleFirstFrame), frame(roleLastFrame), refVideo},
			"cannot mix frame images",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, taskErr := buildContentItems(&relaycommon.TaskSubmitReq{Content: tc.content}, modelMinimaxH3)
			if taskErr == nil || !strings.Contains(taskErr.Message, tc.want) {
				t.Fatalf("应报 %q，实际 %+v", tc.want, taskErr)
			}
		})
	}
}

// ratio 场景规则：闸门是「有无视觉素材」而不是「有无参考图」
func TestValidateRatio(t *testing.T) {
	// 有视觉素材（参考图、参考视频、首尾帧均算）：adaptive 与缺省都放行
	for _, ratio := range []string{ratioAdaptive, "16:9", ""} {
		if taskErr := validateRatio(ratio, ratio != "", true); taskErr != nil {
			t.Fatalf("hasVisual=true ratio=%q 应通过, got %v", ratio, taskErr.Message)
		}
	}
	// 纯文本：缺 ratio 必拦
	if taskErr := validateRatio("", false, false); taskErr == nil || taskErr.Code != "invalid_ratio" ||
		!strings.Contains(taskErr.Message, "ratio is required for text-to-video") {
		t.Fatalf("纯文本缺 ratio 应被拦, got %+v", taskErr)
	}
	// 纯文本：adaptive 必拦
	if taskErr := validateRatio(ratioAdaptive, true, false); taskErr == nil ||
		!strings.Contains(taskErr.Message, "adaptive is only allowed with an image or video input") {
		t.Fatalf("纯文本 adaptive 应被拦, got %+v", taskErr)
	}
	// 纯文本 + 合法比例：放行
	if taskErr := validateRatio("16:9", true, false); taskErr != nil {
		t.Fatalf("纯文本 16:9 应通过, got %v", taskErr.Message)
	}
}

// role 白名单与默认角色：错误文案必须稳定有序（不能因 map 随机遍历而变）
func TestRoleWhitelist(t *testing.T) {
	if got := strings.Join(rolesForType(contentTypeImageURL), "/"); got != "reference_image/first_frame/middle_frame/last_frame" {
		t.Fatalf("image roles = %q", got)
	}
	if got := strings.Join(rolesForType(contentTypeVideoURL), "/"); got != roleReferenceVideo {
		t.Fatalf("video roles = %q", got)
	}
	if got := strings.Join(rolesForType(contentTypeAudioURL), "/"); got != roleReferenceAudio {
		t.Fatalf("audio roles = %q", got)
	}
	if rolesForType(contentTypeText) != nil {
		t.Fatalf("text 不应有 role 白名单")
	}
	if !isAllowedRole(contentTypeImageURL, roleMiddleFrame) {
		t.Fatalf("middle_frame 必须在图片白名单内（文档未提但上游报错原文与上游 new-api 实现均确认存在）")
	}
	if isAllowedRole(contentTypeVideoURL, roleFirstFrame) {
		t.Fatalf("video_url 不得配帧角色")
	}
	for typ, want := range map[string]string{
		contentTypeImageURL: roleReferenceImage,
		contentTypeVideoURL: roleReferenceVideo,
		contentTypeAudioURL: roleReferenceAudio,
		contentTypeText:     "",
	} {
		if got := defaultRoleForType(typ); got != want {
			t.Fatalf("defaultRoleForType(%q) = %q, want %q", typ, got, want)
		}
	}
}

// metaMediaList 兼容单值字符串、[]string 与 JSON 反序列化后的 []interface{}
func TestMetaMediaList(t *testing.T) {
	m := map[string]interface{}{
		"single":   " https://e.com/a.mp4 ",
		"arr":      []interface{}{"https://e.com/b.mp4", "https://e.com/b.mp4", "  ", 42},
		"typed":    []string{"https://e.com/c.mp4"},
		"empty":    "   ",
		"nilv":     nil,
		"notalist": 7,
	}
	if got := metaMediaList(m, "single"); len(got) != 1 || got[0] != "https://e.com/a.mp4" {
		t.Fatalf("single = %v", got)
	}
	if got := metaMediaList(m, "arr"); len(got) != 1 || got[0] != "https://e.com/b.mp4" {
		t.Fatalf("arr 应去重并丢弃非字符串/空值, got %v", got)
	}
	if got := metaMediaList(m, "typed"); len(got) != 1 || got[0] != "https://e.com/c.mp4" {
		t.Fatalf("typed = %v", got)
	}
	for _, k := range []string{"empty", "nilv", "notalist", "missing"} {
		if got := metaMediaList(m, k); len(got) != 0 {
			t.Fatalf("key %q 应为空, got %v", k, got)
		}
	}
	if got := metaMediaList(nil, "single"); len(got) != 0 {
		t.Fatalf("nil metadata 应为空, got %v", got)
	}
}

// 素材地址形态：上游只收 http(s):// 与 data:...;base64
func TestValidateMediaURL(t *testing.T) {
	for _, ok := range []string{
		"http://e.com/a.jpg", "https://e.com/a.jpg", "  https://e.com/a.jpg  ",
		"data:image/jpeg;base64,AAAA", "data:video/mp4;base64,AAAA",
	} {
		if !isAllowedMediaURL(ok) {
			t.Fatalf("%q 应被接受", ok)
		}
	}
	for _, bad := range []string{"", "   ", "asset://abc123", "ftp://e.com/a.jpg", "e.com/a.jpg", "file:///tmp/a.jpg"} {
		if isAllowedMediaURL(bad) {
			t.Fatalf("%q 应被拒", bad)
		}
	}

	// 顶层 content[] 路径：asset:// 要给出指向性提示
	_, _, taskErr := buildContentItems(&relaycommon.TaskSubmitReq{
		Content: []map[string]interface{}{
			{"type": "text", "text": "x"},
			{"type": "video_url", "video_url": map[string]interface{}{"url": "asset://abc123"}, "role": "reference_video"},
		},
	}, modelMinimaxH3)
	if taskErr == nil || !strings.Contains(taskErr.Message, "content[1]") ||
		!strings.Contains(taskErr.Message, "asset:// references belong to the sd asset library") {
		t.Fatalf("asset:// 应带下标与提示被拦, got %+v", taskErr)
	}

	// 便捷字段路径同样要拦（images[] 里的非法地址）
	_, _, taskErr = buildContentItems(&relaycommon.TaskSubmitReq{
		Prompt: "x", Images: []string{"e.com/a.jpg"},
	}, modelMinimaxH3)
	if taskErr == nil || !strings.Contains(taskErr.Message, "must start with http://") {
		t.Fatalf("便捷字段非法地址应被拦, got %+v", taskErr)
	}

	// data: 内联 base64 必须能过（上游实测支持）
	if _, _, taskErr = buildContentItems(&relaycommon.TaskSubmitReq{
		Content: []map[string]interface{}{
			{"type": "text", "text": "x"},
			{"type": "image_url", "image_url": map[string]interface{}{"url": "data:image/jpeg;base64,AAAA"}, "role": "reference_image"},
		},
	}, modelMinimaxH3); taskErr != nil {
		t.Fatalf("data: 内联 base64 应被接受: %v", taskErr.Message)
	}
}

// 出站请求体的完整形状：图+视频+音频混合参考（均为参考模式，允许共存）。
// 该用例的期望值就是拿去跟上游做真机对拍的报文，改字段名/加 omitempty 会直接弄坏上游调用。
func TestBuildRequestBodyMixedReference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model: modelMinimaxH3,
		Content: []map[string]interface{}{
			{"type": "text", "text": "probe"},
			{"type": "image_url", "image_url": map[string]interface{}{"url": "data:image/jpeg;base64,AAAA"}, "role": "reference_image"},
			{"type": "video_url", "video_url": map[string]interface{}{"url": "data:video/mp4;base64,AAAA"}, "role": "reference_video"},
			{"type": "audio_url", "audio_url": map[string]interface{}{"url": "data:audio/mp3;base64,AAAA"}, "role": "reference_audio"},
		},
		Metadata: map[string]interface{}{"resolution": "768p", "duration": 4, "ratio": ratioAdaptive},
	})

	// IsModelMapped / UpstreamModelName 在内嵌的 *ChannelMeta 上，必须显式初始化，
	// 否则 pickModelName 会空指针（生产链路走 InitChannelMeta，永远不为 nil）。
	info := &relaycommon.RelayInfo{
		OriginModelName: modelMinimaxH3,
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	a := &TaskAdaptor{}
	reader, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("BuildRequestBody failed: %v", err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	got := string(raw)

	const want = `{"model":"minimax-h3","content":[` +
		`{"type":"text","text":"probe"},` +
		`{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,AAAA"},"role":"reference_image"},` +
		`{"type":"video_url","video_url":{"url":"data:video/mp4;base64,AAAA"},"role":"reference_video"},` +
		`{"type":"audio_url","audio_url":{"url":"data:audio/mp3;base64,AAAA"},"role":"reference_audio"}` +
		`],"resolution":"768P","duration":4,"ratio":"adaptive"}`
	if got != want {
		t.Fatalf("出站请求体不符\n实际: %s\n期望: %s", got, want)
	}
	// 上游模型名必须已回写到 info（供日志与计费归因）
	if info.UpstreamModelName != modelMinimaxH3 {
		t.Fatalf("UpstreamModelName = %q", info.UpstreamModelName)
	}
	t.Logf("可直接用于上游对拍的报文: %s", got)
}

// 便捷字段路径的报错必须指到客户真写的字段名，而不是 content[N]：
// 客户根本没写 content[]，而且组装顺序（帧→图→视频→音频）与他书写顺序无关。
func TestConveniencePathReportsFieldNames(t *testing.T) {
	cases := []struct {
		name string
		req  relaycommon.TaskSubmitReq
		want string
	}{
		{
			"images 第二项非法",
			relaycommon.TaskSubmitReq{Prompt: "x", Images: []string{"https://e.com/ok.jpg", "e.com/bad.jpg"}},
			"images[1]",
		},
		{
			"images 跳过的空值不占位，下标仍是客户原位置",
			relaycommon.TaskSubmitReq{Prompt: "x", Images: []string{"https://e.com/ok.jpg", "  ", "e.com/bad.jpg"}},
			"images[2]",
		},
		{
			"input_reference 非法",
			relaycommon.TaskSubmitReq{Prompt: "x", InputReference: "asset://img1"},
			"input_reference",
		},
		{
			"videos 非法",
			relaycommon.TaskSubmitReq{Prompt: "x", Videos: []string{"ftp://e.com/v.mp4"}},
			"videos[0]",
		},
		{
			"metadata.reference_video 数组第二项非法",
			relaycommon.TaskSubmitReq{
				Prompt:   "x",
				Metadata: map[string]interface{}{metaReferenceVideo: []interface{}{"https://e.com/ok.mp4", "asset://v1"}},
			},
			"metadata.reference_video[1]",
		},
		{
			"metadata.first_frame_image 非法",
			relaycommon.TaskSubmitReq{
				Prompt:   "x",
				Metadata: map[string]interface{}{metaFirstFrameImage: "ftp://e.com/f.jpg"},
			},
			"metadata.first_frame_image",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			_, _, taskErr := buildContentItems(&req, modelMinimaxH3)
			if taskErr == nil {
				t.Fatalf("应报错")
			}
			if !strings.Contains(taskErr.Message, tc.want) {
				t.Fatalf("报错应指向 %q，实际: %v", tc.want, taskErr.Message)
			}
			if strings.Contains(taskErr.Message, "content[") {
				t.Fatalf("便捷路径不得报 content[N]（客户没写 content）: %v", taskErr.Message)
			}
		})
	}

	// 顶层 content[] 路径仍报 content[i]，与上游自己的报错口径一致
	_, _, taskErr := buildContentItems(&relaycommon.TaskSubmitReq{
		Content: []map[string]interface{}{
			{"type": "text", "text": "x"},
			{"type": "image_url", "image_url": map[string]interface{}{"url": "https://e.com/ok.jpg"}, "role": "reference_image"},
			{"type": "video_url", "video_url": map[string]interface{}{"url": "asset://v1"}, "role": "reference_video"},
		},
	}, modelMinimaxH3)
	if taskErr == nil || !strings.Contains(taskErr.Message, "content[2]") {
		t.Fatalf("顶层路径应报 content[2], got %+v", taskErr)
	}
}

// newTaskContext 造一个带 JSON 请求体的 gin 上下文与最小可用的 RelayInfo，
// 用于整链路测 ValidateRequestAndSetAction（此前该函数零测试覆盖）。
func newTaskContext(t *testing.T, body string) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	// IsModelMapped / UpstreamModelName 在内嵌的 *ChannelMeta 上，Action 在内嵌的
	// *TaskRelayInfo 上，两个指针都必须显式初始化（生产链路分别由 InitChannelMeta
	// 与 GenRelayInfo 填好，永远不为 nil）。
	info := &relaycommon.RelayInfo{
		OriginModelName: modelMinimaxH3,
		ChannelMeta:     &relaycommon.ChannelMeta{},
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
	}
	return c, info
}

// ValidateRequestAndSetAction 整链路：把 validateTopLevelFields、时长/分辨率校验、
// content 组装、validateRatio 的接线全部跑一遍（之前全靠人工推）。
func TestValidateRequestAndSetActionEndToEnd(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string // 空表示应放行
	}{
		{
			// 本次修复的核心行为变更：纯参考视频也能用 adaptive
			"纯参考视频 + adaptive 应放行",
			`{"model":"minimax-h3","prompt":"x","videos":["https://e.com/v.mp4"],"metadata":{"ratio":"adaptive","resolution":"768P","duration":5}}`,
			"",
		},
		{
			// 音频不算视觉素材：只靠上游 ratio 规则会漏，必须本地拦
			"纯参考音频 + adaptive 应拒",
			`{"model":"minimax-h3","prompt":"x","metadata":{"reference_audio":"https://e.com/a.mp3","ratio":"adaptive","resolution":"768P","duration":5}}`,
			`ratio cannot be "adaptive" for text-to-video`,
		},
		{
			"纯文本 + adaptive 应拒",
			`{"model":"minimax-h3","prompt":"x","metadata":{"ratio":"adaptive"}}`,
			`ratio cannot be "adaptive" for text-to-video`,
		},
		{
			"纯文本缺 ratio 应拒",
			`{"model":"minimax-h3","prompt":"x"}`,
			"ratio is required for text-to-video",
		},
		{
			"首尾帧 + adaptive 应放行",
			`{"model":"minimax-h3","prompt":"x","metadata":{"first_frame_image":"https://e.com/f.jpg","last_frame_image":"https://e.com/l.jpg","ratio":"adaptive"}}`,
			"",
		},
		{
			"顶层 resolution 应拒",
			`{"model":"minimax-h3","prompt":"x","resolution":"768P","metadata":{"ratio":"16:9"}}`,
			`top-level "resolution" is ignored`,
		},
		{
			// relay_info.go 对非数字 duration 是静默置 0，不在这里拦就会退化成默认 5 秒
			"顶层 duration 非数字应拒",
			`{"model":"minimax-h3","prompt":"x","duration":"abc","metadata":{"ratio":"16:9"}}`,
			"top-level duration must be an integer or numeric string",
		},
		{
			"顶层 duration 小数应拒",
			`{"model":"minimax-h3","prompt":"x","duration":5.5,"metadata":{"ratio":"16:9"}}`,
			"non-integer number",
		},
		{
			"顶层 duration 布尔应拒",
			`{"model":"minimax-h3","prompt":"x","duration":true,"metadata":{"ratio":"16:9"}}`,
			"top-level duration must be an integer",
		},
		{
			"duration 与 metadata.duration 冲突应拒",
			`{"model":"minimax-h3","prompt":"x","duration":5,"metadata":{"duration":8,"ratio":"16:9"}}`,
			"conflicting duration values",
		},
		{
			"duration 与 metadata.duration 取值一致应放行",
			`{"model":"minimax-h3","prompt":"x","duration":5,"metadata":{"duration":5,"ratio":"16:9"}}`,
			"",
		},
		{
			// 空字符串等于未传，不得报错（部分客户端用空字符串表示未设置）
			"顶层 duration 空字符串应放行",
			`{"model":"minimax-h3","prompt":"x","duration":"","metadata":{"ratio":"16:9"}}`,
			"",
		},
		{
			"h3-max 时长下限 5 秒，传 4 应拒",
			`{"model":"minimax-h3-max","prompt":"x","metadata":{"ratio":"16:9","duration":4}}`,
			"duration must be between 5 and 15",
		},
		{
			"h3-max 不支持 2K",
			`{"model":"minimax-h3-max","prompt":"x","metadata":{"ratio":"16:9","resolution":"2K"}}`,
			"resolution must be one of 480P/768P",
		},
		{
			"h3-max 参考图应放行（旧版错误地拒了）",
			`{"model":"minimax-h3-max","prompt":"x","images":["https://e.com/1.jpg"],"metadata":{"ratio":"adaptive"}}`,
			"",
		},
		{
			"未登记的模型应拒",
			`{"model":"minimax-hailuo-2.3","prompt":"x","metadata":{"ratio":"16:9"}}`,
			"is not supported by this channel",
		},
		{
			"帧模式与参考模式混用应拒",
			`{"model":"minimax-h3","content":[{"type":"text","text":"x"},{"type":"image_url","image_url":{"url":"https://e.com/f.jpg"},"role":"first_frame"},{"type":"video_url","video_url":{"url":"https://e.com/v.mp4"},"role":"reference_video"}],"metadata":{"ratio":"adaptive"}}`,
			"cannot mix frame images",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, info := newTaskContext(t, tc.body)
			taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info)
			if tc.wantErr == "" {
				if taskErr != nil {
					t.Fatalf("应放行, got %v", taskErr.Message)
				}
				return
			}
			if taskErr == nil {
				t.Fatalf("应拒并包含 %q，实际放行了", tc.wantErr)
			}
			if !strings.Contains(taskErr.Message, tc.wantErr) {
				t.Fatalf("报错 %q 不含 %q", taskErr.Message, tc.wantErr)
			}
		})
	}
}

// 渠道模型映射前后必须得到同一个上游模型：ValidateRequestAndSetAction 与
// BuildRequestBody 都走 pickModelName，两处不得各说各话。
func TestModelMappingConsistencyAcrossStages(t *testing.T) {
	const body = `{"model":"h3-alias","prompt":"x","metadata":{"ratio":"16:9"}}`

	// 未映射：别名不在白名单 → 拒
	c, info := newTaskContext(t, body)
	if taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info); taskErr == nil ||
		!strings.Contains(taskErr.Message, `model "h3-alias" is not supported`) {
		t.Fatalf("未映射的别名应被拒, got %+v", taskErr)
	}

	// 已映射到 h3-max：校验阶段按 h3-max 的能力表走（不支持 2K）
	c2, info2 := newTaskContext(t, `{"model":"h3-alias","prompt":"x","metadata":{"ratio":"16:9","resolution":"2K"}}`)
	info2.IsModelMapped = true
	info2.UpstreamModelName = modelMinimaxH3Max
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c2, info2)
	if taskErr == nil || !strings.Contains(taskErr.Message, "resolution must be one of 480P/768P") {
		t.Fatalf("映射后应按 h3-max 能力表校验, got %+v", taskErr)
	}

	// 已映射且参数合法：校验与组装两阶段必须得到同一个上游模型名
	c3, info3 := newTaskContext(t, body)
	info3.IsModelMapped = true
	info3.UpstreamModelName = modelMinimaxH3Max
	a := &TaskAdaptor{}
	if taskErr := a.ValidateRequestAndSetAction(c3, info3); taskErr != nil {
		t.Fatalf("映射后应放行, got %v", taskErr.Message)
	}
	reader, err := a.BuildRequestBody(c3, info3)
	if err != nil {
		t.Fatalf("BuildRequestBody failed: %v", err)
	}
	raw, _ := io.ReadAll(reader)
	if !strings.Contains(string(raw), `"model":"minimax-h3-max"`) {
		t.Fatalf("出站模型名应为映射后的 minimax-h3-max，实际: %s", raw)
	}
}

func TestCanonicalResolution(t *testing.T) {
	cases := map[string]string{
		"480p": resolution480P, "480P": resolution480P, " 480P ": resolution480P,
		"768p": resolution768P, "768P": resolution768P, " 768P ": resolution768P,
		"2k": resolution2K, "2K": resolution2K, " 2K ": resolution2K,
		"1K": "1K", // 上游会 400，此处原样透出以便给出准确报错
		"":   "",
	}
	for in, want := range cases {
		if got := canonicalResolution(in); got != want {
			t.Fatalf("canonicalResolution(%q) = %q, want %q", in, got, want)
		}
	}
}

// 能力边界一律以上游 400 报错原文为准（非文档，文档漏了 480P 与 h3-max）：
//
//	model -H3     ... supported resolutions: 480P, 768P, 2K；supported durations: 4s…15s
//	model -H3-Max ... supported resolutions: 480P, 768P；   supported durations: 5s…15s
func TestModelSpecs(t *testing.T) {
	h3 := h3Spec(t)
	if h3.minDuration != 4 || h3.maxDuration != 15 {
		t.Fatalf("h3 duration = %d–%d, want 4–15", h3.minDuration, h3.maxDuration)
	}
	for _, r := range []string{resolution480P, resolution768P, resolution2K} {
		if _, ok := h3.resolutions[r]; !ok {
			t.Fatalf("h3 must support %s", r)
		}
	}

	max := h3MaxSpec(t)
	// 上游报错：does not support duration 4s, supported durations: 5s…15s
	if max.minDuration != 5 || max.maxDuration != 15 {
		t.Fatalf("h3-max duration = %d–%d, want 5–15", max.minDuration, max.maxDuration)
	}
	if _, ok := max.resolutions[resolution2K]; ok {
		t.Fatalf("h3-max must NOT support 2K")
	}
	for _, r := range []string{resolution480P, resolution768P} {
		if _, ok := max.resolutions[r]; !ok {
			t.Fatalf("h3-max must support %s", r)
		}
	}

	// 上游不存在的模型必须没有 spec
	for _, unknown := range []string{"minimax-hailuo-2.3", "gpt-4o", ""} {
		if _, ok := getSpec(unknown); ok {
			t.Fatalf("getSpec(%q) must not resolve", unknown)
		}
	}
}

// supportedResolutions 必须稳定有序（map 遍历随机，不排序会造成错误文案每次不同）
func TestSupportedResolutionsOrder(t *testing.T) {
	if got := strings.Join(supportedResolutions(h3Spec(t)), "/"); got != "480P/768P/2K" {
		t.Fatalf("h3 resolutions = %q, want 480P/768P/2K", got)
	}
	if got := strings.Join(supportedResolutions(h3MaxSpec(t)), "/"); got != "480P/768P" {
		t.Fatalf("h3-max resolutions = %q, want 480P/768P", got)
	}
}

// /v1/models 实测返回两个模型，文档写「仅 minimax-h3」是举例而非穷举
func TestResolveUpstreamModel(t *testing.T) {
	for _, in := range []string{"minimax-h3", "MiniMax-H3", " MINIMAX-H3 ", "minimax-H3"} {
		got, ok := resolveUpstreamModel(in)
		if !ok || got != modelMinimaxH3 {
			t.Fatalf("resolveUpstreamModel(%q) = %q,%v; want %q,true", in, got, ok, modelMinimaxH3)
		}
	}
	// h3-max 必须能归一（之前误当不存在的模型拒掉，导致客户直接吃 400）
	for _, in := range []string{"minimax-h3-max", "MiniMax-H3-Max", " MINIMAX-H3-MAX "} {
		got, ok := resolveUpstreamModel(in)
		if !ok || got != modelMinimaxH3Max {
			t.Fatalf("resolveUpstreamModel(%q) = %q,%v; want %q,true", in, got, ok, modelMinimaxH3Max)
		}
	}
	for _, bad := range []string{"MiniMax-Hailuo-2.3", "", "gpt-4o", "minimax-h3-maxx"} {
		if got, ok := resolveUpstreamModel(bad); ok {
			t.Fatalf("resolveUpstreamModel(%q) must fail, got %q", bad, got)
		}
	}
	// 逐级回退：首个空值跳过
	if got, ok := resolveUpstreamModel("", "minimax-h3-max"); !ok || got != modelMinimaxH3Max {
		t.Fatalf("fallback broken: %q,%v", got, ok)
	}
}

// 计费倍率取自 GET /v1/models 的 pricing（非文档，文档价是实际值的 1.282 倍）：
//
//	h3     768P $0.0624、2K   $0.1014 → 2K   = 1.625
//	h3-max 768P $0.0624、480P $0.039  → 480P = 0.625
func TestResolutionBillingRatio(t *testing.T) {
	if r, ok := getResolutionBillingRatio(modelMinimaxH3, "768P"); !ok || r != 1.0 {
		t.Fatalf("h3 768P ratio = %v,%v; want 1.0,true", r, ok)
	}
	if r, ok := getResolutionBillingRatio(modelMinimaxH3, "2k"); !ok || r != 1.625 {
		t.Fatalf("h3 2K ratio = %v,%v; want 1.625,true", r, ok)
	}
	if r, ok := getResolutionBillingRatio(modelMinimaxH3Max, "480p"); !ok || r != 0.625 {
		t.Fatalf("h3-max 480P ratio = %v,%v; want 0.625,true", r, ok)
	}
	if r, ok := getResolutionBillingRatio(modelMinimaxH3Max, "768P"); !ok || r != 1.0 {
		t.Fatalf("h3-max 768P ratio = %v,%v; want 1.0,true", r, ok)
	}
	// h3 的 480P 上游 pricing 缺条目（已向上游提问），未登记前不得给倍率
	if _, ok := getResolutionBillingRatio(modelMinimaxH3, "480P"); ok {
		t.Fatalf("h3 480P 上游未给价，不得登记倍率（否则会猜错价）")
	}
	// h3-max 不支持 2K，也不应有倍率
	if _, ok := getResolutionBillingRatio(modelMinimaxH3Max, "2K"); ok {
		t.Fatalf("h3-max 2K must not yield a ratio")
	}
	if _, ok := getResolutionBillingRatio(modelMinimaxH3, "1K"); ok {
		t.Fatalf("undocumented resolution must not yield a ratio")
	}
	if _, ok := getResolutionBillingRatio("gpt-4o", "768P"); ok {
		t.Fatalf("unknown model must not yield a ratio")
	}
}

// h3-max 与 h3 的素材能力完全一致：图片/视频/音频/首尾帧均接受。
// 2026-09-12 实测：h3-max 传 10 张图时上游回的是「at most 9 reference images allowed」
// 而不是「不支持图片输入」，早先「pricing 无 image_input_count ⇒ 不接受图片」的推定已被证伪。
func TestBuildContentH3MaxAcceptsAllMedia(t *testing.T) {
	name := modelMinimaxH3Max

	// 便捷字段 images[]
	items, hasVisual, taskErr := buildContentItems(&relaycommon.TaskSubmitReq{
		Prompt: "x", Images: []string{"https://e.com/1.jpg"},
	}, name)
	if taskErr != nil {
		t.Fatalf("h3-max 必须接受参考图: %v", taskErr.Message)
	}
	if !hasVisual || items[1].Role != roleReferenceImage {
		t.Fatalf("unexpected: %+v hasVisual=%v", items, hasVisual)
	}

	// input_reference 同样要过
	if _, _, taskErr = buildContentItems(&relaycommon.TaskSubmitReq{
		Prompt: "x", InputReference: "https://e.com/1.jpg",
	}, name); taskErr != nil {
		t.Fatalf("h3-max 必须接受 input_reference: %v", taskErr.Message)
	}

	// 顶层 content[] 三类素材
	items, hasVisual, taskErr = buildContentItems(&relaycommon.TaskSubmitReq{
		Content: []map[string]interface{}{
			{"type": "text", "text": "x"},
			{"type": "image_url", "image_url": map[string]interface{}{"url": "https://e.com/1.jpg"}, "role": "reference_image"},
			{"type": "video_url", "video_url": map[string]interface{}{"url": "https://e.com/v.mp4"}, "role": "reference_video"},
			{"type": "audio_url", "audio_url": map[string]interface{}{"url": "https://e.com/a.mp3"}, "role": "reference_audio"},
		},
	}, name)
	if taskErr != nil {
		t.Fatalf("h3-max 必须接受图/视频/音频混合参考: %v", taskErr.Message)
	}
	if !hasVisual || len(items) != 4 {
		t.Fatalf("unexpected items: %+v hasVisual=%v", items, hasVisual)
	}

	// 纯文本仍须能过（h3-max 的主用法）
	items, hasVisual, taskErr = buildContentItems(&relaycommon.TaskSubmitReq{Prompt: "a cat"}, name)
	if taskErr != nil {
		t.Fatalf("text-only must pass on h3-max: %v", taskErr.Message)
	}
	if hasVisual || len(items) != 1 {
		t.Fatalf("unexpected items: %+v hasVisual=%v", items, hasVisual)
	}
}

// h3 仍须正常接受参考图
func TestBuildContentH3StillAcceptsImage(t *testing.T) {
	_, hasVisual, taskErr := buildContentItems(&relaycommon.TaskSubmitReq{
		Prompt: "x", Images: []string{"https://e.com/1.jpg"},
	}, modelMinimaxH3)
	if taskErr != nil {
		t.Fatalf("h3 must accept reference image: %v", taskErr.Message)
	}
	if !hasVisual {
		t.Fatalf("h3 reference image must set hasVisual")
	}
}

func TestResolveDuration(t *testing.T) {
	// 单源：seconds
	sec, has, err := resolveDuration(&relaycommon.TaskSubmitReq{Seconds: "8"})
	if err != nil || !has || sec != 8 {
		t.Fatalf("seconds 应生效: %d,%v,%v", sec, has, err)
	}
	// 空白 seconds 视为未指定，不得当成非法值报错
	if _, has, err = resolveDuration(&relaycommon.TaskSubmitReq{Seconds: "   "}); err != nil || has {
		t.Fatalf("空白 seconds 应视为未指定, got has=%v err=%v", has, err)
	}
	// 回退 duration
	sec, has, err = resolveDuration(&relaycommon.TaskSubmitReq{Duration: 12})
	if err != nil || !has || sec != 12 {
		t.Fatalf("duration fallback broken: %d,%v,%v", sec, has, err)
	}
	// 再回退 metadata.duration（resolution/ratio 必须放 metadata，客户会照同样写法传 duration，
	// 以前会被静默忽略并退回默认 5 秒）
	sec, has, err = resolveDuration(&relaycommon.TaskSubmitReq{
		Metadata: map[string]interface{}{"duration": 4},
	})
	if err != nil || !has || sec != 4 {
		t.Fatalf("metadata.duration（int）应生效: %d,%v,%v", sec, has, err)
	}
	// JSON 反序列化后数字是 float64，字符串形态也要认
	sec, _, _ = resolveDuration(&relaycommon.TaskSubmitReq{Metadata: map[string]interface{}{"duration": float64(7)}})
	if sec != 7 {
		t.Fatalf("metadata.duration（float64）= %d, want 7", sec)
	}
	sec, _, _ = resolveDuration(&relaycommon.TaskSubmitReq{Metadata: map[string]interface{}{"duration": "9"}})
	if sec != 9 {
		t.Fatalf("metadata.duration（字符串）= %d, want 9", sec)
	}
	// 多源取值一致 → 不算冲突，照常放行
	sec, has, err = resolveDuration(&relaycommon.TaskSubmitReq{
		Seconds:  "6",
		Duration: 6,
		Metadata: map[string]interface{}{"duration": 6},
	})
	if err != nil || !has || sec != 6 {
		t.Fatalf("取值一致时不应算冲突: %d,%v,%v", sec, has, err)
	}
	// 多源取值冲突 → 必须报错，不静默按优先级取一
	// （口径与 resolution/ratio 的「写错位置就 400」保持一致）
	for _, tc := range []struct {
		name string
		req  relaycommon.TaskSubmitReq
	}{
		{"seconds vs duration", relaycommon.TaskSubmitReq{Seconds: "8", Duration: 3}},
		{"duration vs metadata", relaycommon.TaskSubmitReq{Duration: 6, Metadata: map[string]interface{}{"duration": 4}}},
		{"seconds vs metadata", relaycommon.TaskSubmitReq{Seconds: "8", Metadata: map[string]interface{}{"duration": 4}}},
	} {
		_, _, err := resolveDuration(&tc.req)
		if err == nil || !strings.Contains(err.Error(), "conflicting duration values") {
			t.Fatalf("%s 应报冲突, got %v", tc.name, err)
		}
	}
	// 冲突报错必须列出各来源的取值，客户才能自查
	_, _, err = resolveDuration(&relaycommon.TaskSubmitReq{Seconds: "8", Duration: 3})
	if !strings.Contains(err.Error(), "seconds=8") || !strings.Contains(err.Error(), "duration=3") {
		t.Fatalf("冲突报错应列出各来源取值: %v", err)
	}
	// metadata.duration 非法时必须报错，不能静默退回默认值
	if _, _, err = resolveDuration(&relaycommon.TaskSubmitReq{
		Metadata: map[string]interface{}{"duration": "abc"},
	}); err == nil {
		t.Fatalf("metadata.duration 非法时应报错")
	}
	// 都未指定
	if _, has, err = resolveDuration(&relaycommon.TaskSubmitReq{}); err != nil || has {
		t.Fatalf("want no value, got has=%v err=%v", has, err)
	}
	// 非法 seconds
	if _, _, err = resolveDuration(&relaycommon.TaskSubmitReq{Seconds: "abc"}); err == nil {
		t.Fatalf("want parse error for non-integer seconds")
	}
}

func TestGetMetaString(t *testing.T) {
	m := map[string]interface{}{
		"ratio":      "16:9",
		"duration":   5,
		"empty":      "",
		"blank":      "   ",
		"nilv":       nil,
		"resolution": "2K",
	}
	if v, ok := getMetaString(m, "ratio"); !ok || v != "16:9" {
		t.Fatalf("ratio = %q,%v", v, ok)
	}
	// 数字被写成 int 时也要能取出
	if v, ok := getMetaString(m, "duration"); !ok || v != "5" {
		t.Fatalf("duration = %q,%v", v, ok)
	}
	for _, k := range []string{"empty", "blank", "nilv", "missing"} {
		if v, ok := getMetaString(m, k); ok || v != "" {
			t.Fatalf("key %q should be absent, got %q,%v", k, v, ok)
		}
	}
	if metaString(m, "resolution") != "2K" {
		t.Fatalf("metaString broken")
	}
	if metaString(nil, "resolution") != "" {
		t.Fatalf("metaString(nil) must be empty")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abc", 5); got != "abc" {
		t.Fatalf("truncate short = %q", got)
	}
	if got := truncate("abcdef", 3); got != "abc..." {
		t.Fatalf("truncate long = %q", got)
	}
}
