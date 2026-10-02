package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

// content_backup_alerts.go 是 content-backup 告警调度的唯一实现：按 design doc
// docs/2026-09-15-channel-content-backup-upload.md §7.3 的规则周期评估每个存储节点，
// 通过 model.ContentBackupStore 的 CAS 方法发布/解除告警，并去重发送告警邮件。
// 邮件只发给配置里的 notify_emails，经本站 SMTP 发出；总开关 notify_email_enabled 默认关。
//
// 刻意不按 common.IsMasterNode 收敛：doc 要求所有节点独立评估，真正的"只发一次"
// 由 ClaimAlertSend 的数据库租约保证——多进程可以重复判断，但只有一个进程真正发送。

const (
	// contentBackupAlertInterval 是本调度器的轮询间隔。pkg/contentbackup.Config 里
	// 没有为"告警扫描频率"单独开字段（已核对 config.go 全部字段），沿用
	// iq_test_schedule.go 硬编码间隔的先例。
	contentBackupAlertInterval = 30 * time.Second
	// contentBackupAlertLeaseWindow 必须短于 contentBackupAlertInterval：发送失败后
	// 租约到期即可在下一轮重试，不占用 AlertDedupMinutes 那个 30 分钟的语义去重窗口
	// ——ClaimAlertSend 的 send_lease_until 和 last_sent_at 是两个独立闸门。
	contentBackupAlertLeaseWindow = 20 * time.Second
)

// StartContentBackupAlertTask 是 main.go 接线用的导出入口。功能默认关闭时的安全行为
// 完全依赖 operation_setting.GetContentBackupConfig().Enabled：关闭时每轮直接跳过，
// 不查库、不发通知。
func StartContentBackupAlertTask() error {
	return backgroundtask.Start("content-backup-alert", func(ctx context.Context) {
		owner := contentBackupAlertOwner()
		tracker := newContentBackupAlertTracker()
		ticker := time.NewTicker(contentBackupAlertInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cfg := operation_setting.GetContentBackupConfig()
				siteID := cfg.SiteLabel
				if siteID == "" || !cfg.Enabled {
					continue
				}
				store := model.NewContentBackupStore(model.DB, siteID)
				if err := runContentBackupAlertRound(ctx, store, cfg, tracker, owner, time.Now()); err != nil {
					common.SysError("content backup alert round: " + err.Error())
				}
			}
		}
	})
}

func contentBackupAlertOwner() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

// contentBackupAlertTracker 记录跨轮次的"上次观测值"，只用于 failed / handoff_rejected
// 这两个没有内建时间戳、只能靠"比上次高"边沿触发的原因。进程重启会丢失历史基线，可接受：
// design doc 把告警定位为 best-effort，真正的发送去重永远由数据库租约兜底，不靠这份内存状态。
type contentBackupAlertTracker struct {
	mu    sync.Mutex
	nodes map[string]contentBackupAlertNodeMemory
}

type contentBackupAlertNodeMemory struct {
	failedCount          int64
	handoffRejectedCount int64
}

func newContentBackupAlertTracker() *contentBackupAlertTracker {
	return &contentBackupAlertTracker{nodes: make(map[string]contentBackupAlertNodeMemory)}
}

// delta 返回本轮相对上一轮是否"新增"。第一次观测到某个节点时只落基线、不触发，
// 避免进程重启后把历史遗留的失败数当成新故障告警一次。
func (t *contentBackupAlertTracker) delta(nodeID string, failedCount, handoffRejectedCount int64) (failedIncreased, handoffIncreased bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	prev, seen := t.nodes[nodeID]
	t.nodes[nodeID] = contentBackupAlertNodeMemory{failedCount: failedCount, handoffRejectedCount: handoffRejectedCount}
	if !seen {
		return false, false
	}
	return failedCount > prev.failedCount, handoffRejectedCount > prev.handoffRejectedCount
}

// contentBackupAlertEvaluation 是一个存储节点、一个告警原因的评估结果。detail 只允许
// 携带 site/node/reason/counts/time 这类运营数字，绝不能塞请求体、路径或 FTPS 凭据。
type contentBackupAlertEvaluation struct {
	storageNodeID string
	reason        string
	firing        bool
	detail        string
}

