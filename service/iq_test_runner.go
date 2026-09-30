package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// ExecuteIQTestRun consumes an immutable candidate/configuration snapshot. It
// owns terminal state and lease cleanup for every exit, including cancellation.
func ExecuteIQTestRun(parent context.Context, runID string, channelID int) (runErr error) {
	run, err := model.GetIQTestRun(runID)
	if err != nil {
		return err
	}
	if run.Status != model.IQTestRunStatusRunning || run.ChannelID != channelID {
		return fmt.Errorf("run is not executable")
	}
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	var success, invalid, failed atomic.Int64
	defer func() {
		if recovered := recover(); recovered != nil {
			runErr = fmt.Errorf("IQ executor panic: %v", recovered)
		}
		status := model.IQTestRunStatusFinished
		if runErr != nil {
			status = model.IQTestRunStatusFailed
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, ErrIQDisabled) || errors.Is(runErr, errIQLeaseLost) {
				status = model.IQTestRunStatusAborted
			}
		} else if invalid.Load()+failed.Load() > 0 {
			status = model.IQTestRunStatusPartial
		}
		updates := map[string]any{"status": status, "finished_at": time.Now().Unix(), "success_count": success.Load(), "invalid_count": invalid.Load(), "error_count": failed.Load()}
		if runErr != nil {
			updates["error_message"] = runErr.Error()
		}
		finishErr := model.DB.Model(&model.IQTestRun{}).Where("run_id = ? AND owner_id = ? AND status = ?", runID, run.OwnerID, model.IQTestRunStatusRunning).Updates(updates).Error
		runErr = errors.Join(runErr, finishErr, model.ReleaseIQTestLease(run.OwnerID))
	}()
	var snapshot IQRunSnapshot
	if err := common.Unmarshal([]byte(run.ConfigSnapshot), &snapshot); err != nil {
		return err
	}
	if err := snapshot.Setting.Validate(); err != nil {
		return err
	}
	if len(snapshot.Models) == 0 || len(snapshot.ChannelIDs) == 0 {
		return fmt.Errorf("run snapshot has no candidates")
	}
	if err := iqCheckRunPermission(ctx, run.OwnerID); err != nil {
		return err
	}
	ctx = context.WithValue(ctx, iqRequestGuardKey{}, func() error {
		err := iqCheckRunPermission(ctx, run.OwnerID)
		if err != nil {
			cancel(err)
		}
		return err
	})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := iqCheckRunPermission(ctx, run.OwnerID); err != nil {
					cancel(err)
					return
				}
				ok, err := model.RenewIQTestLease(run.OwnerID, iqLeaseSeconds)
				if err != nil {
					cancel(err)
					return
				}
				if !ok {
					cancel(errIQLeaseLost)
					return
				}
				ok, err = model.RenewIQTestRunLease(runID, run.OwnerID, time.Now().Unix()+iqLeaseSeconds)
				if err != nil {
					cancel(err)
					return
				}
				if !ok {
					cancel(errIQLeaseLost)
					return
				}
			}
		}
	}()
	defer func() { cancel(nil); <-heartbeatDone }()
	jobs := make(chan int)
	var workers sync.WaitGroup
	for i := 0; i < snapshot.Setting.Concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					cancel(fmt.Errorf("IQ worker panic: %v", recovered))
				}
			}()
			for id := range jobs {
				if err := iqCheckRunPermission(ctx, run.OwnerID); err != nil {
					cancel(err)
					return
				}
				var ch model.Channel
				if err := model.DB.First(&ch, id).Error; err != nil {
					cancel(err)
					return
				}
				for i := range snapshot.Models {
					configured := &snapshot.Models[i]
					if !iqChannelHasModel(&ch, configured.ModelName) {
						continue
					}
					if err := iqCheckRunPermission(ctx, run.OwnerID); err != nil {
						cancel(err)
						return
					}
					result := RunIQTestResultWithConfig(ctx, &ch, configured, runID, snapshot.Setting.QuestionsPerRound, time.Duration(snapshot.Setting.PerQuestionTimeoutSeconds)*time.Second)
					result.ConfigVersion = snapshot.Setting.Version
					if err := RecordIQTestResult(result); err != nil {
						cancel(fmt.Errorf("record IQ result: %w", err))
						return
					}
					countColumn := "error_count"
					switch result.Status {
					case model.IQTestResultStatusSuccess:
						success.Add(1)
						countColumn = "success_count"
					case model.IQTestResultStatusInvalid:
						invalid.Add(1)
						countColumn = "invalid_count"
					default:
						failed.Add(1)
					}
					if err := model.DB.Model(&model.IQTestRun{}).Where("run_id = ? AND owner_id = ? AND status = ?", runID, run.OwnerID, model.IQTestRunStatusRunning).
						UpdateColumn(countColumn, gorm.Expr(countColumn+" + ?", 1)).Error; err != nil {
						cancel(err)
						return
					}
				}
			}
		}()
	}
sendJobs:
	for _, id := range snapshot.ChannelIDs {
		select {
		case <-ctx.Done():
			break sendJobs
		case jobs <- id:
		}
	}
	close(jobs)
	workers.Wait()
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if err := iqCheckRunPermission(ctx, run.OwnerID); err != nil {
		return err
	}
	return EnforceIQTestRun(ctx, runID, snapshot.Setting)
}

var errIQLeaseLost = errors.New("IQ executor lease lost")

type iqRequestGuardKey struct{}

// CheckIQTestRequest enforces the run's live ownership and enabled state before
// each upstream request. Standalone probe tests have no admitted run guard.
func CheckIQTestRequest(ctx context.Context) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if guard, ok := ctx.Value(iqRequestGuardKey{}).(func() error); ok {
		return guard()
	}
	return nil
}

func iqCheckRunPermission(ctx context.Context, owner string) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	setting, err := GetIQTestSetting()
	if err != nil {
		return err
	}
	if !setting.Enabled {
		return ErrIQDisabled
	}
	owned, err := model.OwnsIQTestLease(owner)
	if err != nil {
		return err
	}
	if !owned {
		return errIQLeaseLost
	}
	return nil
}
