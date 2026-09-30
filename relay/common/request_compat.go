package common

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"

	"github.com/tidwall/gjson"
)

// serverToolTypePrefixes 为 Anthropic 服务端工具的 type 前缀，Bedrock 等上游不支持时会直接 400
// （实测渠道报错：tool type 'web_search_20260209' / 'code_execution_20250522' is not supported）。
var serverToolTypePrefixes = []string{"web_search_", "code_execution_", "memory_", "bash_", "text_editor_", "tool_search_"}

// serverToolResultTypes 为服务端工具执行后的结果块类型；开关开启即代表上游完全不支持该特性，
// 无论能否在本次请求内配对到 server_tool_use（历史截断/压缩可能孤儿），一律剥离。
var serverToolResultTypes = map[string]bool{
	"web_search_tool_result":     true,
	"code_execution_tool_result": true,
	"memory_tool_result":         true,
	"bash_tool_result":           true,
	"text_editor_tool_result":    true,
	"tool_search_tool_result":    true,
}

// unsupportedBetaPrefixes 为上游明确拒绝的 anthropic-beta 特性值前缀
// （实测渠道报错：Unexpected value(s) `prompt-caching-scope-2026-01-05` for the `anthropic-beta` header）。
var unsupportedBetaPrefixes = []string{"prompt-caching-scope-"}

func isServerToolType(blockType string) bool {
	for _, prefix := range serverToolTypePrefixes {
		if strings.HasPrefix(blockType, prefix) {
			return true
		}
	}
	return false
}

// ApplyClaudeRequestBodyCompat 渠道级请求体兼容改写统一入口，按固定顺序执行：
// thinking 自适应（enabled→adaptive）→ thinking signature 剥离 → 服务端工具剥离。
// 各步按其开关独立生效，开关关闭即原样透传该步结果。
func ApplyClaudeRequestBodyCompat(jsonData []byte, settings dto.ChannelOtherSettings) ([]byte, error) {
	out, err := ApplyClaudeThinkingAdaptiveCompat(jsonData, settings.ClaudeThinkingAdaptiveCompat)
	if err != nil {
		return out, err
	}
	out, err = ApplyClaudeThinkingBlocksStrip(out, settings.ClaudeThinkingSignatureStrip)
	if err != nil {
		return out, err
	}
	return ApplyClaudeServerToolsStrip(out, settings.ClaudeStripServerToolsCompat)
}

// ClaudeRequestBodyCompatEnabled 报告是否有任一请求体兼容开关开启（透传分支据此决定是否读体改写）。
func ClaudeRequestBodyCompatEnabled(settings dto.ChannelOtherSettings) bool {
	return settings.ClaudeThinkingAdaptiveCompat ||
		settings.ClaudeThinkingSignatureStrip ||
		settings.ClaudeStripServerToolsCompat
}

