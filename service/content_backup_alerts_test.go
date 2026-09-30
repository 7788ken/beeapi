package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// contentBackupAlertTestBase is a fixed instant so every test's timeline math
// (dedup windows, lease windows, offline thresholds) is reproducible and
// independent of wall-clock time. Only the notification rate limiter
// (service/notify-limit.go) keys off real time.Now(); contentBackupAlertTestDB
// neutralizes that below.
var contentBackupAlertTestBase = time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC)

// contentBackupAlertTestDB opens an independent SQLite database under
// t.TempDir(), migrates the content-backup tables plus model.User, and
// points the package-level model.DB at it for the duration of the test
// (restored on cleanup). service already has a TestMain (task_billing_test.go)
// that sets model.DB once for the whole binary, so this swaps it per test
// instead of assuming a fixed global.
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

	models := append(model.ContentBackupModels(), &model.User{})
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("migrate content backup alert test db: %v", err)
	}

	origDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = origDB })

	// notifyLimitStore (service/notify-limit.go) is a package-level sync.Map
	// keyed by userId:notifyType:realTimeBucket. Our fake `now` never reaches
	// it -- only wall-clock time.Now() does -- so without this override, a
	// test that sends several notifications in a few milliseconds of real
	// time could spuriously hit the default 2-per-10-minutes cap and fail for
	// a reason unrelated to the alert logic under test.
	origCount, origMinute := constant.NotifyLimitCount, constant.NotificationLimitDurationMinute
	constant.NotifyLimitCount, constant.NotificationLimitDurationMinute = 1000, 10
	t.Cleanup(func() {
		constant.NotifyLimitCount, constant.NotificationLimitDurationMinute = origCount, origMinute
	})

	return db
}

// contentBackupAlertSeedRootUser creates the root user that
// contentBackupAlertRootUser looks up via model.GetRootUser(). Setting is
// marshaled with common.Marshal per repo CLAUDE.md rule 1 (never raw
// encoding/json in business/test code).
func contentBackupAlertSeedRootUser(t *testing.T, db *gorm.DB, email string, setting dto.UserSetting) *model.User {
	t.Helper()
	settingJSON, err := common.Marshal(setting)
	if err != nil {
		t.Fatalf("marshal user setting: %v", err)
	}
	user := model.User{
		Username: fmt.Sprintf("root-%d", time.Now().UnixNano()),
		Password: "test-password-hash-0123",
		Role:     common.RoleRootUser,
		Status:   1,
		Email:    email,
		Setting:  string(settingJSON),
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create root user: %v", err)
	}
	return &user
}

// contentBackupAlertSpy stands in for contentBackupAlertSendFunc so tests
// never perform real network I/O. It is safe for concurrent use: the lease
// race test below calls it from multiple goroutines.
type contentBackupAlertSpy struct {
	mu      sync.Mutex
	calls   []dto.Notify
	outcome contentBackupAlertOutcome
}

func (s *contentBackupAlertSpy) fn(_ *model.UserBase, notify dto.Notify) contentBackupAlertOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, notify)
	return s.outcome
}

