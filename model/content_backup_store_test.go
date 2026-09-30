package model

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	contentBackupTestMySQLDSNEnv    = "CONTENT_BACKUP_TEST_MYSQL_DSN"
	contentBackupTestPostgresDSNEnv = "CONTENT_BACKUP_TEST_POSTGRES_DSN"
	contentBackupTestDatabaseName   = "content_backup_test"
)

// Ports of the developer's unrelated local services; the acceptance databases
// live on dedicated throwaway ports and must never be confused with them.
var contentBackupForbiddenDSNFragments = []string{"13306", "15432"}

var contentBackupTestNow = time.Date(2026, 9, 15, 4, 34, 56, 0, time.UTC)

type contentBackupDatabaseTarget struct {
	name string
	open func(t *testing.T) *gorm.DB
}

func contentBackupDatabaseTargets() []contentBackupDatabaseTarget {
	return []contentBackupDatabaseTarget{
		{name: "sqlite", open: openContentBackupSQLite},
		{name: "mysql", open: openContentBackupMySQL},
		{name: "postgres", open: openContentBackupPostgres},
	}
}

func runContentBackupDatabaseTest(t *testing.T, fn func(t *testing.T, db *gorm.DB)) {
	t.Helper()
	for _, target := range contentBackupDatabaseTargets() {
		target := target
		t.Run(target.name, func(t *testing.T) {
			db := target.open(t)
			contentBackupPrepareSchema(t, db)
			fn(t, db)
		})
	}
}

func contentBackupTestGormConfig() *gorm.Config {
	return &gorm.Config{
		Logger: logger.New(
			log.New(os.Stderr, "\r\n", log.LstdFlags),
			logger.Config{
				SlowThreshold:             400 * time.Millisecond,
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true,
				Colorful:                  false,
			},
		),
	}
}

func openContentBackupSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "content-backup.db")
	db, err := gorm.Open(
		sqlite.Open(path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"),
		contentBackupTestGormConfig(),
	)
	if err != nil {
		t.Fatalf("open content backup sqlite database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get content backup sqlite sql database: %v", err)
	}
	// The daemon SQLite pool is documented as max_open=1.
	sqlDB.SetMaxOpenConns(1)
	contentBackupSetDialectFlags(t, db)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func openContentBackupMySQL(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := contentBackupRequireDSN(t, contentBackupTestMySQLDSNEnv)
	db, err := gorm.Open(mysql.Open(dsn), contentBackupTestGormConfig())
	if err != nil {
		t.Fatalf("open content backup mysql database: %v", err)
	}
	contentBackupFinishServerDatabase(t, db, "SELECT DATABASE()")
	return db
}

func openContentBackupPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := contentBackupRequireDSN(t, contentBackupTestPostgresDSNEnv)
	db, err := gorm.Open(
		postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}),
		contentBackupTestGormConfig(),
	)
	if err != nil {
		t.Fatalf("open content backup postgres database: %v", err)
	}
	contentBackupFinishServerDatabase(t, db, "SELECT current_database()")
	return db
}

func contentBackupRequireDSN(t *testing.T, env string) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(env))
	if dsn == "" {
		t.Skipf("%s is not configured; this database's content backup acceptance stays blocked", env)
	}
	for _, fragment := range contentBackupForbiddenDSNFragments {
		if strings.Contains(dsn, fragment) {
			t.Fatalf("%s points at port fragment %s, which belongs to an unrelated local service", env, fragment)
		}
	}
	return dsn
}

func contentBackupFinishServerDatabase(t *testing.T, db *gorm.DB, currentDatabaseQuery string) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get content backup sql database: %v", err)
	}
	sqlDB.SetMaxOpenConns(4)
	var current string
	if err := db.Raw(currentDatabaseQuery).Scan(&current).Error; err != nil {
		t.Fatalf("read current content backup database name: %v", err)
	}
	if current != contentBackupTestDatabaseName {
		t.Fatalf("refusing destructive content backup test outside %s, got %q", contentBackupTestDatabaseName, current)
	}
	contentBackupSetDialectFlags(t, db)
	t.Cleanup(func() {
		contentBackupDropTables(t, db)
		_ = sqlDB.Close()
	})
}

func contentBackupSetDialectFlags(t *testing.T, db *gorm.DB) {
	t.Helper()
	previousSQLite := common.UsingSQLite
	previousMySQL := common.UsingMySQL
	previousPostgreSQL := common.UsingPostgreSQL
	name := db.Dialector.Name()
	common.UsingSQLite = name == "sqlite"
	common.UsingMySQL = name == "mysql"
	common.UsingPostgreSQL = name == "postgres"
	initCol()
	t.Cleanup(func() {
		common.UsingSQLite = previousSQLite
		common.UsingMySQL = previousMySQL
		common.UsingPostgreSQL = previousPostgreSQL
		initCol()
	})
}

func contentBackupPrepareSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	contentBackupDropTables(t, db)
	if err := db.AutoMigrate(ContentBackupModels()...); err != nil {
		t.Fatalf("migrate content backup schema: %v", err)
	}
}

func contentBackupDropTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, item := range ContentBackupModels() {
		if db.Migrator().HasTable(item) {
			if err := db.Migrator().DropTable(item); err != nil {
				t.Fatalf("drop content backup table: %v", err)
			}
		}
	}
}

func contentBackupTestJob(siteID, jobID, nodeID string, created time.Time) ContentBackupJob {
	return ContentBackupJob{
		SiteID:              siteID,
		JobID:               jobID,
		RequestID:           "req-" + jobID,
		UserID:              10086,
		TokenID:             42,
		ChannelID:           12,
		ChannelName:         "example-channel",
		ChannelType:         1,
		Model:               "example-model",
		Endpoint:            "/v1/chat/completions",
		SessionSource:       contentbackupSessionSourceUser,
		SessionHash:         strings.Repeat("a", 64),
		SessionHint:         "user-1",
		CreatedAt:           created.Unix(),
		StorageNodeID:       nodeID,
		LocalPath:           "/spool/content-backup/" + jobID + ".json.gz",
		TargetID:            "target-1",
		ConfigVersion:       7,
		RemotePath:          "/ai/2026-09-15/u-10086/nosession/" + jobID + ".json.gz",
		FrameSHA256:         strings.Repeat("b", 64),
		CompressedSHA256:    strings.Repeat("c", 64),
		CompressedBytes:     2048,
		RequestComplete:     true,
		ResponseComplete:    true,
		RequestContentType:  "application/json",
		ResponseContentType: "application/json",
	}
}

const contentbackupSessionSourceUser = "user"

func contentBackupStatDeltaCount(t *testing.T, db *gorm.DB, siteID, nodeID string) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&ContentBackupStatDelta{}).
		Where("site_id = ? AND storage_node_id = ?", siteID, nodeID).
		Count(&n).Error; err != nil {
		t.Fatalf("count content backup stat deltas: %v", err)
	}
	return n
}

func contentBackupFold(t *testing.T, store *ContentBackupStore, nodeID string, now time.Time) int {
	t.Helper()
	n, err := store.FoldStatDeltas(context.Background(), nodeID, 0, now)
	if err != nil {
		t.Fatalf("FoldStatDeltas(%s): %v", nodeID, err)
	}
	return n
}

func contentBackupDailyStat(t *testing.T, db *gorm.DB, siteID, day, nodeID string) ContentBackupDailyStat {
	t.Helper()
	var stat ContentBackupDailyStat
	err := db.
		Where("site_id = ? AND stat_date = ? AND storage_node_id = ?", siteID, day, nodeID).
		First(&stat).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ContentBackupDailyStat{}
	}
	if err != nil {
		t.Fatalf("read content backup daily stat: %v", err)
	}
	return stat
}

func contentBackupJobRow(t *testing.T, db *gorm.DB, siteID, jobID string) ContentBackupJob {
	t.Helper()
	var job ContentBackupJob
	if err := db.Where("site_id = ? AND job_id = ?", siteID, jobID).First(&job).Error; err != nil {
		t.Fatalf("read content backup job row: %v", err)
	}
	return job
}

