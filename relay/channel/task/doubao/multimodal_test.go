package doubao

import (
	"reflect"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestNormalizeAssetURL(t *testing.T) {
	cases := map[string]string{
		"asset://abc":         "asset://abc",
		"Asset://abc":         "asset://abc",
		"ASSET://abc":         "asset://abc",
		"https://x.com/a.mp4": "https://x.com/a.mp4",
		"":                    "",
	}
	for in, want := range cases {
		if got := normalizeAssetURL(in); got != want {
			t.Errorf("normalizeAssetURL(%q)=%q want %q", in, got, want)
		}
	}
}

func TestValidateContentItems(t *testing.T) {
	okItems := []map[string]interface{}{
		{"type": "text", "text": "hi"},
		{"type": "image_url", "role": "first_frame", "image_url": map[string]interface{}{"url": "https://a.png"}},
		{"type": "video_url", "role": "reference_video", "video_url": map[string]interface{}{"url": "asset://v1"}},
	}
	if err := validateContentItems(okItems, "doubao-seedance-2-0"); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	// 非法 type
	if err := validateContentItems([]map[string]interface{}{{"type": "foo"}}, "doubao-seedance-2-0"); err == nil {
		t.Error("expected error for bad type")
	}
	// role 与 type 不匹配
	if err := validateContentItems([]map[string]interface{}{{"type": "video_url", "role": "first_frame"}}, "doubao-seedance-2-0"); err == nil {
		t.Error("expected error for role/type mismatch")
	}
	// mini + audio 拒绝
	if err := validateContentItems([]map[string]interface{}{{"type": "audio_url", "role": "reference_audio"}}, "dreamina-seedance-2-0-mini"); err == nil {
		t.Error("expected error for audio on mini model")
	}
	// 非 mini + audio 允许
	if err := validateContentItems([]map[string]interface{}{{"type": "audio_url"}}, "doubao-seedance-2-0"); err != nil {
		t.Errorf("audio on non-mini should pass, got %v", err)
	}
}

// content[] 透传：原样组装、asset:// 规范化、不叠加 images[]/顶层 prompt
func TestConvertToRequestPayload_TopLevelContentPassthrough(t *testing.T) {
	a := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "doubao-seedance-2-0",
		Prompt: "顶层prompt在content模式下应被忽略",
		Images: []string{"https://ignored.png"},
		Content: []map[string]interface{}{
			{"type": "text", "text": "编辑镜头"},
			{"type": "video_url", "role": "reference_video", "video_url": map[string]interface{}{"url": "Asset://v-1"}},
		},
	}
	r, err := a.convertToRequestPayload(&req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(r.Content) != 2 {
		t.Fatalf("expected 2 passthrough items (no images/prompt append), got %d", len(r.Content))
	}
	var videoURL, textVal string
	for _, c := range r.Content {
		if c.Type == "video_url" && c.VideoURL != nil {
			videoURL = c.VideoURL.URL
		}
		if c.Type == "text" {
			textVal = c.Text
		}
	}
	if videoURL != "asset://v-1" {
		t.Errorf("asset url not normalized: %q", videoURL)
	}
	if textVal != "编辑镜头" {
		t.Errorf("text should come from content, got %q", textVal)
	}
}

// videos[] 便捷字段：去重 + 规范化 + reference_video role + prompt 仍作为唯一 text
func TestConvertToRequestPayload_VideosConvenience(t *testing.T) {
	a := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "doubao-seedance-2-0",
		Prompt: "向后延展",
		Videos: []string{"https://a.mp4", "https://a.mp4", "Asset://b"},
	}
	r, err := a.convertToRequestPayload(&req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var vids []string
	var textCount int
	for _, c := range r.Content {
		switch c.Type {
		case "video_url":
			if c.Role != "reference_video" {
				t.Errorf("video role=%q want reference_video", c.Role)
			}
			vids = append(vids, c.VideoURL.URL)
		case "text":
			textCount++
		}
	}
	if want := []string{"https://a.mp4", "asset://b"}; !reflect.DeepEqual(vids, want) {
		t.Errorf("videos dedup/normalize: got %v want %v", vids, want)
	}
	if textCount != 1 {
		t.Errorf("expected 1 text(prompt) in convenience path, got %d", textCount)
	}
}

func TestHasVideoInput(t *testing.T) {
	cases := []struct {
		name string
		req  relaycommon.TaskSubmitReq
		want bool
	}{
		{"videos[]", relaycommon.TaskSubmitReq{Videos: []string{"a.mp4"}}, true},
		{"content video_url", relaycommon.TaskSubmitReq{Content: []map[string]interface{}{{"type": "video_url"}}}, true},
		{"metadata.content video", relaycommon.TaskSubmitReq{Metadata: map[string]interface{}{"content": []interface{}{map[string]interface{}{"type": "video_url"}}}}, true},
		{"no video", relaycommon.TaskSubmitReq{Prompt: "x", Images: []string{"a.png"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasVideoInput(&tc.req); got != tc.want {
				t.Errorf("hasVideoInput=%v want %v", got, tc.want)
			}
		})
	}
}
