package common

import "strings"

// ResponseModel records upstream declarations before response conversion. It is
// diagnostic only: it must never change routing, pricing, or downstream output.
type ResponseModel struct {
	RequestedModel string `json:"requested_model"`
	UpstreamModel  string `json:"upstream_model"`
	ReturnedModel  string `json:"returned_model"`
	Mismatch       bool   `json:"mismatch"`
}

func (info *RelayInfo) GetUpstreamModelName() string {
	if info == nil || info.ChannelMeta == nil {
		return ""
	}
	return info.ChannelMeta.UpstreamModelName
}

// ObserveResponseModel retains the first differing model for inspection, with
// mismatches taking priority over prefix or case-only differences. A later
// matching or empty event cannot erase it. Only observe upstream declarations,
// never models synthesized by a response converter.
func (info *RelayInfo) ObserveResponseModel(model string) {
	if info == nil || strings.TrimSpace(model) == "" {
		return
	}
	if info.ResponseModel == nil {
		info.ResponseModel = &ResponseModel{
			RequestedModel: info.OriginModelName,
			UpstreamModel:  info.GetUpstreamModelName(),
		}
	}
	observation := info.ResponseModel
	if observation.Mismatch {
		return
	}
	mismatch := true
	for _, expected := range []string{observation.RequestedModel, observation.UpstreamModel} {
		if expected != "" && (strings.HasPrefix(model, expected) || strings.EqualFold(model, expected)) {
			mismatch = false
			break
		}
	}
	if !mismatch && observation.ReturnedModel != "" &&
		observation.ReturnedModel != observation.RequestedModel && observation.ReturnedModel != observation.UpstreamModel {
		return
	}
	observation.ReturnedModel = model
	observation.Mismatch = mismatch
}

func ShouldRecordResponseModel(info *RelayInfo) bool {
	if info == nil || info.ResponseModel == nil {
		return false
	}
	observation := info.ResponseModel
	if !observation.Mismatch &&
		observation.ReturnedModel == observation.RequestedModel &&
		(observation.UpstreamModel == "" || observation.UpstreamModel == observation.RequestedModel) &&
		(info.ChannelMeta == nil || !info.IsModelMapped) {
		return false
	}
	return true
}
