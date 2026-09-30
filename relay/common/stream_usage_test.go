package common

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestApplyOpenAIChatStreamUsage(t *testing.T) {
	t.Parallel()

	t.Run("sets include_usage when stream and channel support it", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{}
		info := &RelayInfo{
			IsStream:    true,
			ChannelMeta: &ChannelMeta{SupportStreamOptions: true},
		}
		ApplyOpenAIChatStreamUsage(req, info)
		require.NotNil(t, req.StreamOptions)
		require.True(t, req.StreamOptions.IncludeUsage)
	})

	t.Run("keeps an existing include_usage flag", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{StreamOptions: &dto.StreamOptions{IncludeUsage: true}}
		info := &RelayInfo{
			IsStream:    true,
			ChannelMeta: &ChannelMeta{SupportStreamOptions: true},
		}
		ApplyOpenAIChatStreamUsage(req, info)
		require.True(t, req.StreamOptions.IncludeUsage)
	})

	t.Run("skips non-stream", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{}
		info := &RelayInfo{ChannelMeta: &ChannelMeta{SupportStreamOptions: true}}
		ApplyOpenAIChatStreamUsage(req, info)
		require.Nil(t, req.StreamOptions)
	})

	t.Run("skips channels without stream options", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{}
		info := &RelayInfo{IsStream: true, ChannelMeta: &ChannelMeta{}}
		ApplyOpenAIChatStreamUsage(req, info)
		require.Nil(t, req.StreamOptions)
	})
}
