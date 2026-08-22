package service

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
)

// notifyLimitStore is used for in-memory rate limiting when Redis is disabled
var (
	notifyLimitStore sync.Map
	cleanupOnce      sync.Once
	cleanupStartErr  error
)

type limitCount struct {
	Count     int
	Timestamp time.Time
}

func getDuration() time.Duration {
	minute := constant.NotificationLimitDurationMinute
	return time.Duration(minute) * time.Minute
}

// quotaNotifyLimitDuration 余额提醒是状态型通知，不需要按分钟级窗口反复发。
// 低余额用户每次请求都会触发一次提醒尝试，短窗口会把 SMTP 服务商的发送配额打满。
const quotaNotifyLimitDuration = 6 * time.Hour

// notifyLimitPolicy 返回该通知类型的窗口内允许条数与窗口长度。
func notifyLimitPolicy(notifyType string) (int, time.Duration) {
	if notifyType == dto.NotifyTypeQuotaExceed {
		return 1, quotaNotifyLimitDuration
	}
	return constant.NotifyLimitCount, getDuration()
}

// notifyLimitKey 按窗口长度对齐分桶，桶号随窗口滚动，key 生命周期与窗口一致。
func notifyLimitKey(userId int, notifyType string, duration time.Duration) string {
	seconds := int64(duration.Seconds())
	if seconds <= 0 {
		seconds = 1
	}
	return fmt.Sprintf("%d:%s:%d", userId, notifyType, time.Now().Unix()/seconds)
}

// maxNotifyLimitWindow 内存清扫阈值取最长窗口，避免 6 小时窗口的余额提醒被提前清掉后重新放行。
func maxNotifyLimitWindow() time.Duration {
	if d := getDuration(); d > quotaNotifyLimitDuration {
		return d
	}
	return quotaNotifyLimitDuration
}

// startCleanupTask starts a background task to clean up expired entries
func startCleanupTask() error {
	return backgroundtask.Start("notification-limit-cleanup", func(ctx context.Context) {
		backgroundtask.RunPeriodic(ctx, time.Hour, false, func() {
			now := time.Now()
			notifyLimitStore.Range(func(key, value interface{}) bool {
				if limit, ok := value.(limitCount); ok {
					if now.Sub(limit.Timestamp) >= maxNotifyLimitWindow() {
						notifyLimitStore.Delete(key)
					}
				}
				return true
			})
		})
	})
}

func StartNotificationLimitCleanup() error {
	cleanupOnce.Do(func() {
		cleanupStartErr = startCleanupTask()
	})
	return cleanupStartErr
}

// CheckNotificationLimit checks if the user has exceeded their notification limit
// Returns true if the user can send notification, false if limit exceeded
func CheckNotificationLimit(userId int, notifyType string) (bool, error) {
	if common.RedisEnabled {
		return checkRedisLimit(userId, notifyType)
	}
	return checkMemoryLimit(userId, notifyType)
}

func checkRedisLimit(userId int, notifyType string) (bool, error) {
	limit, duration := notifyLimitPolicy(notifyType)
	key := "notify_limit:" + notifyLimitKey(userId, notifyType, duration)

	// Get current count
	count, err := common.RedisGet(key)
	if err != nil && err.Error() != "redis: nil" {
		return false, fmt.Errorf("failed to get notification count: %w", err)
	}

	// If key doesn't exist, initialize it
	if count == "" {
		err = common.RedisSet(key, "1", duration)
		return true, err
	}

	currentCount, _ := strconv.Atoi(count)

	// Check if limit is already reached
	if currentCount >= limit {
		return false, nil
	}

	// Only increment if under limit
	err = common.RedisIncr(key, 1)
	if err != nil {
		return false, fmt.Errorf("failed to increment notification count: %w", err)
	}

	return true, nil
}

func checkMemoryLimit(userId int, notifyType string) (bool, error) {
	// Ensure cleanup task is started
	if err := StartNotificationLimitCleanup(); err != nil {
		return false, err
	}

	limit, duration := notifyLimitPolicy(notifyType)
	key := notifyLimitKey(userId, notifyType, duration)
	now := time.Now()

	// Get current limit count or initialize new one
	var currentLimit limitCount
	if value, ok := notifyLimitStore.Load(key); ok {
		currentLimit = value.(limitCount)
		// Check if the entry has expired
		if now.Sub(currentLimit.Timestamp) >= duration {
			currentLimit = limitCount{Count: 0, Timestamp: now}
		}
	} else {
		currentLimit = limitCount{Count: 0, Timestamp: now}
	}

	// Increment count
	currentLimit.Count++

	// Store updated count
	notifyLimitStore.Store(key, currentLimit)

	return currentLimit.Count <= limit, nil
}