func contentBackupCountJobs(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&ContentBackupJob{}).Count(&count).Error; err != nil {
		t.Fatalf("count content backup jobs: %v", err)
	}
	return count
}

// TestContentBackupMinimumStateMachine executes the state assertions frozen in
// the T02 task card, one assertion per check, on every configured database.
func TestContentBackupMinimumStateMachine(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		storeA := NewContentBackupStore(db, "site-a")
		storeB := NewContentBackupStore(db, "site-b")
		jobID := "11111111-2222-4333-8444-555555555555"
		day := "2026-09-15"
		now := contentBackupTestNow

		job := contentBackupTestJob("site-a", jobID, "node-a", now)
		if err := storeA.EnsurePending(ctx, job); err != nil {
			t.Fatalf("EnsurePending first call: %v", err)
		}
		later := now.Add(90 * time.Second)
		rebuilt := contentBackupTestJob("site-a", jobID, "node-a", later)
		rebuilt.CompressedBytes = 999999
		if err := storeA.EnsurePending(ctx, rebuilt); err != nil {
			t.Fatalf("EnsurePending second call: %v", err)
		}
		if got := contentBackupCountJobs(t, db); got != 1 {
			t.Fatalf("job rows after two EnsurePending calls = %d, want 1", got)
		}
		stored := contentBackupJobRow(t, db, "site-a", jobID)
		if stored.CreatedAt != now.Unix() {
			t.Fatalf("created_at = %d, want the original request start %d", stored.CreatedAt, now.Unix())
		}
		if stored.CompressedBytes != 2048 {
			t.Fatalf("EnsurePending overwrote immutable content: compressed_bytes = %d, want 2048", stored.CompressedBytes)
		}
		if stored.Status != ContentBackupStatusPending {
			t.Fatalf("status = %q, want %q", stored.Status, ContentBackupStatusPending)
		}
		if stored.CleanupState != ContentBackupCleanupNotApplicable {
			t.Fatalf("cleanup_state = %q, want %q", stored.CleanupState, ContentBackupCleanupNotApplicable)
		}

		otherSite := contentBackupTestJob("site-b", jobID, "node-a", now)
		if err := storeB.EnsurePending(ctx, otherSite); err != nil {
			t.Fatalf("EnsurePending on a second site: %v", err)
		}
		if got := contentBackupCountJobs(t, db); got != 2 {
			t.Fatalf("job rows across two sites = %d, want 2", got)
		}
		if _, err := storeB.GetJob(ctx, jobID); err != nil {
			t.Fatalf("site-b must see its own job: %v", err)
		}

		if _, ok, err := storeA.Claim(ctx, jobID, "node-b", "owner-b", now, now.Add(180*time.Second)); err != nil {
			t.Fatalf("Claim on a foreign node returned an error: %v", err)
		} else if ok {
			t.Fatal("Claim(node-b, job-1) succeeded, want not claimed")
		}
		if row := contentBackupJobRow(t, db, "site-a", jobID); row.Status != ContentBackupStatusPending {
			t.Fatalf("status after foreign claim = %q, want %q", row.Status, ContentBackupStatusPending)
		}

		lease, ok, err := storeA.Claim(ctx, jobID, "node-a", "owner-a", now, now.Add(180*time.Second))
		if err != nil {
			t.Fatalf("Claim on the owning node: %v", err)
		}
		if !ok {
			t.Fatal("Claim(node-a, job-1, owner-a) did not claim")
		}
		if lease.Generation != 1 {
			t.Fatalf("lease generation = %d, want 1", lease.Generation)
		}
		if lease.Token == "" || lease.Owner != "owner-a" || lease.SiteID != "site-a" || lease.JobID != jobID {
			t.Fatalf("lease identity = %+v, want site-a/%s/owner-a with a token", lease, jobID)
		}
		if !lease.Until.Equal(now.Add(180 * time.Second)) {
			t.Fatalf("lease until = %v, want %v", lease.Until, now.Add(180*time.Second))
		}
		row := contentBackupJobRow(t, db, "site-a", jobID)
		if row.Attempts != 1 {
			t.Fatalf("attempts = %d, want 1", row.Attempts)
		}
		if row.TotalAttempts != 1 {
			t.Fatalf("total_attempts = %d, want 1", row.TotalAttempts)
		}
		if row.Status != ContentBackupStatusProcessing {
			t.Fatalf("status = %q, want %q", row.Status, ContentBackupStatusProcessing)
		}

		if _, ok, err := storeA.Claim(ctx, jobID, "node-a", "owner-a", now, now.Add(180*time.Second)); err != nil {
			t.Fatalf("re-claim before lease expiry: %v", err)
		} else if ok {
			t.Fatal("re-claim while the lease is still valid must fail")
		}
		reclaimed, ok, err := storeA.Claim(ctx, jobID, "node-a", "owner-b", now.Add(200*time.Second), now.Add(380*time.Second))
		if err != nil {
			t.Fatalf("re-claim after lease expiry: %v", err)
		}
		if !ok {
			t.Fatal("expired lease must be reclaimable")
		}
		if reclaimed.Generation != 2 {
			t.Fatalf("re-claim generation = %d, want 2", reclaimed.Generation)
		}

		if err := storeA.MarkUploaded(ctx, lease, now.Add(210*time.Second)); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("MarkUploaded with an expired lease error = %v, want ErrLeaseLost", err)
		}
		row = contentBackupJobRow(t, db, "site-a", jobID)
		if row.Status != ContentBackupStatusProcessing {
			t.Fatalf("status after expired MarkUploaded = %q, want %q", row.Status, ContentBackupStatusProcessing)
		}
		if row.CleanupState != ContentBackupCleanupNotApplicable {
			t.Fatalf("cleanup_state after expired MarkUploaded = %q, want %q", row.CleanupState, ContentBackupCleanupNotApplicable)
		}
		if n := contentBackupStatDeltaCount(t, db, "site-a", "node-a"); n != 0 {
			t.Fatalf("stat deltas after expired MarkUploaded = %d, want 0", n)
		}

		uploadedAt := now.Add(220 * time.Second)
		if err := storeA.MarkUploaded(ctx, reclaimed, uploadedAt); err != nil {
			t.Fatalf("MarkUploaded with the current lease: %v", err)
		}
		if err := storeA.MarkUploaded(ctx, reclaimed, uploadedAt); err != nil {
			t.Fatalf("repeated MarkUploaded must stay idempotent: %v", err)
		}
		row = contentBackupJobRow(t, db, "site-a", jobID)
		if row.Status != ContentBackupStatusUploaded {
			t.Fatalf("status = %q, want %q", row.Status, ContentBackupStatusUploaded)
		}
		if row.CleanupState != ContentBackupCleanupPending {
			t.Fatalf("cleanup_state = %q, want %q", row.CleanupState, ContentBackupCleanupPending)
		}
		if row.UploadedAt != uploadedAt.Unix() {
			t.Fatalf("uploaded_at = %d, want %d", row.UploadedAt, uploadedAt.Unix())
		}
		if row.CleanupAvailableAt != uploadedAt.Unix() {
			t.Fatalf("cleanup_available_at = %d, want %d", row.CleanupAvailableAt, uploadedAt.Unix())
		}
		if row.LeaseToken != "" || row.LeaseUntil != 0 {
			t.Fatalf("lease must be released after upload, got token=%q until=%d", row.LeaseToken, row.LeaseUntil)
		}
		if n := contentBackupStatDeltaCount(t, db, "site-a", "node-a"); n != 1 {
			t.Fatalf("stat deltas after a repeated MarkUploaded = %d, want exactly 1", n)
		}
		if stat := contentBackupDailyStat(t, db, "site-a", day, "node-a"); stat.UploadedCount != 0 {
			t.Fatalf("MarkUploaded must not touch the shared daily row, got uploaded_count %d", stat.UploadedCount)
		}
		contentBackupFold(t, storeA, "node-a", uploadedAt)
		stat := contentBackupDailyStat(t, db, "site-a", day, "node-a")
		if stat.UploadedCount != 1 {
			t.Fatalf("daily uploaded_count = %d, want 1", stat.UploadedCount)
		}
		if stat.UploadedBytes != 2048 {
			t.Fatalf("daily uploaded_bytes = %d, want 2048", stat.UploadedBytes)
		}

		result, err := storeA.RetryFailed(ctx, jobID, now.Add(300*time.Second))
		if err != nil {
			t.Fatalf("RetryFailed on an uploaded job: %v", err)
		}
		if result.JobID != jobID || result.Result != ContentBackupRetrySkipped {
			t.Fatalf("RetryFailed(uploaded) = %+v, want skipped", result)
		}
		row = contentBackupJobRow(t, db, "site-a", jobID)
		if row.Status != ContentBackupStatusUploaded {
			t.Fatalf("status after RetryFailed(uploaded) = %q, want %q", row.Status, ContentBackupStatusUploaded)
		}

		if err := storeA.ScheduleCleanup(ctx, jobID, "node-a", "remove: permission denied", now.Add(310*time.Second), now.Add(370*time.Second)); err != nil {
			t.Fatalf("ScheduleCleanup: %v", err)
		}
		row = contentBackupJobRow(t, db, "site-a", jobID)
		if row.Status != ContentBackupStatusUploaded {
			t.Fatalf("cleanup failure changed upload status to %q", row.Status)
		}
		if row.CleanupState != ContentBackupCleanupPending {
			t.Fatalf("cleanup_state after a cleanup failure = %q, want %q", row.CleanupState, ContentBackupCleanupPending)
		}
		if row.CleanupAttempts != 1 {
			t.Fatalf("cleanup_attempts = %d, want 1", row.CleanupAttempts)
		}
		result, err = storeA.RetryFailed(ctx, jobID, now.Add(400*time.Second))
		if err != nil {
			t.Fatalf("RetryFailed after a cleanup failure: %v", err)
		}
		if result.Result != ContentBackupRetrySkipped {
			t.Fatalf("RetryFailed after cleanup failure = %+v, want skipped", result)
		}
		row = contentBackupJobRow(t, db, "site-a", jobID)
		if row.Status != ContentBackupStatusUploaded || row.CleanupState != ContentBackupCleanupPending {
			t.Fatalf("cleanup failure must not change upload state, got status=%q cleanup=%q", row.Status, row.CleanupState)
		}
		if stat := contentBackupDailyStat(t, db, "site-a", day, "node-a"); stat.UploadedCount != 1 {
			t.Fatalf("cleanup retried after success must not raise uploaded_count, got %d", stat.UploadedCount)
		}
	})
}

