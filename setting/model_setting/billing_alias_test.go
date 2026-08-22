package model_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveBillingModelName(t *testing.T) {
	require.Equal(t, "gpt-5.6-sol", ResolveBillingModelName("gpt-5.6"))
	require.Equal(t, "gpt-5.6-sol", ResolveBillingModelName("gpt-5.6-sol"))
	require.Equal(t, "gpt-5.6-terra", ResolveBillingModelName("gpt-5.6-terra"))
	require.Equal(t, "gpt-5.6-luna", ResolveBillingModelName("gpt-5.6-luna"))
	require.Equal(t, "gpt-5.6-sol-openai-compact", ResolveBillingModelName("gpt-5.6-sol-openai-compact"))
}
