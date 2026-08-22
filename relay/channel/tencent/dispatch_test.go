package tencent

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDispatchAdaptorInit(t *testing.T) {
	tests := []struct {
		name        string
		apiKey      string
		baseURL     string
		wantTC3     bool
		wantBaseURL string
	}{
		{"TC3", "1300000000|AKIDxxxxxxxx|secretxxxxxxxx", constant.ChannelBaseURLs[constant.ChannelTypeTencent], true, constant.ChannelBaseURLs[constant.ChannelTypeTencent]},
		{"TokenHub default", "sk-xxxxxxxx", constant.ChannelBaseURLs[constant.ChannelTypeTencent], false, tokenHubBaseURL},
		{"TokenHub empty", "sk-xxxxxxxx", "", false, tokenHubBaseURL},
		{"TokenHub custom", "sk-xxxxxxxx", "https://proxy.example.com", false, "https://proxy.example.com"},
		{"invalid two segment remains TC3 for explicit validation", "AKIDxxxxxxxx|secretxxxxxxxx", constant.ChannelBaseURLs[constant.ChannelTypeTencent], true, constant.ChannelBaseURLs[constant.ChannelTypeTencent]},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:    constant.ChannelTypeTencent,
				ApiKey:         test.apiKey,
				ChannelBaseUrl: test.baseURL,
			}}
			dispatch := &DispatchAdaptor{}
			dispatch.Init(info)
			require.NotNil(t, dispatch.Adaptor)
			if test.wantTC3 {
				assert.IsType(t, &Adaptor{}, dispatch.Adaptor)
			} else {
				assert.IsType(t, &openai.Adaptor{}, dispatch.Adaptor)
			}
			assert.Equal(t, test.wantBaseURL, info.ChannelBaseUrl)
		})
	}
}

func TestParseTencentConfigRejectsTwoSegmentsExplicitly(t *testing.T) {
	_, _, _, err := parseTencentConfig("AKIDxxxxxxxx|secretxxxxxxxx")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly app_id|secret_id|secret_key")
}
