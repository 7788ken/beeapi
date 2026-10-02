package channel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 渠道密钥放在上游网址查询串里（Vertex API Key 模式、按参数鉴权的自定义渠道等），出错信息里不能带出来
const upstreamSecret = "sk-upstreamsecret"

func captureErrorLog(t *testing.T) *bytes.Buffer {
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

// 接受连接后立刻断开，让发往它的请求必然在网络层失败
func newHangupServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newRelayTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func newRelayTestInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
}

type urlOnlyAdaptor struct {
	Adaptor
	url string
}

func (a urlOnlyAdaptor) GetRequestURL(*relaycommon.RelayInfo) (string, error) {
	return a.url, nil
}

func (a urlOnlyAdaptor) SetupRequestHeader(*gin.Context, *http.Header, *relaycommon.RelayInfo) error {
	return nil
}

type urlOnlyTaskAdaptor struct {
	TaskAdaptor
	url string
}

func (a urlOnlyTaskAdaptor) BuildRequestURL(*relaycommon.RelayInfo) (string, error) {
	return a.url, nil
}

func TestDoRequestFailureLogOmitsUpstreamQuery(t *testing.T) {
	service.InitHttpClient()
	logBuf := captureErrorLog(t)
	srv := newHangupServer(t)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1beta/models/gemini-2.5-pro:generateContent?key="+upstreamSecret+"&alt=sse", http.NoBody)
	require.NoError(t, err)
	_, err = DoRequest(newRelayTestContext(), req, newRelayTestInfo())
	require.Error(t, err)

	logged := logBuf.String()
	require.Contains(t, logged, `do request failed: Post "`+srv.URL+`/v1beta/models/gemini-2.5-pro:generateContent": `)
	require.NotContains(t, logged, upstreamSecret)
	require.NotContains(t, err.Error(), upstreamSecret)
}

func TestNewRequestErrorOmitsUpstreamQuery(t *testing.T) {
	// 控制字符让 http.NewRequest 解析失败，错误里会带原始网址
	badURL := "http://upstream.invalid/v1/chat/completions?key=" + upstreamSecret + "\x7f"
	want := `new request failed: parse "http://upstream.invalid/v1/chat/completions": `

	calls := map[string]func() error{
		"DoApiRequest": func() error {
			_, err := DoApiRequest(urlOnlyAdaptor{url: badURL}, newRelayTestContext(), newRelayTestInfo(), nil)
			return err
		},
		"DoFormRequest": func() error {
			_, err := DoFormRequest(urlOnlyAdaptor{url: badURL}, newRelayTestContext(), newRelayTestInfo(), nil)
			return err
		},
		"DoTaskApiRequest": func() error {
			_, err := DoTaskApiRequest(urlOnlyTaskAdaptor{url: badURL}, newRelayTestContext(), newRelayTestInfo(), nil)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			require.Error(t, err)
			require.Contains(t, err.Error(), want)
			require.NotContains(t, err.Error(), upstreamSecret)
		})
	}
}

// 上层靠错误分类决定切 base_url 还是走渠道重试，脱敏不能改变分类结果
func TestRedactedDoRequestErrorKeepsClassification(t *testing.T) {
	causes := map[string]error{
		"deadline":      context.DeadlineExceeded,
		"canceled":      context.Canceled,
		"refused":       &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
		"dns":           &net.DNSError{Err: "no such host", Name: "upstream.invalid", IsNotFound: true},
		"read timeout":  &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded},
		"eof":           io.EOF,
		"tls handshake": errors.New("remote error: tls: handshake failure"),
	}
	for name, cause := range causes {
		t.Run(name, func(t *testing.T) {
			raw := &url.Error{Op: "Post", URL: "https://upstream.invalid/v1/x?key=" + upstreamSecret, Err: cause}
			redacted := common.RedactURLError(raw)
			require.NotContains(t, redacted.Error(), upstreamSecret)
			require.Equal(t, classifyDoRequestError(raw).GetErrorCode(), classifyDoRequestError(redacted).GetErrorCode())
		})
	}
}

func TestDoRequestBlockedRedirectLogOmitsUpstreamQuery(t *testing.T) {
	service.InitHttpClient()
	setting := system_setting.GetFetchSetting()
	original := setting.EnableSSRFProtection
	setting.EnableSSRFProtection = true
	t.Cleanup(func() { setting.EnableSSRFProtection = original })
	logBuf := captureErrorLog(t)

	// 跳转目标带 ?key= 且端口不在放行名单里，重定向检查会拦下并报出目标网址
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srvURL+"/v2/chat/completions?key="+upstreamSecret, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	srvURL = srv.URL

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", http.NoBody)
	require.NoError(t, err)
	_, err = DoRequest(newRelayTestContext(), req, newRelayTestInfo())
	require.Error(t, err)

	require.Contains(t, logBuf.String(), "redirect to "+srv.URL+"/v2/chat/completions blocked: ")
	require.NotContains(t, logBuf.String(), upstreamSecret)
}

func TestDoRequestInvalidProxyOmitsCredentials(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelSetting: dto.ChannelSettings{Proxy: "http://proxyuser:pa%SEKRET123@proxy.example.com:8080"},
	}}
	req, err := http.NewRequest(http.MethodPost, "http://upstream.invalid/v1/chat/completions", http.NoBody)
	require.NoError(t, err)

	_, err = DoRequest(newRelayTestContext(), req, info)
	require.Error(t, err)
	require.Contains(t, err.Error(), "new proxy http client failed: invalid proxy URL")
	require.NotContains(t, err.Error(), "SEKRET123")
	require.NotContains(t, err.Error(), "proxyuser")
}

func TestDoWssRequestUnparsableURLOmitsUpstreamQuery(t *testing.T) {
	_, err := DoWssRequest(urlOnlyAdaptor{url: "ws://127.0.0.1:1/v1/realtime?model=gpt-realtime&key=" + upstreamSecret + "\x7f"}, newRelayTestContext(), newRelayTestInfo(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "dial failed to ws://127.0.0.1:1/v1/realtime: ")
	require.NotContains(t, err.Error(), upstreamSecret)
}

func TestDoWssRequestDialErrorOmitsUpstreamQuery(t *testing.T) {
	srv := newHangupServer(t)
	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/realtime"

	_, err := DoWssRequest(urlOnlyAdaptor{url: wsBase + "?model=gpt-realtime&key=" + upstreamSecret}, newRelayTestContext(), newRelayTestInfo(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "dial failed to "+wsBase+": ")
	require.NotContains(t, err.Error(), upstreamSecret)
}
