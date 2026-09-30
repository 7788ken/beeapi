package service

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/tidwall/gjson"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

type responseQualityFlags struct {
	apology  bool
	lowToken bool
}

func qualityChannelArmed(global *operation_setting.ResponseQualitySetting, channelOn bool) bool {
	if global == nil {
		return false
	}
	return !global.ChannelSwitchRequired() || channelOn
}

func responseQualityFlagsFor(info *relaycommon.RelayInfo) responseQualityFlags {
	if info == nil || info.ChannelMeta == nil {
		return responseQualityFlags{}
	}
	global := operation_setting.GetResponseQualitySetting()
	if global == nil {
		return responseQualityFlags{}
	}
	return responseQualityFlags{
		apology:  global.BlockApologyEnabled && qualityChannelArmed(global, info.ChannelSetting.BlockApologyEnabled),
		lowToken: global.BlockLowTokenEnabled && qualityChannelArmed(global, info.ChannelSetting.BlockLowTokenEnabled),
	}
}

func ResponseQualityActive(c *gin.Context, info *relaycommon.RelayInfo) bool {
	if qualityIsProbe(c, info) {
		return false
	}
	flags := responseQualityFlagsFor(info)
	return flags.apology || flags.lowToken
}

func qualityIsProbe(c *gin.Context, info *relaycommon.RelayInfo) bool {
	if qualityIsPlatformChannelTest(c) {
		return true
	}
	return qualityRequestIsProbe(info)
}

func qualityIsPlatformChannelTest(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if common.GetContextKeyBool(c, constant.ContextKeyChannelTest) {
		return true
	}
	return c.GetInt("id") == model.ChannelTestUserId && c.GetInt("token_id") == 0
}

func qualityRequestIsProbe(info *relaycommon.RelayInfo) bool {
	if info == nil || info.Request == nil {
		return false
	}
	texts := qualityUserTexts(info.Request)
	if len(texts) == 0 {
		return false
	}
	for _, text := range texts {
		if !qualityIsChannelTestHi(text) {
			return false
		}
	}
	return true
}

// qualityIsChannelTestHi 只认 new-api 基本渠道测试那一句 user 消息：hi。
func qualityIsChannelTestHi(text string) bool {
	return strings.EqualFold(strings.TrimSpace(text), "hi")
}

func qualityUserTexts(req dto.Request) []string {
	switch r := req.(type) {
	case *dto.GeneralOpenAIRequest:
		var texts []string
		for _, msg := range r.Messages {
			if !strings.EqualFold(msg.Role, "user") {
				continue
			}
			if text := strings.TrimSpace(msg.StringContent()); text != "" {
				texts = append(texts, text)
			}
		}
		return texts
	case *dto.ClaudeRequest:
		var texts []string
		for _, msg := range r.Messages {
			if !strings.EqualFold(msg.Role, "user") {
				continue
			}
			if text := strings.TrimSpace(msg.GetStringContent()); text != "" {
				texts = append(texts, text)
			}
		}
		if len(texts) == 0 && strings.TrimSpace(r.Prompt) != "" {
			return []string{strings.TrimSpace(r.Prompt)}
		}
		return texts
	case *dto.OpenAIResponsesRequest:
		if len(r.Input) == 0 {
			return nil
		}
		return contentBackupJSONUserTexts(gjson.ParseBytes([]byte(`{"input":` + string(r.Input) + `}`)))
	case *dto.GeminiChatRequest:
		var texts []string
		for _, content := range r.Contents {
			role := strings.ToLower(content.Role)
			if role != "" && role != "user" {
				continue
			}
			var parts []string
			for _, part := range content.Parts {
				if text := strings.TrimSpace(part.Text); text != "" {
					parts = append(parts, text)
				}
			}
			if text := strings.TrimSpace(strings.Join(parts, "\n")); text != "" {
				texts = append(texts, text)
			}
		}
		return texts
	default:
		return nil
	}
}

// qualityConsumeQuota 生产路径走 PostTextConsumeQuota；测试可替换，避免拉起完整结算。
var qualityConsumeQuota = PostTextConsumeQuota

func shouldChargeQualityFilter(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ChannelMeta != nil && info.ChannelSetting.DisableNoOutputRefund
}

