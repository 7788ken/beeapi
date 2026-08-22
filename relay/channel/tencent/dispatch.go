package tencent

import (
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

const tokenHubBaseURL = "https://tokenhub.tencentmaas.com"

// DispatchAdaptor keeps three-segment TC3 credentials on the native protocol
// and sends a single TokenHub key through the OpenAI-compatible protocol.
// A malformed two-segment key stays on the native adaptor and fails explicitly
// in parseTencentConfig instead of being silently interpreted as TokenHub.
type DispatchAdaptor struct {
	channel.Adaptor
}

func (a *DispatchAdaptor) Init(info *relaycommon.RelayInfo) {
	if strings.Contains(info.ApiKey, "|") {
		a.Adaptor = &Adaptor{}
	} else {
		a.Adaptor = &openai.Adaptor{}
		if info.ChannelBaseUrl == "" ||
			info.ChannelBaseUrl == constant.ChannelBaseURLs[constant.ChannelTypeTencent] {
			info.ChannelBaseUrl = tokenHubBaseURL
		}
	}
	a.Adaptor.Init(info)
}

func (a *DispatchAdaptor) GetModelList() []string {
	return ModelList
}

func (a *DispatchAdaptor) GetChannelName() string {
	return ChannelName
}