// ApplyClaudeServerToolsStrip 渠道级服务端工具剥离兼容。
// 上游不支持 web_search/code_execution 等服务端工具时报：
//
//	tool type 'web_search_20260209' is not supported for this model
//
// 开关开启时：
//   - 剥离 tools 数组中的服务端工具声明（type 命中前缀表；custom/未声明 type 的客户端工具保留）；
//   - 剥离历史中 server_tool_use 块，并按 tool_use_id 成对剥离其结果块，避免孤儿 tool_result；
//   - 剥离后内容为空的消息补一个空 text 块，避免上游拒绝空 content。
//
// 开关关闭、请求无 tools/历史相关块、或 JSON 解析失败时原样返回。
func ApplyClaudeServerToolsStrip(jsonData []byte, enabled bool) ([]byte, error) {
	if !enabled || len(jsonData) == 0 {
		return jsonData, nil
	}
	if !gjson.GetBytes(jsonData, "tools").Exists() && !gjson.GetBytes(jsonData, "messages").Exists() {
		return jsonData, nil
	}

	var data map[string]interface{}
	if err := common.Unmarshal(jsonData, &data); err != nil {
		common.SysError("ApplyClaudeServerToolsStrip Unmarshal error :" + err.Error())
		return jsonData, nil
	}

	modified := false

	// 1) 剥离服务端工具声明
	if tools, ok := data["tools"].([]interface{}); ok {
		kept := make([]interface{}, 0, len(tools))
		toolsChanged := false
		for _, tool := range tools {
			toolMap, ok := tool.(map[string]interface{})
			if !ok {
				kept = append(kept, tool)
				continue
			}
			toolType, _ := toolMap["type"].(string)
			if toolType != "" && toolType != "custom" && isServerToolType(toolType) {
				toolsChanged = true
				continue
			}
			kept = append(kept, tool)
		}
		if toolsChanged {
			modified = true
			if len(kept) == 0 {
				delete(data, "tools")
				// tool_choice 仅允许与 tools 同时提供，工具剥空后残留会触发新的 400
				delete(data, "tool_choice")
			} else {
				data["tools"] = kept
			}
		}
	}

	// 2) 剥离历史中的 server_tool_use 块与服务端工具结果块（后者无条件剥离，兼容孤儿块）
	messages, _ := data["messages"].([]interface{})
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}
		content, ok := msgMap["content"].([]interface{})
		if !ok {
			continue
		}
		kept := make([]interface{}, 0, len(content))
		changed := false
		for _, block := range content {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				kept = append(kept, block)
				continue
			}
			blockType, _ := blockMap["type"].(string)
			if blockType == "server_tool_use" || serverToolResultTypes[blockType] {
				changed = true
				continue
			}
			kept = append(kept, block)
		}
		if changed {
			msgMap["content"] = nonEmptyContent(kept)
			modified = true
		}
	}

	if !modified {
		return jsonData, nil
	}
	jsonDataAfter, err := common.Marshal(data)
	if err != nil {
		common.SysError("ApplyClaudeServerToolsStrip Marshal error :" + err.Error())
		return jsonData, nil
	}
	return jsonDataAfter, nil
}

// nonEmptyContent 剥离后内容为空时补单个空格 text 块：空字符串 text 块会被上游拒绝
// （text content blocks must be non-empty），空格既非空又无语义副作用。
func nonEmptyContent(kept []interface{}) []interface{} {
	if len(kept) == 0 {
		return []interface{}{map[string]interface{}{"type": "text", "text": " "}}
	}
	return kept
}

// FilterClaudeBetaHeader 按拒绝前缀过滤 anthropic-beta 特性值（逗号分隔）。
// 返回过滤后的头值与是否仍需保留该头；全部被过滤时 keep=false，调用方应删除该头。
func FilterClaudeBetaHeader(value string) (filtered string, keep bool) {
	parts := strings.Split(value, ",")
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		v := strings.TrimSpace(part)
		if v == "" {
			continue
		}
		drop := false
		for _, prefix := range unsupportedBetaPrefixes {
			if strings.HasPrefix(v, prefix) {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, v)
		}
	}
	if len(kept) == 0 {
		return "", false
	}
	return strings.Join(kept, ","), true
}

// FilterClaudeBetaHeaderInPlace 对最终出站请求头应用 beta 过滤。
// 必须在 Header Override（含 affinity 规则 pass_headers 透传）应用之后调用：
// override 会把客户端原始 anthropic-beta 重新盖回，仅在 SetupRequestHeader 内过滤会被绕过。
func FilterClaudeBetaHeaderInPlace(header *http.Header, enabled bool) {
	if !enabled || header == nil {
		return
	}
	beta := header.Get("anthropic-beta")
	if beta == "" {
		return
	}
	if filtered, keep := FilterClaudeBetaHeader(beta); keep {
		header.Set("anthropic-beta", filtered)
	} else {
		header.Del("anthropic-beta")
	}
}
