package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 走真实的 SetUpLogger 接线发一个请求，返回写出的 [GIN] 访问日志行。
func serveAndCaptureAccessLog(t *testing.T, target string) string {
	t.Helper()

	var buf bytes.Buffer
	common.LogWriterMu.Lock()
	original := gin.DefaultWriter
	gin.DefaultWriter = &buf
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = original
		common.LogWriterMu.Unlock()
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetUpLogger(engine)
	engine.GET("/*path", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
	return buf.String()
}

func TestSetUpLoggerRedactsCredentialQueryParams(t *testing.T) {
	cases := []struct {
		name   string
		target string
		want   string
		secret string
	}{
		{
			name:   "gemini key in query",
			target: "/v1beta/models/gemini-2.5-pro:generateContent?key=sk-geminisecret&alt=sse",
			want:   "/v1beta/models/gemini-2.5-pro:generateContent?key=[REDACTED]&alt=sse",
			secret: "sk-geminisecret",
		},
		{
			name:   "console token search",
			target: "/api/token/search?keyword=prod&token=sk-searchsecret",
			want:   "/api/token/search?keyword=[REDACTED]&token=[REDACTED]",
			secret: "sk-searchsecret",
		},
		{
			name:   "channel search by upstream key",
			target: "/api/channel/search?keyword=sk-upstreamsecret&p=1",
			want:   "/api/channel/search?keyword=[REDACTED]&p=1",
			secret: "sk-upstreamsecret",
		},
		{
			name:   "value containing question mark",
			target: "/v1beta/models/gemini-2.5-pro:generateContent?key=sk-partone?parttwo&alt=sse",
			want:   "/v1beta/models/gemini-2.5-pro:generateContent?key=[REDACTED]&alt=sse",
			secret: "sk-partone",
		},
		{
			name:   "separators kept as sent",
			target: "/v1/models?x=1;y=2&&key=sk-sepsecret&",
			want:   "/v1/models?x=1;y=2&&key=[REDACTED]&",
			secret: "sk-sepsecret",
		},
		{
			name:   "login code",
			target: "/api/oauth/wechat?code=wxlogincode",
			want:   "/api/oauth/wechat?code=[REDACTED]",
			secret: "wxlogincode",
		},
		{
			name:   "telegram login signature",
			target: "/api/oauth/telegram/login?id=10086&first_name=bee&auth_date=1790000000&hash=tghmacsecret",
			want:   "/api/oauth/telegram/login?id=10086&first_name=bee&auth_date=1790000000&hash=[REDACTED]",
			secret: "tghmacsecret",
		},
		{
			name:   "password reset link",
			target: "/user/reset?email=a%40b.com&token=resetsecret",
			want:   "/user/reset?email=a%40b.com&token=[REDACTED]",
			secret: "resetsecret",
		},
		{
			name:   "case and escaped names",
			target: "/api/channel/?API_KEY=sk-uppersecret&%61ccess_token=atsecret",
			want:   "/api/channel/?API_KEY=[REDACTED]&%61ccess_token=[REDACTED]",
			secret: "secret",
		},
		{
			name:   "names merely containing key or token stay readable",
			target: "/api/log/?monkey=1&token_name=prod&key_fp=ab12&p=1",
			want:   "/api/log/?monkey=1&token_name=prod&key_fp=ab12&p=1",
		},
		{
			name:   "no query",
			target: "/v1/models",
			want:   "/v1/models",
		},
		{
			name:   "empty value",
			target: "/v1/models?key=",
			want:   "/v1/models?key=",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := serveAndCaptureAccessLog(t, tc.target)

			// 运维脚本按 | 切字段，除路径打码外格式必须保持不变
			require.Regexp(t, `^\[GIN\] \d{4}/\d{2}/\d{2} - \d{2}:\d{2}:\d{2} \| web \|  \| 200 \| +\S+ \| +\S+ \| +GET `+regexp.QuoteMeta(tc.want)+"\n$", line)
			if tc.secret != "" {
				require.NotContains(t, line, tc.secret)
			}
		})
	}
}
