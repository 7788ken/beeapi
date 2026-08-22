package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestUsageParsesOpenAICacheWriteTokens(t *testing.T) {
	var chatUsage Usage
	err := common.Unmarshal([]byte(`{
		"prompt_tokens": 3619,
		"completion_tokens": 36,
		"prompt_tokens_details": {
			"cached_tokens": 2921,
			"cache_write_tokens": 3616
		}
	}`), &chatUsage)
	require.NoError(t, err)
	require.Equal(t, 3616, chatUsage.PromptTokensDetails.CacheWriteTokens)
	require.Equal(t, 3616, chatUsage.PromptTokensDetails.CacheCreationTokensTotal())

	var responsesUsage Usage
	err = common.Unmarshal([]byte(`{
		"input_tokens": 1473,
		"output_tokens": 19,
		"input_tokens_details": {
			"cache_write_tokens": 1470
		}
	}`), &responsesUsage)
	require.NoError(t, err)
	require.NotNil(t, responsesUsage.InputTokensDetails)
	require.Equal(t, 1470, responsesUsage.InputTokensDetails.CacheWriteTokens)
}

func TestCacheCreationTokensTotalUsesLargerCompatibleValue(t *testing.T) {
	details := InputTokenDetails{
		CachedCreationTokens: 120,
		CacheWriteTokens:     100,
	}
	require.Equal(t, 120, details.CacheCreationTokensTotal())

	details.CacheWriteTokens = 150
	require.Equal(t, 150, details.CacheCreationTokensTotal())
}
