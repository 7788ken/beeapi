package service

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// QualityStreamDelta 是一块已经解析好的流式输出。可见正文和思考分开。
type QualityStreamDelta struct {
	Visible   string
	Reasoning string
	ToolCalls int
	Media     bool
}

func (d QualityStreamDelta) empty() bool {
	return d.Visible == "" && d.Reasoning == "" && d.ToolCalls == 0 && !d.Media
}

func QualityStreamHoldActive(c *gin.Context) bool {
	hold := qualityHoldFrom(c)
	return hold != nil && hold.active && !hold.released && !hold.blocked && !hold.finished
}

// QualityStreamBlocked 为真时本轮已在放行前命中道歉，读取上游的循环应立即停止。
func QualityStreamBlocked(c *gin.Context) bool {
	hold := qualityHoldFrom(c)
	return hold != nil && hold.active && hold.blocked && !hold.finished
}

func BeginSkipQualityStreamNote(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(qualitySkipNoteKey, skipQualityStreamNoteDepth(c)+1)
}

func EndSkipQualityStreamNote(c *gin.Context) {
	if c == nil {
		return
	}
	depth := skipQualityStreamNoteDepth(c) - 1
	if depth <= 0 {
		delete(c.Keys, qualitySkipNoteKey)
		return
	}
	c.Set(qualitySkipNoteKey, depth)
}

func SkippingQualityStreamNote(c *gin.Context) bool {
	return skipQualityStreamNoteDepth(c) > 0
}

func skipQualityStreamNoteDepth(c *gin.Context) int {
	if c == nil {
		return 0
	}
	depth, ok := c.Get(qualitySkipNoteKey)
	if !ok {
		return 0
	}
	n, ok := depth.(int)
	if !ok || n < 0 {
		return 0
	}
	return n
}

func qualityHoldFrom(c *gin.Context) *QualityHold {
	if c == nil {
		return nil
	}
	value, ok := c.Get(qualityHoldContextKey)
	if !ok {
		return nil
	}
	hold, _ := value.(*QualityHold)
	return hold
}

// NoteQualityStreamJSON 从即将写出的 SSE data 载荷里取可见正文、思考和工具调用。
func NoteQualityStreamJSON(c *gin.Context, payload string) {
	if !QualityStreamHoldActive(c) || skipQualityStreamNoteDepth(c) > 0 {
		return
	}
	payload = strings.TrimSpace(payload)
	payload = strings.TrimPrefix(payload, "data:")
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "[DONE]" || strings.HasPrefix(payload, ":") {
		return
	}
	var chat dto.ChatCompletionsStreamResponse
	if err := common.UnmarshalJsonStr(payload, &chat); err == nil {
		delta := deltaFromChat(&chat)
		if !delta.empty() {
			NoteQualityStreamDelta(c, delta)
			return
		}
	}
	var completion dto.CompletionsStreamResponse
	if err := common.UnmarshalJsonStr(payload, &completion); err != nil {
		return
	}
	var visible strings.Builder
	for _, choice := range completion.Choices {
		visible.WriteString(choice.Text)
	}
	if visible.Len() == 0 {
		return
	}
	NoteQualityStreamDelta(c, QualityStreamDelta{Visible: visible.String()})
}

func NoteQualityChatChunk(c *gin.Context, resp *dto.ChatCompletionsStreamResponse) {
	if resp == nil {
		return
	}
	NoteQualityStreamDelta(c, deltaFromChat(resp))
}

func NoteQualityStreamDelta(c *gin.Context, delta QualityStreamDelta) {
	hold := qualityHoldFrom(c)
	if hold == nil || !hold.active || hold.released || hold.blocked || hold.finished || hold.info == nil || delta.empty() {
		return
	}
	hold.info.AppendQualityInspectText(delta.Visible)
	hold.info.AppendQualityInspectReasoning(delta.Reasoning)
	if delta.ToolCalls > 0 {
		hold.info.AddQualityInspectTools(delta.ToolCalls)
	}
	if delta.Media {
		hold.info.MarkQualityInspectMedia()
	}
	hold.observe()
}

func NoteClaudeStreamDelta(c *gin.Context, resp *dto.ClaudeResponse) {
	if resp == nil {
		return
	}
	NoteQualityStreamDelta(c, deltaFromClaude(resp))
}

func NoteGeminiStreamDelta(c *gin.Context, resp *dto.GeminiChatResponse) {
	if resp == nil {
		return
	}
	NoteQualityStreamDelta(c, deltaFromGemini(resp))
}

func NoteResponsesStreamDelta(c *gin.Context, resp *dto.ResponsesStreamResponse, raw string) {
	if !QualityStreamHoldActive(c) {
		return
	}
	if resp == nil || (resp.Delta == "" && resp.Item == nil && resp.Response == nil && strings.TrimSpace(raw) != "") {
		var parsed dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(strings.TrimSpace(raw), &parsed); err == nil {
			resp = &parsed
		}
	}
	if resp == nil {
		return
	}
	NoteQualityStreamDelta(c, deltaFromResponses(resp))
}