func TestContentBackupEnsurePendingIsSiteScoped(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		storeA := NewContentBackupStore(db, "site-a")
		jobID := "22222222-3333-4444-8555-666666666666"

		foreign := contentBackupTestJob("site-b", jobID, "node-a", contentBackupTestNow)
		if err := storeA.EnsurePending(ctx, foreign); err != nil {
			t.Fatalf("EnsurePending must ignore the caller supplied site_id: %v", err)
		}
		row := contentBackupJobRow(t, db, "site-a", jobID)
		if row.SiteID != "site-a" {
			t.Fatalf("stored site_id = %q, want the store identity site-a", row.SiteID)
		}
		if _, err := storeA.GetJob(ctx, "missing-job"); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("GetJob(missing) error = %v, want gorm.ErrRecordNotFound", err)
		}

		placeholder := ContentBackupJob{SiteID: "site-a", JobID: "33333333-4444-4555-8666-777777777777", CreatedAt: 0}
		if err := storeA.EnsurePending(ctx, placeholder); err == nil {
			t.Fatal("EnsurePending accepted a job without identity and created_at")
		}
	})
}

func TestContentBackupRenewIsChangedRowsSafe(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		jobID := "44444444-5555-4666-8777-888888888888"
		now := contentBackupTestNow
		if err := store.EnsurePending(ctx, contentBackupTestJob("site-a", jobID, "node-a", now)); err != nil {
			t.Fatalf("EnsurePending: %v", err)
		}
		lease, ok, err := store.Claim(ctx, jobID, "node-a", "owner-a", now, now.Add(180*time.Second))
		if err != nil || !ok {
			t.Fatalf("Claim = (%v, %v), want a lease", ok, err)
		}

		// Renewing twice with byte-identical values is the MySQL changed-rows trap.
		for i := 0; i < 3; i++ {
			renewed, err := store.Renew(ctx, lease, now, now.Add(180*time.Second))
			if err != nil {
				t.Fatalf("Renew #%d with identical values: %v", i+1, err)
			}
			if renewed.Generation != lease.Generation || renewed.Token != lease.Token {
				t.Fatalf("Renew #%d changed the lease identity: %+v", i+1, renewed)
			}
			if !renewed.Until.Equal(now.Add(180 * time.Second)) {
				t.Fatalf("Renew #%d until = %v", i+1, renewed.Until)
			}
		}
		if row := contentBackupJobRow(t, db, "site-a", jobID); row.LeaseGeneration != 1 {
			t.Fatalf("lease_generation after renewals = %d, want 1", row.LeaseGeneration)
		}

		stale := lease
		stale.Token = strings.Repeat("f", 32)
		if _, err := store.Renew(ctx, stale, now, now.Add(180*time.Second)); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("Renew with a foreign token error = %v, want ErrLeaseLost", err)
		}
		foreignNode := lease
		foreignNode.StorageNodeID = "node-b"
		if _, err := store.Renew(ctx, foreignNode, now, now.Add(180*time.Second)); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("Renew from a foreign node error = %v, want ErrLeaseLost", err)
		}
		expired := lease
		if _, err := store.Renew(ctx, expired, now.Add(181*time.Second), now.Add(361*time.Second)); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("Renew after lease expiry error = %v, want ErrLeaseLost", err)
		}
	})
}

