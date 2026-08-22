package common

// 邮件发送全局限速：所有邮件（验证码 / 系统通知 / 渠道告警）最终都经 SendEmail 出站，
// 在此统一做进程内滑动窗口限速，防止突发群发（如一次给多个管理员群发渠道告警）撞上
// SMTP 服务商（Zoho 等）的发送频率上限，导致整批被拒或发件账号被临时封禁。
// 与业务层去重（verify 24h 冷却）、用户级通知限流正交互补，各管一层。

var emailSendLimiter InMemoryRateLimiter

// allowEmailSend 判断当前是否允许再发一封邮件：窗口内未超限则计数并返回 true，
// 超限返回 false（调用方应丢弃该邮件并记日志）。限速关闭时恒为 true。
func allowEmailSend() bool {
	if !EmailSendRateLimitEnable {
		return true
	}
	emailSendLimiter.Init(RateLimitKeyExpirationDuration)
	return emailSendLimiter.Request("global_email_send", EmailSendRateLimitNum, EmailSendRateLimitDuration)
}