func (s *contentBackupAlertSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func contentBackupAlertUseSpy(t *testing.T, spy *contentBackupAlertSpy) {
	t.Helper()
	orig := contentBackupAlertSendFunc
	contentBackupAlertSendFunc = spy.fn
	t.Cleanup(func() { contentBackupAlertSendFunc = orig })
}

// Test 1: of two concurrent owners claiming the same firing alert's send
// lease, only one may actually dispatch the notification. The guarantee must
// come from the DB-level CAS in ClaimAlertSend, not from any in-process
// lock, so both calls race for real via goroutines.
func TestContentBackupAlertLeaseOnlyOneSenderWins(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	store := model.NewContentBackupStore(db, "site-alert")
	rootUser := contentBackupAlertSeedRootUser(t, db, "root@example.com", dto.UserSetting{
		NotifyType:        dto.NotifyTypeEmail,
		NotificationEmail: "root@example.com",
	})
	baseUser := rootUser.ToBaseUser()

	spy := &contentBackupAlertSpy{outcome: contentBackupAlertOutcome{attempted: true}}
	contentBackupAlertUseSpy(t, spy)

	ctx := context.Background()
	now := contentBackupAlertTestBase
	eval := contentBackupAlertEvaluation{
		storageNodeID: "node-a",
		reason:        model.ContentBackupAlertNodeOffline,
		firing:        true,
		detail:        "last_seen_age_seconds=999",
	}

	var wg sync.WaitGroup
	owners := []string{"owner-1", "owner-2"}
	errs := make([]error, len(owners))
	for i := range owners {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = applyContentBackupAlertEvaluation(ctx, store, baseUser, eval, contentbackup.DefaultConfig(), 30*time.Minute, owners[i], now)
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
	rootUser := contentBackupAlertSeedRootUser(t, db, "root@example.com", dto.UserSetting{
		NotifyType:        dto.NotifyTypeEmail,
		NotificationEmail: "root@example.com",
	})
	baseUser := rootUser.ToBaseUser()

	failingSpy := &contentBackupAlertSpy{outcome: contentBackupAlertOutcome{attempted: true, err: errors.New("smtp: connection refused")}}
	contentBackupAlertUseSpy(t, failingSpy)

	ctx := context.Background()
	now := contentBackupAlertTestBase
	eval := contentBackupAlertEvaluation{
		storageNodeID: "node-a",
		reason:        model.ContentBackupAlertNodeOffline,
		firing:        true,
		detail:        "last_seen_age_seconds=999",
	}
	dedup := 30 * time.Minute

	if err := applyContentBackupAlertEvaluation(ctx, store, baseUser, eval, contentbackup.DefaultConfig(), dedup, "owner-1", now); err == nil {
		t.Fatal("expected an error from a failed send, got nil")
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
	succeedingSpy := &contentBackupAlertSpy{outcome: contentBackupAlertOutcome{attempted: true}}
	contentBackupAlertUseSpy(t, succeedingSpy)

	retryNow := now.Add(contentBackupAlertLeaseWindow + time.Second)
	if err := applyContentBackupAlertEvaluation(ctx, store, baseUser, eval, contentbackup.DefaultConfig(), dedup, "owner-1", retryNow); err != nil {
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
	rootUser := contentBackupAlertSeedRootUser(t, db, "root@example.com", dto.UserSetting{
		NotifyType:        dto.NotifyTypeEmail,
		NotificationEmail: "root@example.com",
	})
	baseUser := rootUser.ToBaseUser()

	spy := &contentBackupAlertSpy{outcome: contentBackupAlertOutcome{attempted: true}}
	contentBackupAlertUseSpy(t, spy)

	ctx := context.Background()
	base := contentBackupAlertTestBase
	eval := contentBackupAlertEvaluation{
		storageNodeID: "node-a",
		reason:        model.ContentBackupAlertNodeOffline,
		firing:        true,
		detail:        "last_seen_age_seconds=999",
	}
	dedup := 30 * time.Minute

	if err := applyContentBackupAlertEvaluation(ctx, store, baseUser, eval, contentbackup.DefaultConfig(), dedup, "owner-1", base); err != nil {
		t.Fatalf("round 1: unexpected error: %v", err)
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("after round 1: spy called %d times, want 1", got)
	}

	if err := applyContentBackupAlertEvaluation(ctx, store, baseUser, eval, contentbackup.DefaultConfig(), dedup, "owner-1", base.Add(5*time.Minute)); err != nil {
		t.Fatalf("round 2 (within window): unexpected error: %v", err)
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("after round 2 (still within the 30-minute dedup window): spy called %d times, want still 1", got)
	}

	if err := applyContentBackupAlertEvaluation(ctx, store, baseUser, eval, contentbackup.DefaultConfig(), dedup, "owner-1", base.Add(31*time.Minute)); err != nil {
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
	rootUser := contentBackupAlertSeedRootUser(t, db, "root@example.com", dto.UserSetting{
		NotifyType:        dto.NotifyTypeEmail,
		NotificationEmail: "root@example.com",
	})
	baseUser := rootUser.ToBaseUser()

	spy := &contentBackupAlertSpy{outcome: contentBackupAlertOutcome{attempted: true}}
	contentBackupAlertUseSpy(t, spy)

	ctx := context.Background()
	base := contentBackupAlertTestBase
	firingEval := contentBackupAlertEvaluation{
		storageNodeID: "node-a",
		reason:        model.ContentBackupAlertNodeOffline,
		firing:        true,
		detail:        "last_seen_age_seconds=999",
	}
	dedup := 30 * time.Minute

	if err := applyContentBackupAlertEvaluation(ctx, store, baseUser, firingEval, contentbackup.DefaultConfig(), dedup, "owner-1", base); err != nil {
		t.Fatalf("fire: unexpected error: %v", err)
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("after firing: spy called %d times, want 1", got)
	}

	resolvedEval := firingEval
	resolvedEval.firing = false
	resolvedEval.detail = "last_seen_age_seconds=1"

	if err := applyContentBackupAlertEvaluation(ctx, store, baseUser, resolvedEval, contentbackup.DefaultConfig(), dedup, "owner-1", base.Add(time.Minute)); err != nil {
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
	if err := applyContentBackupAlertEvaluation(ctx, store, baseUser, resolvedEval, contentbackup.DefaultConfig(), dedup, "owner-1", base.Add(2*time.Minute)); err != nil {
		t.Fatalf("second not-firing round: unexpected error: %v", err)
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("after a second not-firing round: spy called %d times, want still 1", got)
	}
}

func TestContentBackupAlertConfigMismatchDoesNotNotify(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	store := model.NewContentBackupStore(db, "site-alert")
	rootUser := contentBackupAlertSeedRootUser(t, db, "root@example.com", dto.UserSetting{
		NotifyType:        dto.NotifyTypeEmail,
		NotificationEmail: "root@example.com",
	})
	baseUser := rootUser.ToBaseUser()
	spy := &contentBackupAlertSpy{outcome: contentBackupAlertOutcome{attempted: true}}
	contentBackupAlertUseSpy(t, spy)

	eval := contentBackupAlertEvaluation{
		storageNodeID: "node-a",
		reason:        model.ContentBackupAlertConfigMismatch,
		firing:        true,
		detail:        "applied_config_version=9 published_config_version=10",
	}
	if err := applyContentBackupAlertEvaluation(context.Background(), store, baseUser, eval, contentbackup.DefaultConfig(), 30*time.Minute, "owner-1", contentBackupAlertTestBase); err != nil {
		t.Fatalf("config mismatch: %v", err)
	}
	if got := spy.count(); got != 0 {
		t.Fatalf("config_mismatch emailed %d times, want 0", got)
	}
}

// Test 5 (the case the task calls out as most important): when the
// notification target is not configured, the attempt must NOT be recorded
// as a successful send. This exercises the real defaultContentBackupAlertSend
// / contentBackupAlertTarget path (no spy), since the empty-target guard
// lives there, and additionally proves the lease was not left permanently
// consumed: after it naturally expires, a later round can still claim it.
func TestContentBackupAlertEmptyTargetNotMarkedSent(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	store := model.NewContentBackupStore(db, "site-alert")
	// Empty user.Email AND empty setting: NotifyTypeEmail's fallback to
	// user.Email also resolves to "".
	rootUser := contentBackupAlertSeedRootUser(t, db, "", dto.UserSetting{})
	baseUser := rootUser.ToBaseUser()

	if target := contentBackupAlertTarget(baseUser, baseUser.GetSetting()); target != "" {
		t.Fatalf("contentBackupAlertTarget = %q, want empty", target)
	}

	ctx := context.Background()
	now := contentBackupAlertTestBase
	eval := contentBackupAlertEvaluation{
		storageNodeID: "node-a",
		reason:        model.ContentBackupAlertNodeOffline,
		firing:        true,
		detail:        "last_seen_age_seconds=999",
	}
	dedup := 30 * time.Minute

	if err := applyContentBackupAlertEvaluation(ctx, store, baseUser, eval, contentbackup.DefaultConfig(), dedup, "owner-1", now); err != nil {
		t.Fatalf("unexpected error when the notification target is unconfigured: %v", err)
	}

	alert, err := store.GetAlert(ctx, eval.storageNodeID, eval.reason)
	if err != nil {
		t.Fatalf("get alert: %v", err)
	}
	if alert.LastSentAt != 0 {
		t.Fatalf("LastSentAt = %d, want 0 -- an unconfigured target must never be recorded as sent", alert.LastSentAt)
	}
	if alert.State != model.ContentBackupAlertFiring {
		t.Fatalf("alert state = %q, want %q (bookkeeping still proceeds)", alert.State, model.ContentBackupAlertFiring)
	}

	// The lease naturally expires like any other unsent attempt (same
	// mechanism as the failed-send retry case); once it does, the fault must
	// still be claimable for a later round.
	retryNow := now.Add(contentBackupAlertLeaseWindow + time.Second)
	won, err := store.ClaimAlertSend(ctx, eval.storageNodeID, eval.reason, "owner-2", retryNow, retryNow.Add(contentBackupAlertLeaseWindow), dedup)
	if err != nil {
		t.Fatalf("claim alert send: %v", err)
	}
	if !won {
		t.Fatal("expected the lease to be claimable again once expired -- the unconfigured-target attempt must not have been treated as a permanent send")
	}
}

// Test 6: the notification content must carry only site/node/reason/status/
// time/counters -- never local paths, remote paths, hashes, or any other
// request/response body content.
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

	notify := contentBackupAlertNotify("site-a", eval, false, now)
	blob := notify.Title + " " + notify.Content

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
	if strings.Contains(notify.Title, "Content backup") || strings.Contains(notify.Content, "reason=") {
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
	notify := contentBackupAlertNotify("ai-backup", eval, false, now)
	if notify.Title != "内容备份告警：上传积压（触发中）" {
		t.Fatalf("title = %q", notify.Title)
	}
	if !strings.Contains(notify.Content, "2026-09-20 16:32:20（北京时间）") {
		t.Fatalf("expected Beijing time, got %s", notify.Content)
	}
	if !strings.Contains(notify.Content, "待上传条数") || !strings.Contains(notify.Content, "506") {
		t.Fatalf("missing pending count row: %s", notify.Content)
	}
	if !strings.Contains(notify.Content, "node-&lt;x&gt;") {
		t.Fatalf("node id must be html-escaped: %s", notify.Content)
	}
}

func TestContentBackupAlertDisabledReasonDoesNotNotify(t *testing.T) {
	db := contentBackupAlertTestDB(t)
	store := model.NewContentBackupStore(db, "site-alert")
	rootUser := contentBackupAlertSeedRootUser(t, db, "root@example.com", dto.UserSetting{
		NotifyType:        dto.NotifyTypeEmail,
		NotificationEmail: "root@example.com",
	})
	baseUser := rootUser.ToBaseUser()
	spy := &contentBackupAlertSpy{outcome: contentBackupAlertOutcome{attempted: true}}
	contentBackupAlertUseSpy(t, spy)

	cfg := contentbackup.DefaultConfig()
	cfg.NotifyOldestPending = false
	eval := contentBackupAlertEvaluation{
		storageNodeID: "node-a",
		reason:        model.ContentBackupAlertOldestPending,
		firing:        true,
		detail:        "pending_count=12 oldest_pending_age_minutes=40",
	}
	if err := applyContentBackupAlertEvaluation(context.Background(), store, baseUser, eval, cfg, 30*time.Minute, "owner-1", contentBackupAlertTestBase); err != nil {
		t.Fatalf("disabled oldest_pending: %v", err)
	}
	if got := spy.count(); got != 0 {
		t.Fatalf("disabled oldest_pending emailed %d times, want 0", got)
	}

	cfg.NotifyOldestPending = true
	if err := applyContentBackupAlertEvaluation(context.Background(), store, baseUser, eval, cfg, 30*time.Minute, "owner-1", contentBackupAlertTestBase.Add(time.Minute)); err != nil {
		t.Fatalf("re-enabled oldest_pending: %v", err)
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("re-enabled oldest_pending emailed %d times, want 1", got)
	}
}
