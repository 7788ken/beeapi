package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func iqFloor(base, step int64) int64 {
	if base < math.MinInt64+5*step {
		return math.MinInt64
	}
	return base - 5*step
}

func iqPriorityStep(current, target, step int64) int64 {
	if current < target {
		if current > math.MaxInt64-step || current+step > target {
			return target
		}
		return current + step
	}
	if current > target {
		if current < math.MinInt64+step || current-step < target {
			return target
		}
		return current - step
	}
	return current
}

func EnforceIQTestRun(ctx context.Context, runID string, cfg IQTestSetting) error {
	run, err := model.GetIQTestRun(runID)
	if err != nil {
		return err
	}
	var snapshot IQRunSnapshot
	if err := common.Unmarshal([]byte(run.ConfigSnapshot), &snapshot); err != nil {
		return err
	}
	fresh, err := GetIQTestSetting()
	if err != nil {
		return err
	}
	if !fresh.Enabled || fresh.Version != cfg.Version {
		return nil
	}
	// Report-only mode still scores and publishes, it just never touches routing.
	if !cfg.Enforces() {
		return nil
	}
	var results []model.IQTestResult
	if err := model.DB.Where("run_id = ?", runID).Order("channel_id ASC, requested_model ASC").Find(&results).Error; err != nil {
		return err
	}
	grouped := make(map[int][]model.IQTestResult)
	for _, r := range results {
		grouped[r.ChannelID] = append(grouped[r.ChannelID], r)
	}
	ids := make([]int, 0, len(grouped))
	for id := range grouped {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	eligible := 0
	for _, id := range ids {
		for _, r := range grouped[id] {
			if r.Status == "success" {
				eligible++
				break
			}
		}
	}
	cap := min(eligible, max((eligible+4)/5, 5))
	changed := 0
	for _, id := range ids {
		if err := iqCheckRunPermission(ctx, run.OwnerID); err != nil {
			return err
		}
		rows := grouped[id]
		var selected *model.IQTestResult
		allPassed := true
		for i := range rows {
			r := &rows[i]
			if r.Status != "success" || r.Score == nil || r.Margin == nil {
				allPassed = false
				continue
			}
			if *r.Margin < 0 {
				allPassed = false
			}
			if selected == nil || *r.Margin < *selected.Margin {
				selected = r
			}
		}
		if selected == nil {
			continue
		}
		// Recovery needs success for every configured model, including missing results.
		expectedModels := 0
		var ch model.Channel
		if err := model.DB.First(&ch, id).Error; err != nil {
			return err
		}
		for _, m := range snapshot.Models {
			if iqChannelHasModel(&ch, m.ModelName) {
				expectedModels++
			}
		}
		if len(rows) != expectedModels {
			allPassed = false
		}
		negative := *selected.Margin < 0
		action := "none"
		err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// Verify ownership by reading the row. RowsAffected on the renewal
			// cannot serve as the signal: MySQL reports changed rows, so a
			// same-second renewal that writes an identical expiry returns 0.
			var lease model.IQTestLease
			if err := tx.Where(map[string]any{"key": "scheduler", "owner_id": run.OwnerID}).Where("expires_at > ?", time.Now().Unix()).First(&lease).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errIQLeaseLost
				}
				return err
			}
			now := time.Now().Unix()
			if err := tx.Model(&model.IQTestLease{}).Where(map[string]any{"key": "scheduler", "owner_id": run.OwnerID}).
				Updates(map[string]any{"expires_at": now + iqLeaseSeconds, "updated_at": now}).Error; err != nil {
				return err
			}
			var option model.Option
			if err := tx.Where(&model.Option{Key: iqSettingOptionKey}).First(&option).Error; err != nil {
				return err
			}
			var live IQTestSetting
			if err := common.Unmarshal([]byte(option.Value), &live); err != nil {
				return err
			}
			if !live.Enabled || live.Version != cfg.Version {
				return nil
			}
			var current model.Channel
			if err := tx.First(&current, id).Error; err != nil {
				return err
			}
			expectedRevision, exists := snapshot.ChannelRevisions[id]
			if !exists || current.IQRevision != expectedRevision {
				action = "skipped_conflict"
				return iqRecordDecision(tx, selected, action, "channel changed during detection", current.GetPriority(), current.GetPriority())
			}
			configured := model.IQTestModel{}
			for _, m := range snapshot.Models {
				if m.ModelName == selected.RequestedModel {
					configured = m
					break
				}
			}
			var active model.IQTestModel
			if err := tx.Where("id = ? AND version = ? AND enabled = ?", configured.Id, configured.Version, true).First(&active).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return iqRecordDecision(tx, selected, "skipped_conflict", "model configuration changed", current.GetPriority(), current.GetPriority())
				}
				return err
			}
			control := model.IQTestControl{ChannelID: id}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&control).Error; err != nil {
				return err
			}
			if err := tx.First(&control, "channel_id = ?", id).Error; err != nil {
				return err
			}
			if control.LastRunID == runID {
				return nil
			}
			if control.Revision != current.IQRevision || control.ModelName != selected.RequestedModel || control.ModelVersion != active.Version || control.BankVersion != run.BankVersion {
				control.FailStreak, control.PassStreak = 0, 0
			}
			control.Revision, control.LastRunID, control.ModelName = current.IQRevision, runID, selected.RequestedModel
			control.ModelVersion, control.Baseline, control.BankVersion = active.Version, active.BaselineScore, run.BankVersion
			if negative {
				control.FailStreak++
				control.PassStreak = 0
			} else if allPassed {
				control.PassStreak++
				control.FailStreak = 0
			} else {
				control.PassStreak, control.FailStreak = 0, 0
			}
			if current.IQBaselinePriority == nil {
				control.Step = int64(cfg.PriorityStep)
			}
			before := current.GetPriority()
			after := before
			updates := map[string]any{}
			healthy := common.DerefIntOr(current.DegradeLevel, 0) == 0 && common.DerefIntOr(current.PermanentDisabled, 0) == 0 && common.DerefIntOr(current.VerifyDisabled, 0) == 0
			if !healthy {
				action = "skipped_conflict"
			} else if negative && control.FailStreak >= 2 && current.Status == common.ChannelStatusEnabled {
				if changed >= cap {
					action = "breaker_skip"
				} else if cfg.DisableBelowBaseline {
					action = "disabled"
					updates["status"] = common.ChannelStatusAutoDisabled
					updates["iq_disabled"] = true
				} else {
					base := before
					if current.IQBaselinePriority != nil {
						base = *current.IQBaselinePriority
					}
					after = iqPriorityStep(before, iqFloor(base, control.Step), control.Step)
					if after != before {
						action = "priority_down"
						updates["iq_baseline_priority"] = base
						updates["iq_applied_priority"] = after
						updates["priority"] = after
					}
				}
			} else if allPassed && control.PassStreak >= 2 {
				if current.IQDisabled && current.Status == common.ChannelStatusAutoDisabled {
					action = "enabled"
					updates["status"] = common.ChannelStatusEnabled
					updates["iq_disabled"] = false
				}
				if current.IQBaselinePriority != nil && current.IQAppliedPriority != nil && before == *current.IQAppliedPriority {
					after = iqPriorityStep(before, *current.IQBaselinePriority, control.Step)
					updates["priority"] = after
					updates["iq_applied_priority"] = after
					if action == "none" && after != before {
						action = "priority_up"
					}
					if after == *current.IQBaselinePriority {
						updates["iq_baseline_priority"] = nil
						updates["iq_applied_priority"] = nil
					}
				}
			}
			if len(updates) > 0 {
				res := tx.Set("iq_enforcement", true).Model(&model.Channel{}).Where("id = ? AND iq_revision = ? AND status = ?", id, current.IQRevision, current.Status).Updates(updates)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected != 1 {
					return ErrIQVersionConflict
				}
				enabled := current.Status == common.ChannelStatusEnabled
				if status, ok := updates["status"].(int); ok {
					enabled = status == common.ChannelStatusEnabled
				}
				if err := tx.Model(&model.Ability{}).Where("channel_id = ?", id).Updates(map[string]any{"priority": after, "enabled": enabled}).Error; err != nil {
					return err
				}
				if err := tx.Create(&model.IQTestAction{RunID: runID, ChannelID: id, Action: action, Reason: selected.RequestedModel, PriorityBefore: before, PriorityAfter: after, CreatedAt: time.Now().Unix()}).Error; err != nil {
					return err
				}
			}
			if err := tx.Save(&control).Error; err != nil {
				return err
			}
			return iqRecordDecision(tx, selected, action, selected.RequestedModel, before, after)
		})
		if err != nil {
			if errors.Is(err, ErrIQVersionConflict) {
				if err := iqRecordDecision(model.DB, selected, "skipped_conflict", "channel changed during enforcement", ch.GetPriority(), ch.GetPriority()); err != nil {
					return err
				}
				continue
			}
			return err
		}
		if action == "priority_down" || action == "disabled" {
			changed++
		}
		if action == "priority_down" || action == "priority_up" || action == "disabled" || action == "enabled" {
			if err := model.RefreshIQChannelCache(id); err != nil {
				return fmt.Errorf("refresh IQ channel %d: %w", id, err)
			}
			if action == "enabled" {
				ClearChannelHealthRuntime(id)
			}
			if cfg.NotifyOnAction {
				if err := queueIQNotice(fmt.Sprintf("channel:%d:%s", id, action), "渠道智商检测处置", fmt.Sprintf("渠道 #%d，模型 %s，分数 %d，动作 %s，运行 %s", id, selected.RequestedModel, *selected.Score, action, runID)); err != nil {
					return err
				}
			}
		}
		if action == "breaker_skip" && cfg.NotifyOnAction {
			if err := queueIQNotice("breaker", "渠道智商检测达到处置上限", fmt.Sprintf("运行 %s，本轮已处置 %d 个渠道，上限 %d，其余渠道未处置。", runID, changed, cap)); err != nil {
				return err
			}
		}
	}
	return nil
}

