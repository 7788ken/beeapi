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
	channelPeakRpmTickInterval = 60 * time.Second
	channelPeakRpmRunTimeout   = 30 * time.Second
)

var (
	channelPeakRpmOnce    sync.Once
	channelPeakRpmRunning atomic.Bool
)

// StartChannelPeakRpmTask persists lifetime peak RPM from the realtime RPM window.
func StartChannelPeakRpmTask() error {
	var startErr error
	channelPeakRpmOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		startErr = backgroundtask.Start("channel-peak-rpm", func(ctx context.Context) {
			logger.LogInfo(context.Background(), fmt.Sprintf("channel peak rpm task started: tick=%s", channelPeakRpmTickInterval))
			backgroundtask.RunPeriodic(ctx, channelPeakRpmTickInterval, true, func() {
				runChannelPeakRpmOnce()
			})
		})
	})
	return startErr
}

func runChannelPeakRpmOnce() {
	if !channelPeakRpmRunning.CompareAndSwap(false, true) {
		logger.LogWarn(context.Background(), "channel peak rpm: previous run still inflight, skip")
		return
	}
	defer channelPeakRpmRunning.Store(false)

	ctx, cancel := context.WithTimeout(context.Background(), channelPeakRpmRunTimeout)
	defer cancel()

	if err := updateChannelPeakRpm(ctx); err != nil {
		logger.LogError(ctx, fmt.Sprintf("channel peak rpm: failed: %v", err))
	}
}

func updateChannelPeakRpm(ctx context.Context) error {
	var ids []int
	if err := model.DB.WithContext(ctx).Table("channels").
		Select("id").
		Where("status = ?", common.ChannelStatusEnabled).
		Scan(&ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	now := common.GetTimestamp()
	rpmMap := common.BatchGetChannelRPMs(ids)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		rpm := rpmMap[id]
		if rpm <= 0 {
			continue
		}
		if err := model.DB.WithContext(ctx).Model(&model.Channel{}).
			Where("id = ? AND peak_rpm < ?", id, rpm).
			Updates(map[string]any{
				"peak_rpm":    rpm,
				"peak_rpm_at": now,
			}).Error; err != nil {
			logger.LogError(ctx, fmt.Sprintf("channel peak rpm: update channel %d failed: %v", id, err))
			continue
		}
	}
	return nil
}
