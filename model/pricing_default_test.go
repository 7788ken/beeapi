package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 这些模型名多数同时命中 defaultVendorRules 里的多条规则。
// defaultVendorRules 是 map，Go 每次 range 的顺序都不同，所以每个用例重复跑多次
// 把遍历顺序抖开：结果必须恒定，且等于 matchDefaultVendor 注释里"最左命中优先"的判定。
func TestMatchDefaultVendorIsOrderIndependent(t *testing.T) {
	cases := []struct {
		model  string
		vendor string
		why    string
	}{
		{"gpt-5.3-codex-spark-openai-compact", "OpenAI", "gpt@0 胜 spark@14"},
		{"gpt-5.3-codex-spark", "OpenAI", "gpt@0 胜 spark@14"},
		{"yi-spark", "零一万物", "yi@0 胜 spark@3"},
		{"360gpt-pro", "360", "360@0 胜 gpt@3"},
		{"deepseek-r1-distill-llama-70b", "DeepSeek", "deepseek@0 胜 llama@20"},
		{"chatglm-4", "智谱", "chatglm@0 与 glm-@4 同厂商"},
		{"claude-sonnet-4-5", "Anthropic", "单命中"},
		{"some-unmapped-model", "", "未命中任何规则"},
	}

	for _, c := range cases {
		for i := 0; i < 200; i++ {
			require.Equal(t, c.vendor, matchDefaultVendor(c.model), "%s (%s)", c.model, c.why)
		}
	}
}
