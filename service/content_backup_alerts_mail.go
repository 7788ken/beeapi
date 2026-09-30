package service

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

// 告警邮件固定用东八区：收件人在这个时区看，正文里的「时间」必须和邮箱列表一致。
var contentBackupAlertLocation = time.FixedZone("CST", 8*3600)

func contentBackupAlertReasonTitle(reason string) string {
	switch reason {
	case model.ContentBackupAlertOldestPending:
		return "上传积压"
	case model.ContentBackupAlertCleanupPending:
		return "本地清理积压"
	case model.ContentBackupAlertSpoolHigh:
		return "暂存空间过高"
	case model.ContentBackupAlertInodeHigh:
		return "inode 占用过高"
	case model.ContentBackupAlertFailed:
		return "出现上传失败"
	case model.ContentBackupAlertHandoffRejected:
		return "交接被拒收"
	case model.ContentBackupAlertNodeOffline:
		return "存储节点离线"
	case model.ContentBackupAlertConfigMismatch:
		return "配置版本不一致"
	case model.ContentBackupAlertSpoolCritical:
		return "暂存空间危急"
	case model.ContentBackupAlertTargetUnusable:
		return "远端不可用"
	default:
		return reason
	}
}

func contentBackupAlertReasonHint(reason string) string {
	switch reason {
	case model.ContentBackupAlertOldestPending:
		return "待上传队列里最旧的任务已等待超过设定阈值。"
	case model.ContentBackupAlertCleanupPending:
		return "已上传但仍占本地空间的任务清理超时。"
	case model.ContentBackupAlertSpoolHigh:
		return "本节点暂存目录占用已达到告警比例。"
	case model.ContentBackupAlertInodeHigh:
		return "本节点 inode 占用已达到告警比例。"
	case model.ContentBackupAlertFailed:
		return "本轮扫描发现失败任务数比上次增加。"
	case model.ContentBackupAlertHandoffRejected:
		return "本轮扫描发现交接拒收数比上次增加。"
	case model.ContentBackupAlertNodeOffline:
		return "存储节点超过离线阈值没有心跳。"
	default:
		return ""
	}
}

func contentBackupAlertFieldLabel(key string) string {
	switch key {
	case "pending_count":
		return "待上传条数"
	case "oldest_pending_age_minutes":
		return "最旧任务已等待"
	case "cleanup_pending_count":
		return "待清理条数"
	case "cleanup_pending_bytes":
		return "待清理体积"
	case "cleanup_pending_age_minutes":
		return "最旧待清理已等待"
	case "spool_bytes":
		return "暂存已用"
	case "spool_limit_bytes":
		return "暂存上限"
	case "spool_percent":
		return "暂存占用"
	case "inode_used":
		return "inode 已用"
	case "inode_total":
		return "inode 总量"
	case "inode_percent":
		return "inode 占用"
	case "failed_count":
		return "失败条数"
	case "handoff_rejected_count":
		return "交接拒收次数"
	case "last_seen_age_seconds":
		return "距上次心跳"
	case "applied_config_version":
		return "节点已应用版本"
	case "published_config_version":
		return "已发布版本"
	default:
		return key
	}
}

func contentBackupAlertFormatField(key, raw string) string {
	switch {
	case strings.HasSuffix(key, "_bytes"):
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return raw
		}
		return contentBackupAlertFormatBytes(n)
	case strings.HasSuffix(key, "_percent"):
		return raw + "%"
	case strings.HasSuffix(key, "_age_minutes"):
		return raw + " 分钟"
	case key == "last_seen_age_seconds":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return raw
		}
		if n < 60 {
			return fmt.Sprintf("%d 秒", n)
		}
		return fmt.Sprintf("%d 分 %d 秒", n/60, n%60)
	default:
		return raw
	}
}

func contentBackupAlertFormatBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.2f GB", float64(n)/(1024*1024*1024))
	}
}

func contentBackupAlertParseDetail(detail string) [][2]string {
	fields := strings.Fields(detail)
	rows := make([][2]string, 0, len(fields))
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok || key == "" {
			continue
		}
		rows = append(rows, [2]string{key, value})
	}
	return rows
}

func contentBackupAlertNotify(siteID string, eval contentBackupAlertEvaluation, recovered bool, now time.Time) dto.Notify {
	if now.IsZero() {
		now = time.Now()
	}
	status := "触发中"
	if recovered {
		status = "已恢复"
	}
	reasonTitle := contentBackupAlertReasonTitle(eval.reason)
	title := fmt.Sprintf("内容备份告警：%s（%s）", reasonTitle, status)
	return dto.NewNotify(contentBackupAlertNotifyType, title, contentBackupAlertHTML(siteID, eval, recovered, now), nil)
}

func contentBackupAlertHTML(siteID string, eval contentBackupAlertEvaluation, recovered bool, now time.Time) string {
	status := "触发中"
	statusColor := "#c5221f"
	if recovered {
		status = "已恢复"
		statusColor = "#188038"
	}
	reasonTitle := contentBackupAlertReasonTitle(eval.reason)
	hint := contentBackupAlertReasonHint(eval.reason)
	when := now.In(contentBackupAlertLocation).Format("2006-01-02 15:04:05")

	var b strings.Builder
	b.WriteString(`<div style="max-width:640px;margin:0 auto;font-family:-apple-system,'PingFang SC','Microsoft YaHei',sans-serif;color:#333;line-height:1.7;">`)
	b.WriteString(`<h2 style="color:#1a73e8;margin:0 0 12px;">内容备份告警</h2>`)
	b.WriteString(`<p style="margin:0 0 8px;">类型：<b>`)
	b.WriteString(html.EscapeString(reasonTitle))
	b.WriteString(`</b></p>`)
	b.WriteString(`<p style="margin:0 0 16px;">状态：<b style="color:`)
	b.WriteString(statusColor)
	b.WriteString(`;">`)
	b.WriteString(html.EscapeString(status))
	b.WriteString(`</b></p>`)
	if hint != "" {
		b.WriteString(`<p style="margin:0 0 16px;color:#555;">`)
		b.WriteString(html.EscapeString(hint))
		b.WriteString(`</p>`)
	}
	b.WriteString(`<table border="0" cellpadding="8" cellspacing="0" style="border-collapse:collapse;width:100%;font-size:14px;">`)
	contentBackupAlertWriteRow(&b, "站点", siteID)
	contentBackupAlertWriteRow(&b, "节点", eval.storageNodeID)
	contentBackupAlertWriteRow(&b, "时间", when+"（北京时间）")
	for _, row := range contentBackupAlertParseDetail(eval.detail) {
		contentBackupAlertWriteRow(&b, contentBackupAlertFieldLabel(row[0]), contentBackupAlertFormatField(row[0], row[1]))
	}
	b.WriteString(`</table>`)
	b.WriteString(`<p style="color:#888;font-size:12px;border-top:1px solid #eee;padding-top:8px;margin:16px 0 0;">`)
	b.WriteString(`可在 系统设置 → 内容备份 → 邮件通知 中选择要接收的告警类型。`)
	b.WriteString(`</p></div>`)
	return b.String()
}

func contentBackupAlertWriteRow(b *strings.Builder, label, value string) {
	b.WriteString(`<tr>`)
	b.WriteString(`<td style="border:1px solid #e0e0e0;background:#f8f9fa;width:36%;white-space:nowrap;">`)
	b.WriteString(html.EscapeString(label))
	b.WriteString(`</td>`)
	b.WriteString(`<td style="border:1px solid #e0e0e0;">`)
	b.WriteString(html.EscapeString(value))
	b.WriteString(`</td></tr>`)
}
