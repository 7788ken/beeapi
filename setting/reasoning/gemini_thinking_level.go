package reasoning

import "strings"

var geminiThinkingLevels = []string{"HIGH", "MEDIUM", "LOW", "MINIMAL"}

// NormalizeGeminiThinkingLevel maps HIGH/high/High onto the Gemini enum.
// Unknown values are returned trimmed so callers can still reject them.
func NormalizeGeminiThinkingLevel(level string) string {
	trimmed := strings.TrimSpace(level)
	if trimmed == "" {
		return ""
	}
	for _, canonical := range geminiThinkingLevels {
		if strings.EqualFold(trimmed, canonical) {
			return canonical
		}
	}
	return trimmed
}
