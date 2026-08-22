package common

import "strings"

const (
	ModelTypeClaude   = "claude"
	ModelTypeCodex    = "codex"
	ModelTypeOpenAI   = "openai"
	ModelTypeGemini   = "gemini"
	ModelTypeImage    = "image"
	ModelTypeVideo    = "video"
	ModelTypeAudio    = "audio"
	ModelTypeEmbed    = "embedding"
	ModelTypeQwen     = "qwen"
	ModelTypeDeepSeek = "deepseek"
	ModelTypeMoonshot = "moonshot"
	ModelTypeOther    = "other"
)

func ClassifyModelType(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "codex"):
		return ModelTypeCodex
	case strings.Contains(n, "claude") ||
		strings.Contains(n, "sonnet") ||
		strings.Contains(n, "opus") ||
		strings.Contains(n, "haiku") ||
		strings.Contains(n, "anthropic"):
		return ModelTypeClaude
	case strings.Contains(n, "gemini"):
		return ModelTypeGemini
	case strings.Contains(n, "sora") ||
		strings.Contains(n, "seedance") ||
		strings.Contains(n, "runway") ||
		strings.Contains(n, "veo") ||
		strings.Contains(n, "kling") ||
		strings.Contains(n, "wan") ||
		strings.Contains(n, "minimax-video") ||
		strings.Contains(n, "video"):
		return ModelTypeVideo
	case strings.Contains(n, "dall") ||
		strings.Contains(n, "midjourney") ||
		strings.Contains(n, "stable") ||
		strings.Contains(n, "sdxl") ||
		strings.Contains(n, "flux") ||
		strings.Contains(n, "ideogram") ||
		strings.Contains(n, "imagen") ||
		strings.Contains(n, "image") ||
		strings.Contains(n, "recraft"):
		return ModelTypeImage
	case strings.Contains(n, "whisper") ||
		strings.Contains(n, "tts") ||
		strings.Contains(n, "audio") ||
		strings.Contains(n, "voice"):
		return ModelTypeAudio
	case strings.Contains(n, "embed"):
		return ModelTypeEmbed
	case strings.Contains(n, "gpt") ||
		strings.HasPrefix(n, "o1") ||
		strings.HasPrefix(n, "o3") ||
		strings.HasPrefix(n, "o4") ||
		strings.Contains(n, "chatgpt"):
		return ModelTypeOpenAI
	case strings.Contains(n, "qwen"):
		return ModelTypeQwen
	case strings.Contains(n, "deepseek"):
		return ModelTypeDeepSeek
	case strings.Contains(n, "moonshot") ||
		strings.Contains(n, "kimi"):
		return ModelTypeMoonshot
	default:
		return ModelTypeOther
	}
}