func runContentBackupAlertRound(ctx context.Context, store *model.ContentBackupStore, cfg contentbackup.Config, tracker *contentBackupAlertTracker, owner string, now time.Time) error {
	nodes, err := store.ListNodeStatus(ctx)
	if err != nil {
		return fmt.Errorf("list content backup node status: %w", err)
	}
	if len(nodes) == 0 {
		return nil
	}

	dedup := time.Duration(cfg.AlertDedupMinutes) * time.Minute

	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	for _, node := range nodes {
		evaluations, err := contentBackupEvaluateNode(ctx, store, cfg, tracker, node, now)
		if err != nil {
			common.SysError(fmt.Sprintf("content backup alert evaluate %s: %s", node.StorageNodeID, err.Error()))
			record(err)
			continue
		}
		for _, eval := range evaluations {
			if err := applyContentBackupAlertEvaluation(ctx, store, eval, cfg, dedup, owner, now); err != nil {
				common.SysError(fmt.Sprintf("content backup alert %s/%s: %s", eval.storageNodeID, eval.reason, err.Error()))
				record(err)
			}
		}
	}
	return firstErr
}

func contentBackupEvaluateNode(ctx context.Context, store *model.ContentBackupStore, cfg contentbackup.Config, tracker *contentBackupAlertTracker, node model.ContentBackupNodeStatus, now time.Time) ([]contentBackupAlertEvaluation, error) {
	counts, err := store.CountJobs(ctx, node.StorageNodeID)
	if err != nil {
		return nil, fmt.Errorf("count jobs: %w", err)
	}

	inodeAlreadyFiring, err := contentBackupAlertAlreadyFiring(ctx, store, node.StorageNodeID, model.ContentBackupAlertInodeHigh)
	if err != nil {
		return nil, fmt.Errorf("get inode alert state: %w", err)
	}

	// limit=1: 只要最早一个"已到清理时间但还没清完"的任务的年龄，用它持久化的
	// cleanup_available_at 判断积压时长，比本地内存计时更准（重启、多进程都不失真）。
	cleanupJobs, err := store.ListCleanupJobs(ctx, node.StorageNodeID, now, 1)
	if err != nil {
		return nil, fmt.Errorf("list cleanup jobs: %w", err)
	}

	failedIncreased, handoffIncreased := tracker.delta(node.StorageNodeID, counts.FailedCount, node.HandoffRejectedCount)

	return []contentBackupAlertEvaluation{
		contentBackupEvaluateSpool(cfg, node),
		contentBackupEvaluateInode(cfg, node, inodeAlreadyFiring),
		contentBackupEvaluateOldestPending(cfg, node.StorageNodeID, counts, now),
		contentBackupEvaluateCleanupPending(cfg, node.StorageNodeID, counts, cleanupJobs, now),
		{
			storageNodeID: node.StorageNodeID,
			reason:        model.ContentBackupAlertFailed,
			firing:        failedIncreased,
			detail:        fmt.Sprintf("failed_count=%d", counts.FailedCount),
		},
		{
			storageNodeID: node.StorageNodeID,
			reason:        model.ContentBackupAlertHandoffRejected,
			firing:        handoffIncreased,
			detail:        fmt.Sprintf("handoff_rejected_count=%d", node.HandoffRejectedCount),
		},
		contentBackupEvaluateNodeOffline(cfg, node, now),
		contentBackupEvaluateConfigMismatch(cfg, node),
	}, nil
}

func contentBackupAlertAlreadyFiring(ctx context.Context, store *model.ContentBackupStore, nodeID, reason string) (bool, error) {
	alert, err := store.GetAlert(ctx, nodeID, reason)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return alert.State == model.ContentBackupAlertFiring, nil
}

func contentBackupPercent(used, total int64) int {
	if total <= 0 || used <= 0 {
		return 0
	}
	return int(used * 100 / total)
}

func contentBackupEvaluateSpool(cfg contentbackup.Config, node model.ContentBackupNodeStatus) contentBackupAlertEvaluation {
	percent := contentBackupPercent(node.SpoolBytes, node.SpoolLimitBytes)
	return contentBackupAlertEvaluation{
		storageNodeID: node.StorageNodeID,
		reason:        model.ContentBackupAlertSpoolHigh,
		firing:        node.SpoolLimitBytes > 0 && percent >= cfg.SpoolAlertPercent,
		detail:        fmt.Sprintf("spool_bytes=%d spool_limit_bytes=%d spool_percent=%d", node.SpoolBytes, node.SpoolLimitBytes, percent),
	}
}

