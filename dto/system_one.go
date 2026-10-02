package dto

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// TypeSafe System One（Jev）请求/响应结构。
//
// 这不是聊天模型：客户传一个 state（string/object/array）和一组命名的带类型 questions
// （noul / choice / score），模型一次性返回每个问题的类型化答案与校准概率。没有自回归
// 解码，因此没有流式、没有 assistant 消息，也没有可计费的输出 token（厂商只按输入计费）。
// 请求与响应都原样透传，这里只解析计费需要的字段。
//
// 线上协议：https://docs.typesafe.ai/api
const (
	SystemOneQuestionNoul   = "noul"
	SystemOneQuestionChoice = "choice"
	SystemOneQuestionScore  = "score"
)

// MaxSystemOneQuestions 单次请求允许的问题数上限。厂商按输入 token 计费且整个 state
// 会按每个问题重读一遍，不设上限等于不设账单上限。远高于官方 cookbook 里的用量。
const MaxSystemOneQuestions = 256

type SystemOneRequest struct {
	Model     string         `json:"model"`
	State     any            `json:"state"`
	Questions map[string]any `json:"questions"`
}

func (r *SystemOneRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}

// IsStream 永远为 false：决策模型只回一份类型化答案。
func (r *SystemOneRequest) IsStream(_ *gin.Context) bool {
	return false
}

// GetTokenCountMeta 供预扣费估算。厂商读到的全部是输入——state 加每个问题的
// instructions/criteria——所以把这些文本合在一起计数即可。MaxTokens 为 0：模型不会
// 产出超过类型化答案的内容。
func (r *SystemOneRequest) GetTokenCountMeta() *types.TokenCountMeta {
	var b strings.Builder
	appendSystemOneValue(&b, r.State)
	names := make([]string, 0, len(r.Questions))
	for name := range r.Questions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		b.WriteString(name)
		b.WriteString("\n")
		appendSystemOneValue(&b, r.Questions[name])
	}
	return &types.TokenCountMeta{
		TokenType:   types.TokenTypeTokenizer,
		CombineText: b.String(),
	}
}

// appendSystemOneValue 把 state / question 里任意形状的 JSON 展开成纯文本用于计数。
// 递归遍历 map 与 slice 而不是直接 marshal，计的是内容而不是 JSON 标点。
func appendSystemOneValue(b *strings.Builder, value any) {
	switch v := value.(type) {
	case nil:
		return
	case string:
		b.WriteString(v)
	case []any:
		for _, item := range v {
			appendSystemOneValue(b, item)
			b.WriteString("\n")
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			b.WriteString(key)
			b.WriteString(" ")
			appendSystemOneValue(b, v[key])
			b.WriteString("\n")
		}
	default:
		fmt.Fprintf(b, "%v", v)
	}
}

// SystemOneUsage 厂商回报的用量。output_tokens 会记录但不计费（目录里补全倍率为 0）。
type SystemOneUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// SystemOneResponse 只为取 usage 而解析，其余字段字节级原样回给客户。
type SystemOneResponse struct {
	Model string          `json:"model"`
	Usage *SystemOneUsage `json:"usage"`
}
