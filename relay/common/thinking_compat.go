package common

import (
	"strings"

	"github.com/QuantumNous/new-api/common"

	"github.com/tidwall/gjson"
)

// thinkingAdaptiveOnlyModelPrefixes 列出不再接受 thinking.type="enabled" 的上游模型前缀，
// 这些模型同时拒绝非默认 temperature/top_p/top_k（报 400），改写时需一并清理。
var thinkingAdaptiveOnlyModelPrefixes = []string{"claude-opus-4-7", "claude-opus-4-8"}

// ApplyClaudeThinkingAdaptiveCompat 渠道级 thinking 自适应兼容改写。
// 上游新模型（如 claude-opus-4-7/4-8）对旧式扩展思考参数报：
//
//	"thinking.type.enabled" is not supported for this model.
//	Use "thinking.type.adaptive" and "output_config.effort" to control thinking behavior.
//
// 开关开启时，把请求体中的 {"thinking":{"type":"enabled","budget_tokens":N}} 改写为
// {"thinking":{"type":"adaptive"}} + {"output_config":{"effort":...}}：
//   - budget_tokens 按本仓 effort→budget 口径反向映射（<2048=low，<4096=medium，其余=high），
//     仅在请求未自带 output_config.effort 时写入；
//   - 命中 thinkingAdaptiveOnlyModelPrefixes 的模型额外移除 temperature/top_p/top_k，
//     并补 display="summarized"（adaptive 默认 omitted 会抑制思考输出，与既有后缀路径口径一致）。
//
// 开关关闭、请求不含 thinking.type="enabled"、或 JSON 解析失败时原样返回，不做任何改动。
func ApplyClaudeThinkingAdaptiveCompat(jsonData []byte, enabled bool) ([]byte, error) {
	if !enabled || len(jsonData) == 0 {
		return jsonData, nil
	}
	if gjson.GetBytes(jsonData, "thinking.type").String() != "enabled" {
		return jsonData, nil
	}

	var data map[string]interface{}
	if err := common.Unmarshal(jsonData, &data); err != nil {
		common.SysError("ApplyClaudeThinkingAdaptiveCompat Unmarshal error :" + err.Error())
		return jsonData, nil
	}
	thinking, ok := data["thinking"].(map[string]interface{})
	if !ok || thinking["type"] != "enabled" {
		return jsonData, nil
	}

	var budget float64
	if b, ok := thinking["budget_tokens"].(float64); ok {
		budget = b
	}
	delete(thinking, "budget_tokens")
	thinking["type"] = "adaptive"
	data["thinking"] = thinking

	// budget_tokens 的意图用 output_config.effort 承接；客户端已显式指定 effort 时不覆盖
	if budget > 0 {
		outputConfig, _ := data["output_config"].(map[string]interface{})
		if outputConfig == nil {
			outputConfig = map[string]interface{}{}
		}
		if _, hasEffort := outputConfig["effort"]; !hasEffort {
			outputConfig["effort"] = effortForThinkingBudget(budget)
			data["output_config"] = outputConfig
		}
	}

	// 仅接受 adaptive 的模型同时拒绝非默认采样参数，移除即回落到上游默认值
	model, _ := data["model"].(string)
	for _, prefix := range thinkingAdaptiveOnlyModelPrefixes {
		if strings.HasPrefix(model, prefix) {
			// 与既有 opus-4-7/4-8 处理保持一致：adaptive 默认 display=omitted 会静默丢弃思考内容
			if _, hasDisplay := thinking["display"]; !hasDisplay {
				thinking["display"] = "summarized"
			}
			delete(data, "temperature")
			delete(data, "top_p")
			delete(data, "top_k")
			break
		}
	}

	jsonDataAfter, err := common.Marshal(data)
	if err != nil {
		common.SysError("ApplyClaudeThinkingAdaptiveCompat Marshal error :" + err.Error())
		return jsonData, nil
	}
	return jsonDataAfter, nil
}

// effortForThinkingBudget 与 relayconvert 的 effort→budget_tokens 映射（low=1280/medium=2048/high=4096）
// 保持同一口径反向取值，更大的预算同样归为 high。
func effortForThinkingBudget(budget float64) string {
	switch {
	case budget < 2048:
		return "low"
	case budget < 4096:
		return "medium"
	default:
		return "high"
	}
}

// ApplyClaudeThinkingBlocksStrip 渠道级历史思考块剥离。
// 历史 thinking/redacted_thinking 块的 signature 与上游不匹配时报：
//
//	Invalid `signature` in `thinking` block
//	`thinking` or `redacted_thinking` blocks in the latest assistant message cannot be modified
//
// 而仅删 signature 字段又会触发 `signature: Field required`（上游要求字段存在），
// 因此唯一可行口径是整块剥离：
//   - 移除 messages 中所有 thinking/redacted_thinking 块（含最新 assistant 轮）
//   - 剥离后内容为空的消息补空格 text 块，避免空 content 被拒
//
// 开关关闭、请求不含 messages、或 JSON 解析失败时原样返回，不做任何改动。
func ApplyClaudeThinkingBlocksStrip(jsonData []byte, enabled bool) ([]byte, error) {
	if !enabled || len(jsonData) == 0 {
		return jsonData, nil
	}

	// 快速检查：如果没有 messages 或没有 thinking 相关内容，直接返回
	if !gjson.GetBytes(jsonData, "messages.#.content.#.type").Exists() {
		return jsonData, nil
	}

	var data map[string]interface{}
	if err := common.Unmarshal(jsonData, &data); err != nil {
		common.SysError("ApplyClaudeThinkingBlocksStrip Unmarshal error: " + err.Error())
		return jsonData, nil
	}

	messages, ok := data["messages"].([]interface{})
	if !ok || len(messages) == 0 {
		return jsonData, nil
	}

	modified := false
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
			if blockType, _ := blockMap["type"].(string); blockType == "thinking" || blockType == "redacted_thinking" {
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
		common.SysError("ApplyClaudeThinkingBlocksStrip Marshal error: " + err.Error())
		return jsonData, nil
	}
	return jsonDataAfter, nil
}
