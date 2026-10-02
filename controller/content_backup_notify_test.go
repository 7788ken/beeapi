package controller

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// POST /notify/test 的状态码、code、message、data 是管理端分支提示的依据，逐项锁死。
// 发信走真实的 common.SendEmail：成功路径打到本机一个最小 SMTP 假服务器。

// cbFakeSMTP 只实现 net/smtp 客户端发一封信用得到的命令：EHLO 宣告 AUTH PLAIN LOGIN，
// 任何认证都放行，记下 RCPT TO 和 DATA。
type cbFakeSMTP struct {
	port  int
	mu    sync.Mutex
	rcpts []string
	data  []string
}

func startCBFakeSMTP(t *testing.T) *cbFakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	server := &cbFakeSMTP{port: listener.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	return server
}

func (s *cbFakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	// 客户端没有超时，假服务器出岔子时由这条期限断开，测试报错而不是挂死。
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(conn)
	reply := func(lines ...string) {
		for _, line := range lines {
			_, _ = io.WriteString(conn, line+"\r\n")
		}
	}
	readLine := func() (string, bool) {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", false
		}
		return strings.TrimRight(line, "\r\n"), true
	}

	reply("220 127.0.0.1 fake ESMTP")
	for {
		line, ok := readLine()
		if !ok {
			return
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			reply("250-127.0.0.1", "250 AUTH PLAIN LOGIN")
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			if strings.TrimSpace(line[len("AUTH PLAIN"):]) == "" {
				reply("334 ")
				if _, ok := readLine(); !ok {
					return
				}
			}
			reply("235 2.7.0 accepted")
		case strings.HasPrefix(upper, "AUTH LOGIN"):
			reply("334 VXNlcm5hbWU6")
			if _, ok := readLine(); !ok {
				return
			}
			reply("334 UGFzc3dvcmQ6")
			if _, ok := readLine(); !ok {
				return
			}
			reply("235 2.7.0 accepted")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			reply("250 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			s.mu.Lock()
			s.rcpts = append(s.rcpts, strings.Trim(strings.TrimSpace(line[len("RCPT TO:"):]), "<>"))
			s.mu.Unlock()
			reply("250 OK")
		case upper == "DATA":
			reply("354 end with <CRLF>.<CRLF>")
			var body strings.Builder
			for {
				dataLine, ok := readLine()
				if !ok {
					return
				}
				if dataLine == "." {
					break
				}
				body.WriteString(strings.TrimPrefix(dataLine, ".") + "\r\n")
			}
			s.mu.Lock()
			s.data = append(s.data, body.String())
			s.mu.Unlock()
			reply("250 OK queued")
		case upper == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 OK")
		}
	}
}

func (s *cbFakeSMTP) received() ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.rcpts...), append([]string(nil), s.data...)
}

// cbUseSMTP 把本站发信设置指到 server:port，测试结束恢复。全局发信限速是进程级状态，
// 别的用例发过的信会占它的窗口，这里关掉，只有限流用例自己打开。
func cbUseSMTP(t *testing.T, server string, port int) {
	t.Helper()
	prevServer, prevPort, prevAccount, prevToken, prevFrom := common.SMTPServer, common.SMTPPort, common.SMTPAccount, common.SMTPToken, common.SMTPFrom
	prevSSL, prevForceLogin, prevLimit := common.SMTPSSLEnabled, common.SMTPForceAuthLogin, common.EmailSendRateLimitEnable
	common.SMTPServer, common.SMTPPort = server, port
	common.SMTPAccount, common.SMTPToken, common.SMTPFrom = "alerts@example.com", "fake-smtp-token", "alerts@example.com"
	common.SMTPSSLEnabled, common.SMTPForceAuthLogin, common.EmailSendRateLimitEnable = false, false, false
	t.Cleanup(func() {
		common.SMTPServer, common.SMTPPort = prevServer, prevPort
		common.SMTPAccount, common.SMTPToken, common.SMTPFrom = prevAccount, prevToken, prevFrom
		common.SMTPSSLEnabled, common.SMTPForceAuthLogin, common.EmailSendRateLimitEnable = prevSSL, prevForceLogin, prevLimit
	})
}

// cbClosedPort 返回一个刚关掉的本机端口：连上去必然被拒。
func cbClosedPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}

