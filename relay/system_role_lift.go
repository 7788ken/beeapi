package relay

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

// hasSystemRoleMessage reports whether any message carries the OpenAI-style
// "system" role, which the native Anthropic Messages API rejects outright.
func hasSystemRoleMessage(messages []dto.ClaudeMessage) bool {
	for _, message := range messages {
		if message.Role == "system" {
			return true
		}
	}
	return false
}

// liftSystemRoleMessages normalizes OpenAI-style requests that put system
// instructions inside messages[].role=="system": the text blocks are lifted
// into the top-level system field (existing top-level system first, then
// message systems in original order) and the system messages are removed.
// Requests without a system-role message are returned untouched.
func liftSystemRoleMessages(request *dto.ClaudeRequest) {
	if !hasSystemRoleMessage(request.Messages) {
		return
	}

	var lifted []dto.ClaudeMediaMessage
	appendTextBlock := func(text string, cacheControl json.RawMessage) {
		if text == "" {
			return
		}
		block := dto.ClaudeMediaMessage{
			Type:         dto.ContentTypeText,
			CacheControl: cacheControl,
		}
		block.SetText(text)
		lifted = append(lifted, block)
	}

	kept := make([]dto.ClaudeMessage, 0, len(request.Messages))
	for _, message := range request.Messages {
		if message.Role != "system" {
			kept = append(kept, message)
			continue
		}
		switch content := message.Content.(type) {
		case string:
			appendTextBlock(content, nil)
		default:
			blocks, err := message.ParseContent()
			if err != nil {
				continue
			}
			for _, block := range blocks {
				if block.Type != dto.ContentTypeText {
					continue
				}
				appendTextBlock(block.GetText(), block.CacheControl)
			}
		}
	}

	if len(kept) == 0 {
		kept = append(kept, dto.ClaudeMessage{Role: "user", Content: "..."})
	}

	if len(lifted) > 0 {
		request.System = mergeClaudeSystem(request.System, lifted)
	}
	request.Messages = kept
}

// mergeClaudeSystem combines an existing top-level system (string or block
// array) with lifted system blocks, preserving the original ordering.
func mergeClaudeSystem(existing any, lifted []dto.ClaudeMediaMessage) []dto.ClaudeMediaMessage {
	var merged []dto.ClaudeMediaMessage
	switch system := existing.(type) {
	case nil:
	case string:
		if system != "" {
			block := dto.ClaudeMediaMessage{Type: dto.ContentTypeText}
			block.SetText(system)
			merged = append(merged, block)
		}
	default:
		if blocks, err := common.Any2Type[[]dto.ClaudeMediaMessage](existing); err == nil {
			merged = append(merged, blocks...)
		}
	}
	return append(merged, lifted...)
}
