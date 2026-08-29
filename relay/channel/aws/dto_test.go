package aws

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func marshalFormattedRequest(t *testing.T, body string) map[string]any {
	t.Helper()

	awsClaudeReq, err := formatRequest(bytes.NewBufferString(body), http.Header{})
	require.NoError(t, err)

	payload, err := common.Marshal(awsClaudeReq)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, common.Unmarshal(payload, &decoded))
	return decoded
}

func TestFormatRequestKeepsExplicitZeroSamplingParams(t *testing.T) {
	decoded := marshalFormattedRequest(t,
		`{"messages":[{"role":"user","content":"hi"}],"max_tokens":0,"temperature":0,"top_p":0,"top_k":0}`)

	// While these fields were value types, `omitempty` silently dropped an
	// explicit 0 and Bedrock fell back to its own defaults instead of honouring
	// what the caller asked for.
	assert.Equal(t, float64(0), decoded["max_tokens"])
	assert.Equal(t, float64(0), decoded["temperature"])
	assert.Equal(t, float64(0), decoded["top_p"])
	assert.Equal(t, float64(0), decoded["top_k"])
}

func TestFormatRequestOmitsAbsentSamplingParams(t *testing.T) {
	decoded := marshalFormattedRequest(t, `{"messages":[{"role":"user","content":"hi"}]}`)

	for _, key := range []string{"max_tokens", "temperature", "top_p", "top_k", "context_management"} {
		assert.NotContains(t, decoded, key,
			"%s must stay absent when the caller did not send it", key)
	}
}

func TestFormatRequestForwardsNonZeroSamplingParams(t *testing.T) {
	decoded := marshalFormattedRequest(t,
		`{"messages":[{"role":"user","content":"hi"}],"max_tokens":128,"temperature":0.5,"top_p":0.9,"top_k":40}`)

	assert.Equal(t, float64(128), decoded["max_tokens"])
	assert.Equal(t, 0.5, decoded["temperature"])
	assert.Equal(t, 0.9, decoded["top_p"])
	assert.Equal(t, float64(40), decoded["top_k"])
}

func TestFormatRequestForwardsContextManagement(t *testing.T) {
	decoded := marshalFormattedRequest(t,
		`{"messages":[{"role":"user","content":"hi"}],"context_management":{"edits":[{"type":"clear_tool_uses_20250919"}]}}`)

	contextManagement, ok := decoded["context_management"].(map[string]any)
	require.True(t, ok, "context_management must reach Bedrock unchanged")
	assert.Contains(t, contextManagement, "edits")
}

func TestFormatRequestSetsAnthropicVersion(t *testing.T) {
	decoded := marshalFormattedRequest(t, `{"messages":[{"role":"user","content":"hi"}]}`)

	assert.Equal(t, "bedrock-2023-05-31", decoded["anthropic_version"])
}