func cbPostTestNotify(t *testing.T) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/content_backup/notify/test", nil)
	ContentBackupTestNotify(c)
	var body map[string]any
	require.NoError(t, common.UnmarshalJsonStr(rec.Body.String(), &body), "响应体: %s", rec.Body.String())
	return rec.Code, body
}

func TestContentBackupTestNotifyWithoutRecipients(t *testing.T) {
	t18Publish(t, func(c *contentbackup.Config) { c.NotifyEmails = " ; " })
	cbUseSMTP(t, "127.0.0.1", cbClosedPort(t))

	status, body := cbPostTestNotify(t)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, map[string]any{"success": false, "code": "no_recipients", "message": "no notification recipients saved"}, body)
}

func TestContentBackupTestNotifyWithoutSMTPServer(t *testing.T) {
	t18Publish(t, func(c *contentbackup.Config) { c.NotifyEmails = "a@x.com" })
	cbUseSMTP(t, "", 587)

	status, body := cbPostTestNotify(t)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, map[string]any{"success": false, "code": "smtp_not_configured", "message": "SMTP server is not configured on this site"}, body)
}

func TestContentBackupTestNotifySendFailed(t *testing.T) {
	t18Publish(t, func(c *contentbackup.Config) { c.NotifyEmails = "a@x.com" })
	cbUseSMTP(t, "127.0.0.1", cbClosedPort(t))

	status, body := cbPostTestNotify(t)
	require.Equal(t, http.StatusBadGateway, status, "响应体: %v", body)
	require.Equal(t, false, body["success"])
	require.Equal(t, "send_failed", body["code"])
	require.NotEmpty(t, body["message"], "发信失败必须带上真实原因")
}

// 全局发信限速的窗口是进程级状态：限额压到 1 封后，最多第二次一定被限流。
func TestContentBackupTestNotifyThrottledByGlobalEmailLimit(t *testing.T) {
	t18Publish(t, func(c *contentbackup.Config) { c.NotifyEmails = "a@x.com" })
	cbUseSMTP(t, "127.0.0.1", cbClosedPort(t))
	prevNum, prevDuration := common.EmailSendRateLimitNum, common.EmailSendRateLimitDuration
	common.EmailSendRateLimitEnable, common.EmailSendRateLimitNum, common.EmailSendRateLimitDuration = true, 1, 3600
	t.Cleanup(func() { common.EmailSendRateLimitNum, common.EmailSendRateLimitDuration = prevNum, prevDuration })

	status, body := cbPostTestNotify(t)
	if status != http.StatusTooManyRequests {
		require.Equal(t, http.StatusBadGateway, status, "第一次要么被限流、要么真去连已关的端口: %v", body)
		status, body = cbPostTestNotify(t)
	}
	require.Equal(t, http.StatusTooManyRequests, status, "响应体: %v", body)
	require.Equal(t, false, body["success"])
	require.Equal(t, "email_throttled", body["code"])
	require.Contains(t, body["message"], common.ErrEmailThrottled.Error())
}

func TestContentBackupTestNotifyFailureMapping(t *testing.T) {
	tests := []struct {
		err        error
		wantCode   string
		wantStatus int
	}{
		{fmt.Errorf("wrapped: %w", service.ErrContentBackupNoRecipients), "no_recipients", http.StatusBadRequest},
		{fmt.Errorf("wrapped: %w", service.ErrContentBackupSMTPNotConfigured), "smtp_not_configured", http.StatusBadRequest},
		{fmt.Errorf("%w (30 per 60s), dropped: 内容备份测试邮件（sitea）", common.ErrEmailThrottled), "email_throttled", http.StatusTooManyRequests},
		{errors.New("535 5.7.8 authentication failed"), "send_failed", http.StatusBadGateway},
	}
	for _, test := range tests {
		code, status := contentBackupTestNotifyFailure(test.err)
		require.Equal(t, test.wantCode, code, "%v", test.err)
		require.Equal(t, test.wantStatus, status, "%v", test.err)
	}
}

