package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 2026-09-18 起备份管道跑在 beeapi 进程内，远端凭据存在配置整包里。GET /config 是唯一
// 会把配置送到浏览器的地方：口令必须被抹掉，只留"已存"布尔位；徽章的事实来源就是配置本身。

func t18Publish(t *testing.T, mutate func(*contentbackup.Config)) {
	t.Helper()
	cfg := contentbackup.DefaultConfig()
	mutate(&cfg)
	require.NoError(t, operation_setting.SetContentBackupConfig(cfg))
	t.Cleanup(func() {
		require.NoError(t, operation_setting.SetContentBackupConfig(contentbackup.DefaultConfig()))
	})
}

func t18GetConfig(t *testing.T) (map[string]any, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/content_backup/config", nil)
	ContentBackupGetConfig(c)
	require.Equal(t, http.StatusOK, rec.Code, "响应体: %s", rec.Body.String())
	return contentBackupJobDataOf(t, rec.Body.String()), rec.Body.String()
}

func TestContentBackupGetConfigNeverEchoesThePassword(t *testing.T) {
	t18Publish(t, func(c *contentbackup.Config) {
		c.RemoteUsername = "backup-user"
		c.RemotePassword = "s3cret-please-hide"
	})
	data, body := t18GetConfig(t)
	require.False(t, strings.Contains(body, "s3cret-please-hide"), "口令出现在响应里: %s", body)
	config, ok := data["config"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "backup-user", config["remote_username"])
	require.Equal(t, "", config["remote_password"], "口令字段必须回空串而不是缺失，表单才能整包回传")
	require.Equal(t, true, data["remote_password_set"])
	require.Equal(t, true, data["remote_credentials_set"])
}

func TestContentBackupGetConfigReportsHalfCredentialsAsNotSet(t *testing.T) {
	t18Publish(t, func(c *contentbackup.Config) { c.RemoteUsername = "backup-user" })
	data, _ := t18GetConfig(t)
	require.Equal(t, false, data["remote_password_set"])
	require.Equal(t, false, data["remote_credentials_set"], "只有用户名连不上任何远端，不能报已配置")

	t18Publish(t, func(c *contentbackup.Config) { c.RemotePassword = "only-password" })
	data, _ = t18GetConfig(t)
	require.Equal(t, true, data["remote_password_set"])
	require.Equal(t, false, data["remote_credentials_set"])
}
