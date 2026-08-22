package openaicompat

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestResponsesResponseToChatPreservesCacheWriteTokens(t *testing.T) {
	response := &dto.OpenAIResponsesResponse{
		Usage: &dto.Usage{
			InputTokens:  1473,
			OutputTokens: 19,
			TotalTokens:  1492,
			InputTokensDetails: &dto.InputTokenDetails{
				CachedTokens:     1000,
				CacheWriteTokens: 1470,
			},
		},
	}

	_, usage, err := ResponsesResponseToChatCompletionsResponse(response, "resp_1")
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 1000, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 1470, usage.PromptTokensDetails.CacheWriteTokens)
}
