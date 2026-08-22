package controller

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_monitor_setting"

	"github.com/gin-gonic/gin"
)

// 上游分组倍率变化监控。详见 docs/2026-08-05-upstream-group-ratio-monitor.md

const ratioMonitorMaxConcurrency = 8

var channelRatioMonitorTaskOnce sync.Once

// 同一时刻只允许一轮抓取：定时轮次与管理员手动触发共用该闸门，
// 否则两轮并发会对同一批 baseline 交错 UPSERT，产生重复或错向的变更记录。
var channelRatioMonitorRunning atomic.Bool

// runChannelRatioMonitorOnce 抓取全部有 base_url 的启用渠道并落库比对结果。
func runChannelRatioMonitorOnce(ctx context.Context) {
	if !channelRatioMonitorRunning.CompareAndSwap(false, true) {
		common.SysLog("ratio monitor: another round is still running, skipped")
		return
	}
	defer channelRatioMonitorRunning.Store(false)

	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		common.SysError("ratio monitor: failed to list channels: " + err.Error())
		return
	}

	batchAt := common.GetTimestamp()
	client := service.RatioMonitorClient()
	// 一次性加载分站凭据，避免每个渠道各查一次全表
	creds := service.LoadSubSiteCredentials()
	// 同面板多渠道共享一次抓取（pomoai 一家 18 个渠道）
	panelCache := service.NewPanelCache()

	// 实付倍率反推的基准：一次查询覆盖全部渠道，避免逐渠道扫 logs（28M 行）。
	// 窗口 = 上一轮到本轮；各渠道的 ratio_usage_at 可能略有差异，用统一窗口是可接受的近似。
	windowStart := prevRoundStart(channels)
	var windowUsage map[int][]model.ChannelModelUsage
	if windowStart > 0 && windowStart < batchAt {
		var err error
		windowUsage, err = model.GetChannelWindowUsage(windowStart, batchAt)
		if err != nil {
			common.SysError("ratio monitor: window usage query failed: " + err.Error())
		}
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, ratioMonitorMaxConcurrency)
	results := make([]service.ChannelRatioRoundResult, 0, len(channels))
	var resultMu sync.Mutex
	channelNames := make(map[int]string, len(channels))
	deviations := make([]service.RatioDeviation, 0)
	var devMu sync.Mutex
	alertPercent := ratio_monitor_setting.GetRatioMonitorSetting().DeviationAlertPercent
	// 上游 pricing 不可得时的兜底基准（本站倍率表锚定官方价）
	localRatios := service.LocalModelRatios()

	for _, channel := range channels {
		// 自动禁用多为上游临时故障（502/超时），渠道随时可能被健康度机制拉回；
		// 跳过它们会让这类渠道长期停在"未抓取"。只有手动禁用才真正排除。
		if channel.Status != common.ChannelStatusEnabled && channel.Status != common.ChannelStatusAutoDisabled {
			continue
		}
		if service.RatioPanelURL(channel) == "" {
			continue
		}
		channelNames[channel.Id] = channel.Name

		wg.Add(1)
		go func(ch *model.Channel) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res := service.RunChannelRatioRound(ctx, client, ch, batchAt, creds, panelCache)
			up, down := service.CountBadgeDirections(res.Diffs)

			// 实付倍率：本轮实付累计 - 上轮快照，除以同窗口基准 quota。
			// 快照必须来自上一轮（容差 5min）：渠道若漏过几轮（如期间被禁用），
			// Δ实付横跨多小时而基准只覆盖本窗口，算出的倍率会虚高数倍。
			var effective *float64
			usageFresh := ch.RatioUsageAt > 0 && windowStart > 0 && windowStart-ch.RatioUsageAt <= 300
			if res.UsageOK && usageFresh {
				ratios := res.ModelRatios
				mapping := parseModelMapping(ch)
				if len(ratios) == 0 {
					// 本地表按我方模型名索引，不套渠道映射
					ratios, mapping = localRatios, nil
				}
				effective = service.InferEffectiveRatio(ch.RatioUsageSnapshot, res.Usage, windowUsage[ch.Id], ratios, mapping)
				effAt := batchAt
				if effective == nil {
					effAt = 0
				}
				if err := model.UpdateChannelEffectiveRatio(ch.Id, res.Usage, batchAt, effective, effAt); err != nil {
					common.SysError(fmt.Sprintf("ratio monitor: failed to update channel %d effective ratio: %s", ch.Id, err.Error()))
				}
			} else if res.UsageOK {
				// 快照过期只刷新快照，不产出倍率；下一轮窗口对齐后恢复计算
				if err := model.UpdateChannelEffectiveRatio(ch.Id, res.Usage, batchAt, nil, 0); err != nil {
					common.SysError(fmt.Sprintf("ratio monitor: failed to update channel %d usage snapshot: %s", ch.Id, err.Error()))
				}
			}
			if effective == nil && ch.RatioEffective != nil {
				// 本轮无流量时沿用上轮实测值，避免展示忽有忽无
				effective = ch.RatioEffective
			}
			if dev := service.CheckRatioDeviation(ch, effective, res.PanelRatio, alertPercent); dev != nil {
				devMu.Lock()
				deviations = append(deviations, *dev)
				devMu.Unlock()
			}
			// 判据必须与角标口径一致：sub2api 每天进出高峰两次会让 effective 变动，
			// 若按 len(Diffs)>0 判定，就会用 up=down=0 覆盖掉上一轮真实涨价的角标。
			if err := model.UpdateChannelRatioSnapshot(ch.Id, res.Status, res.Msg, res.Kind, res.Summary, res.Group, up, down, batchAt, up > 0 || down > 0); err != nil {
				common.SysError(fmt.Sprintf("ratio monitor: failed to update channel %d snapshot: %s", ch.Id, err.Error()))
			}

			resultMu.Lock()
			results = append(results, res)
			resultMu.Unlock()
		}(channel)
	}
	wg.Wait()

	changed := make([]service.ChannelRatioRoundResult, 0)
	okCount, failCount := 0, 0
	for _, r := range results {
		if r.Status == "ok" {
			okCount++
		} else {
			failCount++
		}
		if up, down := service.CountBadgeDirections(r.Diffs); up > 0 || down > 0 {
			changed = append(changed, r)
		}
	}

	common.SysLog(fmt.Sprintf("ratio monitor done: checked=%d ok=%d failed=%d changed_channels=%d",
		len(results), okCount, failCount, len(changed)))

	if len(changed) > 0 {
		service.NotifyUpstreamModelUpdateWatchers(
			"上游分组倍率变化通知",
			service.BuildRatioChangeNotification(changed, channelNames),
		)
	}
	if len(deviations) > 0 {
		common.SysLog(fmt.Sprintf("ratio monitor: %d channels exceed expected ratio", len(deviations)))
		service.NotifyUpstreamModelUpdateWatchers(
			"渠道实测倍率高于登记采购价",
			service.BuildRatioDeviationNotification(deviations, channelNames),
		)
	}
}

