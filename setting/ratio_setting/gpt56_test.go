package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGPT56AliasUsesSolBillingDefaults(t *testing.T) {
	name := formatBillingModelName("gpt-5.6")
	require.Equal(t, "gpt-5.6-sol", name)
	// 倍率 × $2 = $/1M；sol 官方 input $4 / output $20（2026-08-21 促销价）
	require.Equal(t, 2.0, defaultModelRatio[name])
	require.Equal(t, 0.1, defaultCacheRatio[name])
	require.Equal(t, 1.25, defaultCreateCacheRatio[name])

	completionRatio, locked := getHardcodedCompletionModelRatio(name)
	require.Equal(t, 5.0, completionRatio)
	require.False(t, locked)
}

func TestGPT56TerraLunaBillingDefaults(t *testing.T) {
	// terra 官方 $2/$12、luna $0.2/$1.2 ⇒ 输出都是输入的 6 倍
	for name, ratio := range map[string]float64{"gpt-5.6-terra": 1, "gpt-5.6-luna": 0.1} {
		require.Equal(t, ratio, defaultModelRatio[name], name)
		require.Equal(t, 0.1, defaultCacheRatio[name], name)
		require.Equal(t, 1.25, defaultCreateCacheRatio[name], name)

		completionRatio, locked := getHardcodedCompletionModelRatio(name)
		require.Equal(t, 6.0, completionRatio, name)
		require.False(t, locked, name)
	}
}

func TestGPT56CompactNamesRemainUnchanged(t *testing.T) {
	require.Equal(
		t,
		"gpt-5.6-sol-openai-compact",
		formatBillingModelName("gpt-5.6-sol-openai-compact"),
	)
}
