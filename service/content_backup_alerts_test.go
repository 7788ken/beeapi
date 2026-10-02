package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// contentBackupAlertTestBase is a fixed instant so every test's timeline math
// (dedup windows, lease windows, offline thresholds) is reproducible and
// independent of wall-clock time.
var contentBackupAlertTestBase = time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC)

// contentBackupAlertTestDB opens an independent SQLite database under
// t.TempDir(), migrates the content-backup tables, and points the
// package-level model.DB at it for the duration of the test (restored on
// cleanup). service already has a TestMain (task_billing_test.go) that sets
// model.DB once for the whole binary, so this swaps it per test instead of
// assuming a fixed global.
func contentBackupAlertTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "content-backup-alert.db")
	db, err := gorm.Open(sqlite.Open(path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open content backup alert test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get content backup alert test sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(model.ContentBackupModels()...); err != nil {
		t.Fatalf("migrate content backup alert test db: %v", err)
	}

	origDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = origDB })
	return db
}

// contentBackupSentMail is one call to contentBackupSendEmail.
type contentBackupSentMail struct {
	subject  string
	receiver string
	content  string
}

// contentBackupMailSpy stands in for contentBackupSendEmail so tests never
// perform real SMTP I/O. It is safe for concurrent use: the lease race test
// below calls it from multiple goroutines.
type contentBackupMailSpy struct {
	mu   sync.Mutex
	sent []contentBackupSentMail
	err  error
}

func (s *contentBackupMailSpy) send(subject, receiver, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, contentBackupSentMail{subject: subject, receiver: receiver, content: content})
	return s.err
}

func (s *contentBackupMailSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

func (s *contentBackupMailSpy) last(t *testing.T) contentBackupSentMail {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sent) == 0 {
		t.Fatal("no email was sent")
	}
	return s.sent[len(s.sent)-1]
}

func contentBackupUseMailSpy(t *testing.T, spy *contentBackupMailSpy) {
	t.Helper()
	orig := contentBackupSendEmail
	contentBackupSendEmail = spy.send
	t.Cleanup(func() { contentBackupSendEmail = orig })
}

// contentBackupAlertEmailConfig is a config with the alert email switch on and
// two recipients in the form the console saves. It must pass ValidateConfig,
// so every test runs against a config a real site can hold.
func contentBackupAlertEmailConfig(t *testing.T) contentbackup.Config {
	t.Helper()
	cfg := contentbackup.DefaultConfig()
	cfg.NotifyEmailEnabled = true
	cfg.NotifyEmails = "a@x.com; b@y.com"
	if err := contentbackup.ValidateConfig(cfg); err != nil {
		t.Fatalf("alert email test config must be valid: %v", err)
	}
	return cfg
}

func contentBackupNodeOfflineEval() contentBackupAlertEvaluation {
	return contentBackupAlertEvaluation{
		storageNodeID: "node-a",
		reason:        model.ContentBackupAlertNodeOffline,
		firing:        true,
		detail:        "last_seen_age_seconds=999",
	}
}

// Test 1: of two concurrent owners claiming the same firing alert's send
// lease, only one may actually dispatch the notification. The guarantee must
// come from the DB-level CAS in ClaimAlertSend, not from any in-process
// lock, so both calls race for real via goroutines.
func TestContentBackupAlertLeaseOnlyOneSenderWins(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	store := model.NewContentBackupStore(db, "site-alert")
	spy := &contentBackupMailSpy{}
	contentBackupUseMailSpy(t, spy)
	cfg := contentBackupAlertEmailConfig(t)

	ctx := context.Background()
	now := contentBackupAlertTestBase
	eval := contentBackupNodeOfflineEval()

	var wg sync.WaitGroup
	owners := []string{"owner-1", "owner-2"}
	errs := make([]error, len(owners))
	for i := range owners {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = applyContentBackupAlertEvaluation(ctx, store, eval, cfg, 30*time.Minute, owners[i], now)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("owner %s: unexpected error: %v", owners[i], err)
		}
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("spy called %d times, want exactly 1", got)
	}
}

