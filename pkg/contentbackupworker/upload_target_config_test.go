package contentbackupworker

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
)

// t19Target 是一份能构造成功的目标身份，各用例只改一个字段，
// 保证失败原因唯一。
func t19Target() FTPSTarget {
	return FTPSTarget{
		TargetID:   t05TargetID,
		SiteID:     t05SiteID,
		Host:       "ftps.invalid",
		Port:       21,
		Username:   "backup",
		Password:   "secret",
		CertSHA256: strings.Repeat("a", 64),
	}
}

// 目标配置错误必须由真实构造器产生：手写一个 errors.New("username is required")
// 只能证明 switch 里那行字符串没打错，构造器换一种错误表达时测试照样绿。
func t19ConstructErr(t *testing.T, mutate func(*FTPSTarget)) error {
	t.Helper()
	target := t19Target()
	mutate(&target)
	store, err := NewFTPSRemoteStore(target)
	if err == nil {
		t.Fatalf("NewFTPSRemoteStore(%+v) 应当失败，却构造成功了 %v", target, store)
	}
	return err
}

// 缺凭据是运维一眼可诊断的配置错误：它必须让任务落 failed 并暂停目标，
// 而不是进 1m/5m/30m/2h/6h 退避阶梯白跑 16 次。
func TestContentBackupUploadMissingCredentialsFailsTargetInsteadOfRetrying(t *testing.T) {
	f := t05NewFixture(t)
	job := f.seedJob(t, "missing-credentials")
	// 凭据自 2026-09-18 起是配置的一部分；这里把它们清空，模拟运维还没填。
	f.cfg.RemoteUsername = ""
	f.cfg.RemotePassword = ""

	now := time.Date(2026, 9, 18, 6, 0, 0, 0, time.UTC)
	u := f.uploader(now, 0, func(cfg *UploadConfig) {
		// 生产用的就是这个工厂：凭据从配置快照读，此刻两个都是空的。
		cfg.RemoteFactory = newRemoteFactory(t05SiteID, func() contentbackup.Config { return f.cfg })
	})

	result, err := u.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if result.Claimed != 1 || result.Failed != 1 || result.Retried != 0 {
		t.Fatalf("cycle result = %+v, want 1 claimed / 1 failed / 0 retried", result)
	}
	if result.PausedJobs != 1 {
		t.Fatalf("paused_jobs = %d, want 1：配置错误必须暂停向该目标新建上传连接（文档 6.2）", result.PausedJobs)
	}

	after := f.job(t, job.JobID)
	if after.Status != model.ContentBackupStatusFailed {
		t.Fatalf("status = %q, want failed", after.Status)
	}
	if after.LastErrorCode != UploadCodeTargetConfig {
		t.Fatalf("last_error_code = %q, want %q", after.LastErrorCode, UploadCodeTargetConfig)
	}
	if !strings.Contains(after.LastErrorMessage, "username") {
		t.Fatalf("last_error_message = %q, 必须保留真实原因", after.LastErrorMessage)
	}
	// MarkFailed 的终态约定是 available_at=0；只要不是 now+RetryDelay，就说明没进退避阶梯。
	if after.AvailableAt != 0 {
		t.Fatalf("available_at = %d, want 0：终态不该再排下一次退避", after.AvailableAt)
	}
	if after.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1：Claim 抬一次，之后不该再空跑 15 次", after.Attempts)
	}
	if _, err := os.Stat(job.LocalPath); err != nil {
		t.Fatalf("本地备份必须留着等运维改完配置重试: %v", err)
	}

	// 配置错误是人改完就能恢复的：不能被记成不可恢复码而永久挡住重试。
	retry, err := f.store.RetryFailed(context.Background(), job.JobID, now)
	if err != nil {
		t.Fatalf("RetryFailed: %v", err)
	}
	if retry.Result != model.ContentBackupRetryQueued {
		t.Fatalf("RetryFailed = %+v, want queued：改完凭据必须能把任务放回队列", retry)
	}
}

func TestContentBackupClassifyTargetConfigErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*FTPSTarget)
	}{
		{"缺用户名", func(tg *FTPSTarget) { tg.Username = "" }},
		{"缺主机", func(tg *FTPSTarget) { tg.Host = "" }},
		{"端口越界", func(tg *FTPSTarget) { tg.Port = 0 }},
		{"缺 target_id", func(tg *FTPSTarget) { tg.TargetID = "" }},
		{"站点标签非法", func(tg *FTPSTarget) { tg.SiteID = "AI!" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := ClassifyUploadError(t19ConstructErr(t, tc.mutate))
			if info.Class != UploadClassTargetUnusable {
				t.Fatalf("class = %s, want target_unusable", info.Class)
			}
			if info.Code != UploadCodeTargetConfig {
				t.Fatalf("code = %q, want %q", info.Code, UploadCodeTargetConfig)
			}
			if info.Retryable {
				t.Fatal("配置错误退避重试 16 次不会自己变好")
			}
			if !info.PauseTarget {
				t.Fatal("必须暂停目标")
			}
		})
	}

	t.Run("证书指纹保留更具体的码", func(t *testing.T) {
		info := ClassifyUploadError(t19ConstructErr(t, func(tg *FTPSTarget) { tg.CertSHA256 = "abc" }))
		if info.Code != UploadCodeInvalidPin || !info.PauseTarget {
			t.Fatalf("info = %+v, want invalid_pin 且暂停目标", info)
		}
	})

	t.Run("未知错误仍然可重试", func(t *testing.T) {
		info := ClassifyUploadError(errors.New("content backup: brand new failure mode"))
		if info.Code != UploadCodeUnknown || !info.Retryable {
			t.Fatalf("info = %+v, want unknown 且可重试：新故障模式不该被一刀切判死", info)
		}
	})
}

// 凭据只配了一半（有用户名、没口令）过得了构造期，只能由服务器的 530 兜住。
// 这条路径同样必须是终态 + 暂停目标，不能滑回退避重试白跑 16 次。
func TestContentBackupHalfConfiguredCredentialsAlsoPauseTarget(t *testing.T) {
	content := cbTestContent(2048)
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, func(tg *FTPSTarget) { tg.Password = "" })
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	info := ClassifyUploadError(err)
	if info.Code != UploadCodeAuth {
		t.Fatalf("code = %q, want %q（err=%v）", info.Code, UploadCodeAuth, err)
	}
	if info.Retryable || !info.PauseTarget {
		t.Fatalf("info = %+v, 半配凭据必须落终态并暂停目标", info)
	}
	if srv.hasEvent("STOR") || len(srv.listPaths()) != 0 {
		t.Fatalf("认证没过不能有任何正文落到远端: %v", srv.eventSnapshot())
	}
}
