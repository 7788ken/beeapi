package common

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
)

func TestApplyClaudeServerToolsStripDeclsAndPairedBlocks(t *testing.T) {
	input := `{
		"model":"claude-opus-4-8",
		"tools":[
			{"type":"web_search_20260209","name":"web_search"},
			{"type":"code_execution_20250522","name":"code_execution"},
			{"name":"my_tool","description":"d","input_schema":{"type":"object"}}
		],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hi"}]},
			{"role":"assistant","content":[
				{"type":"server_tool_use","id":"srvu_1","name":"web_search","input":{}},
				{"type":"text","text":"searching"}
			]},
			{"role":"user","content":[
				{"type":"web_search_tool_result","tool_use_id":"srvu_1","content":[]},
				{"type":"text","text":"result noted"}
			]}
		]
	}`
	out, err := ApplyClaudeServerToolsStrip([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeServerToolsStrip returned error: %v", err)
	}
	assertJSONEqual(t, `{
		"model":"claude-opus-4-8",
		"tools":[
			{"name":"my_tool","description":"d","input_schema":{"type":"object"}}
		],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hi"}]},
			{"role":"assistant","content":[
				{"type":"text","text":"searching"}
			]},
			{"role":"user","content":[
				{"type":"text","text":"result noted"}
			]}
		]
	}`, string(out))
}

func TestApplyClaudeServerToolsStripEmptyContentPlaceholder(t *testing.T) {
	input := `{
		"model":"claude-opus-4-8",
		"tools":[{"type":"web_search_20260209","name":"web_search"}],
		"tool_choice":{"type":"auto"},
		"messages":[
			{"role":"assistant","content":[{"type":"server_tool_use","id":"srvu_9","name":"web_search","input":{}}]},
			{"role":"user","content":[{"type":"web_search_tool_result","tool_use_id":"srvu_9","content":[]}]},
			{"role":"assistant","content":[{"type":"web_search_tool_result","tool_use_id":"srvu_gone","content":[]}]}
		]
	}`
	out, err := ApplyClaudeServerToolsStrip([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeServerToolsStrip returned error: %v", err)
	}
	// tools 剥空连带删 tool_choice；孤儿结果块（无配对 server_tool_use）也剥离；空 content 补空格 text 块
	assertJSONEqual(t, `{
		"model":"claude-opus-4-8",
		"messages":[
			{"role":"assistant","content":[{"type":"text","text":" "}]},
			{"role":"user","content":[{"type":"text","text":" "}]},
			{"role":"assistant","content":[{"type":"text","text":" "}]}
		]
	}`, string(out))
}

func TestApplyClaudeServerToolsStripKeepsToolChoiceWhenCustomToolsRemain(t *testing.T) {
	input := `{
		"model":"claude-opus-4-8",
		"tools":[
			{"type":"web_search_20260209","name":"web_search"},
			{"name":"my_tool","description":"d","input_schema":{"type":"object"}}
		],
		"tool_choice":{"type":"tool","name":"my_tool"}
	}`
	out, err := ApplyClaudeServerToolsStrip([]byte(input), true)
	if err != nil {
		t.Fatalf("ApplyClaudeServerToolsStrip returned error: %v", err)
	}
	assertJSONEqual(t, `{
		"model":"claude-opus-4-8",
		"tools":[
			{"name":"my_tool","description":"d","input_schema":{"type":"object"}}
		],
		"tool_choice":{"type":"tool","name":"my_tool"}
	}`, string(out))
}

func TestApplyClaudeServerToolsStripDisabledNoChange(t *testing.T) {
	input := `{"model":"claude-opus-4-8","tools":[{"type":"web_search_20260209","name":"web_search"}]}`
	out, err := ApplyClaudeServerToolsStrip([]byte(input), false)
	if err != nil {
		t.Fatalf("ApplyClaudeServerToolsStrip returned error: %v", err)
	}
	assertJSONEqual(t, input, string(out))
}

func TestFilterClaudeBetaHeader(t *testing.T) {
	filtered, keep := FilterClaudeBetaHeader("interleaved-thinking-2025-05-14,prompt-caching-scope-2026-01-05, context-1m-2025-08-07")
	if !keep || filtered != "interleaved-thinking-2025-05-14,context-1m-2025-08-07" {
		t.Fatalf("unexpected filtered beta header: %q keep=%v", filtered, keep)
	}

	_, keep = FilterClaudeBetaHeader("prompt-caching-scope-2026-01-05")
	if keep {
		t.Fatalf("expected header to be dropped when all values unsupported")
	}

	filtered, keep = FilterClaudeBetaHeader("interleaved-thinking-2025-05-14")
	if !keep || filtered != "interleaved-thinking-2025-05-14" {
		t.Fatalf("unexpected passthrough beta header: %q keep=%v", filtered, keep)
	}
}

func TestApplyClaudeRequestBodyCompatCombined(t *testing.T) {
	settings := dto.ChannelOtherSettings{
		ClaudeThinkingAdaptiveCompat: true,
		ClaudeThinkingSignatureStrip: true,
		ClaudeStripServerToolsCompat: true,
	}
	input := `{
		"model":"claude-opus-4-8",
		"thinking":{"type":"enabled","budget_tokens":4096},
		"temperature":0.7,
		"tools":[{"type":"web_search_20260209","name":"web_search"}],
		"messages":[
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"...","signature":"stale-sig"},
				{"type":"text","text":"ok"}
			]}
		]
	}`
	out, err := ApplyClaudeRequestBodyCompat([]byte(input), settings)
	if err != nil {
		t.Fatalf("ApplyClaudeRequestBodyCompat returned error: %v", err)
	}
	// adaptive 改写（含 opus-4-8 采样参数清理与 display 补齐）+ 历史思考块整块剥离 + 服务端工具剥离
	assertJSONEqual(t, `{
		"model":"claude-opus-4-8",
		"thinking":{"type":"adaptive","display":"summarized"},
		"output_config":{"effort":"high"},
		"messages":[
			{"role":"assistant","content":[
				{"type":"text","text":"ok"}
			]}
		]
	}`, string(out))
}