// Test 2: a failed send must not advance LastSentAt, and must be retried
// once the (short) send lease expires. contentBackupAlertLeaseWindow (20s)
// is deliberately shorter than the poll interval (30s) precisely so a failed
// send's lease has already expired by the next round -- that IS the retry
// mechanism, there is no separate retry queue.
func TestContentBackupAlertSendFailureNotMarkedSentAndRetried(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	store := model.NewContentBackupStore(db, "site-alert")
	cfg := contentBackupAlertEmailConfig(t)

	sendErr := errors.New("smtp: connection refused")
	failingSpy := &contentBackupMailSpy{err: sendErr}
	contentBackupUseMailSpy(t, failingSpy)

	ctx := context.Background()
	now := contentBackupAlertTestBase
	eval := contentBackupNodeOfflineEval()
	dedup := 30 * time.Minute

	if err := applyContentBackupAlertEvaluation(ctx, store, eval, cfg, dedup, "owner-1", now); !errors.Is(err, sendErr) {
		t.Fatalf("expected the send error to surface, got %v", err)
	}

	alert, err := store.GetAlert(ctx, eval.storageNodeID, eval.reason)
	if err != nil {
		t.Fatalf("get alert: %v", err)
	}
	if alert.LastSentAt != 0 {
		t.Fatalf("LastSentAt = %d, want 0 after a failed send", alert.LastSentAt)
	}
	if got := failingSpy.count(); got != 1 {
		t.Fatalf("failing spy called %d times, want 1", got)
	}

	// Retry after the lease window has expired but well inside the 30-minute
	// dedup window -- this must succeed precisely because MarkAlertSent was
	// never called above.
	succeedingSpy := &contentBackupMailSpy{}
	contentBackupUseMailSpy(t, succeedingSpy)

	retryNow := now.Add(contentBackupAlertLeaseWindow + time.Second)
	if err := applyContentBackupAlertEvaluation(ctx, store, eval, cfg, dedup, "owner-1", retryNow); err != nil {
		t.Fatalf("retry: unexpected error: %v", err)
	}
	alert, err = store.GetAlert(ctx, eval.storageNodeID, eval.reason)
	if err != nil {
		t.Fatalf("get alert after retry: %v", err)
	}
	if alert.LastSentAt == 0 {
		t.Fatal("LastSentAt still 0 after a successful retry")
	}
	if got := succeedingSpy.count(); got != 1 {
		t.Fatalf("retry spy called %d times, want 1", got)
	}
}

// Test 3: the same fault must be deduped for the configured AlertDedupMinutes
// window (30 minutes here) and sent again only once that window has elapsed.
func TestContentBackupAlertDedupWithinWindow(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	store := model.NewContentBackupStore(db, "site-alert")
	spy := &contentBackupMailSpy{}
	contentBackupUseMailSpy(t, spy)
	cfg := contentBackupAlertEmailConfig(t)

	ctx := context.Background()
	base := contentBackupAlertTestBase
	eval := contentBackupNodeOfflineEval()
	dedup := 30 * time.Minute

	if err := applyContentBackupAlertEvaluation(ctx, store, eval, cfg, dedup, "owner-1", base); err != nil {
		t.Fatalf("round 1: unexpected error: %v", err)
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("after round 1: spy called %d times, want 1", got)
	}

	if err := applyContentBackupAlertEvaluation(ctx, store, eval, cfg, dedup, "owner-1", base.Add(5*time.Minute)); err != nil {
		t.Fatalf("round 2 (within window): unexpected error: %v", err)
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("after round 2 (still within the 30-minute dedup window): spy called %d times, want still 1", got)
	}

	if err := applyContentBackupAlertEvaluation(ctx, store, eval, cfg, dedup, "owner-1", base.Add(31*time.Minute)); err != nil {
		t.Fatalf("round 3 (past window): unexpected error: %v", err)
	}
	if got := spy.count(); got != 2 {
		t.Fatalf("after round 3 (past the 30-minute dedup window): spy called %d times, want 2", got)
	}
}