// contentBackupEvaluateInode 需要迟滞：90% 触发、80% 恢复（design doc §7.3）。已经在
// firing 时改用恢复阈值判断"是否还在故障"，避免卡在 80%-90% 之间来回抖动发通知。
func contentBackupEvaluateInode(cfg contentbackup.Config, node model.ContentBackupNodeStatus, alreadyFiring bool) contentBackupAlertEvaluation {
	used := node.InodeTotal - node.FreeInodes
	percent := contentBackupPercent(used, node.InodeTotal)
	threshold := cfg.InodeAlertPercent
	if alreadyFiring {
		threshold = cfg.InodeRecoverPercent
	}
	return contentBackupAlertEvaluation{
		storageNodeID: node.StorageNodeID,
		reason:        model.ContentBackupAlertInodeHigh,
		firing:        node.InodeTotal > 0 && percent >= threshold,
		detail:        fmt.Sprintf("inode_used=%d inode_total=%d inode_percent=%d", used, node.InodeTotal, percent),
	}
}

func contentBackupEvaluateOldestPending(cfg contentbackup.Config, nodeID string, counts model.ContentBackupJobCounts, now time.Time) contentBackupAlertEvaluation {
	threshold := time.Duration(cfg.OldestPendingAlertMinutes) * time.Minute
	firing := false
	var ageMinutes int64
	if counts.OldestPendingAt > 0 {
		age := now.Sub(time.Unix(counts.OldestPendingAt, 0))
		ageMinutes = int64(age / time.Minute)
		firing = age >= threshold
	}
	return contentBackupAlertEvaluation{
		storageNodeID: nodeID,
		reason:        model.ContentBackupAlertOldestPending,
		firing:        firing,
		detail:        fmt.Sprintf("pending_count=%d oldest_pending_age_minutes=%d", counts.PendingCount, ageMinutes),
	}
}

func contentBackupEvaluateCleanupPending(cfg contentbackup.Config, nodeID string, counts model.ContentBackupJobCounts, oldest []model.ContentBackupJob, now time.Time) contentBackupAlertEvaluation {
	threshold := time.Duration(cfg.CleanupPendingAlertMinutes) * time.Minute
	firing := false
	var ageMinutes int64
	if len(oldest) > 0 {
		// cleanup_available_at=0 是历史上传路径的默认值，不能当成 1970 年起算，
		// 否则刚上传、还没来得及删本地文件就会按「积压几十年」告警。
		avail := oldest[0].CleanupAvailableAt
		if avail <= 0 {
			avail = oldest[0].UploadedAt
		}
		if avail > 0 {
			age := now.Sub(time.Unix(avail, 0))
			ageMinutes = int64(age / time.Minute)
			firing = age >= threshold
		}
	}
	return contentBackupAlertEvaluation{
		storageNodeID: nodeID,
		reason:        model.ContentBackupAlertCleanupPending,
		firing:        firing,
		detail:        fmt.Sprintf("cleanup_pending_count=%d cleanup_pending_bytes=%d cleanup_pending_age_minutes=%d", counts.CleanupPendingCount, counts.CleanupPendingBytes, ageMinutes),
	}
}

func contentBackupEvaluateNodeOffline(cfg contentbackup.Config, node model.ContentBackupNodeStatus, now time.Time) contentBackupAlertEvaluation {
	threshold := time.Duration(cfg.NodeOfflineSeconds) * time.Second
	age := now.Sub(time.Unix(node.LastSeenAt, 0))
	return contentBackupAlertEvaluation{
		storageNodeID: node.StorageNodeID,
		reason:        model.ContentBackupAlertNodeOffline,
		firing:        age >= threshold,
		detail:        fmt.Sprintf("last_seen_age_seconds=%d", int64(age/time.Second)),
	}
}

// contentBackupEvaluateConfigMismatch 比的是"节点已应用版本"对"业务进程已发布版本"，
// 不是节点行自己那两列：心跳把同一份快照同时写进 config_version 和 applied_config_version
// （pkg/contentbackupworker/runtime.go），两列恒等，自己跟自己比永远看不见不一致。真正要抓的
// 是 daemon 重载配置失败后保留旧快照继续跑（cmd/content-backupd/main.go 只记日志），而那个
// "已发布到哪一版"只有业务进程手里的配置知道。
//
// 用 < 而不是 !=：版本号由 SaveContentBackupConfig 以 expected_version+1 严格单调发放，
// 节点版本高于本进程所见只说明本进程的 option 内存快照还没轮询到（最长 60 秒），不是节点故障。
func contentBackupEvaluateConfigMismatch(cfg contentbackup.Config, node model.ContentBackupNodeStatus) contentBackupAlertEvaluation {
	return contentBackupAlertEvaluation{
		storageNodeID: node.StorageNodeID,
		reason:        model.ContentBackupAlertConfigMismatch,
		firing:        node.AppliedConfigVersion < cfg.Version,
		detail:        fmt.Sprintf("applied_config_version=%d published_config_version=%d", node.AppliedConfigVersion, cfg.Version),
	}
}

