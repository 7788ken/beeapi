package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

// duration=-1 是 Seedance 视频编辑的自适应时长哨兵，入口校验只对 Seedance 家族放行：
// 渠道类型为豆包/sd 网关视频渠道，或模型名含 seedance（子站 passthrough 时渠道类型不是豆包）。
// 其它平台维持原有 1~3600 规则，-1 之外的负数一律拒绝。
func TestValidateTaskDurationBounds_AdaptiveSentinel(t *testing.T) {
	sdV2 := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}, ChannelMeta: &ChannelMeta{ChannelType: constant.ChannelTypeSdVideoV2}}
	openai := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}, ChannelMeta: &ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
	noMeta := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}

	cases := []struct {
		name    string
		req     TaskSubmitReq
		info    *RelayInfo
		wantErr bool
	}{
		{"sd-v2 渠道 顶层 duration=-1 放行", TaskSubmitReq{Model: "dreamina-seedance-2-5-260628-max", Duration: -1}, sdV2, false},
		{"sd-v2 渠道 seconds=\"-1\" 放行", TaskSubmitReq{Model: "dreamina-seedance-2-5-260628-max", Seconds: "-1"}, sdV2, false},
		{"无渠道元数据 但模型名含 seedance 放行", TaskSubmitReq{Model: "dreamina-seedance-2-5-260628-max", Duration: -1}, noMeta, false},
		{"OpenAI 渠道 passthrough seedance 模型 放行", TaskSubmitReq{Model: "doubao-seedance-2-5-260628", Duration: -1}, openai, false},
		{"OpenAI 渠道 sora-2 duration=-1 仍拒", TaskSubmitReq{Model: "sora-2", Duration: -1}, openai, true},
		{"nil info 非 seedance 模型 duration=-1 拒", TaskSubmitReq{Model: "kling-v1", Duration: -1}, nil, true},
		{"sd-v2 渠道 -2 不是哨兵 拒", TaskSubmitReq{Model: "dreamina-seedance-2-5-260628-max", Duration: -2}, sdV2, true},
		{"超上限仍拒", TaskSubmitReq{Model: "dreamina-seedance-2-5-260628-max", Duration: MaxTaskDurationSeconds + 1}, sdV2, true},
		{"正常时长照常放行", TaskSubmitReq{Model: "sora-2", Duration: 8}, openai, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			taskErr := validateTaskDurationBounds(tc.req, tc.info)
			if tc.wantErr {
				require.NotNil(t, taskErr)
				require.Equal(t, "invalid_seconds", taskErr.Code)
			} else {
				require.Nil(t, taskErr)
			}
		})
	}
}

// 顶层 duration=-1 走完整 ValidateMultipartDirect（子站 passthrough 路径）也应通过，
// 且 -1 以数值原样保留在 task_request 里，供后续透传。
func TestValidateMultipartDirect_AdaptiveDurationPreserved(t *testing.T) {
	body := `{"model":"dreamina-seedance-2-5-260628-max","duration":-1,"content":[` +
		`{"type":"text","text":"edit"},` +
		`{"type":"video_url","video_url":{"url":"https://cdn/src.mp4"},"role":"reference_video"}],` +
		`"metadata":{"ratio":"adaptive","resolution":"480p"}}`
	c := mkMultipartDirectCtx(body)
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}

	require.Nil(t, ValidateMultipartDirect(c, info))
	req, err := GetTaskRequest(c)
	require.NoError(t, err)
	require.Equal(t, AdaptiveTaskDuration, req.Duration)
}
