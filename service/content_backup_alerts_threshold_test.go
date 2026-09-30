package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"gorm.io/gorm"
)

// 本文件只测判定阈值本身：8 种告警原因里，原有用例覆盖的是发送租约、去重和恢复通知，
// 「多少算故障」这一层一条没测。阈值算错比没有告警更糟——它会让运维相信一切正常。
// 所有断言都取自设计文档 7.3 的原文数值，不取实现里的常量，否则实现改了测试跟着改，
// 等于没测。

const (
	contentBackupAlertTestSite = "cbalert"
	contentBackupAlertTestNode = "node-a"
)

// contentBackupAlertRestingNode 是一台完全健康的节点，字段严格按 runtimeHeartbeat
// 的真实写法填：两个配置版本列都来自 daemon 自己的同一份快照。每个用例只移动一个字段，
// 于是任何 firing 都只能由那个字段引起。
func contentBackupAlertRestingNode(cfg contentbackup.Config, now time.Time) model.ContentBackupNodeStatus {
	return model.ContentBackupNodeStatus{
		SiteID:               contentBackupAlertTestSite,
		StorageNodeID:        contentBackupAlertTestNode,
		ProcessID:            "process-1",
		ConfigVersion:        cfg.Version,
		AppliedConfigVersion: cfg.Version,
		LastSeenAt:           now.Unix(),
		SampledAt:            now.Unix(),
	}
}

func contentBackupAlertFiringByReason(t *testing.T, db *gorm.DB, cfg contentbackup.Config, tracker *contentBackupAlertTracker, node model.ContentBackupNodeStatus, now time.Time) map[string]bool {
	t.Helper()
	store := model.NewContentBackupStore(db, contentBackupAlertTestSite)
	evaluations, err := contentBackupEvaluateNode(context.Background(), store, cfg, tracker, node, now)
	if err != nil {
		t.Fatalf("evaluate node: %v", err)
	}
	firing := make(map[string]bool, len(evaluations))
	for _, eval := range evaluations {
		firing[eval.reason] = eval.firing
	}
	return firing
}

func contentBackupAlertSeedJob(t *testing.T, db *gorm.DB, job model.ContentBackupJob) {
	t.Helper()
	job.SiteID = contentBackupAlertTestSite
	job.StorageNodeID = contentBackupAlertTestNode
	job.JobID = contentbackup.NewJobID()
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed content backup job: %v", err)
	}
}

// 配置不一致（文档 7.3）。daemon 每 5 秒重载一次配置，校验失败时保留旧快照且只记日志
// （cmd/content-backupd/main.go），所以"节点还在跑老配置"是真实可达状态。但心跳把同一份
// 快照同时写进 config_version 和 applied_config_version，拿节点行自己的两列相比永远相等，
// 这条告警结构性永不触发。唯一知道"已发布到哪一版"的是业务进程手里的配置。
func TestContentBackupAlertConfigMismatchFiresWhenNodeLagsPublishedVersion(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	now := contentBackupAlertTestBase
	cfg := contentbackup.DefaultConfig()
	cfg.Version = 5

	lagging := contentBackupAlertRestingNode(cfg, now)
	lagging.ConfigVersion, lagging.AppliedConfigVersion = 3, 3
	if !contentBackupAlertFiringByReason(t, db, cfg, newContentBackupAlertTracker(), lagging, now)[model.ContentBackupAlertConfigMismatch] {
		t.Fatalf("节点仍在 v3、已发布 v5：config_mismatch 必须告警")
	}

	current := contentBackupAlertRestingNode(cfg, now)
	if contentBackupAlertFiringByReason(t, db, cfg, newContentBackupAlertTracker(), current, now)[model.ContentBackupAlertConfigMismatch] {
		t.Fatalf("节点已应用 v5：config_mismatch 不得告警")
	}

	// 版本号由 SaveContentBackupConfig 以 expectedVersion+1 严格单调发放，所以"节点版本
	// 高于本进程所见"只意味着本进程的 option 内存快照还没轮询到（最长 60 秒），不是节点故障。
	ahead := contentBackupAlertRestingNode(cfg, now)
	ahead.ConfigVersion, ahead.AppliedConfigVersion = 6, 6
	if contentBackupAlertFiringByReason(t, db, cfg, newContentBackupAlertTracker(), ahead, now)[model.ContentBackupAlertConfigMismatch] {
		t.Fatalf("本进程配置缓存落后于节点时不得误报 config_mismatch")
	}
}

