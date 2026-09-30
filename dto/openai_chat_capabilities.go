package dto

import (
	"strings"
	"time"
)

func IsOpenAIReasoningOModel(modelName string) bool {
	return strings.HasPrefix(modelName, "o1") ||
		strings.HasPrefix(modelName, "o3") ||
		strings.HasPrefix(modelName, "o4")
}

// IsOpenAIGPT5Model identifies the GPT-5 family, independently of request capabilities.
func IsOpenAIGPT5Model(modelName string) bool {
	return modelName == "gpt-5" || strings.HasPrefix(modelName, "gpt-5-") || strings.HasPrefix(modelName, "gpt-5.")
}

// OpenAIChatCapabilities describes independent Chat Completions compatibility rules.
type OpenAIChatCapabilities struct {
	UseMaxCompletionTokens bool
	UseDeveloperRole       bool
	SupportsTemperature    bool
	SupportsTopP           bool
	SupportsLogProbs       bool // Also governs top_logprobs.
}

// GetOpenAIChatCapabilities uses the mapped model and resolved reasoning effort.
// Unrecognized models retain their parameters; future GPT generations do not
// automatically inherit the restrictions of existing models.
func GetOpenAIChatCapabilities(modelName, reasoningEffort string) OpenAIChatCapabilities {
	capabilities := OpenAIChatCapabilities{
		SupportsTemperature: true,
		SupportsTopP:        true,
		SupportsLogProbs:    true,
	}
	if IsOpenAIReasoningOModel(modelName) {
		capabilities.UseMaxCompletionTokens = true
		capabilities.UseDeveloperRole = !strings.HasPrefix(modelName, "o1-mini") && !strings.HasPrefix(modelName, "o1-preview")
		capabilities.SupportsTemperature = false
		return capabilities
	}

	isGPT5Model := IsOpenAIGPT5Model(modelName)
	if !isGPT5Model && !isOpenAIModelSnapshot(modelName, "gpt-6-astra") {
		return capabilities
	}
	capabilities.UseMaxCompletionTokens = true
	capabilities.UseDeveloperRole = true

	// These standard GPT-5 models default to none and support sampling only
	// without reasoning. Named variants (pro, codex, chat-latest, etc.) do not
	// inherit this exception. GPT-6 Astra never supports these parameters.
	supportsSampling := false
	if isGPT5Model && (reasoningEffort == "" || reasoningEffort == "none") {
		for _, model := range []string{"gpt-5.1", "gpt-5.2", "gpt-5.4"} {
			if isOpenAIModelSnapshot(modelName, model) {
				supportsSampling = true
				break
			}
		}
	}
	capabilities.SupportsTemperature = supportsSampling
	capabilities.SupportsTopP = supportsSampling
	capabilities.SupportsLogProbs = supportsSampling
	return capabilities
}

func isOpenAIModelSnapshot(modelName, baseModel string) bool {
	if modelName == baseModel {
		return true
	}
	snapshot, ok := strings.CutPrefix(modelName, baseModel+"-")
	if !ok {
		return false
	}
	_, err := time.Parse(time.DateOnly, snapshot)
	return err == nil
}
