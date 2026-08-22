package common

import "testing"

// 验证全局邮件限速：窗口内放行 Num 封，超出丢弃；关闭开关后恒放行。
func TestAllowEmailSendThrottle(t *testing.T) {
	origEnable, origNum, origDur := EmailSendRateLimitEnable, EmailSendRateLimitNum, EmailSendRateLimitDuration
	defer func() {
		EmailSendRateLimitEnable, EmailSendRateLimitNum, EmailSendRateLimitDuration = origEnable, origNum, origDur
	}()

	EmailSendRateLimitEnable = true
	EmailSendRateLimitNum = 3
	EmailSendRateLimitDuration = 60

	for i := 1; i <= 3; i++ {
		if !allowEmailSend() {
			t.Fatalf("第 %d 封应放行", i)
		}
	}
	if allowEmailSend() {
		t.Fatal("超出窗口上限的邮件应被限速丢弃")
	}

	EmailSendRateLimitEnable = false
	if !allowEmailSend() {
		t.Fatal("限速关闭时应恒放行")
	}
}
