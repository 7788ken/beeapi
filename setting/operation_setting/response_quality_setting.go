package operation_setting

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

// ResponseQualitySetting 上游回复质量闸门：命中后丢弃上游回复，按可配状态码和文案
// 返回客户端。默认不重试。RetryOnBlock 打开后，拦截按普通失败走重试次数和状态码白名单。
// ApplyAllChannels 为真时，全局开关对全部渠道生效；关掉后才要求渠道 setting 再开一次。
type ResponseQualitySetting struct {
	// BlockApologyEnabled 拦截开场道歉/套话拒绝。
	BlockApologyEnabled bool `json:"block_apology_enabled"`
	// ApologyStatusCode 道歉拦截返回的 HTTP 状态码。非法或 0 时按 503。
	ApologyStatusCode int `json:"apology_status_code"`
	// ApologyMessage 道歉拦截返回给客户端的文案。空则用默认。
	ApologyMessage string `json:"apology_message"`
	// ApologyKeywords 拒绝/道歉拦截词表，一行一条。空则用 DefaultApologyKeywords。
	ApologyKeywords string `json:"apology_keywords"`
	// ApplyAllChannels 为真时，只要全局开关打开就对全部渠道生效，不再要求渠道开关。
	ApplyAllChannels bool `json:"apply_all_channels"`
	// BlockLowTokenEnabled 拦截过短 completion（默认低于 300 token）。
	BlockLowTokenEnabled bool `json:"block_low_token_enabled"`
	// LowTokenThreshold 低 token 阈值。<=0 时按 300。
	LowTokenThreshold int `json:"low_token_threshold"`
	// LowTokenStatusCode 低 token 拦截返回的 HTTP 状态码。非法或 0 时按 503。
	LowTokenStatusCode int `json:"low_token_status_code"`
	// LowTokenMessage 低 token 拦截文案。支持 {tokens}、{threshold}。空则用默认。
	LowTokenMessage string `json:"low_token_message"`
	// RetryOnBlock 为真时，道歉和低 token 拦截不再带 SkipRetry，按普通失败重试。
	// 零值必须是 false：旧配置没有这个键时保持「不重试」。
	RetryOnBlock bool `json:"retry_on_block"`
	// NewChannelBlockApology 新建渠道时，管理端表单里道歉拦截的初始值。
	// 零值 false。只在某个站的 options 里写成 true 才默认打开，其它站不变。
	// 不改变已有渠道，也不参与请求时的拦截判断。
	NewChannelBlockApology bool `json:"new_channel_block_apology"`
	// NewChannelBlockLowToken 新建渠道时，管理端表单里低 token 拦截的初始值。
	NewChannelBlockLowToken bool `json:"new_channel_block_low_token"`
}

const (
	DefaultLowTokenThreshold = 300
	DefaultQualityStatusCode = 503
	DefaultApologyMessage    = "upstream apology reply blocked"
	DefaultLowTokenMessage   = "upstream completion tokens {tokens} below {threshold}"
)

// DefaultApologyKeywords 内置拒绝/道歉词。英文按小写匹配；中文原样。
var DefaultApologyKeywords = []string{
	"i'm sorry",
	"i am sorry",
	"i apologize",
	"my apologies",
	"sorry, i cannot",
	"sorry, i can't",
	"sorry, but i",
	"很抱歉",
	"抱歉",
	"对不起",
	"深表歉意",
	"不好意思",
	"notice",
	"i can't engage with this content",
	"i can't discuss that",
	"i don't roleplay",
	"i appreciate your interest",
	"我必须拒绝这个请求",
	"i can't follow these instructions",
	"i cannot follow the instructions in your message",
	"i need to be completely clear about what i can and cannot do",
	"i appreciate you trying to test my boundaries",
	"i cannot and will not",
	"i'm claude, an ai assistant made by anthropic",
	"我不能",
	"我无法",
	"我无法继续",
	"我注意到",
	"我很感激你",
}

var responseQualitySetting = ResponseQualitySetting{
	BlockApologyEnabled:  false,
	ApologyStatusCode:    DefaultQualityStatusCode,
	ApologyMessage:       DefaultApologyMessage,
	ApologyKeywords:      DefaultApologyKeywordsText(),
	ApplyAllChannels:     true,
	BlockLowTokenEnabled: false,
	LowTokenThreshold:    DefaultLowTokenThreshold,
	LowTokenStatusCode:   DefaultQualityStatusCode,
	LowTokenMessage:      DefaultLowTokenMessage,
}

func init() {
	config.GlobalConfig.Register("response_quality_setting", &responseQualitySetting)
}

func GetResponseQualitySetting() *ResponseQualitySetting {
	return &responseQualitySetting
}

func DefaultApologyKeywordsText() string {
	return strings.Join(DefaultApologyKeywords, "\n")
}

func ParseApologyKeywords(raw string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		k := strings.ToLower(strings.TrimSpace(line))
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

func (s *ResponseQualitySetting) EffectiveApologyKeywords() []string {
	if s != nil {
		if parsed := ParseApologyKeywords(s.ApologyKeywords); len(parsed) > 0 {
			return parsed
		}
	}
	return append([]string(nil), DefaultApologyKeywords...)
}

func (s *ResponseQualitySetting) EffectiveApologyKeywordsText() string {
	if s != nil && strings.TrimSpace(s.ApologyKeywords) != "" {
		return strings.ReplaceAll(s.ApologyKeywords, "\r\n", "\n")
	}
	return DefaultApologyKeywordsText()
}

func (s *ResponseQualitySetting) ChannelSwitchRequired() bool {
	return s == nil || !s.ApplyAllChannels
}

// RetryAfterBlock 为真时，拦截错误不带 SkipRetry，交给重试循环。
// nil 与零值都是不重试。
func (s *ResponseQualitySetting) RetryAfterBlock() bool {
	return s != nil && s.RetryOnBlock
}

func (s *ResponseQualitySetting) EffectiveLowTokenThreshold() int {
	if s == nil || s.LowTokenThreshold <= 0 {
		return DefaultLowTokenThreshold
	}
	return s.LowTokenThreshold
}

func (s *ResponseQualitySetting) EffectiveApologyStatusCode() int {
	if s == nil {
		return DefaultQualityStatusCode
	}
	return effectiveQualityStatusCode(s.ApologyStatusCode)
}

func (s *ResponseQualitySetting) EffectiveLowTokenStatusCode() int {
	if s == nil {
		return DefaultQualityStatusCode
	}
	return effectiveQualityStatusCode(s.LowTokenStatusCode)
}

func (s *ResponseQualitySetting) EffectiveApologyMessage() string {
	if s == nil || strings.TrimSpace(s.ApologyMessage) == "" {
		return DefaultApologyMessage
	}
	return s.ApologyMessage
}

func (s *ResponseQualitySetting) EffectiveLowTokenMessage(tokens, threshold int) string {
	msg := DefaultLowTokenMessage
	if s != nil && strings.TrimSpace(s.LowTokenMessage) != "" {
		msg = s.LowTokenMessage
	}
	msg = strings.ReplaceAll(msg, "{tokens}", strconv.Itoa(tokens))
	msg = strings.ReplaceAll(msg, "{threshold}", strconv.Itoa(threshold))
	return msg
}

func effectiveQualityStatusCode(code int) int {
	if code < 100 || code > 599 {
		return DefaultQualityStatusCode
	}
	return code
}
