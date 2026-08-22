package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGPT56AliasUsesSolBillingDefaults(t *testing.T) {
	name := formatBillingModelName("gpt-5.6")
	require.Equal(t, "gpt-5.6-sol", name)
	require.Equal(t, 2.5, defaultModelRatio[name])
	require.Equal(t, 0.1, defaultCacheRatio[name])
	require.Equal(t, 1.25, defaultCreateCacheRatio[name])

	completionRatio, locked := getHardcodedCompletionModelRatio(name)
	require.Equal(t, 6.0, completionRatio)
	require.False(t, locked)
}

func TestGPT56CompactNamesRemainUnchanged(t *testing.T) {
	require.Equal(
		t,
		"gpt-5.6-sol-openai-compact",
		formatBillingModelName("gpt-5.6-sol-openai-compact"),
	)
}
