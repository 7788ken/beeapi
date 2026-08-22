package doubao

import (
	"encoding/json"
	"reflect"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// TestConvertToRequestPayload_DurationFallback 覆盖 L1：
// 顶层 seconds(string) > 顶层 duration(int) > metadata.duration。
func TestConvertToRequestPayload_DurationFallback(t *testing.T) {
	a := &TaskAdaptor{}
	cases := []struct {
		name     string
		req      relaycommon.TaskSubmitReq
		wantSecs int
	}{
		{
			name:     "seconds wins over duration and metadata",
			req:      relaycommon.TaskSubmitReq{Model: "m", Prompt: "p", Seconds: "12", Duration: 8, Metadata: map[string]interface{}{"duration": 5}},
			wantSecs: 12,
		},
		{
			name:     "top-level duration falls back when seconds empty",
			req:      relaycommon.TaskSubmitReq{Model: "m", Prompt: "p", Duration: 15, Metadata: map[string]interface{}{"duration": 5}},
			wantSecs: 15,
		},
		{
			name:     "metadata duration used when both top-level empty",
			req:      relaycommon.TaskSubmitReq{Model: "m", Prompt: "p", Metadata: map[string]interface{}{"duration": 9}},
			wantSecs: 9,
		},
		{
			name:     "no duration anywhere stays nil",
			req:      relaycommon.TaskSubmitReq{Model: "m", Prompt: "p"},
			wantSecs: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.convertToRequestPayload(&tc.req)
			if err != nil {
				t.Fatalf("convertToRequestPayload err: %v", err)
			}
			if tc.wantSecs == 0 {
				if got.Duration != nil {
					t.Fatalf("expected nil duration, got %v", got.Duration)
				}
				return
			}
			if got.Duration == nil {
				t.Fatalf("expected duration=%d, got nil", tc.wantSecs)
			}
			if int(*got.Duration) != tc.wantSecs {
				t.Fatalf("expected duration=%d, got %d", tc.wantSecs, *got.Duration)
			}
		})
	}
}

func TestConvertToRequestPayloadPreservesExplicitZeroPriority(t *testing.T) {
	a := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "doubao-seedance-2-0",
		Prompt: "animate",
		Metadata: map[string]interface{}{
			"safety_identifier": "user-123",
			"priority":          float64(0),
			"resolution":        "4k",
		},
	}
	payload, err := a.convertToRequestPayload(&req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["priority"] != float64(0) {
		t.Fatalf("priority 0 was not preserved: %s", body)
	}
	if decoded["safety_identifier"] != "user-123" {
		t.Fatalf("safety_identifier missing: %s", body)
	}
	if decoded["resolution"] != "4k" {
		t.Fatalf("resolution missing: %s", body)
	}
}

// TestConvertToRequestPayload_ImageRoleAndOrder 覆盖 L3/L5/L8：
// - images 顺序保留
// - input_reference 兜底追加在末尾
// - 重复 URL 去重
// - 每张图都打 role: "reference_image"
func TestConvertToRequestPayload_ImageRoleAndOrder(t *testing.T) {
	a := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:          "m",
		Prompt:         "p",
		Images:         []string{"https://a.png", "https://b.png", "https://a.png"},
		InputReference: "https://c.png",
	}
	r, err := a.convertToRequestPayload(&req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	var gotImgs []string
	for _, c := range r.Content {
		if c.Type != "image_url" {
			continue
		}
		if c.Role != "reference_image" {
			t.Errorf("image %s missing role=reference_image, got role=%q", c.ImageURL.URL, c.Role)
		}
		gotImgs = append(gotImgs, c.ImageURL.URL)
	}
	want := []string{"https://a.png", "https://b.png", "https://c.png"}
	if !reflect.DeepEqual(gotImgs, want) {
		t.Fatalf("image order/dedup wrong: want %v, got %v", want, gotImgs)
	}
}

// TestConvertToRequestPayload_InputReferenceOnly 覆盖 L3：
// 客户只给 input_reference 不给 images[]，也应该被采纳为参考图。
func TestConvertToRequestPayload_InputReferenceOnly(t *testing.T) {
	a := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:          "m",
		Prompt:         "p",
		InputReference: "https://only.png",
	}
	r, err := a.convertToRequestPayload(&req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var imgCount int
	for _, c := range r.Content {
		if c.Type == "image_url" {
			imgCount++
			if c.ImageURL.URL != "https://only.png" || c.Role != "reference_image" {
				t.Errorf("got url=%q role=%q", c.ImageURL.URL, c.Role)
			}
		}
	}
	if imgCount != 1 {
		t.Fatalf("expected 1 image, got %d", imgCount)
	}
}

// TestConvertToRequestPayload_BoolDurationRejected_AtJSONLayer：
// boolean duration 在 TaskSubmitReq.UnmarshalJSON 阶段会被 string/int 两次尝试都失败，
// 最终 req.Duration 保持 0。这里只验证 convertToRequestPayload 在 req.Duration=0 时不会
// 误把 0 当作有效值发给上游。顶层 boolean 的真正拦截发生在 validateNoForbiddenTopLevel。
func TestConvertToRequestPayload_ZeroDurationStaysNil(t *testing.T) {
	a := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{Model: "m", Prompt: "p", Duration: 0, Seconds: ""}
	r, err := a.convertToRequestPayload(&req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.Duration != nil {
		t.Fatalf("expected Duration nil for zero input, got %v", r.Duration)
	}
}

// TestFormatDoubaoFailReason 覆盖 L7：合并 code + message。
func TestFormatDoubaoFailReason(t *testing.T) {
	mk := func(code, msg string) responseTask {
		var t responseTask
		t.Error.Code = code
		t.Error.Message = msg
		return t
	}
	cases := []struct {
		in   responseTask
		want string
	}{
		{mk("content_policy", "sensitive"), "code=content_policy: sensitive"},
		{mk("", "sensitive"), "sensitive"},
		{mk("content_policy", ""), "code=content_policy"},
		{mk("", ""), "upstream returned failed status without details"},
		{mk("  ", "  hello  "), "hello"},
	}
	for _, tc := range cases {
		if got := formatDoubaoFailReason(tc.in); got != tc.want {
			t.Errorf("formatDoubaoFailReason(%+v) = %q, want %q", tc.in.Error, got, tc.want)
		}
	}
}