// parseModelMapping 解析渠道的 model_mapping（我方模型名 → 上游模型名）。
func parseModelMapping(ch *model.Channel) map[string]string {
	raw := ch.GetModelMapping()
	if raw == "" {
		return nil
	}
	out := map[string]string{}
	if err := common.UnmarshalJsonStr(raw, &out); err != nil {
		return nil
	}
	return out
}

// prevRoundStart 取上一轮抓取时间作为窗口起点（各渠道快照时间一致，取最大值即可）。
func prevRoundStart(channels []*model.Channel) int64 {
	var latest int64
	for _, ch := range channels {
		if ch.RatioUsageAt > latest {
			latest = ch.RatioUsageAt
		}
	}
	return latest
}

func StartChannelRatioMonitorTask() error {
	var startErr error
	channelRatioMonitorTaskOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		setting := ratio_monitor_setting.GetRatioMonitorSetting()
		if !setting.Enabled {
			common.SysLog("ratio monitor task disabled by ratio_monitor_setting.enabled")
			return
		}
		interval := time.Duration(setting.GetIntervalMinutes()) * time.Minute
		startErr = backgroundtask.Start("channel-ratio-monitor", func(ctx context.Context) {
			common.SysLog(fmt.Sprintf("ratio monitor task started: interval=%s", interval))
			backgroundtask.RunPeriodic(ctx, interval, true, func() {
				if !ratio_monitor_setting.GetRatioMonitorSetting().Enabled {
					return
				}
				runChannelRatioMonitorOnce(ctx)
			})
		})
	})
	return startErr
}

// RunChannelRatioMonitorNow 管理员手动触发一轮抓取。
func RunChannelRatioMonitorNow(c *gin.Context) {
	go runChannelRatioMonitorOnce(context.Background())
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "已触发上游倍率抓取，请稍后刷新查看"})
}

// GetChannelRatioChanges 返回单渠道在时间窗口内的分组倍率变更明细。
func GetChannelRatioChanges(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "渠道 id 不合法"})
		return
	}
	days := ratio_monitor_setting.GetRatioMonitorSetting().GetBadgeDays()
	if raw := c.Query("days"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			days = parsed
		}
	}
	since := common.GetTimestamp() - int64(days)*86400
	changes, err := model.GetChannelGroupRatioChangesSince(channelId, since)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 同时返回当前基线：弹窗要先回答"此刻各分组倍率是多少"，再给变更历史
	current, err := model.GetChannelGroupRatioBaselines(channelId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"message":    "",
		"data":       changes,
		"current":    current,
		"badge_days": ratio_monitor_setting.GetRatioMonitorSetting().GetBadgeDays(),
	})
}