func (h *QualityHold) observe() {
	if h == nil || !h.active || h.finished || h.released || h.blocked || h.info == nil {
		return
	}
	inspect := h.info.QualityInspect
	if inspect.ToolCalls > 0 || inspect.Media {
		_ = h.Flush()
		return
	}
	flags := responseQualityFlagsFor(h.info)
	cfg := operation_setting.GetResponseQualitySetting()
	threshold := cfg.EffectiveLowTokenThreshold()
	if flags.apology && apologyMatches(inspect.Text, cfg.EffectiveApologyKeywords()) {
		h.block(cfg)
		return
	}
	model := ""
	if h.info.ChannelMeta != nil {
		model = h.info.UpstreamModelName
	}
	if strings.TrimSpace(inspect.Text) != "" && CountTextToken(inspect.Text, model) >= threshold {
		_ = h.Flush()
	}
}

func (h *QualityHold) block(cfg *operation_setting.ResponseQualitySetting) {
	if h == nil || !h.active || h.finished || h.released || h.blocked {
		return
	}
	h.blocked = true
	h.blockErr = qualityApologyError(cfg)
	if h.writer != nil {
		h.writer.drop = true
	}
	qualityLog(h.c, "response quality filter: apology blocked before release, channel="+strconv.Itoa(qualityChannelID(h.info)))
}

func deltaFromChat(resp *dto.ChatCompletionsStreamResponse) QualityStreamDelta {
	var delta QualityStreamDelta
	if resp == nil {
		return delta
	}
	var visible, reasoning strings.Builder
	for _, choice := range resp.Choices {
		visible.WriteString(choice.Delta.GetContentString())
		reasoning.WriteString(choice.Delta.GetReasoningContent())
		if len(choice.Delta.ToolCalls) > 0 {
			delta.ToolCalls = 1
		}
	}
	if resp.Usage != nil && (resp.Usage.CompletionTokenDetails.AudioTokens > 0 || resp.Usage.CompletionTokenDetails.ImageTokens > 0) {
		delta.Media = true
	}
	delta.Visible = visible.String()
	delta.Reasoning = reasoning.String()
	return delta
}

func deltaFromClaude(resp *dto.ClaudeResponse) QualityStreamDelta {
	var delta QualityStreamDelta
	if resp.Type == "content_block_start" && resp.ContentBlock != nil {
		switch resp.ContentBlock.Type {
		case "tool_use", "server_tool_use", "mcp_tool_use":
			delta.ToolCalls = 1
		}
	}
	if resp.Delta != nil {
		if resp.Delta.Text != nil {
			delta.Visible += *resp.Delta.Text
		}
		if resp.Delta.Thinking != nil {
			delta.Reasoning += *resp.Delta.Thinking
		}
		if resp.Delta.Type == "input_json_delta" || resp.Delta.PartialJson != nil {
			delta.ToolCalls = 1
		}
	}
	if resp.Delta == nil && resp.Completion == "" {
		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				delta.Visible += block.GetText()
			case "thinking":
				if block.Thinking != nil {
					delta.Reasoning += *block.Thinking
				}
			case "tool_use", "server_tool_use", "mcp_tool_use":
				delta.ToolCalls++
			}
		}
	}
	if resp.Completion != "" {
		delta.Visible += resp.Completion
	}
	return delta
}

func deltaFromGemini(resp *dto.GeminiChatResponse) QualityStreamDelta {
	var delta QualityStreamDelta
	var visible, reasoning strings.Builder
	for _, candidate := range resp.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil && part.InlineData.MimeType != "" {
				delta.Media = true
			}
			if part.FunctionCall != nil {
				delta.ToolCalls++
			}
			if part.Text == "" {
				continue
			}
			if part.Thought {
				reasoning.WriteString(part.Text)
			} else {
				visible.WriteString(part.Text)
			}
		}
	}
	delta.Visible = visible.String()
	delta.Reasoning = reasoning.String()
	return delta
}

func deltaFromResponses(resp *dto.ResponsesStreamResponse) QualityStreamDelta {
	var delta QualityStreamDelta
	switch resp.Type {
	case "response.output_text.delta":
		delta.Visible = resp.Delta
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		delta.Reasoning = resp.Delta
	case "response.output_item.added", "response.output_item.done":
		if resp.Item != nil {
			switch resp.Item.Type {
			case "function_call", "custom_tool_call", dto.BuildInCallWebSearchCall:
				delta.ToolCalls = 1
			case dto.ResponsesOutputTypeImageGenerationCall:
				delta.Media = true
			}
		}
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		delta.ToolCalls = 1
	case "response.completed", "response.done":
		if resp.Response != nil && resp.Response.HasImageGenerationCall() {
			delta.Media = true
		}
	}
	return delta
}
