package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIOCopyBytesGracefullyPreservesLocalRequestIdAndCapturesUpstreamId(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Header(common.RequestIdKey, "local-request-id")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			common.RequestIdKey: {"upstream-request-id"},
			"Content-Length":    {"999"},
			"X-Upstream-Test":   {"copied"},
		},
	}

	IOCopyBytesGracefully(c, resp, []byte("ok"))

	require.Equal(t, "local-request-id", recorder.Header().Get(common.RequestIdKey))
	require.Equal(t, "upstream-request-id", c.GetString(common.UpstreamRequestIdKey))
	require.Equal(t, "copied", recorder.Header().Get("X-Upstream-Test"))
	require.Equal(t, "2", recorder.Header().Get("Content-Length"))
	require.Equal(t, "ok", recorder.Body.String())
}