func TestContentBackupStaleLeaseCannotWriteState(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		jobID := "55555555-6666-4777-8888-999999999999"
		now := contentBackupTestNow
		if err := store.EnsurePending(ctx, contentBackupTestJob("site-a", jobID, "node-a", now)); err != nil {
			t.Fatalf("EnsurePending: %v", err)
		}
		stale, ok, err := store.Claim(ctx, jobID, "node-a", "owner-a", now, now.Add(10*time.Second))
		if err != nil || !ok {
			t.Fatalf("first Claim = (%v, %v)", ok, err)
		}
		current, ok, err := store.Claim(ctx, jobID, "node-a", "owner-b", now.Add(20*time.Second), now.Add(200*time.Second))
		if err != nil || !ok {
			t.Fatalf("second Claim = (%v, %v)", ok, err)
		}

		if err := store.ScheduleRetry(ctx, stale, "net_timeout", "dial timeout", now.Add(21*time.Second), now.Add(80*time.Second)); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("ScheduleRetry with a stale lease error = %v, want ErrLeaseLost", err)
		}
		if err := store.MarkFailed(ctx, stale, "auth_failed", "certificate pin mismatch", now.Add(22*time.Second)); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("MarkFailed with a stale lease error = %v, want ErrLeaseLost", err)
		}
		row := contentBackupJobRow(t, db, "site-a", jobID)
		if row.Status != ContentBackupStatusProcessing || row.LeaseToken != current.Token {
			t.Fatalf("stale writes leaked into the row: status=%q token=%q", row.Status, row.LeaseToken)
		}

		if err := store.ScheduleRetry(ctx, current, "net_timeout", "dial timeout", now.Add(30*time.Second), now.Add(90*time.Second)); err != nil {
			t.Fatalf("ScheduleRetry with the current lease: %v", err)
		}
		row = contentBackupJobRow(t, db, "site-a", jobID)
		if row.Status != ContentBackupStatusPending {
			t.Fatalf("status after ScheduleRetry = %q, want %q", row.Status, ContentBackupStatusPending)
		}
		if row.AvailableAt != now.Add(90*time.Second).Unix() {
			t.Fatalf("available_at = %d, want %d", row.AvailableAt, now.Add(90*time.Second).Unix())
		}
		if row.Attempts != 2 || row.TotalAttempts != 2 {
			t.Fatalf("ScheduleRetry must not re-increment attempts, got attempts=%d total=%d", row.Attempts, row.TotalAttempts)
		}
		if row.LastErrorCode != "net_timeout" || row.LastErrorMessage != "dial timeout" {
			t.Fatalf("last_error = %q/%q", row.LastErrorCode, row.LastErrorMessage)
		}
		if row.LeaseToken != "" || row.LeaseOwner != "" || row.LeaseUntil != 0 {
			t.Fatalf("lease must be cleared after ScheduleRetry, got %+v", row)
		}

		next, ok, err := store.Claim(ctx, jobID, "node-a", "owner-a", now.Add(100*time.Second), now.Add(280*time.Second))
		if err != nil || !ok {
			t.Fatalf("Claim after ScheduleRetry = (%v, %v)", ok, err)
		}
		if row := contentBackupJobRow(t, db, "site-a", jobID); row.Attempts != 3 || row.TotalAttempts != 3 {
			t.Fatalf("attempts after the second claim = %d/%d, want 3/3", row.Attempts, row.TotalAttempts)
		}
		if err := store.MarkFailed(ctx, next, ContentBackupErrorHashError, "compressed sha256 mismatch", now.Add(110*time.Second)); err != nil {
			t.Fatalf("MarkFailed: %v", err)
		}
		row = contentBackupJobRow(t, db, "site-a", jobID)
		if row.Status != ContentBackupStatusFailed {
			t.Fatalf("status after MarkFailed = %q, want %q", row.Status, ContentBackupStatusFailed)
		}
		if row.TotalAttempts != 3 {
			t.Fatalf("total_attempts after MarkFailed = %d, want 3", row.TotalAttempts)
		}

		result, err := store.RetryFailed(ctx, jobID, now.Add(120*time.Second))
		if err != nil {
			t.Fatalf("RetryFailed on a hash error job: %v", err)
		}
		if result.Result != ContentBackupRetrySkipped {
			t.Fatalf("RetryFailed(hash_error) = %+v, want skipped", result)
		}
		if row := contentBackupJobRow(t, db, "site-a", jobID); row.Status != ContentBackupStatusFailed {
			t.Fatal("a non-retryable failure must stay failed")
		}
	})
}

func TestContentBackupRetryFailedResetsAttemptsAndKeepsTotals(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		jobID := "66666666-7777-4888-8999-aaaaaaaaaaaa"
		now := contentBackupTestNow
		if err := store.EnsurePending(ctx, contentBackupTestJob("site-a", jobID, "node-a", now)); err != nil {
			t.Fatalf("EnsurePending: %v", err)
		}
		lease, ok, err := store.Claim(ctx, jobID, "node-a", "owner-a", now, now.Add(180*time.Second))
		if err != nil || !ok {
			t.Fatalf("Claim = (%v, %v)", ok, err)
		}
		if err := store.MarkFailed(ctx, lease, "net_timeout", "connection reset", now.Add(time.Second)); err != nil {
			t.Fatalf("MarkFailed: %v", err)
		}

		result, err := store.RetryFailed(ctx, jobID, now.Add(2*time.Second))
		if err != nil {
			t.Fatalf("RetryFailed: %v", err)
		}
		if result.Result != ContentBackupRetryQueued {
			t.Fatalf("RetryFailed(failed) = %+v, want queued", result)
		}
		row := contentBackupJobRow(t, db, "site-a", jobID)
		if row.Attempts != 0 {
			t.Fatalf("attempts = %d, want 0", row.Attempts)
		}
		if row.RetryRound != 1 {
			t.Fatalf("retry_round = %d, want 1", row.RetryRound)
		}
		if row.TotalAttempts != 1 {
			t.Fatalf("total_attempts = %d, want 1 (preserved)", row.TotalAttempts)
		}
		if row.Status != ContentBackupStatusPending {
			t.Fatalf("status = %q, want %q", row.Status, ContentBackupStatusPending)
		}
		if row.AvailableAt != now.Add(2*time.Second).Unix() {
			t.Fatalf("available_at = %d, want %d", row.AvailableAt, now.Add(2*time.Second).Unix())
		}
		if row.CreatedAt != now.Unix() {
			t.Fatalf("RetryFailed changed created_at: %d, want %d", row.CreatedAt, now.Unix())
		}

		again, err := store.RetryFailed(ctx, jobID, now.Add(3*time.Second))
		if err != nil {
			t.Fatalf("RetryFailed on a pending job: %v", err)
		}
		if again.Result != ContentBackupRetrySkipped {
			t.Fatalf("RetryFailed(pending) = %+v, want skipped", again)
		}
		if row := contentBackupJobRow(t, db, "site-a", jobID); row.RetryRound != 1 {
			t.Fatalf("retry_round after a skipped retry = %d, want 1", row.RetryRound)
		}

		missing, err := store.RetryFailed(ctx, "no-such-job", now)
		if err != nil {
			t.Fatalf("RetryFailed on a missing job: %v", err)
		}
		if missing.Result != ContentBackupRetryFailed {
			t.Fatalf("RetryFailed(missing) = %+v, want failed", missing)
		}
	})
}

func TestContentBackupMarkCleanedAndOwnership(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		jobID := "77777777-8888-4999-8aaa-bbbbbbbbbbbb"
		now := contentBackupTestNow
		if err := store.EnsurePending(ctx, contentBackupTestJob("site-a", jobID, "node-a", now)); err != nil {
			t.Fatalf("EnsurePending: %v", err)
		}
		if err := store.MarkCleaned(ctx, jobID, "node-a", now); !errors.Is(err, ErrCleanupConflict) {
			t.Fatalf("MarkCleaned on a pending job error = %v, want ErrCleanupConflict", err)
		}
		if err := store.ScheduleCleanup(ctx, jobID, "node-a", "too early", now, now.Add(time.Minute)); !errors.Is(err, ErrCleanupConflict) {
			t.Fatalf("ScheduleCleanup on a pending job error = %v, want ErrCleanupConflict", err)
		}

		lease, ok, err := store.Claim(ctx, jobID, "node-a", "owner-a", now, now.Add(180*time.Second))
		if err != nil || !ok {
			t.Fatalf("Claim = (%v, %v)", ok, err)
		}
		if err := store.MarkUploaded(ctx, lease, now.Add(time.Second)); err != nil {
			t.Fatalf("MarkUploaded: %v", err)
		}
		if err := store.MarkCleaned(ctx, jobID, "node-b", now.Add(2*time.Second)); !errors.Is(err, ErrCleanupConflict) {
			t.Fatalf("MarkCleaned from a foreign node error = %v, want ErrCleanupConflict", err)
		}
		cleanedAt := now.Add(3 * time.Second)
		if err := store.MarkCleaned(ctx, jobID, "node-a", cleanedAt); err != nil {
			t.Fatalf("MarkCleaned: %v", err)
		}
		if err := store.MarkCleaned(ctx, jobID, "node-a", cleanedAt); err != nil {
			t.Fatalf("MarkCleaned must be idempotent after a lost ACK: %v", err)
		}
		row := contentBackupJobRow(t, db, "site-a", jobID)
		if row.CleanupState != ContentBackupCleanupDone {
			t.Fatalf("cleanup_state = %q, want %q", row.CleanupState, ContentBackupCleanupDone)
		}
		if row.CleanedAt != cleanedAt.Unix() {
			t.Fatalf("cleaned_at = %d, want %d", row.CleanedAt, cleanedAt.Unix())
		}
		if row.Status != ContentBackupStatusUploaded {
			t.Fatalf("cleanup changed the upload status to %q", row.Status)
		}
		contentBackupFold(t, store, "node-a", cleanedAt)
		stat := contentBackupDailyStat(t, db, "site-a", "2026-09-15", "node-a")
		if stat.UploadedCount != 1 {
			t.Fatalf("cleanup must not raise uploaded_count, got %d", stat.UploadedCount)
		}
		if stat.CleanedCount != 1 || stat.FreedBytes != 2048 {
			t.Fatalf("cleaned stat = count %d bytes %d, want 1/2048", stat.CleanedCount, stat.FreedBytes)
		}
	})
}

