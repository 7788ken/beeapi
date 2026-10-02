package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

func TestTypeSafeChannelMapsToItsOwnApiTypeAndEndpoint(t *testing.T) {
	apiType, ok := ChannelType2APIType(constant.ChannelTypeTypeSafe)
	require.True(t, ok)
	require.Equal(t, constant.APITypeTypeSafe, apiType)

	eps := GetEndpointTypesByChannelType(constant.ChannelTypeTypeSafe, "jev-latest")
	require.Equal(t, []constant.EndpointType{constant.EndpointTypeTypeSafeSystemOne}, eps)

	info, ok := GetDefaultEndpointInfo(constant.EndpointTypeTypeSafeSystemOne)
	require.True(t, ok)
	require.Equal(t, "/v1/systemone", info.Path)
	require.Equal(t, "https://api.typesafe.ai", constant.ChannelBaseURLs[constant.ChannelTypeTypeSafe])
	require.Equal(t, "TypeSafe", constant.GetChannelTypeName(constant.ChannelTypeTypeSafe))
}
