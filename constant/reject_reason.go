package constant

// admin_reject_reason（ContextKeyAdminRejectReason）取值协议。
// 写点在 relay 适配层（claude/openai/gemini），消费点在 service.isUpstreamRefusalReject——
// 该键参与零输出免单的计费判定（quota_setting.refund_no_output_exclude_upstream_refusal），
// 两端必须引用本文件常量，禁止裸字面量；新增取值须同步评估是否属于"上游显式拒绝"家族。
const (
	RejectReasonClaudeRefusal         = "claude_stop_reason=refusal"
	RejectReasonOpenAIContentFilter   = "openai_finish_reason=content_filter"
	RejectReasonGeminiBlockPrefix     = "gemini_block_reason="
	RejectReasonGeminiFinishPrefix    = "gemini_finish_reason="
	RejectReasonGeminiEmptyCandidates = "gemini_empty_candidates"
)

// GeminiRefusalDenyValues：Gemini blockReason / finishReason 两个枚举中判为
// "客户内容触发上游风控"的取值全集（写点标记与计费拒退共用同一份，防止两端漂移）。
// 刻意排除的取值及理由——归因不清或模型侧原因不向客户收费：
//   - OTHER / BLOCK_REASON_UNSPECIFIED / IMAGE_OTHER：上游"原因不明"桶，多为链路/上游侧异常
//   - RECITATION / IMAGE_RECITATION：模型复述训练语料被掐，归因在模型侧
//   - LANGUAGE：不支持的语言，能力缺口而非风控
//
// 注意：service/relayconvert 里 Gemini→OpenAI 的展示层映射（含 OTHER→content_filter）
// 是给客户端看的宽口径，与本计费白名单语义不同，属刻意分离，勿互相对齐。
var GeminiRefusalDenyValues = map[string]bool{
	"SAFETY":                   true,
	"PROHIBITED_CONTENT":       true,
	"BLOCKLIST":                true,
	"SPII":                     true,
	"IMAGE_SAFETY":             true,
	"IMAGE_PROHIBITED_CONTENT": true,
}
