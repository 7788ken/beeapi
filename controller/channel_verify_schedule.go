package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// StartChannelVerifyScheduleTask 启动「渠道外部测评自动定时」后台任务。
// 仅 master 节点运行（对齐 AutomaticallyTestChannels / StartChannelMetricsTask）。
// 串行扫描，天然非重入。详见 docs/2026-06-18-channel-verify-auto-schedule-plan.md
func StartChannelVerifyScheduleTask() error {
	if !common.IsMasterNode {
		return nil
	}
	return backgroundtask.Start("channel-verify-schedule", func(ctx context.Context) {
		defer func() {
			if r := recover(); r != nil {
				common.SysError(fmt.Sprintf("channel verify schedule task panic: %v", r))
			}
		}()
		for {
			cfg := operation_setting.GetChannelVerifySetting()
			tick := cfg.SchedulerTickMinutes
			if tick <= 0 {
				tick = 30
			}
			timer := time.NewTimer(time.Duration(tick) * time.Minute)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			runVerifyScheduleOnce()
		}
	})
}

// runVerifyScheduleOnce 扫描一轮：对到期的可测评渠道（Anthropic / OpenAI）沿用上次模型重测，按结果做分数下降告警 / 阈值自动启停。
// 纳入「被 verify 禁用」的渠道（status=AutoDisabled AND verify_disabled=1）一并重测，
// 否则被 verify 禁掉的渠道再没人测、永远无法靠分数回升自动启用（死锁）。
func runVerifyScheduleOnce() {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("runVerifyScheduleOnce panic: %v", r))
		}
	}()

	cfg := operation_setting.GetChannelVerifySetting()
	if !cfg.AutoVerifyEnabled {
		return
	}

	var channels []*model.Channel
	if err := model.DB.
		Where("type IN ?", []int{constant.ChannelTypeAnthropic, constant.ChannelTypeOpenAI}).
		Where("status = ? OR (status = ? AND verify_disabled = ?)",
			common.ChannelStatusEnabled, common.ChannelStatusAutoDisabled, 1).
		Find(&channels).Error; err != nil {
		common.SysError("verify schedule: query channels failed: " + err.Error())
		return
	}

	now := time.Now().Unix()
	// 熔断：单轮自动禁用上限，防测评网关 异常返低分批量误禁生产渠道
	maxDisables := len(channels) / 5
	if maxDisables < 5 {
		maxDisables = 5
	}
	disabledThisRound := 0
	for _, ch := range channels {
		interval := resolveVerifyInterval(ch, cfg)
		if interval <= 0 {
			continue // 0=本渠道关闭
		}
		// 最近一条报告（含 failed）：取「上次模型」+「到期判断」基准，避免失败渠道每 tick 狂重试。
		reports, _, err := model.ListReportsByChannel(ch.Id, 1, 1)
		if err != nil || len(reports) == 0 {
			continue // 无历史 → 跳过（需先手动测一次定模型）
		}
		last := reports[0]
		if now-last.TestedAt < int64(interval)*60 {
			continue // 未到期
		}
		modelName := strings.TrimSpace(last.Model)
		if modelName == "" {
			continue
		}
		if _, ok := resolveVerifyProtocol(ch.Type, modelName); !ok {
			continue // 模型/渠道类型不在测评白名单（claude- / gpt-/codex- 等）
		}
		if msg := validateVerifyTarget(ch, modelName); msg != "" {
			common.SysLog(fmt.Sprintf("verify schedule skip channel #%d: %s", ch.Id, msg))
			continue
		}

		prevScore := ch.VerifyScore // 测评前快照（上一次 success 分数；可能为 nil）
		reportId, newScore, grade, status, errMsg := runChannelVerify(context.Background(), ch, modelName, 0, verifyHooks{})

		if status == model.VerifyStatusSuccess {
			if prevScore != nil && newScore < *prevScore && *prevScore-newScore >= cfg.ScoreDropThreshold {
				service.NotifyChannelScoreDrop(ch.Id, ch.Name, modelName, *prevScore, newScore, grade, reportId)
			}
			allowDisable := disabledThisRound < maxDisables
			if applyVerifyThresholdAction(ch, newScore, allowDisable) {
				disabledThisRound++
				if disabledThisRound == maxDisables {
					common.SysError(fmt.Sprintf("verify schedule: auto-disable circuit breaker reached (%d this round), further auto-disables skipped", maxDisables))
				}
			}
		} else if cfg.NotifyOnFailure {
			service.NotifyChannelVerifyFailure(ch.Id, ch.Name, modelName, errMsg)
		}

		time.Sleep(common.RequestInterval) // 渠道间隔，避免灌爆测评队列
	}
}

