package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
)

// 测试邮件只测已保存的收件人和本站 SMTP，不看总开关。错误必须能用 errors.Is 区分：
// controller 按它们映射成管理端认的 code。

func contentBackupUseSMTPServer(t *testing.T, server string) {
	t.Helper()
	previous := common.SMTPServer
	common.SMTPServer = server
	t.Cleanup(func() { common.SMTPServer = previous })
}

func contentBackupTestEmailConfig(t *testing.T) contentbackup.Config {
	t.Helper()
	cfg := contentbackup.DefaultConfig()
	cfg.SiteLabel = "sitea"
	cfg.NotifyEmails = "a@x.com; b@y.com"
	if err := contentbackup.ValidateConfig(cfg); err != nil {
		t.Fatalf("test email config must be valid: %v", err)
	}
	return cfg
}

func TestContentBackupTestEmailNeedsSavedRecipients(t *testing.T) {
	spy := &contentBackupMailSpy{}
	contentBackupUseMailSpy(t, spy)
	contentBackupUseSMTPServer(t, "smtp.example.com")

	cfg := contentbackup.DefaultConfig()
	cfg.NotifyEmails = " ; , "
	recipients, err := SendContentBackupTestEmail(cfg, contentBackupAlertTestBase)
	if !errors.Is(err, ErrContentBackupNoRecipients) {
		t.Fatalf("err = %v, want %v", err, ErrContentBackupNoRecipients)
	}
	if recipients != nil || spy.count() != 0 {
		t.Fatalf("nothing may be sent without recipients: recipients=%v sent=%d", recipients, spy.count())
	}
}

func TestContentBackupTestEmailNeedsSMTPServer(t *testing.T) {
	spy := &contentBackupMailSpy{}
	contentBackupUseMailSpy(t, spy)
	contentBackupUseSMTPServer(t, "")

	recipients, err := SendContentBackupTestEmail(contentBackupTestEmailConfig(t), contentBackupAlertTestBase)
	if !errors.Is(err, ErrContentBackupSMTPNotConfigured) {
		t.Fatalf("err = %v, want %v", err, ErrContentBackupSMTPNotConfigured)
	}
	if recipients != nil || spy.count() != 0 {
		t.Fatalf("nothing may be sent without an SMTP server: recipients=%v sent=%d", recipients, spy.count())
	}
}

func TestContentBackupTestEmailReturnsSendErrors(t *testing.T) {
	contentBackupUseSMTPServer(t, "smtp.example.com")
	throttled := fmt.Errorf("%w (30 per 60s), dropped: 内容备份测试邮件（sitea）", common.ErrEmailThrottled)
	refused := errors.New("dial tcp 127.0.0.1:25: connect: connection refused")

	for _, sendErr := range []error{throttled, refused} {
		spy := &contentBackupMailSpy{err: sendErr}
		contentBackupUseMailSpy(t, spy)
		recipients, err := SendContentBackupTestEmail(contentBackupTestEmailConfig(t), contentBackupAlertTestBase)
		if !errors.Is(err, sendErr) || err.Error() != sendErr.Error() {
			t.Fatalf("err = %v, want the send error unchanged: %v", err, sendErr)
		}
		if recipients != nil || spy.count() != 1 {
			t.Fatalf("a failed send must report no recipients: recipients=%v sent=%d", recipients, spy.count())
		}
	}
	spy := &contentBackupMailSpy{err: refused}
	contentBackupUseMailSpy(t, spy)
	if _, err := SendContentBackupTestEmail(contentBackupTestEmailConfig(t), contentBackupAlertTestBase); errors.Is(err, common.ErrEmailThrottled) {
		t.Fatalf("a plain send failure must not look throttled: %v", err)
	}
}

func TestContentBackupTestEmailSendsToSavedRecipients(t *testing.T) {
	spy := &contentBackupMailSpy{}
	contentBackupUseMailSpy(t, spy)
	contentBackupUseSMTPServer(t, "smtp.example.com")

	cfg := contentBackupTestEmailConfig(t)
	if cfg.NotifyEmailEnabled {
		t.Fatal("the test email must not depend on the alert email switch")
	}
	now := time.Date(2026, 9, 20, 8, 32, 20, 0, time.UTC)
	recipients, err := SendContentBackupTestEmail(cfg, now)
	if err != nil {
		t.Fatalf("SendContentBackupTestEmail = %v", err)
	}
	if strings.Join(recipients, "|") != "a@x.com|b@y.com" {
		t.Fatalf("recipients = %q", recipients)
	}
	mail := spy.last(t)
	if spy.count() != 1 || mail.receiver != "a@x.com;b@y.com" {
		t.Fatalf("sent %d mails, receiver %q", spy.count(), mail.receiver)
	}
	if mail.subject != "内容备份测试邮件（sitea）" {
		t.Fatalf("subject = %q", mail.subject)
	}
	for _, want := range []string{
		"这是一封测试邮件，用来确认本站内容备份告警邮件的收件人和发信设置可用。",
		"sitea",
		"2026-09-20 16:32:20（北京时间）",
		contentBackupMailFooter,
	} {
		if !strings.Contains(mail.content, want) {
			t.Fatalf("content missing %q: %s", want, mail.content)
		}
	}

	cfg.SiteLabel = ""
	if _, err := SendContentBackupTestEmail(cfg, now); err != nil {
		t.Fatalf("SendContentBackupTestEmail without a site label = %v", err)
	}
	if got := spy.last(t).subject; got != "内容备份测试邮件（未设站点标签）" {
		t.Fatalf("subject without a site label = %q", got)
	}

	cfg.SiteLabel = "a<b>"
	if _, err := SendContentBackupTestEmail(cfg, now); err != nil {
		t.Fatalf("SendContentBackupTestEmail = %v", err)
	}
	if content := spy.last(t).content; !strings.Contains(content, "a&lt;b&gt;") || strings.Contains(content, "a<b>") {
		t.Fatalf("the site label must be html-escaped in the body: %s", content)
	}
}