func qualityUsageHasTokens(u *dto.Usage) bool {
	if u == nil {
		return false
	}
	return u.PromptTokens > 0 || u.CompletionTokens > 0 || u.TotalTokens > 0 || u.InputTokens > 0
}

func qualityChargeUsage(info *relaycommon.RelayInfo, usage any) *dto.Usage {
	if u := qualityUsage(usage); qualityUsageHasTokens(u) {
		return u
	}
	if info == nil {
		return nil
	}
	completion := qualityCompletionTokens(usage, info.QualityInspect, info)
	if completion <= 0 {
		return nil
	}
	prompt := info.GetEstimatePromptTokens()
	return &dto.Usage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}
}

func qualityFilterChargeExtra(qerr *types.NewAPIError) string {
	reason := "质量门拦截"
	if qerr != nil {
		switch qerr.GetErrorCode() {
		case types.ErrorCodeResponseQualityApology:
			reason = "质量门拦截-道歉"
		case types.ErrorCodeResponseQualityLowToken:
			reason = "质量门拦截-低token"
		}
	}
	return reason + "（渠道已开 0 输出不免单），按实际上游用量计费"
}

// ChargeQualityFilterIfNeeded 在质量门丢弃上游回复后，按渠道「0 输出不免单」决定是否结算。
// 结算成功后 BillingSession.settled=true，Relay defer 的 Refund 成为空操作，用户承担这次上游费用。
func ChargeQualityFilterIfNeeded(c *gin.Context, info *relaycommon.RelayInfo, usage any, qerr *types.NewAPIError) {
	if c == nil || !shouldChargeQualityFilter(info) {
		return
	}
	u := qualityChargeUsage(info, usage)
	if u == nil {
		qualityLog(c, "response quality filter: skip charge, no billable usage")
		return
	}
	extra := qualityFilterChargeExtra(qerr)
	qualityLog(c, fmt.Sprintf("response quality filter: charging user (%s), channel=%d prompt=%d completion=%d",
		extra, qualityChannelID(info), u.PromptTokens, u.CompletionTokens))
	qualityConsumeQuota(c, info, u, []string{extra})
}

// ChargeOrDeferQualityFilter 默认立即结算。打开「拦截后重试」时先挂起，
// 等请求最终仍以质量门错误结束再结，避免重试成功时账单已被这次拦截结掉。
func ChargeOrDeferQualityFilter(c *gin.Context, info *relaycommon.RelayInfo, usage any, qerr *types.NewAPIError) {
	if qerr == nil {
		return
	}
	if operation_setting.GetResponseQualitySetting().RetryAfterBlock() {
		if info != nil {
			info.QualityChargePending = qualityChargeUsage(info, usage)
		}
		return
	}
	ChargeQualityFilterIfNeeded(c, info, usage, qerr)
}

// SettleDeferredQualityCharge 在请求以质量门错误收尾时结算挂起的用量。
// 中途拦截后又成功、或最终不是质量门错误时，挂起的用量不结算。
func SettleDeferredQualityCharge(c *gin.Context, info *relaycommon.RelayInfo, qerr *types.NewAPIError) {
	if info == nil || info.QualityChargePending == nil || !types.IsResponseQualityFilterError(qerr) {
		return
	}
	usage := info.QualityChargePending
	info.QualityChargePending = nil
	ChargeQualityFilterIfNeeded(c, info, usage, qerr)
}