func TestContentBackupExpireCleanedIndexesGuards(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		base := contentBackupTestNow.Add(-72 * time.Hour)

		type fixture struct {
			jobID     string
			node      string
			status    string
			cleanup   string
			uploaded  time.Time
			available time.Time
		}
		fixtures := []fixture{
			{"aaaaaaaa-0000-4000-8000-000000000001", "node-a", ContentBackupStatusUploaded, ContentBackupCleanupDone, base, base},
			{"aaaaaaaa-0000-4000-8000-000000000002", "node-a", ContentBackupStatusUploaded, ContentBackupCleanupPending, base, base},
			{"aaaaaaaa-0000-4000-8000-000000000003", "node-a", ContentBackupStatusFailed, ContentBackupCleanupNotApplicable, time.Time{}, base},
			{"aaaaaaaa-0000-4000-8000-000000000004", "node-a", ContentBackupStatusPending, ContentBackupCleanupNotApplicable, time.Time{}, base},
			{"aaaaaaaa-0000-4000-8000-000000000005", "node-b", ContentBackupStatusUploaded, ContentBackupCleanupDone, base, base},
			{"aaaaaaaa-0000-4000-8000-000000000006", "node-a", ContentBackupStatusUploaded, ContentBackupCleanupDone, base.Add(48 * time.Hour), base},
		}
		for _, item := range fixtures {
			job := contentBackupTestJob("site-a", item.jobID, item.node, base)
			if err := store.EnsurePending(ctx, job); err != nil {
				t.Fatalf("EnsurePending %s: %v", item.jobID, err)
			}
			err := db.Model(&ContentBackupJob{}).
				Where("site_id = ? AND job_id = ?", "site-a", item.jobID).
				Updates(map[string]any{
					"status":          item.status,
					"cleanup_state":   item.cleanup,
					"uploaded_at":     contentBackupUnix(item.uploaded),
					"available_at":    contentBackupUnix(item.available),
					"lease_until":     0,
					"storage_node_id": item.node,
				}).Error
			if err != nil {
				t.Fatalf("seed %s: %v", item.jobID, err)
			}
		}

		deleted, err := store.ExpireCleanedIndexes(ctx, "node-a", base.Add(24*time.Hour), 10)
		if err != nil {
			t.Fatalf("ExpireCleanedIndexes: %v", err)
		}
		if deleted != 1 {
			t.Fatalf("expired rows = %d, want 1", deleted)
		}
		if _, err := store.GetJob(ctx, "aaaaaaaa-0000-4000-8000-000000000001"); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("the cleaned index should be gone, GetJob error = %v", err)
		}
		for _, survivor := range []string{
			"aaaaaaaa-0000-4000-8000-000000000002",
			"aaaaaaaa-0000-4000-8000-000000000003",
			"aaaaaaaa-0000-4000-8000-000000000004",
			"aaaaaaaa-0000-4000-8000-000000000005",
			"aaaaaaaa-0000-4000-8000-000000000006",
		} {
			if _, err := store.GetJob(ctx, survivor); err != nil {
				t.Fatalf("job %s must survive index expiry: %v", survivor, err)
			}
		}

		bounded, err := store.ExpireCleanedIndexes(ctx, "node-a", base.Add(72*time.Hour), 0)
		if err != nil {
			t.Fatalf("ExpireCleanedIndexes with a non-positive limit: %v", err)
		}
		if bounded != 0 {
			t.Fatalf("a non-positive limit expired %d rows, want 0", bounded)
		}
	})
}

func TestContentBackupWorkerScanQueriesAreBounded(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		now := contentBackupTestNow

		seed := func(jobID, node, status string, available, leaseUntil time.Time, cleanup string, cleanupAt time.Time) {
			job := contentBackupTestJob("site-a", jobID, node, now)
			if err := store.EnsurePending(ctx, job); err != nil {
				t.Fatalf("EnsurePending %s: %v", jobID, err)
			}
			err := db.Model(&ContentBackupJob{}).
				Where("site_id = ? AND job_id = ?", "site-a", jobID).
				Updates(map[string]any{
					"status":               status,
					"available_at":         contentBackupUnix(available),
					"lease_until":          contentBackupUnix(leaseUntil),
					"cleanup_state":        cleanup,
					"cleanup_available_at": contentBackupUnix(cleanupAt),
					"uploaded_at":          contentBackupUnix(available),
				}).Error
			if err != nil {
				t.Fatalf("seed %s: %v", jobID, err)
			}
		}

		seed("bbbbbbbb-0000-4000-8000-000000000001", "node-a", ContentBackupStatusPending, now.Add(-time.Minute), time.Time{}, ContentBackupCleanupNotApplicable, time.Time{})
		seed("bbbbbbbb-0000-4000-8000-000000000002", "node-a", ContentBackupStatusPending, now.Add(time.Hour), time.Time{}, ContentBackupCleanupNotApplicable, time.Time{})
		seed("bbbbbbbb-0000-4000-8000-000000000003", "node-b", ContentBackupStatusPending, now.Add(-time.Minute), time.Time{}, ContentBackupCleanupNotApplicable, time.Time{})
		seed("bbbbbbbb-0000-4000-8000-000000000004", "node-a", ContentBackupStatusProcessing, time.Time{}, now.Add(-time.Minute), ContentBackupCleanupNotApplicable, time.Time{})
		seed("bbbbbbbb-0000-4000-8000-000000000005", "node-a", ContentBackupStatusProcessing, time.Time{}, now.Add(time.Hour), ContentBackupCleanupNotApplicable, time.Time{})
		seed("bbbbbbbb-0000-4000-8000-000000000006", "node-a", ContentBackupStatusUploaded, now.Add(-time.Minute), time.Time{}, ContentBackupCleanupPending, now.Add(-time.Minute))
		seed("bbbbbbbb-0000-4000-8000-000000000007", "node-a", ContentBackupStatusUploaded, now.Add(-time.Minute), time.Time{}, ContentBackupCleanupPending, now.Add(time.Hour))
		seed("bbbbbbbb-0000-4000-8000-000000000008", "node-a", ContentBackupStatusUploaded, now.Add(-time.Minute), time.Time{}, ContentBackupCleanupDone, time.Time{})
		seed("bbbbbbbb-0000-4000-8000-000000000009", "node-b", ContentBackupStatusUploaded, now.Add(-time.Minute), time.Time{}, ContentBackupCleanupPending, now.Add(-time.Minute))

		claimable, err := store.ListClaimableJobs(ctx, "node-a", now, 10)
		if err != nil {
			t.Fatalf("ListClaimableJobs: %v", err)
		}
		if got := contentBackupJobIDs(claimable); len(got) != 1 || got[0] != "bbbbbbbb-0000-4000-8000-000000000001" {
			t.Fatalf("claimable jobs = %v, want only the due pending job on node-a", got)
		}

		expired, err := store.ListExpiredLeases(ctx, "node-a", now, 10)
		if err != nil {
			t.Fatalf("ListExpiredLeases: %v", err)
		}
		if got := contentBackupJobIDs(expired); len(got) != 1 || got[0] != "bbbbbbbb-0000-4000-8000-000000000004" {
			t.Fatalf("expired leases = %v, want only the lapsed processing job on node-a", got)
		}

		cleanup, err := store.ListCleanupJobs(ctx, "node-a", now, 10)
		if err != nil {
			t.Fatalf("ListCleanupJobs: %v", err)
		}
		if got := contentBackupJobIDs(cleanup); len(got) != 1 || got[0] != "bbbbbbbb-0000-4000-8000-000000000006" {
			t.Fatalf("cleanup jobs = %v, want only the due uploaded/pending job on node-a", got)
		}

		limited, err := store.ListClaimableJobs(ctx, "node-a", now.Add(2*time.Hour), 1)
		if err != nil {
			t.Fatalf("ListClaimableJobs with a limit: %v", err)
		}
		if len(limited) != 1 {
			t.Fatalf("bounded scan returned %d rows, want 1", len(limited))
		}
		empty, err := store.ListClaimableJobs(ctx, "node-a", now, 0)
		if err != nil {
			t.Fatalf("ListClaimableJobs with a non-positive limit: %v", err)
		}
		if len(empty) != 0 {
			t.Fatalf("a non-positive limit returned %d rows, want 0", len(empty))
		}
		if _, err := store.ListClaimableJobs(ctx, "", now, 10); err == nil {
			t.Fatal("ListClaimableJobs accepted an empty storage node id")
		}

		counts, err := store.CountJobs(ctx, "node-a")
		if err != nil {
			t.Fatalf("CountJobs: %v", err)
		}
		if counts.PendingCount != 2 || counts.ProcessingCount != 2 || counts.FailedCount != 0 {
			t.Fatalf("counts = %+v, want pending 2 processing 2 failed 0", counts)
		}
		if counts.CleanupPendingCount != 2 || counts.CleanupPendingBytes != 4096 {
			t.Fatalf("cleanup counts = %+v, want 2 rows / 4096 bytes", counts)
		}
		if counts.OldestPendingAt != now.Unix() {
			t.Fatalf("oldest_pending_at = %d, want %d", counts.OldestPendingAt, now.Unix())
		}
	})
}

