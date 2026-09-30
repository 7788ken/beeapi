package common

import "github.com/QuantumNous/new-api/dto"

// ApplyOpenAIChatStreamUsage asks the upstream for a stream usage frame when
// the outbound request is OpenAI chat and the channel advertises stream
// options. Cross-protocol converters do not copy stream_options; without this,
// a missing usage frame plus 0-output refund can settle a real stream at zero.
func ApplyOpenAIChatStreamUsage(req *dto.GeneralOpenAIRequest, info *RelayInfo) {
	if req == nil || info == nil || info.ChannelMeta == nil || !info.SupportStreamOptions || !info.IsStream {
		return
	}
	if req.StreamOptions == nil {
		req.StreamOptions = &dto.StreamOptions{}
	}
	req.StreamOptions.IncludeUsage = true
}
