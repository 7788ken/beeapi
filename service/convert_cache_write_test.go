package service

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestBuildClaudeUsageFromOpenAICacheWriteUsage(t *testing.T) {
	response := ResponseOpenAI2Claude(&dto.OpenAITextResponse{
		Usage: dto.Usage{
			PromptTokens:     3619,
			CompletionTokens: 36,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:     2921,
				CacheWriteTokens: 3616,
			},
		},
	}, nil)

	require.NotNil(t, response)
	require.NotNil(t, response.Usage)
	require.Equal(t, 0, response.Usage.InputTokens)
	require.Equal(t, 2921, response.Usage.CacheReadInputTokens)
	require.Equal(t, 3616, response.Usage.CacheCreationInputTokens)
	require.Equal(t, 36, response.Usage.OutputTokens)
}
