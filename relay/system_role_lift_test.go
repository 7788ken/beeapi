package relay

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

func strPtr(s string) *string { return &s }

func TestLiftSystemRoleMessagesNoopWhenAbsent(t *testing.T) {
	request := &dto.ClaudeRequest{
		Model:  "claude-opus-5",
		System: "existing system",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
			{Role: "user", Content: "bye"},
		},
	}
	liftSystemRoleMessages(request)
	if request.System != "existing system" {
		t.Fatalf("system should be untouched, got %v", request.System)
	}
	if len(request.Messages) != 3 || request.Messages[0].Role != "user" {
		t.Fatalf("messages should be untouched, got %+v", request.Messages)
	}
}

func TestLiftSystemRoleMessagesFromStringContent(t *testing.T) {
	request := &dto.ClaudeRequest{
		Model: "claude-opus-5",
		Messages: []dto.ClaudeMessage{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "hi"},
		},
	}
	liftSystemRoleMessages(request)
	if len(request.Messages) != 1 || request.Messages[0].Role != "user" {
		t.Fatalf("system message should be removed, got %+v", request.Messages)
	}
	systems := request.ParseSystem()
	if len(systems) != 1 || systems[0].Type != "text" || systems[0].GetText() != "You are a helpful assistant." {
		t.Fatalf("unexpected lifted system: %+v", request.System)
	}
}

func TestLiftSystemRoleMessagesMultipleAndMergedWithTopLevel(t *testing.T) {
	request := &dto.ClaudeRequest{
		Model:  "claude-opus-5",
		System: "top level system",
		Messages: []dto.ClaudeMessage{
			{Role: "system", Content: "first system"},
			{Role: "user", Content: "hi"},
			{Role: "system", Content: []any{
				map[string]any{"type": "text", "text": "second system"},
			}},
		},
	}
	liftSystemRoleMessages(request)
	if len(request.Messages) != 1 || request.Messages[0].Role != "user" {
		t.Fatalf("system messages should be removed, got %+v", request.Messages)
	}
	systems := request.ParseSystem()
	if len(systems) != 3 {
		t.Fatalf("expected 3 system blocks (top-level first), got %d: %+v", len(systems), request.System)
	}
	if systems[0].GetText() != "top level system" {
		t.Fatalf("top-level system must come first, got %+v", systems[0])
	}
	if systems[1].GetText() != "first system" || systems[2].GetText() != "second system" {
		t.Fatalf("lifted systems out of order: %+v", systems)
	}
}

func TestLiftSystemRoleMessagesAllSystemAddsUserPlaceholder(t *testing.T) {
	request := &dto.ClaudeRequest{
		Model: "claude-opus-5",
		Messages: []dto.ClaudeMessage{
			{Role: "system", Content: "only system"},
		},
	}
	liftSystemRoleMessages(request)
	if len(request.Messages) != 1 || request.Messages[0].Role != "user" {
		t.Fatalf("expected user placeholder, got %+v", request.Messages)
	}
	if request.Messages[0].Content != "..." {
		t.Fatalf("expected placeholder content, got %v", request.Messages[0].Content)
	}
}

func TestLiftSystemRoleMessagesPreservesCacheControl(t *testing.T) {
	request := &dto.ClaudeRequest{
		Model: "claude-opus-5",
		Messages: []dto.ClaudeMessage{
			{Role: "system", Content: []any{
				map[string]any{
					"type":          "text",
					"text":          "cached system",
					"cache_control": map[string]any{"type": "ephemeral"},
				},
			}},
			{Role: "user", Content: "hi"},
		},
	}
	liftSystemRoleMessages(request)
	systems := request.ParseSystem()
	if len(systems) != 1 {
		t.Fatalf("expected 1 system block, got %+v", request.System)
	}
	if systems[0].CacheControl == nil || string(systems[0].CacheControl) == "" {
		t.Fatalf("cache_control should be preserved, got %+v", systems[0])
	}
}

func TestLiftSystemRoleMessagesSkipsEmptySystem(t *testing.T) {
	request := &dto.ClaudeRequest{
		Model: "claude-opus-5",
		Messages: []dto.ClaudeMessage{
			{Role: "system", Content: ""},
			{Role: "user", Content: "hi"},
		},
	}
	liftSystemRoleMessages(request)
	if len(request.Messages) != 1 || request.Messages[0].Role != "user" {
		t.Fatalf("empty system should still be removed from messages, got %+v", request.Messages)
	}
	if request.System != nil {
		t.Fatalf("empty system should not create system field, got %v", request.System)
	}
}

func TestLiftSystemRoleMessagesNilContent(t *testing.T) {
	request := &dto.ClaudeRequest{
		Model: "claude-opus-5",
		Messages: []dto.ClaudeMessage{
			{Role: "system", Content: nil},
			{Role: "user", Content: "hi"},
		},
	}
	liftSystemRoleMessages(request)
	if len(request.Messages) != 1 || request.Messages[0].Role != "user" {
		t.Fatalf("nil-content system should be removed from messages, got %+v", request.Messages)
	}
	if request.System != nil {
		t.Fatalf("nil-content system should not create system field, got %v", request.System)
	}
}