// spool 80% 告警、inode 90% 告警且 80% 恢复、45 秒无心跳告警（文档 7.3）。
func TestContentBackupAlertSpoolAndOfflineBoundaries(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	now := contentBackupAlertTestBase
	cfg := contentbackup.DefaultConfig()

	cases := []struct {
		name   string
		reason string
		mutate func(*model.ContentBackupNodeStatus)
		want   bool
	}{
		{"spool 79% 不告警", model.ContentBackupAlertSpoolHigh, func(n *model.ContentBackupNodeStatus) {
			n.SpoolLimitBytes, n.SpoolBytes = 1000, 799
		}, false},
		{"spool 恰好 80% 告警", model.ContentBackupAlertSpoolHigh, func(n *model.ContentBackupNodeStatus) {
			n.SpoolLimitBytes, n.SpoolBytes = 1000, 800
		}, true},
		{"spool 上限未知时不告警", model.ContentBackupAlertSpoolHigh, func(n *model.ContentBackupNodeStatus) {
			n.SpoolLimitBytes, n.SpoolBytes = 0, 1<<40
		}, false},
		{"inode 89% 不告警", model.ContentBackupAlertInodeHigh, func(n *model.ContentBackupNodeStatus) {
			n.InodeTotal, n.FreeInodes = 100, 11
		}, false},
		{"inode 恰好 90% 告警", model.ContentBackupAlertInodeHigh, func(n *model.ContentBackupNodeStatus) {
			n.InodeTotal, n.FreeInodes = 100, 10
		}, true},
		{"心跳 44 秒前不算离线", model.ContentBackupAlertNodeOffline, func(n *model.ContentBackupNodeStatus) {
			n.LastSeenAt = now.Add(-44 * time.Second).Unix()
		}, false},
		{"心跳 45 秒前算离线", model.ContentBackupAlertNodeOffline, func(n *model.ContentBackupNodeStatus) {
			n.LastSeenAt = now.Add(-45 * time.Second).Unix()
		}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := contentBackupAlertRestingNode(cfg, now)
			tc.mutate(&node)
			got := contentBackupAlertFiringByReason(t, db, cfg, newContentBackupAlertTracker(), node, now)[tc.reason]
			if got != tc.want {
				t.Fatalf("%s: firing=%v want %v", tc.reason, got, tc.want)
			}
		})
	}
}

// 最旧积压超过 15 分钟、待清理超过 30 分钟告警（文档 7.3）。这两条的年龄不取节点心跳里的
// 汇总列，而是 CountJobs / ListCleanupJobs 从任务行现算，所以用例必须落真实任务行，
// 否则测不到取数这一段接线。
func TestContentBackupAlertPendingAgeBoundaries(t *testing.T) {
	now := contentBackupAlertTestBase
	cfg := contentbackup.DefaultConfig()

	cases := []struct {
		name   string
		reason string
		age    time.Duration
		seed   func(t *testing.T, db *gorm.DB, at int64)
		want   bool
	}{
		{"最旧 pending 14 分钟不告警", model.ContentBackupAlertOldestPending, 14 * time.Minute, contentBackupAlertSeedPending, false},
		{"最旧 pending 15 分钟告警", model.ContentBackupAlertOldestPending, 15 * time.Minute, contentBackupAlertSeedPending, true},
		{"待清理 29 分钟不告警", model.ContentBackupAlertCleanupPending, 29 * time.Minute, contentBackupAlertSeedCleanup, false},
		{"待清理 30 分钟告警", model.ContentBackupAlertCleanupPending, 30 * time.Minute, contentBackupAlertSeedCleanup, true},
		{"可清理时间为 0 且刚上传不告警", model.ContentBackupAlertCleanupPending, 0, contentBackupAlertSeedCleanupZeroAvailable, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := contentBackupAlertTestDB(t)
			tc.seed(t, db, now.Add(-tc.age).Unix())
			node := contentBackupAlertRestingNode(cfg, now)
			got := contentBackupAlertFiringByReason(t, db, cfg, newContentBackupAlertTracker(), node, now)[tc.reason]
			if got != tc.want {
				t.Fatalf("%s: firing=%v want %v", tc.reason, got, tc.want)
			}
		})
	}
}

func contentBackupAlertSeedPending(t *testing.T, db *gorm.DB, at int64) {
	t.Helper()
	contentBackupAlertSeedJob(t, db, model.ContentBackupJob{
		Status:       model.ContentBackupStatusPending,
		CleanupState: model.ContentBackupCleanupNotApplicable,
		CreatedAt:    at,
	})
}

func contentBackupAlertSeedCleanup(t *testing.T, db *gorm.DB, at int64) {
	t.Helper()
	contentBackupAlertSeedJob(t, db, model.ContentBackupJob{
		Status:             model.ContentBackupStatusUploaded,
		CleanupState:       model.ContentBackupCleanupPending,
		CreatedAt:          at,
		CleanupAvailableAt: at,
		CompressedBytes:    4096,
	})
}

func contentBackupAlertSeedCleanupZeroAvailable(t *testing.T, db *gorm.DB, at int64) {
	t.Helper()
	contentBackupAlertSeedJob(t, db, model.ContentBackupJob{
		Status:             model.ContentBackupStatusUploaded,
		CleanupState:       model.ContentBackupCleanupPending,
		CreatedAt:          at,
		UploadedAt:         at,
		CleanupAvailableAt: 0,
		CompressedBytes:    4096,
	})
}

