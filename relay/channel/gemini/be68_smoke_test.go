package gemini

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSmokeBE68ConvertOpenAINormalizesThinkingLevel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeGemini,
			UpstreamModelName: "gemini-2.5-pro",
		},
	}
	req := &dto.GeneralOpenAIRequest{
		Model:     "gemini-2.5-pro",
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		ExtraBody: json.RawMessage(`{"google":{"thinking_config":{"thinking_level":"high"}}}`),
	}

	got, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, req)
	require.NoError(t, err)
	geminiReq, ok := got.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.NotNil(t, geminiReq.GenerationConfig.ThinkingConfig)
	require.Equal(t, "HIGH", geminiReq.GenerationConfig.ThinkingConfig.ThinkingLevel)
}

func TestSmokeBE68CovertOpenAI2GeminiNormalizesSuffixAndExtraBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	t.Run("extra_body high", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeGemini,
				UpstreamModelName: "gemini-2.5-pro",
			},
		}
		req := dto.GeneralOpenAIRequest{
			Model:     "gemini-2.5-pro",
			Messages:  []dto.Message{{Role: "user", Content: "hi"}},
			ExtraBody: json.RawMessage(`{"google":{"thinking_config":{"thinking_level":" Medium "}}}`),
		}
		got, err := CovertOpenAI2Gemini(c, req, info)
		require.NoError(t, err)
		require.NotNil(t, got.GenerationConfig.ThinkingConfig)
		require.Equal(t, "MEDIUM", got.GenerationConfig.ThinkingConfig.ThinkingLevel)
	})

	t.Run("model suffix -high", func(t *testing.T) {
		settings := model_setting.GetGeminiSettings()
		old := settings.ThinkingAdapterEnabled
		settings.ThinkingAdapterEnabled = true
		t.Cleanup(func() { settings.ThinkingAdapterEnabled = old })

		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeGemini,
				UpstreamModelName: "gemini-2.5-flash-high",
			},
		}
		req := dto.GeneralOpenAIRequest{
			Model:    "gemini-2.5-flash-high",
			Messages: []dto.Message{{Role: "user", Content: "hi"}},
		}
		got, err := CovertOpenAI2Gemini(c, req, info)
		require.NoError(t, err)
		require.NotNil(t, got.GenerationConfig.ThinkingConfig)
		require.Equal(t, "HIGH", got.GenerationConfig.ThinkingConfig.ThinkingLevel)
	})
}