// Test 4: recovery is recorded but never emailed. Re-applying the same
// not-firing evaluation does not send anything.
func TestContentBackupAlertRecoveryDoesNotNotify(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	store := model.NewContentBackupStore(db, "site-alert")
	spy := &contentBackupMailSpy{}
	contentBackupUseMailSpy(t, spy)
	cfg := contentBackupAlertEmailConfig(t)

	ctx := context.Background()
	base := contentBackupAlertTestBase
	firingEval := contentBackupNodeOfflineEval()
	dedup := 30 * time.Minute

	if err := applyContentBackupAlertEvaluation(ctx, store, firingEval, cfg, dedup, "owner-1", base); err != nil {
		t.Fatalf("fire: unexpected error: %v", err)
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("after firing: spy called %d times, want 1", got)
	}

	resolvedEval := firingEval
	resolvedEval.firing = false
	resolvedEval.detail = "last_seen_age_seconds=1"

	if err := applyContentBackupAlertEvaluation(ctx, store, resolvedEval, cfg, dedup, "owner-1", base.Add(time.Minute)); err != nil {
		t.Fatalf("recover: unexpected error: %v", err)
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("after recovery: spy called %d times, want 1 (firing only)", got)
	}

	alert, err := store.GetAlert(ctx, firingEval.storageNodeID, firingEval.reason)
	if err != nil {
		t.Fatalf("get alert: %v", err)
	}
	if alert.State != model.ContentBackupAlertResolved {
		t.Fatalf("alert state = %q, want %q", alert.State, model.ContentBackupAlertResolved)
	}

	// ResolveAlert only reports firing->resolved once; a second not-firing
	// round must not send a duplicate recovery notice.
	if err := applyContentBackupAlertEvaluation(ctx, store, resolvedEval, cfg, dedup, "owner-1", base.Add(2*time.Minute)); err != nil {
		t.Fatalf("second not-firing round: unexpected error: %v", err)
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("after a second not-firing round: spy called %d times, want still 1", got)
	}
}

// config_mismatch 在总开关打开时也不发信（保存后下一轮心跳就会对齐）。
func TestContentBackupAlertConfigMismatchDoesNotNotify(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	store := model.NewContentBackupStore(db, "site-alert")
	spy := &contentBackupMailSpy{}
	contentBackupUseMailSpy(t, spy)

	eval := contentBackupAlertEvaluation{
		storageNodeID: "node-a",
		reason:        model.ContentBackupAlertConfigMismatch,
		firing:        true,
		detail:        "applied_config_version=9 published_config_version=10",
	}
	if err := applyContentBackupAlertEvaluation(context.Background(), store, eval, contentBackupAlertEmailConfig(t), 30*time.Minute, "owner-1", contentBackupAlertTestBase); err != nil {
		t.Fatalf("config mismatch: %v", err)
	}
	if got := spy.count(); got != 0 {
		t.Fatalf("config_mismatch emailed %d times, want 0", got)
	}
}

