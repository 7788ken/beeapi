package openaicompat

import "github.com/QuantumNous/new-api/setting/model_setting"

func ShouldChatCompletionsUseResponsesPolicy(policy model_setting.ChatCompletionsToResponsesPolicy, channelID int, channelType int, model string) bool {
	if !policy.IsChannelEnabled(channelID, channelType) {
		return false
	}
	return matchAnyRegex(policy.ModelPatterns, model)
}

func ShouldChatCompletionsUseResponsesGlobal(channelID int, channelType int, model string) bool {
	return ShouldChatCompletionsUseResponsesPolicy(
		model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy,
		channelID,
		channelType,
		model,
	)
}

// RequiresResponsesForToolsAndReasoning 仅当请求同时携带 function tools 与 reasoning_effort、
// 且模型命中策略的 AutoToolsReasoningModels 时返回 true(上游硬约束,独立于 Enabled)。
func RequiresResponsesForToolsAndReasoning(policy model_setting.ChatCompletionsToResponsesPolicy, model string, hasFunctionTool bool, hasReasoningEffort bool) bool {
	if !hasFunctionTool || !hasReasoningEffort {
		return false
	}
	return matchAnyRegex(policy.AutoToolsReasoningModels, model)
}

func RequiresResponsesForToolsAndReasoningGlobal(model string, hasFunctionTool bool, hasReasoningEffort bool) bool {
	return RequiresResponsesForToolsAndReasoning(
		model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy,
		model,
		hasFunctionTool,
		hasReasoningEffort,
	)
}
