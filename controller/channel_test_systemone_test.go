package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// TypeSafe 渠道的自动测试必须走 /v1/systemone 探针，而不是聊天请求（否则会被误禁）。
func TestChannelTestForTypeSafeUsesSystemOneProbe(t *testing.T) {
	ch := &model.Channel{Type: constant.ChannelTypeTypeSafe}
	endpoint := normalizeChannelTestEndpoint(ch, "jev-latest", "")
	require.Equal(t, string(constant.EndpointTypeTypeSafeSystemOne), endpoint)

	req := buildTestRequest("jev-latest", endpoint, ch, false)
	probe, ok := req.(*dto.SystemOneRequest)
	require.True(t, ok)
	require.Equal(t, "jev-latest", probe.Model)
	require.NotEmpty(t, probe.State)
	require.Len(t, probe.Questions, 1)
	require.False(t, probe.IsStream(nil))
}
