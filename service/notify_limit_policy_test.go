package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
)

// 余额提醒必须收敛到 1 封 / 6 小时，其余通知类型保持原有窗口。
func TestNotifyLimitPolicy(t *testing.T) {
	origCount, origMinute := constant.NotifyLimitCount, constant.NotificationLimitDurationMinute
	defer func() {
		constant.NotifyLimitCount, constant.NotificationLimitDurationMinute = origCount, origMinute
	}()
	constant.NotifyLimitCount, constant.NotificationLimitDurationMinute = 2, 10

	if count, duration := notifyLimitPolicy(dto.NotifyTypeQuotaExceed); count != 1 || duration != 6*time.Hour {
		t.Fatalf("quota notify policy = (%d, %v), want (1, 6h)", count, duration)
	}
	if count, duration := notifyLimitPolicy(dto.NotifyTypeChannelUpdate); count != 2 || duration != 10*time.Minute {
		t.Fatalf("default notify policy = (%d, %v), want (2, 10m)", count, duration)
	}
}

// 同一窗口内余额提醒只放行一封，历史实现按小时命名 key 却用 10 分钟 TTL，
// 实际放行 12 封/小时，打满了 SMTP 服务商配额。
func TestCheckMemoryLimitQuotaNotifyOncePerWindow(t *testing.T) {
	origRedis := common.RedisEnabled
	common.RedisEnabled = false
	defer func() { common.RedisEnabled = origRedis }()

	const userId = 990001
	notifyLimitStore.Delete(notifyLimitKey(userId, dto.NotifyTypeQuotaExceed, quotaNotifyLimitDuration))

	allowed := 0
	for i := 0; i < 20; i++ {
		ok, err := CheckNotificationLimit(userId, dto.NotifyTypeQuotaExceed)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			allowed++
		}
	}
	if allowed != 1 {
		t.Fatalf("allowed %d quota notifications in one window, want 1", allowed)
	}
}

// key 必须随窗口滚动：窗口长度不同的通知类型不能共用同一个桶。
func TestNotifyLimitKeyRollsWithWindow(t *testing.T) {
	const userId = 990002
	short := notifyLimitKey(userId, dto.NotifyTypeQuotaExceed, time.Minute)
	long := notifyLimitKey(userId, dto.NotifyTypeQuotaExceed, quotaNotifyLimitDuration)
	if short == long {
		t.Fatalf("key must differ across window sizes, got %s", short)
	}
	if got := notifyLimitKey(userId, dto.NotifyTypeQuotaExceed, 0); got == "" {
		t.Fatal("zero duration must not produce empty key")
	}
}