func contentBackupJobIDs(jobs []ContentBackupJob) []string {
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.JobID)
	}
	return ids
}

func TestContentBackupSchemaMigrationCreatesIndexes(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		for _, table := range ContentBackupModels() {
			if !db.Migrator().HasTable(table) {
				t.Fatalf("missing table for %T", table)
			}
		}
		for _, expected := range ContentBackupJobIndexNames() {
			if !db.Migrator().HasIndex(&ContentBackupJob{}, expected) {
				t.Fatalf("missing index %s on content_backup_jobs (%s)", expected, db.Dialector.Name())
			}
		}
		if !db.Migrator().HasIndex(&ContentBackupDailyStat{}, "uk_content_backup_daily_stats") {
			t.Fatal("missing unique index uk_content_backup_daily_stats")
		}
		if !db.Migrator().HasIndex(&ContentBackupNodeStatus{}, "uk_content_backup_node_status") {
			t.Fatal("missing unique index uk_content_backup_node_status")
		}
		if !db.Migrator().HasIndex(&ContentBackupAlert{}, "uk_content_backup_alerts") {
			t.Fatal("missing unique index uk_content_backup_alerts")
		}

		// Migration must be repeatable: the daemon restarts and re-checks schema.
		if err := db.AutoMigrate(ContentBackupModels()...); err != nil {
			t.Fatalf("second AutoMigrate: %v", err)
		}
	})
}

func TestContentBackupListJobsCursorPagination(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		other := NewContentBackupStore(db, "site-b")
		base := contentBackupTestNow

		for i := 0; i < 5; i++ {
			jobID := fmt.Sprintf("cccccccc-0000-4000-8000-%012d", i+1)
			job := contentBackupTestJob("site-a", jobID, "node-a", base.Add(time.Duration(i)*time.Hour))
			job.UserID = 10086
			job.ChannelID = 12
			job.RequestID = fmt.Sprintf("req-%d", i+1)
			if err := store.EnsurePending(ctx, job); err != nil {
				t.Fatalf("EnsurePending %s: %v", jobID, err)
			}
		}
		if err := other.EnsurePending(ctx, contentBackupTestJob("site-b", "cccccccc-9999-4000-8000-000000000009", "node-a", base)); err != nil {
			t.Fatalf("EnsurePending on site-b: %v", err)
		}

		page, err := store.ListJobs(ctx, ContentBackupJobFilter{View: ContentBackupViewArchive, PageSize: 2})
		if err != nil {
			t.Fatalf("ListJobs first page: %v", err)
		}
		if len(page.Items) != 2 || !page.HasMore || page.NextCursor == nil {
			t.Fatalf("first page = %d items hasMore=%v cursor=%v, want 2/true/non-nil", len(page.Items), page.HasMore, page.NextCursor)
		}
		if page.Items[0].JobID != "cccccccc-0000-4000-8000-000000000005" {
			t.Fatalf("archive must be newest first, got %s", page.Items[0].JobID)
		}
		if page.Items[0].SiteID != "site-a" {
			t.Fatalf("ListJobs leaked another site: %q", page.Items[0].SiteID)
		}

		seen := contentBackupJobIDs(page.Items)
		cursor := page.NextCursor
		for i := 0; i < 10 && cursor != nil; i++ {
			next, err := store.ListJobs(ctx, ContentBackupJobFilter{View: ContentBackupViewArchive, PageSize: 2, Cursor: *cursor})
			if err != nil {
				t.Fatalf("ListJobs page %d: %v", i+2, err)
			}
			seen = append(seen, contentBackupJobIDs(next.Items)...)
			cursor = next.NextCursor
			if !next.HasMore && cursor != nil {
				t.Fatalf("page %d reports no more rows but returned a cursor", i+2)
			}
		}
		if len(seen) != 5 {
			t.Fatalf("paged %d jobs, want 5 (%v)", len(seen), seen)
		}
		unique := map[string]bool{}
		for _, id := range seen {
			if unique[id] {
				t.Fatalf("cursor pagination repeated job %s", id)
			}
			unique[id] = true
		}

		byRequest, err := store.ListJobs(ctx, ContentBackupJobFilter{RequestID: "req-3", PageSize: 10})
		if err != nil {
			t.Fatalf("ListJobs by request id: %v", err)
		}
		if len(byRequest.Items) != 1 || byRequest.Items[0].RequestID != "req-3" {
			t.Fatalf("request_id filter returned %+v", contentBackupJobIDs(byRequest.Items))
		}

		userID := 10086
		byUser, err := store.ListJobs(ctx, ContentBackupJobFilter{UserID: &userID, SessionHash: strings.Repeat("a", 64), PageSize: 100})
		if err != nil {
			t.Fatalf("ListJobs by user and session hash: %v", err)
		}
		if len(byUser.Items) != 5 {
			t.Fatalf("user+session filter returned %d rows, want 5", len(byUser.Items))
		}

		channelID := 99
		byChannel, err := store.ListJobs(ctx, ContentBackupJobFilter{ChannelID: &channelID, PageSize: 10})
		if err != nil {
			t.Fatalf("ListJobs by channel: %v", err)
		}
		if len(byChannel.Items) != 0 {
			t.Fatalf("channel filter returned %d rows, want 0", len(byChannel.Items))
		}

		from := base.Add(2 * time.Hour)
		to := base.Add(3 * time.Hour)
		byWindow, err := store.ListJobs(ctx, ContentBackupJobFilter{From: &from, To: &to, PageSize: 10})
		if err != nil {
			t.Fatalf("ListJobs by window: %v", err)
		}
		if len(byWindow.Items) != 2 {
			t.Fatalf("window filter returned %d rows, want 2", len(byWindow.Items))
		}

		queue, err := store.ListJobs(ctx, ContentBackupJobFilter{View: ContentBackupViewQueue, PageSize: 10})
		if err != nil {
			t.Fatalf("ListJobs queue view: %v", err)
		}
		if len(queue.Items) != 5 {
			t.Fatalf("queue view returned %d rows, want the 5 unfinished jobs", len(queue.Items))
		}
		claimAt := base.Add(5 * time.Hour)
		lease, ok, err := store.Claim(ctx, "cccccccc-0000-4000-8000-000000000005", "node-a", "owner-a", claimAt, claimAt.Add(180*time.Second))
		if err != nil || !ok {
			t.Fatalf("Claim = (%v, %v)", ok, err)
		}
		if err := store.MarkFailed(ctx, lease, "net_timeout", "reset", claimAt.Add(time.Second)); err != nil {
			t.Fatalf("MarkFailed: %v", err)
		}
		queue, err = store.ListJobs(ctx, ContentBackupJobFilter{View: ContentBackupViewQueue, PageSize: 10})
		if err != nil {
			t.Fatalf("ListJobs queue view after a failure: %v", err)
		}
		if queue.Items[0].JobID != "cccccccc-0000-4000-8000-000000000005" {
			t.Fatalf("queue view must sort failed first, got %s", queue.Items[0].JobID)
		}
		failedOnly, err := store.ListJobs(ctx, ContentBackupJobFilter{View: ContentBackupViewQueue, Status: ContentBackupStatusFailed, PageSize: 10})
		if err != nil {
			t.Fatalf("ListJobs failed only: %v", err)
		}
		if len(failedOnly.Items) != 1 {
			t.Fatalf("status filter returned %d rows, want 1", len(failedOnly.Items))
		}

		if _, err := store.ListJobs(ctx, ContentBackupJobFilter{Cursor: "not-a-cursor"}); err == nil {
			t.Fatal("ListJobs accepted a malformed cursor")
		}
		oversized, err := store.ListJobs(ctx, ContentBackupJobFilter{PageSize: 5000})
		if err != nil {
			t.Fatalf("ListJobs with an oversized page: %v", err)
		}
		if len(oversized.Items) > 100 {
			t.Fatalf("page size was not capped, got %d rows", len(oversized.Items))
		}
		if len(oversized.Items) != 5 {
			t.Fatalf("oversized page returned %d rows, want the 5 site-a jobs", len(oversized.Items))
		}
	})
}

