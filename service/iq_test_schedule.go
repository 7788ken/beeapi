package service

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
)

func StartIQTestScheduleTask() error {
	if !common.IsMasterNode {
		return nil
	}
	return backgroundtask.Start("iq-test-schedule", func(ctx context.Context) {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		// Resume the cadence from the last scheduled round so a restart does not
		// silently postpone detection by another full interval.
		lastAttempt := time.Now()
		if last, err := model.LastScheduledIQTestRunAt(); err != nil {
			common.SysError("IQ schedule anchor: " + err.Error())
		} else if last > 0 {
			lastAttempt = time.Unix(last, 0)
		}
		lastMaintenance := time.Time{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if time.Since(lastMaintenance) >= time.Minute {
					if err := IQTestMaintenance(ctx); err != nil {
						common.SysError("IQ maintenance: " + err.Error())
					}
					lastMaintenance = time.Now()
				}
				if err := ReconcileIQTestState(ctx); err != nil {
					common.SysError("IQ reconcile: " + err.Error())
				}
				if err := model.AbortExpiredIQTestRuns(time.Now().Unix()); err != nil {
					common.SysError("IQ expired run cleanup: " + err.Error())
					continue
				}
				setting, err := GetIQTestSetting()
				if err != nil {
					common.SysError("IQ schedule setting: " + err.Error())
					continue
				}
				if !setting.Enabled {
					lastAttempt = time.Now()
					continue
				}
				if time.Since(lastAttempt) < time.Duration(setting.IntervalMinutes)*time.Minute {
					continue
				}
				lastAttempt = time.Now()
				run, created, err := PrepareIQTestRun(model.IQTestRunTriggerSchedule, 0, 0, "", nil)
				if errors.Is(err, ErrIQBusy) || errors.Is(err, ErrIQDisabled) {
					continue
				}
				if err != nil {
					common.SysError("IQ schedule admission: " + err.Error())
					continue
				}
				if created {
					if err := SubmitIQTestRun(run); err != nil {
						common.SysError("IQ schedule submission: " + err.Error())
					}
				}
			}
		}
	})
}