// resolveVerifyInterval 解析渠道生效的测评间隔（分钟）：nil=继承全局；0=关闭；>0=自定义。
func resolveVerifyInterval(ch *model.Channel, cfg *operation_setting.ChannelVerifySetting) int {
	if ch.VerifyIntervalMinutes == nil {
		// 全局间隔被误配成 <=0 会让所有继承渠道静默停测，兜底回默认 360（6h）
		if cfg.GlobalIntervalMinutes <= 0 {
			return 360
		}
		return cfg.GlobalIntervalMinutes
	}
	return *ch.VerifyIntervalMinutes
}

// applyVerifyThresholdAction 按本次测评分数对渠道做阈值自动启停（彻底隔离于健康度机制）。
//
// 设计（v5，用户确认「彻底隔离」）：
//   - 禁用只看「低于绝对值」；用 model.UpdateChannelStatus 直改（不走 service.DisableChannel，
//     不触发 auto_ban 判定 / 健康度反弹保护 / 降级事件）；并打 verify_disabled=1 标记「本渠道由 verify 禁用」。
//   - 自动启用仅恢复「verify 自己禁用」的渠道（status=AutoDisabled 且 verify_disabled=1）；
//     绝不碰健康度禁用的（verify_disabled=0）或手动禁用的（ManuallyDisabled）。
//   - verify_disabled 在任意「启用」时被 UpdateChannelStatus 中心清零；这里再显式清一次兜底多 key。
func applyVerifyThresholdAction(ch *model.Channel, newScore int, allowDisable bool) (didDisable bool) {
	// 重读最新状态/阈值，避免长测评（最长 600s）期间被管理员手动改状态后误覆盖
	fresh, err := model.GetChannelById(ch.Id, false)
	if err != nil || fresh == nil {
		return false
	}
	below, above := fresh.VerifyAutoDisableBelow, fresh.VerifyAutoEnableAbove
	// 阈值反转保护：below>above 会导致中间分数区反复启停（抖动），直接跳过
	if below != nil && above != nil && *below > *above {
		common.SysLog(fmt.Sprintf("channel #%d verify thresholds inverted (below %d > above %d), skip auto enable/disable", ch.Id, *below, *above))
		return false
	}
	if below != nil && newScore < *below {
		if fresh.Status == common.ChannelStatusEnabled {
			if !allowDisable {
				common.SysError(fmt.Sprintf("channel #%d verify auto-disable skipped by circuit breaker (score %d < %d)", ch.Id, newScore, *below))
				return false
			}
			reason := fmt.Sprintf("verify score %d < disable threshold %d", newScore, *below)
			if model.UpdateChannelStatus(ch.Id, "", common.ChannelStatusAutoDisabled, reason) {
				_ = model.SetChannelVerifyDisabled(ch.Id, true)
				common.SysLog(fmt.Sprintf("channel #%d auto-disabled by verify score: %s", ch.Id, reason))
				return true
			}
			common.SysError(fmt.Sprintf("channel #%d verify auto-disable: UpdateChannelStatus failed, skip marking", ch.Id))
			return false
		}
		return false
	}
	if above != nil && newScore >= *above {
		if fresh.Status == common.ChannelStatusAutoDisabled && common.DerefIntOr(fresh.VerifyDisabled, 0) == 1 {
			if model.UpdateChannelStatus(ch.Id, "", common.ChannelStatusEnabled, "") {
				_ = model.SetChannelVerifyDisabled(ch.Id, false)
				common.SysLog(fmt.Sprintf("channel #%d auto-enabled by verify score %d >= %d", ch.Id, newScore, *above))
			}
		}
	}
	return false
}
