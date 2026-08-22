package service

import (
	"fmt"
	"html"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

// 分数下降告警去重冷却：同一渠道在冷却窗口内只发一次，防止"每天小幅下降天天邮件"。
// 仅 master 跑调度，单点内存 map 足够；进程重启冷却重置（至多多发一封，可接受）。
const verifyAlertCooldownSeconds = 86400 // 24h

var (
	verifyAlertMu       sync.Mutex
	verifyAlertLastSent = map[string]int64{}
)

// kind 区分告警类型（drop/failure），避免"分数下降"与"测不通"两类事件共用冷却互相压制。
func shouldSendVerifyAlert(channelId int, kind string) bool {
	key := fmt.Sprintf("%d:%s", channelId, kind)
	now := time.Now().Unix()
	verifyAlertMu.Lock()
	defer verifyAlertMu.Unlock()
	if last, ok := verifyAlertLastSent[key]; ok && now-last < verifyAlertCooldownSeconds {
		return false
	}
	verifyAlertLastSent[key] = now
	return true
}

// NotifyChannelScoreDrop 渠道测评分数较上次下降（降幅已达阈值）时，邮件告警所有管理员（带 24h 冷却）。
func NotifyChannelScoreDrop(channelId int, channelName, modelName string, prevScore, newScore int, grade string, reportId int64) {
	if !shouldSendVerifyAlert(channelId, "drop") {
		common.SysLog(fmt.Sprintf("channel verify score drop alert suppressed by cooldown: channel #%d", channelId))
		return
	}
	subject := fmt.Sprintf("渠道「%s」(#%d) 测评分数下降：%d → %d", channelName, channelId, prevScore, newScore)
	// HTML 正文转义用户可控字段（渠道名/模型/等级），防注入
	content := fmt.Sprintf(
		"渠道「%s」(#%d) 的外部测评分数较上次下降。<br/>"+
			"测试模型：%s<br/>上次分数：%d<br/>本次分数：%d（等级 %s）<br/>降幅：%d 分<br/>报告 ID：%d<br/>请到渠道管理页查看详情。",
		html.EscapeString(channelName), channelId, html.EscapeString(modelName), prevScore, newScore, html.EscapeString(grade), prevScore-newScore, reportId,
	)
	notifyAllAdmins(dto.NotifyTypeChannelUpdate, subject, content)
}

// NotifyChannelVerifyFailure 测评失败（测不通）时告警（仅 NotifyOnFailure 开启时由调度器调用；与下降共用冷却）。
func NotifyChannelVerifyFailure(channelId int, channelName, modelName, errMsg string) {
	if !shouldSendVerifyAlert(channelId, "failure") {
		return
	}
	subject := fmt.Sprintf("渠道「%s」(#%d) 自动测评失败", channelName, channelId)
	content := fmt.Sprintf("渠道「%s」(#%d) 自动测评未能完成。<br/>测试模型：%s<br/>原因：%s",
		html.EscapeString(channelName), channelId, html.EscapeString(modelName), html.EscapeString(errMsg))
	notifyAllAdmins(dto.NotifyTypeChannelUpdate, subject, content)
}

// notifyAllAdmins 给所有启用的管理员（role >= RoleAdminUser，含 admin 与 root）逐个发通知，
// 走各自偏好渠道（邮件/webhook/bark/gotify）。仿 NotifyUpstreamModelUpdateWatchers。
func notifyAllAdmins(notifyType string, subject string, content string) {
	var users []model.User
	if err := model.DB.
		Select("id", "email", "role", "status", "setting").
		Where("status = ? AND role >= ?", common.UserStatusEnabled, common.RoleAdminUser).
		Find(&users).Error; err != nil {
		common.SysLog(fmt.Sprintf("notifyAllAdmins: failed to query admin users: %s", err.Error()))
		return
	}
	notification := dto.NewNotify(notifyType, subject, content, nil)
	for _, user := range users {
		if err := NotifyUser(user.Id, user.Email, user.GetSetting(), notification); err != nil {
			common.SysLog(fmt.Sprintf("notifyAllAdmins: failed to notify user %d: %s", user.Id, err.Error()))
			continue
		}
	}
}