// contentBackupAlertShouldNotify 只放真正要人处理、管理端打开了告警邮件总开关、且勾选了
// 该类型的故障。恢复、配置版本短暂落后（保存后下一轮心跳就会对齐）都不发信。
func contentBackupAlertShouldNotify(cfg contentbackup.Config, reason string, recovered bool) bool {
	if recovered || reason == model.ContentBackupAlertConfigMismatch {
		return false
	}
	return cfg.NotifyEmailEnabled && cfg.AlertNotifyEnabled(reason)
}

// applyContentBackupAlertEvaluation 是触发态与恢复态两条路径的唯一入口。
//
// 触发态：UpsertAlert 记账 -> ClaimAlertSend 抢租约 -> 抢到才发邮件 -> 真发出去才
// MarkAlertSent。发送出错不能调用 MarkAlertSent，否则 30 分钟去重窗口内不会再重试。
// 不该打扰的原因（配置版本短暂落后）仍记账，但标记为已发送以免空转抢租约。
//
// 恢复态：ResolveAlert 只在 firing->resolved 这一次跳变时落库，不再发信。
func applyContentBackupAlertEvaluation(ctx context.Context, store *model.ContentBackupStore, eval contentBackupAlertEvaluation, cfg contentbackup.Config, dedup time.Duration, owner string, now time.Time) error {
	if eval.firing {
		if err := store.UpsertAlert(ctx, model.ContentBackupAlert{
			StorageNodeID: eval.storageNodeID,
			Reason:        eval.reason,
			Message:       eval.detail,
		}, now); err != nil {
			return fmt.Errorf("upsert alert: %w", err)
		}

		// 总开关关着、或管理员关掉的类型只记账、不抢发送租约，重新打开后下一轮就能立刻发。
		// 配置版本短暂落后仍抢租约并标已发送，避免空转。
		if !contentBackupAlertShouldNotify(cfg, eval.reason, false) {
			if eval.reason == model.ContentBackupAlertConfigMismatch {
				won, err := store.ClaimAlertSend(ctx, eval.storageNodeID, eval.reason, owner, now, now.Add(contentBackupAlertLeaseWindow), dedup)
				if err != nil {
					return fmt.Errorf("claim alert send: %w", err)
				}
				if !won {
					return nil
				}
				return store.MarkAlertSent(ctx, eval.storageNodeID, eval.reason, owner, now)
			}
			return nil
		}

		won, err := store.ClaimAlertSend(ctx, eval.storageNodeID, eval.reason, owner, now, now.Add(contentBackupAlertLeaseWindow), dedup)
		if err != nil {
			return fmt.Errorf("claim alert send: %w", err)
		}
		if !won {
			return nil
		}

		// 不判收件人为空：配置校验保证总开关打开时至少有一个收件人。
		subject, content := contentBackupAlertMail(store.SiteID(), eval, false, now)
		if err := contentBackupSendEmail(subject, strings.Join(cfg.NotifyEmailList(), ";"), content); err != nil {
			return fmt.Errorf("send alert email: %w", err)
		}
		if err := store.MarkAlertSent(ctx, eval.storageNodeID, eval.reason, owner, now); err != nil {
			return fmt.Errorf("mark alert sent: %w", err)
		}
		return nil
	}

	if _, err := store.ResolveAlert(ctx, eval.storageNodeID, eval.reason, now); err != nil {
		return fmt.Errorf("resolve alert: %w", err)
	}
	return nil
}

// contentBackupSendEmail 是告警邮件和测试邮件共用的唯一出口：经本站 SMTP 发出，多个收件人
// 用 ; 连接（common.SendEmail 按 ; 切开逐个 RCPT）。测试重写这个包级变量避免真实网络 I/O。
var contentBackupSendEmail = common.SendEmail
