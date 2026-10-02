package dto

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// state 是对象/数组时递归展开计数，不会估成 0；输出为空不影响；结果确定（键排序）。
func TestSystemOneRequestTokenMetaFlattensStateAndQuestions(t *testing.T) {
	r := &SystemOneRequest{
		Model: "jev-latest",
		State: map[string]any{
			"ticket": map[string]any{
				"subject":  "Duplicate charge",
				"messages": []any{map[string]any{"from": "customer", "text": "I was charged twice"}},
			},
		},
		Questions: map[string]any{
			"refund_requested": map[string]any{"type": SystemOneQuestionNoul, "instructions": "asks for a refund"},
			"department": map[string]any{
				"type":         SystemOneQuestionChoice,
				"instructions": "which team",
				"criteria":     map[string]any{"billing": "money issues", "technical": "bugs"},
			},
		},
	}
	meta := r.GetTokenCountMeta()
	for _, s := range []string{"Duplicate charge", "I was charged twice", "asks for a refund", "money issues", "bugs", "refund_requested", "department"} {
		require.Contains(t, meta.CombineText, s)
	}
	require.NotContains(t, meta.CombineText, "{", "计的是内容不是 JSON 标点")
	require.Equal(t, 0, meta.MaxTokens)
	require.Equal(t, meta.CombineText, r.GetTokenCountMeta().CombineText, "同一请求两次估算必须一致")
	require.False(t, r.IsStream(nil))
}

func TestSystemOneRequestSetModelNameIgnoresEmpty(t *testing.T) {
	r := &SystemOneRequest{Model: "jev-latest"}
	r.SetModelName("jev-1.13.0")
	require.Equal(t, "jev-1.13.0", r.Model)
	r.SetModelName("")
	require.Equal(t, "jev-1.13.0", r.Model)
}

func TestSystemOneRequestTokenMetaStringState(t *testing.T) {
	r := &SystemOneRequest{Model: "jev-latest", State: "用户说：这张发票金额不对，我要退款。", Questions: map[string]any{"q": map[string]any{"type": "noul", "instructions": "账务问题？"}}}
	meta := r.GetTokenCountMeta()
	require.Contains(t, meta.CombineText, "发票金额不对")
	require.Contains(t, meta.CombineText, "账务问题")
}
