package common

import (
	"testing"
)

func TestApplyClaudeThinkingAdaptiveCompatDisabled(t *testing.T) {
	input := `{"model":"claude-opus-4-7","thinking":{"type":"enabled","budget_tokens":4096}}`
	out, err := ApplyClaudeThinkingAdaptiveCompat([]byte(input), false)
	if err != nil {
		t.Fatalf("ApplyClaudeThinkingAdaptiveCompat returned error: %v", err)
	}
	assertJSONEqual(t, input, string(out))
}

func TestApplyClaudeThinkingAdaptiveCompatNoLegacyThinking(t *testing.T) {
	input := `{"model":"claude-opus-4-7","thinking":{"type":"adaptive"},"temperature":0.7}`
	out, err := ApplyClaudeThinkingAdaptiveCompat([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeThinkingAdaptiveCompat returned error: %v", err)
	}
	assertJSONEqual(t, input, string(out))
}

func TestApplyClaudeThinkingAdaptiveCompatRewriteAndEffortMapping(t *testing.T) {
	input := `{"model":"claude-sonnet-4-6","thinking":{"type":"enabled","budget_tokens":1024},"max_tokens":4096}`
	out, err := ApplyClaudeThinkingAdaptiveCompat([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeThinkingAdaptiveCompat returned error: %v", err)
	}
	assertJSONEqual(t, `{"model":"claude-sonnet-4-6","thinking":{"type":"adaptive"},"output_config":{"effort":"low"},"max_tokens":4096}`, string(out))
}

func TestApplyClaudeThinkingAdaptiveCompatKeepsExplicitEffort(t *testing.T) {
	input := `{"model":"claude-opus-4-8","thinking":{"type":"enabled","budget_tokens":2048},"output_config":{"effort":"medium"},"temperature":0.7,"top_p":0.9,"top_k":10}`
	out, err := ApplyClaudeThinkingAdaptiveCompat([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeThinkingAdaptiveCompat returned error: %v", err)
	}
	// opus-4-8 命中 adaptive-only 前缀：采样参数被清理、补 display=summarized，显式 effort 保留
	assertJSONEqual(t, `{"model":"claude-opus-4-8","thinking":{"type":"adaptive","display":"summarized"},"output_config":{"effort":"medium"}}`, string(out))
}

func TestApplyClaudeThinkingAdaptiveCompatHighBudgetOnAdaptiveOnlyModel(t *testing.T) {
	input := `{"model":"claude-opus-4-7","thinking":{"type":"enabled","budget_tokens":32768},"temperature":1.0}`
	out, err := ApplyClaudeThinkingAdaptiveCompat([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeThinkingAdaptiveCompat returned error: %v", err)
	}
	assertJSONEqual(t, `{"model":"claude-opus-4-7","thinking":{"type":"adaptive","display":"summarized"},"output_config":{"effort":"high"}}`, string(out))
}

func TestApplyClaudeThinkingAdaptiveCompatKeepsExplicitDisplay(t *testing.T) {
	input := `{"model":"claude-opus-4-7","thinking":{"type":"enabled","budget_tokens":4096,"display":"omitted"}}`
	out, err := ApplyClaudeThinkingAdaptiveCompat([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeThinkingAdaptiveCompat returned error: %v", err)
	}
	// 客户端显式指定 display 时不覆盖
	assertJSONEqual(t, `{"model":"claude-opus-4-7","thinking":{"type":"adaptive","display":"omitted"},"output_config":{"effort":"high"}}`, string(out))
}

// === ApplyClaudeThinkingBlocksStrip 测试 ===

func TestApplyClaudeThinkingBlocksStripDisabled(t *testing.T) {
	input := `{"model":"claude-sonnet-4-6","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"Let me analyze...","signature":"abc123"}]}]}`
	out, err := ApplyClaudeThinkingBlocksStrip([]byte(input), false)
	if err != nil {
		t.Fatalf("ApplyClaudeThinkingBlocksStrip returned error: %v", err)
	}
	assertJSONEqual(t, input, string(out))
}

func TestApplyClaudeThinkingBlocksStripNoMessages(t *testing.T) {
	input := `{"model":"claude-sonnet-4-6","max_tokens":1024}`
	out, err := ApplyClaudeThinkingBlocksStrip([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeThinkingBlocksStrip returned error: %v", err)
	}
	assertJSONEqual(t, input, string(out))
}

func TestApplyClaudeThinkingBlocksStripNoThinkingBlocks(t *testing.T) {
	input := `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"Hello"}]}]}`
	out, err := ApplyClaudeThinkingBlocksStrip([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeThinkingBlocksStrip returned error: %v", err)
	}
	assertJSONEqual(t, input, string(out))
}

func TestApplyClaudeThinkingBlocksStripRemovesSignature(t *testing.T) {
	input := `{"model":"claude-sonnet-4-6","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"Let me analyze...","signature":"abc123"},{"type":"text","text":"Here is my answer"}]}]}`
	out, err := ApplyClaudeThinkingBlocksStrip([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeThinkingBlocksStrip returned error: %v", err)
	}
	// 整块剥离 thinking（仅删 signature 会触发 Field required）
	assertJSONEqual(t, `{"model":"claude-sonnet-4-6","messages":[{"role":"assistant","content":[{"type":"text","text":"Here is my answer"}]}]}`, string(out))
}

func TestApplyClaudeThinkingBlocksStripRemovesRedactedThinkingSignature(t *testing.T) {
	input := `{"model":"claude-sonnet-4-6","messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"encrypted...","signature":"xyz789"}]}]}`
	out, err := ApplyClaudeThinkingBlocksStrip([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeThinkingBlocksStrip returned error: %v", err)
	}
	// redacted_thinking 整块剥离，空 content 补空格 text 块
	assertJSONEqual(t, `{"model":"claude-sonnet-4-6","messages":[{"role":"assistant","content":[{"type":"text","text":" "}]}]}`, string(out))
}

func TestApplyClaudeThinkingBlocksStripMultipleMessages(t *testing.T) {
	input := `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"Question"}]},{"role":"assistant","content":[{"type":"thinking","thinking":"Thinking...","signature":"sig1"},{"type":"text","text":"Answer"}]},{"role":"user","content":[{"type":"text","text":"Follow-up"}]},{"role":"assistant","content":[{"type":"thinking","thinking":"More thinking...","signature":"sig2"},{"type":"text","text":"Final answer"}]}]}`
	out, err := ApplyClaudeThinkingBlocksStrip([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeThinkingBlocksStrip returned error: %v", err)
	}
	// 所有 assistant 轮的 thinking block 都整块剥离
	assertJSONEqual(t, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"Question"}]},{"role":"assistant","content":[{"type":"text","text":"Answer"}]},{"role":"user","content":[{"type":"text","text":"Follow-up"}]},{"role":"assistant","content":[{"type":"text","text":"Final answer"}]}]}`, string(out))
}

func TestApplyClaudeThinkingBlocksStripStringContent(t *testing.T) {
	// content 是字符串而非数组时不应报错
	input := `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"Hello"}]}`
	out, err := ApplyClaudeThinkingBlocksStrip([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeThinkingBlocksStrip returned error: %v", err)
	}
	assertJSONEqual(t, input, string(out))
}

func TestApplyClaudeThinkingBlocksStripNoSignatureField(t *testing.T) {
	// thinking block 没有 signature 字段时同样整块剥离（上游对块本身即不接受）
	input := `{"model":"claude-sonnet-4-6","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"Let me analyze..."}]}]}`
	out, err := ApplyClaudeThinkingBlocksStrip([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeThinkingBlocksStrip returned error: %v", err)
	}
	assertJSONEqual(t, `{"model":"claude-sonnet-4-6","messages":[{"role":"assistant","content":[{"type":"text","text":" "}]}]}`, string(out))
}