func TestLiftSystemRoleMessagesSkipsNonTextBlocks(t *testing.T) {
	request := &dto.ClaudeRequest{
		Model: "claude-opus-5",
		Messages: []dto.ClaudeMessage{
			{Role: "system", Content: []any{
				map[string]any{"type": "image", "source": map[string]any{"type": "base64"}},
				map[string]any{"type": "text", "text": "real system"},
			}},
			{Role: "user", Content: "hi"},
		},
	}
	liftSystemRoleMessages(request)
	systems := request.ParseSystem()
	if len(systems) != 1 || systems[0].GetText() != "real system" {
		t.Fatalf("non-text blocks should be dropped, got %+v", request.System)
	}
}

// TestLiftAfterSystemPromptInjection pins the ClaudeHelper ordering: channel
// SystemPrompt injection runs first, then the lift appends after it.
func TestLiftAfterSystemPromptInjection(t *testing.T) {
	// 模拟渠道配置了 SystemPrompt 且未开 override、请求顶层无 system 的场景：
	// handler 先 SetStringSystem(渠道提示词)，lift 随后把 messages 里的 system 追加在后
	request := &dto.ClaudeRequest{
		Model: "claude-opus-5",
		Messages: []dto.ClaudeMessage{
			{Role: "system", Content: "client system"},
			{Role: "user", Content: "hi"},
		},
	}
	channelPrompt := "channel prompt"
	if request.System == nil {
		request.SetStringSystem(channelPrompt)
	}
	liftSystemRoleMessages(request)
	systems := request.ParseSystem()
	if len(systems) != 2 {
		t.Fatalf("expected channel prompt + lifted system, got %+v", request.System)
	}
	if systems[0].GetText() != channelPrompt {
		t.Fatalf("channel prompt must survive and come first, got %+v", systems[0])
	}
	if systems[1].GetText() != "client system" {
		t.Fatalf("lifted system must come after channel prompt, got %+v", systems[1])
	}
}

func TestHasSystemRoleMessage(t *testing.T) {
	cases := []struct {
		messages []dto.ClaudeMessage
		want     bool
	}{
		{[]dto.ClaudeMessage{{Role: "user", Content: "hi"}}, false},
		{[]dto.ClaudeMessage{{Role: "system", Content: "x"}}, true},
		{[]dto.ClaudeMessage{{Role: "user", Content: "hi"}, {Role: "system", Content: "x"}}, true},
		{nil, false},
	}
	for _, tc := range cases {
		if got := hasSystemRoleMessage(tc.messages); got != tc.want {
			t.Fatalf("hasSystemRoleMessage(%+v) = %v, want %v", tc.messages, got, tc.want)
		}
	}
}

func TestLiftSystemRoleMessagesStructuralNoop(t *testing.T) {
	// 回归护栏：无 system 角色时请求结构 deep-equal 不变
	request := &dto.ClaudeRequest{
		Model:  "claude-opus-5",
		System: []dto.ClaudeMediaMessage{{Type: "text", Text: strPtr("sys")}},
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: []any{map[string]any{"type": "text", "text": "hello"}}},
		},
		MaxTokens: common.GetPointer[uint](100),
	}
	marshaledBefore, err := common.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	liftSystemRoleMessages(request)
	marshaledAfter, err := common.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if string(marshaledBefore) != string(marshaledAfter) {
		t.Fatalf("request serialization must be byte-identical when no system role present\nbefore: %s\nafter:  %s", marshaledBefore, marshaledAfter)
	}
}

func TestLiftSystemRoleMessagesJSONRoundTrip(t *testing.T) {
	// 模拟 OpenAI 风格客户端打到 /v1/messages 的真实 payload
	raw := `{
	  "model": "claude-opus-5",
	  "max_tokens": 1024,
	  "stream": true,
	  "messages": [
	    {"role": "system", "content": "You are a coding assistant."},
	    {"role": "user", "content": "hi"},
	    {"role": "assistant", "content": "hello"},
	    {"role": "user", "content": "what is 1+1"}
	  ]
	}`
	var request dto.ClaudeRequest
	if err := common.Unmarshal([]byte(raw), &request); err != nil {
		t.Fatal(err)
	}
	liftSystemRoleMessages(&request)
	out, err := common.Marshal(&request)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, `"role":"system"`) {
		t.Fatalf("output still contains system role: %s", s)
	}
	if !strings.Contains(s, `"system":[{"type":"text","text":"You are a coding assistant."}`) {
		t.Fatalf("system not lifted to top level: %s", s)
	}
	if !strings.Contains(s, `"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"},{"role":"user","content":"what is 1+1"}]`) {
		t.Fatalf("messages mangled: %s", s)
	}
}