func TestContentBackupClaimRaceHasExactlyOneWinner(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		jobID := "99999999-0000-4111-8222-333333333333"
		now := contentBackupTestNow
		if err := store.EnsurePending(ctx, contentBackupTestJob("site-a", jobID, "node-a", now)); err != nil {
			t.Fatalf("EnsurePending: %v", err)
		}

		const workers = 6
		var wins atomic.Int32
		start := make(chan struct{})
		var wait sync.WaitGroup
		failures := make(chan error, workers)
		for i := 0; i < workers; i++ {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				<-start
				owner := fmt.Sprintf("owner-%d", index)
				lease, ok, err := store.Claim(ctx, jobID, "node-a", owner, now, now.Add(180*time.Second))
				if err != nil {
					failures <- err
					return
				}
				if ok {
					wins.Add(1)
					if lease.Generation != 1 {
						failures <- fmt.Errorf("winning lease generation = %d, want 1", lease.Generation)
					}
				}
			}(i)
		}
		close(start)
		wait.Wait()
		close(failures)
		for err := range failures {
			t.Fatalf("concurrent Claim: %v", err)
		}
		if wins.Load() != 1 {
			t.Fatalf("concurrent claims produced %d winners, want exactly 1", wins.Load())
		}
		row := contentBackupJobRow(t, db, "site-a", jobID)
		if row.Attempts != 1 || row.TotalAttempts != 1 || row.LeaseGeneration != 1 {
			t.Fatalf("losing claims mutated the row: %+v", row)
		}
	})
}

func TestContentBackupGetJobByFrameSHA256(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		store := NewContentBackupStore(db, "site-a")
		other := NewContentBackupStore(db, "site-b")
		jobID := "abababab-0000-4111-8222-cdcdcdcdcdcd"
		job := contentBackupTestJob("site-a", jobID, "node-a", contentBackupTestNow)
		if err := store.EnsurePending(ctx, job); err != nil {
			t.Fatalf("EnsurePending: %v", err)
		}

		found, err := store.GetJobByFrameSHA256(ctx, "node-a", job.FrameSHA256)
		if err != nil {
			t.Fatalf("GetJobByFrameSHA256: %v", err)
		}
		if found.JobID != jobID {
			t.Fatalf("frame lookup returned %q, want %q", found.JobID, jobID)
		}
		if _, err := store.GetJobByFrameSHA256(ctx, "node-b", job.FrameSHA256); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("frame lookup on a foreign node error = %v, want gorm.ErrRecordNotFound", err)
		}
		if _, err := other.GetJobByFrameSHA256(ctx, "node-a", job.FrameSHA256); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("frame lookup on a foreign site error = %v, want gorm.ErrRecordNotFound", err)
		}
		if _, err := store.GetJobByFrameSHA256(ctx, "node-a", strings.Repeat("0", 64)); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("frame lookup for an unknown digest error = %v, want gorm.ErrRecordNotFound", err)
		}
	})
}

type contentBackupCatalogIndexColumn struct {
	IndexName  string `gorm:"column:index_name"`
	SeqInIndex int    `gorm:"column:seq_in_index"`
	ColumnName string `gorm:"column:column_name"`
}

// contentBackupExpectedJobIndexes is the ordered column list mandated by
// design doc 6.1; every composite index starts with site_id.
func contentBackupExpectedJobIndexes() map[string][]string {
	return map[string][]string{
		"uk_content_backup_jobs_site_job":  {"site_id", "job_id"},
		"idx_cb_jobs_request":              {"site_id", "request_id"},
		"idx_cb_jobs_status_created":       {"site_id", "status", "created_at", "job_id"},
		"idx_cb_jobs_channel_created":      {"site_id", "channel_id", "created_at", "job_id"},
		"idx_cb_jobs_user_session_created": {"site_id", "user_id", "session_hash", "created_at", "job_id"},
		"idx_cb_jobs_claim":                {"site_id", "storage_node_id", "status", "available_at", "job_id"},
		"idx_cb_jobs_lease":                {"site_id", "storage_node_id", "status", "lease_until", "job_id"},
		"idx_cb_jobs_cleanup":              {"site_id", "storage_node_id", "status", "cleanup_state", "cleanup_available_at", "job_id"},
		"idx_cb_jobs_expire":               {"site_id", "status", "cleanup_state", "uploaded_at", "job_id"},
		"idx_cb_jobs_site_created":         {"site_id", "created_at", "job_id"},
	}
}

func contentBackupCatalogIndexColumns(t *testing.T, db *gorm.DB, table string, allow []string) map[string][]string {
	t.Helper()
	var rows []contentBackupCatalogIndexColumn
	switch db.Dialector.Name() {
	case "mysql":
		// MySQL 8 reports information_schema labels in upper case, so the
		// projection is aliased down to what GORM matches struct fields on.
		if err := db.Raw(
			"SELECT INDEX_NAME AS index_name, SEQ_IN_INDEX AS seq_in_index, COLUMN_NAME AS column_name"+
				" FROM information_schema.statistics"+
				" WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME <> 'PRIMARY'"+
				" ORDER BY INDEX_NAME, SEQ_IN_INDEX",
			table,
		).Scan(&rows).Error; err != nil {
			t.Fatalf("read mysql index catalog: %v", err)
		}
	case "postgres":
		if err := db.Raw(
			"SELECT i.relname AS index_name, k.ord AS seq_in_index, a.attname AS column_name"+
				" FROM pg_class t"+
				" JOIN pg_namespace n ON n.oid = t.relnamespace"+
				" JOIN pg_index ix ON t.oid = ix.indrelid"+
				" JOIN pg_class i ON i.oid = ix.indexrelid"+
				" CROSS JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord)"+
				" JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum"+
				" WHERE n.nspname = current_schema() AND t.relname = ? AND NOT ix.indisprimary"+
				" ORDER BY i.relname, k.ord",
			table,
		).Scan(&rows).Error; err != nil {
			t.Fatalf("read postgres index catalog: %v", err)
		}
	default:
		var names []string
		if err := db.Raw(
			"SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND name NOT LIKE 'sqlite_%' ORDER BY name",
			table,
		).Scan(&names).Error; err != nil {
			t.Fatalf("read sqlite index list: %v", err)
		}
		indexes := make(map[string][]string, len(names))
		for _, name := range names {
			if allow != nil && !contentBackupContainsString(allow, name) {
				t.Fatalf("sqlite reported an unexpected index %q on %s", name, table)
			}
			type sqliteIndexColumn struct {
				SeqNo int    `gorm:"column:seqno"`
				Name  string `gorm:"column:name"`
			}
			var columns []sqliteIndexColumn
			// PRAGMA takes no bind parameter; the name was just validated
			// against the frozen allow list above.
			if err := db.Raw(fmt.Sprintf("PRAGMA index_info(%q)", name)).Scan(&columns).Error; err != nil {
				t.Fatalf("read sqlite index info for %s: %v", name, err)
			}
			for _, column := range columns {
				indexes[name] = append(indexes[name], column.Name)
			}
		}
		return indexes
	}

	indexes := make(map[string][]string, len(rows))
	for _, row := range rows {
		indexes[row.IndexName] = append(indexes[row.IndexName], row.ColumnName)
	}
	return indexes
}

