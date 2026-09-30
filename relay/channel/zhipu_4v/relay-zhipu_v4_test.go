package zhipu_4v

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestOpenAI2ZhipuKeepsReasoningEffort(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:           "glm-5",
		ReasoningEffort: "low",
		THINKING:        json.RawMessage(`{"type":"enabled"}`),
		Messages: []dto.Message{{
			Role:    "user",
			Content: "hi",
		}},
	}

	got := requestOpenAI2Zhipu(req)
	require.NotNil(t, got)
	assert.Equal(t, "low", got.ReasoningEffort)
	assert.JSONEq(t, `{"type":"enabled"}`, string(got.THINKING))

	raw, err := json.Marshal(got)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	assert.Equal(t, "low", payload["reasoning_effort"])
}

func TestRequestOpenAI2ZhipuOmitsEmptyReasoningEffort(t *testing.T) {
	got := requestOpenAI2Zhipu(dto.GeneralOpenAIRequest{
		Model: "glm-4-flash",
		Messages: []dto.Message{{
			Role:    "user",
			Content: "hi",
		}},
	})
	require.NotNil(t, got)
	assert.Empty(t, got.ReasoningEffort)

	raw, err := json.Marshal(got)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	_, present := payload["reasoning_effort"]
	assert.False(t, present)
}