// 总开关关着（默认值，也是所有升级上来的站点的初始状态）：告警照常记账，但不发信、也不抢
// 发送租约——否则这条故障会被一个"没人发"的租约占着。打开后同一时刻的下一轮就能立刻发出。
func TestContentBackupAlertSwitchOffRecordsWithoutClaimingLease(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	store := model.NewContentBackupStore(db, "site-alert")
	spy := &contentBackupMailSpy{}
	contentBackupUseMailSpy(t, spy)

	ctx := context.Background()
	now := contentBackupAlertTestBase
	eval := contentBackupNodeOfflineEval()
	dedup := 30 * time.Minute

	withRecipientsButOff := contentBackupAlertEmailConfig(t)
	withRecipientsButOff.NotifyEmailEnabled = false
	for _, cfg := range []contentbackup.Config{contentbackup.DefaultConfig(), withRecipientsButOff} {
		if err := applyContentBackupAlertEvaluation(ctx, store, eval, cfg, dedup, "owner-1", now); err != nil {
			t.Fatalf("switch off: unexpected error: %v", err)
		}
		if got := spy.count(); got != 0 {
			t.Fatalf("switch off emailed %d times, want 0", got)
		}
		alert, err := store.GetAlert(ctx, eval.storageNodeID, eval.reason)
		if err != nil {
			t.Fatalf("get alert: %v", err)
		}
		if alert.State != model.ContentBackupAlertFiring {
			t.Fatalf("alert state = %q, want %q (bookkeeping still proceeds)", alert.State, model.ContentBackupAlertFiring)
		}
		if alert.SendLeaseOwner != "" || alert.SendLeaseUntil != 0 || alert.LastSentAt != 0 {
			t.Fatalf("switch off must not claim the send lease, got owner=%q until=%d last_sent=%d", alert.SendLeaseOwner, alert.SendLeaseUntil, alert.LastSentAt)
		}
	}

	if err := applyContentBackupAlertEvaluation(ctx, store, eval, contentBackupAlertEmailConfig(t), dedup, "owner-2", now); err != nil {
		t.Fatalf("switch on: unexpected error: %v", err)
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("after switching on: emailed %d times, want 1", got)
	}
}

// 总开关和分类开关都开：只发一封，收件人就是配置里的全部地址、按 SendEmail 的 ; 口径连接，
// 成功后才记已发送。
func TestContentBackupAlertSendsOneEmailToConfiguredRecipients(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	store := model.NewContentBackupStore(db, "site-alert")
	spy := &contentBackupMailSpy{}
	contentBackupUseMailSpy(t, spy)

	ctx := context.Background()
	now := contentBackupAlertTestBase
	eval := contentBackupNodeOfflineEval()
	if err := applyContentBackupAlertEvaluation(ctx, store, eval, contentBackupAlertEmailConfig(t), 30*time.Minute, "owner-1", now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("emailed %d times, want 1", got)
	}
	mail := spy.last(t)
	if mail.receiver != "a@x.com;b@y.com" {
		t.Fatalf("receiver = %q, want %q", mail.receiver, "a@x.com;b@y.com")
	}
	if mail.subject != "内容备份告警：站点节点离线（触发中）" {
		t.Fatalf("subject = %q", mail.subject)
	}
	if !strings.Contains(mail.content, "site-alert") || !strings.Contains(mail.content, "node-a") {
		t.Fatalf("content must name the site and node: %s", mail.content)
	}
	alert, err := store.GetAlert(ctx, eval.storageNodeID, eval.reason)
	if err != nil {
		t.Fatalf("get alert: %v", err)
	}
	if alert.LastSentAt != now.Unix() {
		t.Fatalf("LastSentAt = %d, want %d after a successful send", alert.LastSentAt, now.Unix())
	}
}

// The alert email must carry only site/node/reason/status/time/counters --
// never local paths, remote paths, hashes, or any other request/response
// body content.
func TestContentBackupAlertContentExcludesBodyAndCredentials(t *testing.T) {
	cfg := contentbackup.Config{CleanupPendingAlertMinutes: 30}
	now := contentBackupAlertTestBase
	job := model.ContentBackupJob{
		StorageNodeID:      "node-a",
		LocalPath:          "/var/spool/content-backup/secret-body-payload/frame.bin",
		RemotePath:         "s3://bucket/secret-body-payload/frame.bin.zst",
		FrameSHA256:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CompressedSHA256:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CleanupAvailableAt: now.Add(-45 * time.Minute).Unix(),
	}
	counts := model.ContentBackupJobCounts{CleanupPendingCount: 3, CleanupPendingBytes: 1024}

	eval := contentBackupEvaluateCleanupPending(cfg, "node-a", counts, []model.ContentBackupJob{job}, now)
	if !eval.firing {
		t.Fatal("expected cleanup_pending to be firing for a 45-minute-old job against a 30-minute threshold")
	}

	subject, content := contentBackupAlertMail("site-a", eval, false, now)
	blob := subject + " " + content

	forbidden := []string{job.LocalPath, job.RemotePath, job.FrameSHA256, job.CompressedSHA256, "secret-body-payload"}
	for _, s := range forbidden {
		if strings.Contains(blob, s) {
			t.Fatalf("notification content leaked a forbidden field %q: %s", s, blob)
		}
	}

	mustContain := []string{"内容备份告警：本地清理积压", "节点", "node-a", "待清理条数", "3"}
	for _, s := range mustContain {
		if !strings.Contains(blob, s) {
			t.Fatalf("notification content missing expected safe field %q: %s", s, blob)
		}
	}
	if strings.Contains(subject, "Content backup") || strings.Contains(content, "reason=") {
		t.Fatalf("notification stayed in the old English key=value form: %s", blob)
	}
}

