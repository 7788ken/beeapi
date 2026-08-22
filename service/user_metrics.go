// Package service - user_metrics.go
// 用户 RPM 快照：每 5min 重算所有用户的 rpm_24h（过去 24h 平均每分钟请求数）。
// 复用 channel_metrics.go 的设计（docs/2026-05-12-channel-quality-rpm-list-plan.md）：
//   - SQL 端纯标量聚合，跨库通用；
//   - 非重入 + 4min 超时；启动时立即跑一次。
package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
)

const (
	userMetricsTickInterval = 5 * time.Minute
	userMetricsRunTimeout   = 4 * time.Minute
	userMetricsWindow       = int64(86400) // 24h
)

var (
	userMetricsOnce    sync.Once
	userMetricsRunning atomic.Bool
)

// StartUserMetricsTask 启动用户 RPM 快照后台任务。
// 启动时立刻跑一次，随后每 5min tick。非 master 节点不参与。
func StartUserMetricsTask() error {
	var startErr error
	userMetricsOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		startErr = backgroundtask.Start("user-metrics", func(ctx context.Context) {
			logger.LogInfo(context.Background(),
				fmt.Sprintf("user metrics task started: tick=%s window=24h", userMetricsTickInterval))
			backgroundtask.RunPeriodic(ctx, userMetricsTickInterval, true, func() {
				runUserMetricsOnce()
			})
		})
	})
	return startErr
}

// runUserMetricsOnce 非重入封装。
func runUserMetricsOnce() {
	if !userMetricsRunning.CompareAndSwap(false, true) {
		logger.LogWarn(context.Background(), "user metrics: previous run still inflight, skip")
		return
	}
	defer userMetricsRunning.Store(false)

	ctx, cancel := context.WithTimeout(context.Background(), userMetricsRunTimeout)
	defer cancel()

	start := time.Now()
	if err := RecomputeUserMetricsOnce(ctx); err != nil {
		logger.LogError(ctx, fmt.Sprintf("user metrics: failed: %v", err))
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf("user metrics: done elapsed=%s", time.Since(start)))
}

// RecomputeUserMetricsOnce 重算所有用户 rpm_24h。
// 单条 SQL 聚合每个 user 的请求数（type=2 + type=5）/ 1440 得到 RPM，逐条 UPDATE 写回。
// 用户量比 channel 大（~300+），但 logs.user_id 有索引（MUL key）。
func RecomputeUserMetricsOnce(ctx context.Context) error {
	if model.DB == nil || model.LOG_DB == nil {
		return fmt.Errorf("database not initialized")
	}
	now := common.GetTimestamp()
	from := now - userMetricsWindow

	type aggRow struct {
		UserId int   `gorm:"column:user_id"`
		Total  int64 `gorm:"column:total"`
	}
	var rows []aggRow
	if err := model.LOG_DB.WithContext(ctx).Raw(`
		SELECT user_id, COUNT(*) AS total
		FROM logs
		WHERE created_at >= ? AND user_id > 0 AND type IN (2, 5)
		GROUP BY user_id`, from).Scan(&rows).Error; err != nil {
		return fmt.Errorf("agg query failed: %w", err)
	}

	// 把 24h 无流量的用户的 rpm_24h 重置为 0（避免显示陈旧值）
	if err := model.DB.WithContext(ctx).Exec(`
		UPDATE users SET rpm_24h = 0, rpm_updated_at = ?
		WHERE rpm_24h > 0`, now).Error; err != nil {
		return fmt.Errorf("reset stale rpm failed: %w", err)
	}

	// 有流量的用户写入新值
	for _, r := range rows {
		rpm := float64(r.Total) / 1440.0
		if err := model.DB.WithContext(ctx).Model(&model.User{}).
			Where("id = ?", r.UserId).
			Updates(map[string]any{
				"rpm_24h":        rpm,
				"rpm_updated_at": now,
			}).Error; err != nil {
			logger.LogError(ctx, fmt.Sprintf("user metrics: update user %d failed: %v", r.UserId, err))
			continue
		}
	}
	return nil
}
