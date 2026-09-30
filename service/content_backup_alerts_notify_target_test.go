package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

// 设计文档 7.3 原文："不能把 NotifyUser 因空接收目标直接返回 nil 当发送成功"。
// NotifyUser（service/user_notify.go）在目标为空或 notify_type 不认识时是静默 return nil，
// 照单全收就会写下 last_sent_at，30 分钟去重窗口内这条故障再也不会提醒第二次——静默失联。
//
// 原有用例只覆盖了 Email 一条通道。真正容易踩的恰恰是另外三条：管理员在后台把通知方式改成
// Webhook/Bark/Gotify 却没填 URL 或 token，配置看起来"已切换"，实际一条也发不出去。
// 这里逐通道锁死：目标为空时 attempted 必须为 false（调用方据此绝不 MarkAlertSent）。
func TestContentBackupAlertNotifyTargetPerChannel(t *testing.T) {
	cases := []struct {
		name       string
		email      string
		setting    dto.UserSetting
		wantTarget string
	}{
		{"未设置通知方式时回落到邮件", "root@example.com", dto.UserSetting{}, "root@example.com"},
		{"邮件优先用专用通知邮箱", "root@example.com",
			dto.UserSetting{NotifyType: dto.NotifyTypeEmail, NotificationEmail: "ops@example.com"}, "ops@example.com"},
		{"邮件无专用邮箱时用账号邮箱", "root@example.com",
			dto.UserSetting{NotifyType: dto.NotifyTypeEmail}, "root@example.com"},
		{"邮件两处都空即无目标", "",
			dto.UserSetting{NotifyType: dto.NotifyTypeEmail}, ""},

		{"Webhook 已填地址", "root@example.com",
			dto.UserSetting{NotifyType: dto.NotifyTypeWebhook, WebhookUrl: "https://hook.example.com/x"}, "https://hook.example.com/x"},
		{"Webhook 未填地址即无目标", "root@example.com",
			dto.UserSetting{NotifyType: dto.NotifyTypeWebhook}, ""},
		{"Webhook 未填地址时不得回落到邮箱", "root@example.com",
			dto.UserSetting{NotifyType: dto.NotifyTypeWebhook, NotificationEmail: "ops@example.com"}, ""},

		{"Bark 已填地址", "root@example.com",
			dto.UserSetting{NotifyType: dto.NotifyTypeBark, BarkUrl: "https://bark.example.com/k"}, "https://bark.example.com/k"},
		{"Bark 未填地址即无目标", "root@example.com",
			dto.UserSetting{NotifyType: dto.NotifyTypeBark}, ""},

		{"Gotify 地址与令牌齐全", "root@example.com",
			dto.UserSetting{NotifyType: dto.NotifyTypeGotify, GotifyUrl: "https://gotify.example.com", GotifyToken: "tok"}, "https://gotify.example.com"},
		{"Gotify 缺令牌即无目标", "root@example.com",
			dto.UserSetting{NotifyType: dto.NotifyTypeGotify, GotifyUrl: "https://gotify.example.com"}, ""},
		{"Gotify 缺地址即无目标", "root@example.com",
			dto.UserSetting{NotifyType: dto.NotifyTypeGotify, GotifyToken: "tok"}, ""},

		{"未知通知方式即无目标", "root@example.com",
			dto.UserSetting{NotifyType: "carrier-pigeon", NotificationEmail: "ops@example.com"}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settingJSON, err := common.Marshal(tc.setting)
			if err != nil {
				t.Fatalf("marshal user setting: %v", err)
			}
			user := &model.UserBase{Id: 1, Email: tc.email, Setting: string(settingJSON)}

			if got := contentBackupAlertTarget(user, user.GetSetting()); got != tc.wantTarget {
				t.Fatalf("target=%q want %q", got, tc.wantTarget)
			}

			// 目标为空的分支必须在真正调用 NotifyUser 之前返回，所以这段断言不产生网络 I/O；
			// 目标非空的分支会走真实发送，留给已有的 spy 用例，这里不碰。
			if tc.wantTarget == "" {
				outcome := defaultContentBackupAlertSend(user, dto.NewNotify("t", "title", "content", nil))
				if outcome.attempted {
					t.Fatalf("无接收目标却报告已尝试发送，调用方会据此写下已通知")
				}
				if outcome.err != nil {
					t.Fatalf("无接收目标不该产生错误，实得 %v", outcome.err)
				}
			}
		})
	}
}
