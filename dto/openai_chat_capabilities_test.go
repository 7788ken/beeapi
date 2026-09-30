package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsOpenAIGPT5Model(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model string
		want  bool
	}{
		{name: "exact", model: "gpt-5", want: true},
		{name: "dash variant", model: "gpt-5-mini", want: true},
		{name: "dot family", model: "gpt-5.6-luna", want: true},
		{name: "dated gpt-5", model: "gpt-5-2025-08-07", want: true},
		{name: "gpt-50 not a family hit", model: "gpt-50"},
		{name: "gpt-5custom not a family hit", model: "gpt-5custom"},
		{name: "gpt-4.1", model: "gpt-4.1"},
		{name: "gpt-6-astra", model: "gpt-6-astra"},
		{name: "gpt-7", model: "gpt-7"},
		{name: "vendor prefix", model: "openai/gpt-5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsOpenAIGPT5Model(tc.model))
		})
	}
}

func TestIsOpenAIReasoningOModel(t *testing.T) {
	assert.True(t, IsOpenAIReasoningOModel("o1"))
	assert.True(t, IsOpenAIReasoningOModel("o3-mini"))
	assert.True(t, IsOpenAIReasoningOModel("o4-mini"))
	assert.False(t, IsOpenAIReasoningOModel("omni-moderation"))
	assert.False(t, IsOpenAIReasoningOModel("openai/gpt-4.1"))
}

func TestGetOpenAIChatCapabilities(t *testing.T) {
	sampling := OpenAIChatCapabilities{
		UseMaxCompletionTokens: true,
		UseDeveloperRole:       true,
		SupportsTemperature:    true,
		SupportsTopP:           true,
		SupportsLogProbs:       true,
	}
	restricted := OpenAIChatCapabilities{
		UseMaxCompletionTokens: true,
		UseDeveloperRole:       true,
		SupportsTemperature:    false,
		SupportsTopP:           false,
		SupportsLogProbs:       false,
	}
	passthrough := OpenAIChatCapabilities{
		SupportsTemperature: true,
		SupportsTopP:        true,
		SupportsLogProbs:    true,
	}

	for _, tc := range []struct {
		name   string
		model  string
		effort string
		want   OpenAIChatCapabilities
	}{
		{name: "gpt-6-astra", model: "gpt-6-astra", want: restricted},
		{name: "gpt-6-astra snapshot", model: "gpt-6-astra-2026-09-03", want: restricted},
		{name: "gpt-6-astra high", model: "gpt-6-astra", effort: "high", want: restricted},
		{name: "gpt-5 original", model: "gpt-5", want: restricted},
		{name: "gpt-5.6-luna", model: "gpt-5.6-luna", want: restricted},
		{name: "gpt-5.2 default none", model: "gpt-5.2", want: sampling},
		{name: "gpt-5.2 dated", model: "gpt-5.2-2025-12-11", want: sampling},
		{name: "gpt-5.4 none", model: "gpt-5.4", effort: "none", want: sampling},
		{name: "gpt-5.4 high", model: "gpt-5.4", effort: "high", want: restricted},
		{name: "pro variant", model: "gpt-5.2-pro-2025-12-11", want: restricted},
		{name: "chat-latest", model: "gpt-5.2-chat-latest", want: restricted},
		{name: "codex max", model: "gpt-5.1-codex-max", want: restricted},
		{name: "gpt-4.1 unchanged", model: "gpt-4.1", want: passthrough},
		{name: "future gpt-7 unchanged", model: "gpt-7", want: passthrough},
		{
			name:  "o3-mini",
			model: "o3-mini",
			want: OpenAIChatCapabilities{
				UseMaxCompletionTokens: true,
				UseDeveloperRole:       true,
				SupportsTemperature:    false,
				SupportsTopP:           true,
				SupportsLogProbs:       true,
			},
		},
		{
			name:  "o1-mini keeps system",
			model: "o1-mini",
			want: OpenAIChatCapabilities{
				UseMaxCompletionTokens: true,
				SupportsTemperature:    false,
				SupportsTopP:           true,
				SupportsLogProbs:       true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, GetOpenAIChatCapabilities(tc.model, tc.effort))
		})
	}
}

func TestGetSystemRoleNameUsesCapabilities(t *testing.T) {
	assert.Equal(t, "developer", (&GeneralOpenAIRequest{Model: "gpt-6-astra"}).GetSystemRoleName())
	assert.Equal(t, "developer", (&GeneralOpenAIRequest{Model: "gpt-5.2"}).GetSystemRoleName())
	assert.Equal(t, "system", (&GeneralOpenAIRequest{Model: "o1-mini"}).GetSystemRoleName())
	assert.Equal(t, "system", (&GeneralOpenAIRequest{Model: "gpt-4.1"}).GetSystemRoleName())
	assert.Equal(t, "system", (&GeneralOpenAIRequest{Model: "gpt-7"}).GetSystemRoleName())
}
