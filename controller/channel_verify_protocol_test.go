package controller

import "testing"

// 验证渠道类型/模型前缀 → protocol 白名单解析（新增 OpenAI/codex 支持）。
func TestResolveVerifyProtocol(t *testing.T) {
	cases := []struct {
		channelType int
		model       string
		want        string
		ok          bool
	}{
		{14, "claude-opus-4-8", "claude", true},
		{14, "claude-sonnet-4-6", "claude", true},
		{1, "gpt-5.5", "openai", true},
		{1, "gpt-5.3-codex-spark", "openai", true},
		{1, "codex-mini", "openai", true},
		{1, "o3-mini", "openai", true},
		{1, "o1", "openai", true},
		{1, "openai/gpt-4o", "openai", true},
		{1, "claude-opus-4-8", "", false}, // OpenAI 渠道挂 claude 模型 → 不命中
		{14, "gpt-5.5", "", false},        // Anthropic 渠道挂 gpt 模型 → 不命中
		{3, "gpt-5.5", "", false},         // 不支持的渠道类型
		{1, "text-embedding-3", "", false}, // 非测评模型前缀
	}
	for _, c := range cases {
		got, ok := resolveVerifyProtocol(c.channelType, c.model)
		if got != c.want || ok != c.ok {
			t.Errorf("resolveVerifyProtocol(%d, %q) = (%q, %v); want (%q, %v)",
				c.channelType, c.model, got, ok, c.want, c.ok)
		}
	}
}
