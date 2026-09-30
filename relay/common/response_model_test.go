package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObserveResponseModel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		models   []string
		returned string
		mismatch bool
	}{
		{name: "absent", models: []string{"", " "}},
		{name: "requested model", models: []string{"requested"}, returned: "requested"},
		{name: "mapped model", models: []string{"mapped"}, returned: "mapped"},
		{name: "different model", models: []string{"other"}, returned: "other", mismatch: true},
		{name: "case differs", models: []string{"Requested"}, returned: "Requested"},
		{name: "requested prefix", models: []string{"requested-2026-09-01"}, returned: "requested-2026-09-01"},
		{name: "mapped case differs", models: []string{"MAPPED"}, returned: "MAPPED"},
		{name: "mapped prefix", models: []string{"mapped-2026-09-01"}, returned: "mapped-2026-09-01"},
		{name: "reverse prefix still warns", models: []string{"request"}, returned: "request", mismatch: true},
		{name: "substring still warns", models: []string{"other-requested"}, returned: "other-requested", mismatch: true},
		{name: "compatible difference survives matching frames", models: []string{"mapped", "Requested", "", "requested"}, returned: "Requested"},
		{name: "warning supersedes compatible difference", models: []string{"Requested", "other"}, returned: "other", mismatch: true},
		{name: "compatible difference cannot erase warning", models: []string{"other", "Requested"}, returned: "other", mismatch: true},
		{name: "mismatch survives later frames", models: []string{"mapped", "other", "", "requested"}, returned: "other", mismatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &RelayInfo{
				OriginModelName: "requested",
				ChannelMeta:     &ChannelMeta{UpstreamModelName: "mapped", IsModelMapped: true},
			}
			for _, name := range tc.models {
				info.ObserveResponseModel(name)
			}
			if tc.returned == "" {
				assert.Nil(t, info.ResponseModel)
				return
			}
			require.NotNil(t, info.ResponseModel)
			assert.Equal(t, &ResponseModel{
				RequestedModel: "requested",
				UpstreamModel:  "mapped",
				ReturnedModel:  tc.returned,
				Mismatch:       tc.mismatch,
			}, info.ResponseModel)
			assert.Equal(t, "mapped", info.UpstreamModelName)
		})
	}
}

func TestShouldRecordResponseModel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		upstream string
		returned string
		mapped   bool
		record   bool
	}{
		{name: "same model", upstream: "requested", returned: "requested"},
		{name: "no upstream name", returned: "requested"},
		{name: "mapped model", upstream: "mapped", returned: "mapped", mapped: true, record: true},
		{name: "mapped response echoes request", upstream: "mapped", returned: "requested", mapped: true, record: true},
		{name: "prefix difference", upstream: "requested", returned: "requested-2026-09-01", record: true},
		{name: "case difference", upstream: "requested", returned: "REQUESTED", record: true},
		{name: "mismatch", upstream: "requested", returned: "other", record: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &RelayInfo{
				OriginModelName: "requested",
				ChannelMeta:     &ChannelMeta{UpstreamModelName: tc.upstream, IsModelMapped: tc.mapped},
			}
			info.ObserveResponseModel(tc.returned)
			require.NotNil(t, info.ResponseModel)
			assert.Equal(t, tc.record, ShouldRecordResponseModel(info))
		})
	}
}

func TestObserveResponseModelEmptyExpectedNamesDoNotMatchEveryPrefix(t *testing.T) {
	for _, tc := range []struct{ requested, upstream string }{
		{},
		{upstream: "mapped"},
		{requested: "requested"},
	} {
		t.Run(tc.requested+"/"+tc.upstream, func(t *testing.T) {
			info := &RelayInfo{
				OriginModelName: tc.requested,
				ChannelMeta:     &ChannelMeta{UpstreamModelName: tc.upstream},
			}
			info.ObserveResponseModel("other")
			require.NotNil(t, info.ResponseModel)
			assert.True(t, info.ResponseModel.Mismatch)
		})
	}
}

func TestObserveResponseModelNilSafe(t *testing.T) {
	var info *RelayInfo
	info.ObserveResponseModel("gpt-5.6-luna")
	assert.Nil(t, info)
}
