package constant

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPath2RelayModeSystemOne(t *testing.T) {
	require.Equal(t, RelayModeSystemOne, Path2RelayMode("/v1/systemone"))
	require.Equal(t, RelayModeRerank, Path2RelayMode("/v1/rerank"))
	require.Equal(t, RelayModeChatCompletions, Path2RelayMode("/v1/chat/completions"))
}