func ApplyResponseQualityFilter(c *gin.Context, info *relaycommon.RelayInfo, usage any) *types.NewAPIError {
	if qualityIsProbe(c, info) {
		return nil
	}
	flags := responseQualityFlagsFor(info)
	if !flags.apology && !flags.lowToken {
		return nil
	}

	inspect := relaycommon.QualityInspect{}
	if info != nil {
		inspect = info.QualityInspect
	}
	tokens := qualityCompletionTokens(usage, inspect, info)
	if qualitySkipNonText(usage, inspect) {
		return nil
	}

	cfg := operation_setting.GetResponseQualitySetting()
	if flags.apology && containsApologyWithinThreshold(inspect.Text, info, cfg.EffectiveApologyKeywords()) {
		qualityLog(c, fmt.Sprintf("response quality filter: apology blocked, channel=%d tokens=%d preview=%q",
			qualityChannelID(info), tokens, qualityPreview(inspect.Text)))
		if info != nil {
			info.ClearSentResponse()
		}
		return qualityFilterError(cfg, fmt.Errorf("%s", cfg.EffectiveApologyMessage()), types.ErrorCodeResponseQualityApology, cfg.EffectiveApologyStatusCode())
	}

	threshold := cfg.EffectiveLowTokenThreshold()
	if flags.lowToken && tokens < threshold && !qualityRequestedShortOutput(info, threshold) {
		qualityLog(c, fmt.Sprintf("response quality filter: low token blocked, channel=%d tokens=%d threshold=%d",
			qualityChannelID(info), tokens, threshold))
		if info != nil {
			info.ClearSentResponse()
		}
		return qualityFilterError(cfg, fmt.Errorf("%s", cfg.EffectiveLowTokenMessage(tokens, threshold)), types.ErrorCodeResponseQualityLowToken, cfg.EffectiveLowTokenStatusCode())
	}
	return nil
}

func qualityApologyError(cfg *operation_setting.ResponseQualitySetting) *types.NewAPIError {
	return qualityFilterError(cfg, fmt.Errorf("%s", cfg.EffectiveApologyMessage()), types.ErrorCodeResponseQualityApology, cfg.EffectiveApologyStatusCode())
}

func qualityFilterError(cfg *operation_setting.ResponseQualitySetting, err error, code types.ErrorCode, status int) *types.NewAPIError {
	if cfg != nil && cfg.RetryAfterBlock() {
		return types.NewErrorWithStatusCode(err, code, status)
	}
	return types.NewErrorWithStatusCode(err, code, status, types.ErrOptionWithSkipRetry())
}

// ContainsApology 只在低 token 阈值内的前缀里找道歉词。整段不足阈值时扫描整段。
func ContainsApology(text string, phrases []string) bool {
	threshold := operation_setting.GetResponseQualitySetting().EffectiveLowTokenThreshold()
	return apologyMatches(qualityTokenPrefix(text, "", threshold), phrases)
}

func containsApologyWithinThreshold(text string, info *relaycommon.RelayInfo, phrases []string) bool {
	model := ""
	if info != nil && info.ChannelMeta != nil {
		model = info.UpstreamModelName
	}
	threshold := operation_setting.GetResponseQualitySetting().EffectiveLowTokenThreshold()
	return apologyMatches(qualityTokenPrefix(text, model, threshold), phrases)
}