// inode 的迟滞是非对称的：90% 触发、掉回 80% 以下才恢复（文档 7.3）。判定要读库里已存的
// 告警状态，所以整轮跑 runContentBackupAlertRound，断言落库的 state，而不是直接调判定函数——
// 后者测不到"已 firing 就换用恢复阈值"这条接线。
func TestContentBackupAlertInodeHysteresis(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	contentBackupAlertSeedRootUser(t, db, "root@example.com", dto.UserSetting{NotifyType: dto.NotifyTypeEmail})
	contentBackupAlertUseSpy(t, &contentBackupAlertSpy{outcome: contentBackupAlertOutcome{attempted: true}})

	cfg := contentbackup.DefaultConfig()
	store := model.NewContentBackupStore(db, contentBackupAlertTestSite)
	tracker := newContentBackupAlertTracker()

	steps := []struct {
		name       string
		freeInodes int64
		want       string
	}{
		{"89% 不触发", 11, ""},
		{"90% 触发", 10, model.ContentBackupAlertFiring},
		{"回落到 81% 仍在告警", 19, model.ContentBackupAlertFiring},
		{"跌破 80% 才恢复", 21, model.ContentBackupAlertResolved},
	}

	now := contentBackupAlertTestBase
	for i, step := range steps {
		now = contentBackupAlertTestBase.Add(time.Duration(i) * time.Minute)
		node := contentBackupAlertRestingNode(cfg, now)
		node.InodeTotal, node.FreeInodes = 100, step.freeInodes
		if err := store.SaveNodeStatus(context.Background(), node); err != nil {
			t.Fatalf("%s: save node status: %v", step.name, err)
		}
		if err := runContentBackupAlertRound(context.Background(), store, cfg, tracker, "owner-1", now); err != nil {
			t.Fatalf("%s: alert round: %v", step.name, err)
		}

		alert, err := store.GetAlert(context.Background(), contentBackupAlertTestNode, model.ContentBackupAlertInodeHigh)
		if step.want == "" {
			if err == nil {
				t.Fatalf("%s: 不该产生 inode_high 告警行，实得 state=%s", step.name, alert.State)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: 读取 inode_high 告警: %v", step.name, err)
		}
		if alert.State != step.want {
			t.Fatalf("%s: state=%s want %s", step.name, alert.State, step.want)
		}
	}
}

// failed 新增、交接拒收（文档 7.3）。这两条没有内建时间戳，只能靠"比上一轮高"做边沿触发：
// 首次观测必须只落基线不告警，否则进程一重启就会把历史遗留的失败数当成新故障喊一次。
func TestContentBackupAlertFailedAndHandoffEdgeTriggers(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	now := contentBackupAlertTestBase
	cfg := contentbackup.DefaultConfig()
	tracker := newContentBackupAlertTracker()

	node := contentBackupAlertRestingNode(cfg, now)
	node.HandoffRejectedCount = 2
	contentBackupAlertSeedJob(t, db, model.ContentBackupJob{
		Status:       model.ContentBackupStatusFailed,
		CleanupState: model.ContentBackupCleanupNotApplicable,
		CreatedAt:    now.Unix(),
	})

	firing := contentBackupAlertFiringByReason(t, db, cfg, tracker, node, now)
	if firing[model.ContentBackupAlertFailed] || firing[model.ContentBackupAlertHandoffRejected] {
		t.Fatalf("首次观测只该落基线，不得告警：failed=%v handoff=%v",
			firing[model.ContentBackupAlertFailed], firing[model.ContentBackupAlertHandoffRejected])
	}

	firing = contentBackupAlertFiringByReason(t, db, cfg, tracker, node, now)
	if firing[model.ContentBackupAlertFailed] || firing[model.ContentBackupAlertHandoffRejected] {
		t.Fatalf("计数没变不得重复告警：failed=%v handoff=%v",
			firing[model.ContentBackupAlertFailed], firing[model.ContentBackupAlertHandoffRejected])
	}

	contentBackupAlertSeedJob(t, db, model.ContentBackupJob{
		Status:       model.ContentBackupStatusFailed,
		CleanupState: model.ContentBackupCleanupNotApplicable,
		CreatedAt:    now.Unix(),
	})
	firing = contentBackupAlertFiringByReason(t, db, cfg, tracker, node, now)
	if !firing[model.ContentBackupAlertFailed] {
		t.Fatalf("failed 由 1 增到 2 必须告警")
	}
	if firing[model.ContentBackupAlertHandoffRejected] {
		t.Fatalf("交接拒收数未变，不得被 failed 的变化连带触发")
	}

	node.HandoffRejectedCount = 3
	firing = contentBackupAlertFiringByReason(t, db, cfg, tracker, node, now)
	if !firing[model.ContentBackupAlertHandoffRejected] {
		t.Fatalf("交接拒收由 2 增到 3 必须告警")
	}
	if firing[model.ContentBackupAlertFailed] {
		t.Fatalf("failed 数未变，不得被交接拒收的变化连带触发")
	}
}