func TestContentBackupStoreHonoursCallerContext(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		store := NewContentBackupStore(db, "site-a")
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		jobID := "fefefefe-0000-4111-8222-0f0f0f0f0f0f"
		now := contentBackupTestNow
		lease := ContentBackupLease{
			SiteID: "site-a", JobID: jobID, StorageNodeID: "node-a",
			Owner: "owner-a", Token: strings.Repeat("e", 32), Generation: 1, Until: now.Add(time.Minute),
		}

		if err := store.EnsurePending(cancelled, contentBackupTestJob("site-a", jobID, "node-a", now)); !errors.Is(err, context.Canceled) {
			t.Fatalf("EnsurePending with a cancelled context = %v, want context.Canceled", err)
		}
		if got := contentBackupCountJobs(t, db); got != 0 {
			t.Fatalf("a cancelled EnsurePending left %d rows behind", got)
		}
		if _, err := store.GetJob(cancelled, jobID); !errors.Is(err, context.Canceled) {
			t.Fatalf("GetJob with a cancelled context = %v, want context.Canceled", err)
		}
		if _, err := store.ListJobs(cancelled, ContentBackupJobFilter{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("ListJobs with a cancelled context = %v, want context.Canceled", err)
		}
		if _, _, err := store.Claim(cancelled, jobID, "node-a", "owner-a", now, now.Add(time.Minute)); !errors.Is(err, context.Canceled) {
			t.Fatalf("Claim with a cancelled context = %v, want context.Canceled", err)
		}
		if _, err := store.Renew(cancelled, lease, now, now.Add(time.Minute)); !errors.Is(err, context.Canceled) {
			t.Fatalf("Renew with a cancelled context = %v, want context.Canceled", err)
		}
		if err := store.MarkUploaded(cancelled, lease, now); !errors.Is(err, context.Canceled) {
			t.Fatalf("MarkUploaded with a cancelled context = %v, want context.Canceled", err)
		}
		if err := store.ScheduleRetry(cancelled, lease, "x", "y", now, now.Add(time.Minute)); !errors.Is(err, context.Canceled) {
			t.Fatalf("ScheduleRetry with a cancelled context = %v, want context.Canceled", err)
		}
		if err := store.MarkFailed(cancelled, lease, "x", "y", now); !errors.Is(err, context.Canceled) {
			t.Fatalf("MarkFailed with a cancelled context = %v, want context.Canceled", err)
		}
		if err := store.MarkCleaned(cancelled, jobID, "node-a", now); !errors.Is(err, context.Canceled) {
			t.Fatalf("MarkCleaned with a cancelled context = %v, want context.Canceled", err)
		}
		if err := store.ScheduleCleanup(cancelled, jobID, "node-a", "y", now, now.Add(time.Minute)); !errors.Is(err, context.Canceled) {
			t.Fatalf("ScheduleCleanup with a cancelled context = %v, want context.Canceled", err)
		}
		if _, err := store.RetryFailed(cancelled, jobID, now); !errors.Is(err, context.Canceled) {
			t.Fatalf("RetryFailed with a cancelled context = %v, want context.Canceled", err)
		}
		if err := store.SaveNodeStatus(cancelled, ContentBackupNodeStatus{StorageNodeID: "node-a", LastSeenAt: now.Unix()}); !errors.Is(err, context.Canceled) {
			t.Fatalf("SaveNodeStatus with a cancelled context = %v, want context.Canceled", err)
		}
		if _, err := store.ExpireCleanedIndexes(cancelled, "node-a", now, 10); !errors.Is(err, context.Canceled) {
			t.Fatalf("ExpireCleanedIndexes with a cancelled context = %v, want context.Canceled", err)
		}
		if _, err := store.ListClaimableJobs(cancelled, "node-a", now, 10); !errors.Is(err, context.Canceled) {
			t.Fatalf("ListClaimableJobs with a cancelled context = %v, want context.Canceled", err)
		}
		if _, err := store.CountJobs(cancelled, "node-a"); !errors.Is(err, context.Canceled) {
			t.Fatalf("CountJobs with a cancelled context = %v, want context.Canceled", err)
		}
	})
}

func contentBackupContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestContentBackupIndexInventoryMatchesDesign(t *testing.T) {
	runContentBackupDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		jobAllow := ContentBackupJobIndexNames()
		actual := contentBackupCatalogIndexColumns(t, db, "content_backup_jobs", jobAllow)
		expected := contentBackupExpectedJobIndexes()

		names := make([]string, 0, len(actual))
		for name, columns := range actual {
			names = append(names, fmt.Sprintf("%s(%s)", name, strings.Join(columns, ",")))
		}
		sort.Strings(names)
		t.Logf("%s content_backup_jobs index inventory: %s", db.Dialector.Name(), strings.Join(names, " | "))

		if len(actual) != len(expected) {
			t.Fatalf("%s built %d indexes on content_backup_jobs, want %d: %v",
				db.Dialector.Name(), len(actual), len(expected), names)
		}
		for name, wantColumns := range expected {
			gotColumns, ok := actual[name]
			if !ok {
				t.Fatalf("%s is missing index %s", db.Dialector.Name(), name)
			}
			if !equalContentBackupStrings(gotColumns, wantColumns) {
				t.Fatalf("%s index %s columns = %v, want %v",
					db.Dialector.Name(), name, gotColumns, wantColumns)
			}
			if gotColumns[0] != "site_id" {
				t.Fatalf("%s index %s does not start with site_id: %v", db.Dialector.Name(), name, gotColumns)
			}
		}

		for _, check := range []struct {
			table string
			index string
			want  []string
		}{
			{"content_backup_daily_stats", "uk_content_backup_daily_stats", []string{"site_id", "stat_date", "storage_node_id"}},
			{"content_backup_node_status", "uk_content_backup_node_status", []string{"site_id", "storage_node_id"}},
			{"content_backup_alerts", "uk_content_backup_alerts", []string{"site_id", "storage_node_id", "reason"}},
		} {
			catalog := contentBackupCatalogIndexColumns(t, db, check.table, []string{check.index})
			got, ok := catalog[check.index]
			if !ok {
				t.Fatalf("%s is missing %s on %s", db.Dialector.Name(), check.index, check.table)
			}
			if !equalContentBackupStrings(got, check.want) {
				t.Fatalf("%s %s columns = %v, want %v", db.Dialector.Name(), check.index, got, check.want)
			}
			t.Logf("%s %s index inventory: %s(%s)",
				db.Dialector.Name(), check.table, check.index, strings.Join(got, ","))
		}
	})
}
