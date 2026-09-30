package reasoning

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeGeminiThinkingLevel(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "HIGH", NormalizeGeminiThinkingLevel("high"))
	assert.Equal(t, "HIGH", NormalizeGeminiThinkingLevel("HIGH"))
	assert.Equal(t, "MEDIUM", NormalizeGeminiThinkingLevel(" Medium "))
	assert.Equal(t, "LOW", NormalizeGeminiThinkingLevel("low"))
	assert.Equal(t, "MINIMAL", NormalizeGeminiThinkingLevel("minimal"))
	assert.Equal(t, "ULTRA", NormalizeGeminiThinkingLevel("ULTRA"))
	assert.Equal(t, "", NormalizeGeminiThinkingLevel("  "))
}