func iqRecordDecision(tx *gorm.DB, r *model.IQTestResult, action, reason string, before, after int64) error {
	return tx.Model(&model.IQTestResult{}).Where("id = ?", r.Id).Updates(map[string]any{"selected_for_action": true, "action": action, "action_reason": reason, "priority_before": before, "priority_after": after}).Error
}

// ReconcileIQTestState releases only state still owned by IQ. It sends no probes.
func ReconcileIQTestState(ctx context.Context) error {
	owner := uuid.NewString()
	owned, err := model.ClaimIQTestLease(owner, iqLeaseSeconds)
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	defer func() {
		if err := model.ReleaseIQTestLease(owner); err != nil {
			common.SysError("IQ reconcile lease: " + err.Error())
		}
	}()
	cfg, err := GetIQTestSetting()
	if err != nil {
		return err
	}
	models, err := model.ListIQTestModels(true)
	if err != nil {
		return err
	}
	active := map[string]model.IQTestModel{}
	for _, m := range models {
		active[m.ModelName] = m
	}
	var channels []model.Channel
	if err := model.DB.WithContext(ctx).Where("iq_disabled = ? OR iq_baseline_priority IS NOT NULL", true).Find(&channels).Error; err != nil {
		return err
	}
	for _, ch := range channels {
		var control model.IQTestControl
		if err := model.DB.First(&control, "channel_id = ?", ch.Id).Error; err != nil {
			return err
		}
		target, exists := active[control.ModelName]
		release := !cfg.Enabled || !exists || target.Version != control.ModelVersion || !iqChannelHasModel(&ch, control.ModelName)
		releaseDisable := ch.IQDisabled && !cfg.DisableBelowBaseline
		if !release && !releaseDisable {
			continue
		}
		err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			updates := map[string]any{}
			after := ch.GetPriority()
			if release && ch.IQBaselinePriority != nil && ch.IQAppliedPriority != nil && after == *ch.IQAppliedPriority {
				after = *ch.IQBaselinePriority
				updates["priority"] = after
				updates["iq_baseline_priority"] = nil
				updates["iq_applied_priority"] = nil
			}
			healthy := common.DerefIntOr(ch.DegradeLevel, 0) == 0 && common.DerefIntOr(ch.PermanentDisabled, 0) == 0 && common.DerefIntOr(ch.VerifyDisabled, 0) == 0
			if ch.IQDisabled && ch.Status == common.ChannelStatusAutoDisabled && healthy {
				updates["status"] = common.ChannelStatusEnabled
				updates["iq_disabled"] = false
			}
			if len(updates) == 0 {
				return nil
			}
			res := tx.Set("iq_enforcement", true).Model(&model.Channel{}).Where("id = ? AND iq_revision = ? AND status = ?", ch.Id, ch.IQRevision, ch.Status).Updates(updates)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return nil
			}
			enabled := ch.Status == common.ChannelStatusEnabled
			if _, ok := updates["status"]; ok {
				enabled = true
			}
			if err := tx.Model(&model.Ability{}).Where("channel_id = ?", ch.Id).Updates(map[string]any{"priority": after, "enabled": enabled}).Error; err != nil {
				return err
			}
			if release {
				if err := tx.Delete(&model.IQTestControl{}, "channel_id = ?", ch.Id).Error; err != nil {
					return err
				}
			} else {
				if err := tx.Model(&model.IQTestControl{}).Where("channel_id = ?", ch.Id).Updates(map[string]any{"fail_streak": 0, "pass_streak": 0}).Error; err != nil {
					return err
				}
			}
			return tx.Create(&model.IQTestAction{ChannelID: ch.Id, Action: "reconciled", Reason: "configuration changed", PriorityBefore: ch.GetPriority(), PriorityAfter: after, CreatedAt: time.Now().Unix()}).Error
		})
		if err != nil {
			return err
		}
		if err := model.RefreshIQChannelCache(ch.Id); err != nil {
			return err
		}
	}
	return nil
}
