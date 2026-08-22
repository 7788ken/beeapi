package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

// TestImageRequestStreamJSON verifies that image requests preserve stream=true.
func TestImageRequestStreamJSON(t *testing.T) {
	var req ImageRequest
	require.NoError(t, req.UnmarshalJSON([]byte(`{"model":"gpt-image-1","prompt":"draw a cat","stream":true}`)))

	require.NotNil(t, req.Stream)
	require.True(t, *req.Stream)
	require.True(t, req.IsStream(nil))
}

func TestImageRequestExplicitFalseStreamJSON(t *testing.T) {
	var req ImageRequest
	require.NoError(t, req.UnmarshalJSON([]byte(`{"model":"gpt-image-1","prompt":"draw a cat","stream":false}`)))

	require.NotNil(t, req.Stream)
	require.False(t, *req.Stream)
	require.False(t, req.IsStream(nil))

	encoded, err := common.Marshal(req)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"gpt-image-1","prompt":"draw a cat","stream":false}`, string(encoded))
}
