package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/pkg/contentbackupworker"

	"github.com/gin-gonic/gin"
)

// 读取失败的 HTTP 状态码不是装饰：前端模板在 queryCache.onError 里对任何 500
// 直接 router.navigate('/500')，也就是说凡是被兜底成 500 的读取失败，
// 都会把整个后台管理页面掀掉，而不是在抽屉里显示一行原因。
// 设计文档 9.3 要求"接口失败显示真实状态"，这一层是最后的关口。
func callContentBackupReadError(t *testing.T, err error) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/content_backup/jobs/j/preview", nil)
	contentBackupReadError(c, err)
	return rec
}

func TestContentBackupReadErrorNeverFallsBackTo500ForKnownStates(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"目标未配置/凭据缺失", &contentbackupworker.ReadError{
			Code: contentbackupworker.ReadCodeTargetUnavailable,
			Err:  errors.New("content backup ftps: username is required"),
		}, http.StatusServiceUnavailable},
		{"连不上远端", &contentbackupworker.ReadError{
			Code: contentbackupworker.ReadCodeRemoteError,
			Err:  fmt.Errorf("content backup ftps: dial: %w", contentbackupworker.ErrFTPSNetwork),
		}, http.StatusBadGateway},
		{"远端文件确实没了", &contentbackupworker.ReadError{
			Code: contentbackupworker.ReadCodeRemoteMissing,
			Err:  contentbackupworker.ErrFTPSMissing,
		}, http.StatusConflict},
		{"读槽占用", &contentbackupworker.ReadError{
			Code: contentbackupworker.ReadCodeBusy,
			Err:  contentbackupworker.ErrReadBusy,
		}, http.StatusTooManyRequests},
		{"正文超预算", &contentbackupworker.ReadError{
			Code: contentbackupworker.ReadCodeTooLarge,
			Err:  contentbackupworker.ErrReadTooLarge,
		}, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := callContentBackupReadError(t, tc.err)
			if rec.Code == http.StatusInternalServerError {
				t.Fatalf("%s 被兜底成 500，前端会整页跳 /500 而不是显示原因", tc.name)
			}
			if rec.Code != tc.want {
				t.Fatalf("状态码 = %d，应为 %d；响应体: %s", rec.Code, tc.want, rec.Body.String())
			}
			if rec.Body.String() == "" {
				t.Fatalf("%s 必须带真实原因，空响应等于让运营去猜", tc.name)
			}
		})
	}
}