// 总开关关着也照发：运维正要在打开它之前确认收得到。
func TestContentBackupTestNotifyThroughRealSMTP(t *testing.T) {
	t18Publish(t, func(c *contentbackup.Config) {
		c.SiteLabel = "sitea"
		c.NotifyEmails = "a@x.com; b@y.com"
	})
	server := startCBFakeSMTP(t)
	cbUseSMTP(t, "127.0.0.1", server.port)

	before := time.Now().UTC().Truncate(time.Second)
	status, body := cbPostTestNotify(t)
	require.Equal(t, http.StatusOK, status, "响应体: %v", body)
	require.Equal(t, true, body["success"])
	require.Equal(t, "", body["message"])
	data, ok := body["data"].(map[string]any)
	require.True(t, ok, "响应体: %v", body)
	require.Equal(t, []any{"a@x.com", "b@y.com"}, data["recipients"])
	sentAtText, _ := data["sent_at"].(string)
	sentAt, err := time.Parse(time.RFC3339, sentAtText)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(sentAtText, "Z"), "sent_at 必须是 UTC: %s", sentAtText)
	require.False(t, sentAt.Before(before) || sentAt.After(time.Now().UTC()), "sent_at=%s", sentAtText)

	rcpts, messages := server.received()
	require.Equal(t, []string{"a@x.com", "b@y.com"}, rcpts)
	require.Len(t, messages, 1)
	message, err := mail.ReadMessage(strings.NewReader(messages[0]))
	require.NoError(t, err)
	subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
	require.NoError(t, err)
	require.Contains(t, subject, "内容备份测试邮件")
	require.Equal(t, "内容备份测试邮件（sitea）", subject)
	content, err := io.ReadAll(message.Body)
	require.NoError(t, err)
	require.Contains(t, string(content), "这是一封测试邮件，用来确认本站内容备份告警邮件的收件人和发信设置可用。")
}

// 管理端靠读回的 config 里有没有 notify_email_enabled 区分新旧站点，靠 smtp_configured 提示发不出信。
func TestContentBackupGetConfigReportsSMTPConfigured(t *testing.T) {
	t18Publish(t, func(c *contentbackup.Config) { c.NotifyEmails = "a@x.com; b@y.com" })
	previous := common.SMTPServer
	t.Cleanup(func() { common.SMTPServer = previous })

	common.SMTPServer = "smtp.example.com"
	data, _ := t18GetConfig(t)
	require.Equal(t, true, data["smtp_configured"])
	config, ok := data["config"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, config, "notify_email_enabled")
	require.Equal(t, false, config["notify_email_enabled"])
	require.Equal(t, "a@x.com; b@y.com", config["notify_emails"])

	common.SMTPServer = ""
	data, _ = t18GetConfig(t)
	require.Equal(t, false, data["smtp_configured"])
}

func TestContentBackupPutConfigReportsSMTPConfigured(t *testing.T) {
	db := setupContentBackupContentTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	t.Cleanup(func() {
		require.NoError(t, operation_setting.SetContentBackupConfig(contentbackup.DefaultConfig()))
	})
	previous := common.SMTPServer
	common.SMTPServer = "smtp.example.com"
	t.Cleanup(func() { common.SMTPServer = previous })

	put := func(cfg contentbackup.Config, expectedVersion int64) (int, string) {
		payload, err := common.Marshal(dto.ContentBackupConfigUpdateRequest{Config: cfg, ExpectedVersion: expectedVersion})
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/content_backup/config", bytes.NewReader(payload))
		c.Request.Header.Set("Content-Type", "application/json")
		ContentBackupPutConfig(c)
		return rec.Code, rec.Body.String()
	}

	cfg := contentbackup.DefaultConfig()
	cfg.NotifyEmailEnabled = true
	cfg.NotifyEmails = "a@x.com; b@y.com"
	status, body := put(cfg, 1)
	require.Equal(t, http.StatusOK, status, "响应体: %s", body)
	data := contentBackupJobDataOf(t, body)
	require.Equal(t, true, data["smtp_configured"])
	config, ok := data["config"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, config["notify_email_enabled"])
	require.Equal(t, "a@x.com; b@y.com", config["notify_emails"])
	require.True(t, operation_setting.GetContentBackupConfig().NotifyEmailEnabled, "保存后必须发布到内存快照")

	cfg.NotifyEmails = ""
	status, body = put(cfg, 2)
	require.Equal(t, http.StatusBadRequest, status, "响应体: %s", body)
	require.Contains(t, body, "contentbackup: notify_emails: at least one recipient is required when notify_email_enabled is on")
}
