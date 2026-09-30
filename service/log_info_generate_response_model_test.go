package service

import (
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateTextOtherInfoRecordsResponseModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-6-astra",
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "gpt-6-astra"},
	}
	info.ObserveResponseModel("gpt-5.6-luna")
	other := GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 0, 0, 1)
	got, ok := other["response_model"].(relaycommon.ResponseModel)
	require.True(t, ok)
	assert.True(t, got.Mismatch)
	assert.Equal(t, "gpt-6-astra", got.RequestedModel)
	assert.Equal(t, "gpt-5.6-luna", got.ReturnedModel)
}

func TestGenerateTextOtherInfoOmitsUnchangedResponseModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		OriginModelName: "requested",
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "requested"},
	}
	info.ObserveResponseModel("requested")
	other := GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 0, 0, 1)
	assert.NotContains(t, other, "response_model")
}
