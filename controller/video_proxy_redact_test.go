package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Gemini 视频地址会被追加 ?key=<渠道密钥>，拉取失败的日志和回给客户端的报错里都不能带出来
const videoUpstreamSecret = "AIzaUpstreamVideoSecret"

func captureVideoProxyErrorLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	common.LogWriterMu.Lock()
	original := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &buf
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = original
		common.LogWriterMu.Unlock()
	})
	return &buf
}

func withSSRFProtection(t *testing.T, enabled bool) {
	t.Helper()

	setting := system_setting.GetFetchSetting()
	original := setting.EnableSSRFProtection
	setting.EnableSSRFProtection = enabled
	t.Cleanup(func() {
		setting.EnableSSRFProtection = original
	})
}

func serveGeminiVideoProxy(t *testing.T, videoURL string) *httptest.ResponseRecorder {
	t.Helper()

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Task{}))
	require.NoError(t, db.Create(&model.Channel{
		Id:     1,
		Type:   constant.ChannelTypeGemini,
		Name:   "gemini-video",
		Key:    videoUpstreamSecret,
		Status: common.ChannelStatusEnabled,
	}).Error)
	data, err := common.Marshal(map[string]any{"uri": videoURL})
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Task{
		TaskID:      "task-video-1",
		UserId:      1001,
		ChannelId:   1,
		Status:      model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{Key: videoUpstreamSecret},
		Data:        data,
	}).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/task-video-1/content", nil)
	c.Params = gin.Params{{Key: "task_id", Value: "task-video-1"}}
	c.Set("id", 1001)
	VideoProxy(c)
	return recorder
}

func TestVideoProxyFetchFailureLogOmitsKey(t *testing.T) {
	service.InitHttpClient()
	withSSRFProtection(t, false)
	logBuf := captureVideoProxyErrorLog(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer upstream.Close()

	recorder := serveGeminiVideoProxy(t, upstream.URL+"/v1beta/files/abc:download?alt=media")

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, logBuf.String(), "Failed to fetch video from "+upstream.URL+"/v1beta/files/abc:download: ")
	require.NotContains(t, logBuf.String(), videoUpstreamSecret)
	require.NotContains(t, recorder.Body.String(), videoUpstreamSecret)
}

func TestVideoProxyUpstreamStatusLogOmitsKey(t *testing.T) {
	service.InitHttpClient()
	withSSRFProtection(t, false)
	logBuf := captureVideoProxyErrorLog(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	recorder := serveGeminiVideoProxy(t, upstream.URL+"/v1beta/files/abc:download?alt=media")

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, logBuf.String(), "Upstream returned status 404 for "+upstream.URL+"/v1beta/files/abc:download")
	require.NotContains(t, logBuf.String(), videoUpstreamSecret)
	require.NotContains(t, recorder.Body.String(), videoUpstreamSecret)
}

func TestVideoProxyParseFailureLogOmitsKey(t *testing.T) {
	service.InitHttpClient()
	withSSRFProtection(t, false)
	logBuf := captureVideoProxyErrorLog(t)

	// 关掉 SSRF 校验后不在校验阶段解析，改由构造请求时的 url.Parse 失败
	recorder := serveGeminiVideoProxy(t, "http://bad host/v1beta/files/abc:download?alt=media")

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Contains(t, logBuf.String(), "Failed to parse URL http://bad host/v1beta/files/abc:download: ")
	require.NotContains(t, logBuf.String(), videoUpstreamSecret)
	require.NotContains(t, recorder.Body.String(), videoUpstreamSecret)
}

func TestVideoProxyBlockedRedirectLogOmitsKey(t *testing.T) {
	service.InitHttpClient()
	withSSRFProtection(t, true)
	setting := system_setting.GetFetchSetting()
	allowPrivateIp, allowedPorts := setting.AllowPrivateIp, setting.AllowedPorts
	t.Cleanup(func() {
		setting.AllowPrivateIp, setting.AllowedPorts = allowPrivateIp, allowedPorts
	})
	logBuf := captureVideoProxyErrorLog(t)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1beta/files/abc:download?alt=media&key="+videoUpstreamSecret, http.StatusFound)
	}))
	defer upstream.Close()
	upstreamURL, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	// 只放行首跳的本机端口，跳转到另一个端口会被重定向检查拦下
	setting.AllowPrivateIp = true
	setting.AllowedPorts = []string{upstreamURL.Port()}

	recorder := serveGeminiVideoProxy(t, upstream.URL+"/v1beta/files/abc:download?alt=media")

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, logBuf.String(), "redirect to "+target.URL+"/v1beta/files/abc:download blocked: ")
	require.NotContains(t, logBuf.String(), videoUpstreamSecret)
}

func TestVideoProxyBlockedURLOmitsKeyInResponseAndLog(t *testing.T) {
	service.InitHttpClient()
	withSSRFProtection(t, true)
	logBuf := captureVideoProxyErrorLog(t)

	// 主机名里有空格，网址校验在解析阶段就失败，报错会原样回给客户端
	recorder := serveGeminiVideoProxy(t, "http://bad host/v1beta/files/abc:download?alt=media")

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "invalid URL format")
	require.NotContains(t, recorder.Body.String(), videoUpstreamSecret)
	require.NotContains(t, logBuf.String(), videoUpstreamSecret)
}
