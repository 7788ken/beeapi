package doubao

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 视频编辑任务：上游要求 duration=-1 + ratio=adaptive。-1 只对支持编辑的 Seedance 2.5 族放行，
// 2.0 及更早版本上传原片会被上游以 TaskTypeConstraint 拒绝，直接在本地拦下。
func TestIsSeedanceDurationAllowed_Adaptive(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"dreamina-seedance-2-5-260628-max", true},
		{"Dreamina-Seedance-2-5-260628-MAX", true},
		{"dreamina-seedance-2-5-hc", true},
		{"doubao-seedance-2-5-260628", true},
		{"dreamina-seedance-2-0-260128-max", false},
		{"doubao-seedance-2-0-fast-260128", false},
		{"doubao-seedance-1-5-pro-251215", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			require.Equal(t, tc.want, isSeedanceDurationAllowed(tc.model, -1))
			// -1 之外的负数任何模型都不放行
			require.False(t, isSeedanceDurationAllowed(tc.model, -2))
		})
	}

	// 错误文案：不支持的模型要点明 -1 仅 2.5 支持；支持的模型在可用取值里列出 -1
	msg := seedanceDurationError("dreamina-seedance-2-0-260128-max", -1).Error()
	require.Contains(t, msg, "seedance 2.5")
	msg = seedanceDurationError("dreamina-seedance-2-5-260628-max", 20).Error()
	require.Contains(t, msg, "-1")
}

// 2.5-max 放开到方舟官方的 4~30s 连续区间；其它模型维持 4~15s，2-5-hc 仍只多放精确值 30。
func TestIsSeedanceDurationAllowed_25MaxRangeTo30(t *testing.T) {
	const max25 = "dreamina-seedance-2-5-260628-max"
	for sec := 4; sec <= 30; sec++ {
		require.True(t, isSeedanceDurationAllowed(max25, sec), "2.5-max 应接受 %ds", sec)
	}
	require.True(t, isSeedanceDurationAllowed("Dreamina-Seedance-2-5-260628-MAX", 25), "大小写不敏感")
	require.False(t, isSeedanceDurationAllowed(max25, 3))
	require.False(t, isSeedanceDurationAllowed(max25, 31))
	require.False(t, isSeedanceDurationAllowed("dreamina-seedance-2-0-260128-max", 20), "2.0-max 不放宽")
	require.False(t, isSeedanceDurationAllowed("dreamina-seedance-2-5-hc", 20), "2-5-hc 仍只放精确值 30")
	require.Contains(t, seedanceDurationError(max25, 31).Error(), "between 4 and 30")
	require.Contains(t, seedanceDurationError("dreamina-seedance-2-0-260128-max", 20).Error(), "between 4 and 15")
}