func apologyMatches(text string, phrases []string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	normalized := qualityNormalize(text)
	if normalized == "" {
		return false
	}
	if len(phrases) == 0 {
		phrases = operation_setting.GetResponseQualitySetting().EffectiveApologyKeywords()
	}
	for _, phrase := range phrases {
		if phrase != "" && strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

// qualityTokenPrefix 返回 token 数不超过阈值的最长前缀。
// 流式过线那一块不走这里，调用方会把整块交给 apologyMatches。
func qualityTokenPrefix(text, model string, threshold int) string {
	if threshold <= 0 {
		threshold = operation_setting.DefaultLowTokenThreshold
	}
	if text == "" || CountTextToken(text, model) <= threshold {
		return text
	}
	runes := []rune(text)
	lo, hi := 1, len(runes)
	best := 1
	for lo <= hi {
		mid := (lo + hi) / 2
		if CountTextToken(string(runes[:mid]), model) <= threshold {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return string(runes[:best])
}

func qualityNormalize(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	prevSpace := false
	for _, r := range strings.ToLower(text) {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func qualityCompletionTokens(usage any, inspect relaycommon.QualityInspect, info *relaycommon.RelayInfo) int {
	if u := qualityUsage(usage); u != nil && u.CompletionTokens > 0 {
		return u.CompletionTokens
	}
	model := ""
	if info != nil && info.ChannelMeta != nil {
		model = info.UpstreamModelName
	}
	tokens := 0
	if strings.TrimSpace(inspect.Text) != "" {
		tokens += CountTextToken(inspect.Text, model)
	}
	if strings.TrimSpace(inspect.Reasoning) != "" {
		tokens += CountTextToken(inspect.Reasoning, model)
	}
	if tokens > 0 {
		return tokens
	}
	if u := qualityUsage(usage); u != nil {
		return u.CompletionTokens
	}
	return 0
}

func qualityUsage(usage any) *dto.Usage {
	switch u := usage.(type) {
	case *dto.Usage:
		return u
	case dto.Usage:
		return &u
	default:
		return nil
	}
}

func qualitySkipNonText(usage any, inspect relaycommon.QualityInspect) bool {
	if inspect.ToolCalls > 0 || inspect.Media {
		return true
	}
	u := qualityUsage(usage)
	if u == nil {
		return false
	}
	return u.CompletionTokenDetails.AudioTokens > 0 || u.CompletionTokenDetails.ImageTokens > 0
}

func qualityRequestedShortOutput(info *relaycommon.RelayInfo, threshold int) bool {
	maxTokens := qualityRequestedMaxTokens(info)
	return maxTokens > 0 && maxTokens < threshold
}

func qualityRequestedMaxTokens(info *relaycommon.RelayInfo) int {
	if info == nil || info.Request == nil {
		return 0
	}
	switch r := info.Request.(type) {
	case *dto.GeneralOpenAIRequest:
		completion := int(lo.FromPtrOr(r.MaxCompletionTokens, 0))
		legacy := int(lo.FromPtrOr(r.MaxTokens, 0))
		if completion > legacy {
			return completion
		}
		return legacy
	case *dto.OpenAIResponsesRequest:
		return int(lo.FromPtrOr(r.MaxOutputTokens, 0))
	case *dto.ClaudeRequest:
		sample := int(lo.FromPtrOr(r.MaxTokensToSample, 0))
		maxTokens := int(lo.FromPtrOr(r.MaxTokens, 0))
		if sample > maxTokens {
			return sample
		}
		return maxTokens
	case *dto.GeminiChatRequest:
		if r.GenerationConfig.MaxOutputTokens != nil {
			return int(*r.GenerationConfig.MaxOutputTokens)
		}
	}
	return 0
}

func qualityChannelID(info *relaycommon.RelayInfo) int {
	if info == nil || info.ChannelMeta == nil {
		return 0
	}
	return info.ChannelId
}

func qualityLog(c *gin.Context, msg string) {
	if c == nil {
		return
	}
	logger.LogWarn(c, msg)
}

func qualityPreview(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= 80 {
		return string(runes)
	}
	return string(runes[:80])
}

func NoteQualityInspectFromOpenAIText(info *relaycommon.RelayInfo, resp *dto.OpenAITextResponse) {
	if info == nil || resp == nil {
		return
	}
	for _, choice := range resp.Choices {
		info.AppendQualityInspectText(choice.Message.StringContent())
		if tools := choice.Message.ParseToolCalls(); len(tools) > 0 {
			info.AddQualityInspectTools(len(tools))
		}
	}
}

func NoteQualityInspectFromClaude(info *relaycommon.RelayInfo, resp *dto.ClaudeResponse) {
	if info == nil || resp == nil {
		return
	}
	if resp.Completion != "" {
		info.AppendQualityInspectText(resp.Completion)
	}
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			info.AppendQualityInspectText(block.GetText())
		case "tool_use":
			info.AddQualityInspectTools(1)
		}
	}
}

func NoteQualityInspectFromGemini(info *relaycommon.RelayInfo, resp *dto.GeminiChatResponse) {
	if info == nil || resp == nil {
		return
	}
	for _, candidate := range resp.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.FunctionCall != nil {
				info.AddQualityInspectTools(1)
				continue
			}
			if part.Thought || part.Text == "" {
				continue
			}
			info.AppendQualityInspectText(part.Text)
		}
	}
}

func NoteQualityInspectFromResponses(info *relaycommon.RelayInfo, resp *dto.OpenAIResponsesResponse) {
	if info == nil || resp == nil {
		return
	}
	info.AppendQualityInspectText(ExtractOutputTextFromResponses(resp))
	for _, output := range resp.Output {
		switch output.Type {
		case "function_call", dto.BuildInCallWebSearchCall, dto.ResponsesOutputTypeImageGenerationCall:
			info.AddQualityInspectTools(1)
		}
	}
}
