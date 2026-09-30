package service

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestSmokeBE61TieredParamsReadsOutputImageTokens(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     7,
		CompletionTokens: 1124,
		PromptTokensDetails: dto.InputTokenDetails{
			ImageTokens: 2,
			TextTokens:  1,
		},
		CompletionTokenDetails: dto.OutputTokenDetails{
			ImageTokens: 1120,
			TextTokens:  4,
		},
	}

	params := BuildTieredTokenParams(usage, false, map[string]bool{"img": true, "img_o": true})
	require.Equal(t, float64(2), params.Img)
	require.Equal(t, float64(1120), params.ImgO)
	require.Equal(t, float64(4), params.C)
}