func TestContentBackupAlertMailUsesBeijingTimeAndEscapes(t *testing.T) {
	eval := contentBackupAlertEvaluation{
		storageNodeID: "node-<x>",
		reason:        model.ContentBackupAlertOldestPending,
		firing:        true,
		detail:        "pending_count=506 oldest_pending_age_minutes=32",
	}
	now := time.Date(2026, 9, 20, 8, 32, 20, 0, time.UTC)
	subject, content := contentBackupAlertMail("ai-backup", eval, false, now)
	if subject != "内容备份告警：上传积压（触发中）" {
		t.Fatalf("subject = %q", subject)
	}
	if !strings.Contains(content, "2026-09-20 16:32:20（北京时间）") {
		t.Fatalf("expected Beijing time, got %s", content)
	}
	if !strings.Contains(content, "待上传条数") || !strings.Contains(content, "506") {
		t.Fatalf("missing pending count row: %s", content)
	}
	if !strings.Contains(content, "node-&lt;x&gt;") {
		t.Fatalf("node id must be html-escaped: %s", content)
	}
}

// 站内设置页已下线：页脚必须指向管理端，不能再让人去找已经不存在的「系统设置 → 内容备份」。
func TestContentBackupAlertMailFooterPointsToConsole(t *testing.T) {
	_, content := contentBackupAlertMail("ai-backup", contentBackupNodeOfflineEval(), false, contentBackupAlertTestBase)
	if !strings.Contains(content, "可在内容备份管理端该站「设置 → 邮件通知」里调整收件人和要接收的告警类型。") {
		t.Fatalf("footer must point to the console settings: %s", content)
	}
	if strings.Contains(content, "系统设置") {
		t.Fatalf("footer still points to the removed site settings page: %s", content)
	}
}

// 总开关开着、但该类型的分类开关关着：不发；重新勾上后下一轮就发。
func TestContentBackupAlertDisabledReasonDoesNotNotify(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	store := model.NewContentBackupStore(db, "site-alert")
	spy := &contentBackupMailSpy{}
	contentBackupUseMailSpy(t, spy)

	cfg := contentBackupAlertEmailConfig(t)
	cfg.NotifyOldestPending = false
	eval := contentBackupAlertEvaluation{
		storageNodeID: "node-a",
		reason:        model.ContentBackupAlertOldestPending,
		firing:        true,
		detail:        "pending_count=12 oldest_pending_age_minutes=40",
	}
	if err := applyContentBackupAlertEvaluation(context.Background(), store, eval, cfg, 30*time.Minute, "owner-1", contentBackupAlertTestBase); err != nil {
		t.Fatalf("disabled oldest_pending: %v", err)
	}
	if got := spy.count(); got != 0 {
		t.Fatalf("disabled oldest_pending emailed %d times, want 0", got)
	}

	cfg.NotifyOldestPending = true
	if err := applyContentBackupAlertEvaluation(context.Background(), store, eval, cfg, 30*time.Minute, "owner-1", contentBackupAlertTestBase.Add(time.Minute)); err != nil {
		t.Fatalf("re-enabled oldest_pending: %v", err)
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("re-enabled oldest_pending emailed %d times, want 1", got)
	}
}