// resolveSeedanceDuration 三个入口都要能看见 -1（此前 Duration>0 的判断把 -1 当作"未传"，
// 导致 metadata.duration=-1 在本地完全绕过校验）。
func TestResolveSeedanceDuration_AdaptiveFromAllSources(t *testing.T) {
	cases := []struct {
		name string
		req  relaycommon.TaskSubmitReq
	}{
		{"top-level duration", relaycommon.TaskSubmitReq{Duration: -1}},
		{"top-level seconds string", relaycommon.TaskSubmitReq{Seconds: "-1"}},
		{"metadata duration (json float64)", relaycommon.TaskSubmitReq{Metadata: map[string]interface{}{"duration": float64(-1)}}},
		{"metadata duration (multipart int)", relaycommon.TaskSubmitReq{Metadata: map[string]interface{}{"duration": -1}}},
		{"metadata duration (numeric string)", relaycommon.TaskSubmitReq{Metadata: map[string]interface{}{"duration": "-1"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sec, has, err := resolveSeedanceDuration(&tc.req)
			require.NoError(t, err)
			require.True(t, has)
			require.Equal(t, -1, sec)
		})
	}

	_, has, err := resolveSeedanceDuration(&relaycommon.TaskSubmitReq{})
	require.NoError(t, err)
	require.False(t, has, "没传时长时不应视为有值")
}

// 下送 payload：-1 必须以 JSON 数值原样出现在顶层 duration，ratio 在顶层（上游 seedance 线协议位置），
// 不能被丢弃、转字符串或覆写成默认秒数。三种传法结果一致。
func TestConvertToRequestPayload_AdaptiveDurationPreserved(t *testing.T) {
	a := &TaskAdaptor{UpstreamFlavor: UpstreamFlavorSdV2}
	const editBody = `{"model":"dreamina-seedance-2-5-260628-max",%s` +
		`"metadata":{%s"ratio":"adaptive","resolution":"480p","generate_audio":false},` +
		`"content":[{"type":"text","text":"edit"},` +
		`{"type":"image_url","image_url":{"url":"https://cdn/ref.png"},"role":"reference_image"},` +
		`{"type":"video_url","video_url":{"url":"https://cdn/src.mp4"},"role":"reference_video"}]}`

	cases := []struct {
		name string
		body string
	}{
		{"top-level duration:-1", strings.NewReplacer("%s", "").Replace(replaceTwo(editBody, `"duration":-1,`, ``))},
		{"top-level seconds:\"-1\"", replaceTwo(editBody, `"seconds":"-1",`, ``)},
		{"metadata.duration:-1", replaceTwo(editBody, ``, `"duration":-1,`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req relaycommon.TaskSubmitReq
			require.NoError(t, common.Unmarshal([]byte(tc.body), &req))
			p, err := a.convertToRequestPayload(&req)
			require.NoError(t, err)
			out, err := common.Marshal(p)
			require.NoError(t, err)

			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(out, &raw))
			require.Equal(t, `-1`, string(raw["duration"]), "duration 必须是数值 -1: %s", out)
			require.Equal(t, `"adaptive"`, string(raw["ratio"]), "ratio 必须在顶层: %s", out)
			require.Equal(t, `"480p"`, string(raw["resolution"]))
			require.Equal(t, `false`, string(raw["generate_audio"]))
			_, hasMeta := raw["metadata"]
			require.False(t, hasMeta, "metadata 包装不得透到上游")
			require.Len(t, p.Content, 3)
		})
	}
}

func replaceTwo(tpl, first, second string) string {
	s := strings.Replace(tpl, "%s", first, 1)
	return strings.Replace(s, "%s", second, 1)
}

func mkDoubaoCtx(body string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

// 完整入口校验（ValidateRequestAndSetAction）：下游反馈的两种传法在 2.5-max 上都应通过；
// 同样的请求打到 2.0-max 要在本地被 invalid_duration 拦下，不再透到上游换回 400。
func TestValidateRequestAndSetAction_EditMode(t *testing.T) {
	const tpl = `{"model":"%s",%s"metadata":{%s"ratio":"adaptive","resolution":"480p","generate_audio":false},` +
		`"content":[{"type":"text","text":"edit"},` +
		`{"type":"video_url","video_url":{"url":"https://cdn/src.mp4"},"role":"reference_video"}]}`
	mk := func(model, top, meta string) string {
		s := strings.Replace(tpl, "%s", model, 1)
		s = strings.Replace(s, "%s", top, 1)
		return strings.Replace(s, "%s", meta, 1)
	}
	newInfo := func() *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{
			TaskRelayInfo: &relaycommon.TaskRelayInfo{},
			ChannelMeta:   &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeSdVideoV2},
		}
	}

	cases := []struct {
		name     string
		body     string
		wantCode string
	}{
		{"2.5-max 顶层 duration=-1 通过", mk("dreamina-seedance-2-5-260628-max", `"duration":-1,`, ``), ""},
		{"2.5-max metadata.duration=-1 通过", mk("dreamina-seedance-2-5-260628-max", ``, `"duration":-1,`), ""},
		{"2.5-max seconds=\"-1\" 通过", mk("dreamina-seedance-2-5-260628-max", `"seconds":"-1",`, ``), ""},
		{"2.0-max 顶层 duration=-1 本地拦下", mk("dreamina-seedance-2-0-260128-max", `"duration":-1,`, ``), "invalid_duration"},
		{"2.0-max metadata.duration=-1 本地拦下", mk("dreamina-seedance-2-0-260128-max", ``, `"duration":-1,`), "invalid_duration"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &TaskAdaptor{}
			info := newInfo()
			a.Init(info)
			taskErr := a.ValidateRequestAndSetAction(mkDoubaoCtx(tc.body), info)
			if tc.wantCode == "" {
				require.Nil(t, taskErr, "unexpected error: %+v", taskErr)
			} else {
				require.NotNil(t, taskErr)
				require.Equal(t, tc.wantCode, taskErr.Code)
				require.True(t, taskErr.LocalError, "应为本地错误，不触发渠道重试")
			}
		})
	}
}
