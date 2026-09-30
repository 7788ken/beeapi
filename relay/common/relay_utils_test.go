package common

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// mkMultipartDirectCtx 构造一个带 JSON body 的 gin 测试上下文，供 ValidateMultipartDirect 走
// UnmarshalBodyReusable（依赖 c.Request.Body + Content-Type）。
func mkMultipartDirectCtx(body string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

// 多模态 content[]（prompt 写在 content 的 text 项、无顶层 prompt）应通过校验：
// 博士站以 OpenAI/Sora 渠道 passthrough 转发 seedance 全能参考请求时依赖此豁免，
// 否则请求会在博士站被 400 拦下、根本到不了上游 AI 站。
func TestValidateMultipartDirect_ContentExemptsTopLevelPrompt(t *testing.T) {
	body := `{"model":"doubao-seedance-2-0-260128-max","content":[` +
		`{"type":"text","text":"@Image1 在跳舞"},` +
		`{"type":"image_url","image_url":{"url":"https://cdn/a.png"},"role":"reference_image"}],` +
		`"duration":5,"metadata":{"resolution":"720p","ratio":"16:9"}}`
	c := mkMultipartDirectCtx(body)
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}

	taskErr := ValidateMultipartDirect(c, info)
	require.Nil(t, taskErr, "content[] 场景应豁免顶层 prompt")

	// 校验后应已存入 task_request，Content 完整保留供后续 BuildRequestBody 透传。
	req, err := GetTaskRequest(c)
	require.NoError(t, err)
	require.Equal(t, "doubao-seedance-2-0-260128-max", req.Model)
	require.Len(t, req.Content, 2)
	require.Equal(t, 5, req.Duration)
}

// 无 content[] 且无顶层 prompt → 仍应报错（豁免不能放水到普通请求）。
func TestValidateMultipartDirect_NoContentStillRequiresPrompt(t *testing.T) {
	c := mkMultipartDirectCtx(`{"model":"sora-2","seconds":"5"}`)
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}

	taskErr := ValidateMultipartDirect(c, info)
	require.NotNil(t, taskErr, "无 content[] 时缺 prompt 应报错")
}

// 有顶层 prompt（无 content[]）→ 通过（保持原行为）。
func TestValidateMultipartDirect_TopLevelPromptAccepted(t *testing.T) {
	c := mkMultipartDirectCtx(`{"model":"sora-2","prompt":"a cat walking","seconds":"5"}`)
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}

	taskErr := ValidateMultipartDirect(c, info)
	require.Nil(t, taskErr)
}
