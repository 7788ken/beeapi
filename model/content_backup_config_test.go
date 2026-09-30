package model

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

func runContentBackupConfigDatabaseTest(t *testing.T, fn func(t *testing.T, db *gorm.DB)) {
	t.Helper()
	for _, target := range contentBackupDatabaseTargets() {
		target := target
		t.Run(target.name, func(t *testing.T) {
			db := target.open(t)
			contentBackupConfigPrepareSchema(t, db)
			contentBackupConfigPrepareOptionMap(t)
			fn(t, db)
		})
	}
}

func contentBackupConfigPrepareSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	contentBackupConfigDropOptionTable(t, db)
	if err := db.AutoMigrate(&Option{}); err != nil {
		t.Fatalf("migrate options table: %v", err)
	}
	t.Cleanup(func() { contentBackupConfigDropOptionTable(t, db) })
}

func contentBackupConfigDropOptionTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if db.Migrator().HasTable(&Option{}) {
		if err := db.Migrator().DropTable(&Option{}); err != nil {
			t.Fatalf("drop options table: %v", err)
		}
	}
}

// SaveContentBackupConfig publishes through the existing option machinery, which
// writes common.OptionMap; that map is nil until InitOptionMap runs.
func contentBackupConfigPrepareOptionMap(t *testing.T) {
	t.Helper()
	resetContentBackupConfigSnapshot := func() {
		if err := operation_setting.SetContentBackupConfig(contentbackup.DefaultConfig()); err != nil {
			t.Fatalf("reset content backup config snapshot: %v", err)
		}
	}
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	resetContentBackupConfigSnapshot()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
		resetContentBackupConfigSnapshot()
	})
}

func contentBackupStoredOption(t *testing.T, db *gorm.DB) (string, bool) {
	t.Helper()
	var option Option
	err := db.Where(map[string]any{"key": ContentBackupSettingOptionKey}).Take(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false
	}
	if err != nil {
		t.Fatalf("read stored content backup option: %v", err)
	}
	return option.Value, true
}

func contentBackupValidConfig(version int64) contentbackup.Config {
	cfg := contentbackup.DefaultConfig()
	cfg.Version = version
	cfg.Enabled = true
	cfg.TargetID = "target-a"
	cfg.SiteLabel = "sitea"
	cfg.RemoteUsername = "backup-user"
	cfg.RemotePassword = "backup-pass"
	cfg.FTPSHost = "backup.example.com"
	cfg.CertSHA256 = strings.Repeat("a", 64)
	return cfg
}

func contentBackupMustJSON(t *testing.T, cfg contentbackup.Config) string {
	t.Helper()
	blob, err := common.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal content backup config: %v", err)
	}
	return string(blob)
}

func TestContentBackupConfigSaveCreatesRowAndPublishesVersion(t *testing.T) {
	runContentBackupConfigDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		if _, found := contentBackupStoredOption(t, db); found {
			t.Fatal("options table must start without a content backup row")
		}
		if got := operation_setting.GetContentBackupConfig(); got != contentbackup.DefaultConfig() {
			t.Fatalf("snapshot = %+v, want the published defaults", got)
		}

		cfg := contentBackupValidConfig(999)
		cfg.CaptureMemoryMB = 32
		saved, err := SaveContentBackupConfig(context.Background(), db, cfg, contentbackup.DefaultConfig().Version)
		if err != nil {
			t.Fatalf("first save: %v", err)
		}
		// The caller cannot pick the published version: it is always the expected
		// version plus one, so applied_config_version stays monotonic.
		if saved.Version != contentbackup.DefaultConfig().Version+1 {
			t.Fatalf("saved version = %d, want %d", saved.Version, contentbackup.DefaultConfig().Version+1)
		}
		if saved.CaptureMemoryMB != 32 {
			t.Fatalf("saved capture_memory_mb = %d, want 32", saved.CaptureMemoryMB)
		}

		stored, found := contentBackupStoredOption(t, db)
		if !found {
			t.Fatal("save did not create the option row")
		}
		var roundTripped contentbackup.Config
		if err := common.UnmarshalJsonStr(stored, &roundTripped); err != nil {
			t.Fatalf("stored blob is not valid config JSON: %v", err)
		}
		if roundTripped != saved {
			t.Fatalf("stored blob = %+v, want %+v", roundTripped, saved)
		}

		if got := operation_setting.GetContentBackupConfig(); got != saved {
			t.Fatalf("in-memory snapshot = %+v, want the saved config %+v", got, saved)
		}
		common.OptionMapRWMutex.RLock()
		mapped := common.OptionMap[ContentBackupSettingOptionKey]
		common.OptionMapRWMutex.RUnlock()
		if mapped != stored {
			t.Fatalf("option map holds %q, want the stored blob %q", mapped, stored)
		}
	})
}

func TestContentBackupConfigSaveRejectsStaleExpectedVersion(t *testing.T) {
	runContentBackupConfigDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		first, err := SaveContentBackupConfig(ctx, db, contentBackupValidConfig(0), 1)
		if err != nil {
			t.Fatalf("bootstrap save: %v", err)
		}
		before, _ := contentBackupStoredOption(t, db)

		stale := contentBackupValidConfig(0)
		stale.TargetID = "target-hijack"
		stale.FTPSHost = "attacker.example.com"
		got, err := SaveContentBackupConfig(ctx, db, stale, first.Version-1)
		if !errors.Is(err, ErrContentBackupConfigConflict) {
			t.Fatalf("err = %v, want ErrContentBackupConfigConflict", err)
		}
		if got != (contentbackup.Config{}) {
			t.Fatalf("a rejected save returned %+v, want the zero config", got)
		}

		after, found := contentBackupStoredOption(t, db)
		if !found {
			t.Fatal("the stored config disappeared after a rejected save")
		}
		if after != before {
			t.Fatalf("stored config changed after a stale save:\n before %s\n after  %s", before, after)
		}
		if snapshot := operation_setting.GetContentBackupConfig(); snapshot != first {
			t.Fatalf("snapshot = %+v, want the untouched %+v", snapshot, first)
		}

		next, err := SaveContentBackupConfig(ctx, db, stale, first.Version)
		if err != nil {
			t.Fatalf("save with the live expected_version: %v", err)
		}
		if next.Version != first.Version+1 {
			t.Fatalf("next version = %d, want %d", next.Version, first.Version+1)
		}
		if next.TargetID != "target-hijack" {
			t.Fatalf("the live-version save dropped the edit: %+v", next)
		}
	})
}

func TestContentBackupConfigSaveRejectsInvalidConfigWithoutTouchingStorage(t *testing.T) {
	runContentBackupConfigDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		published, err := SaveContentBackupConfig(ctx, db, contentBackupValidConfig(0), 1)
		if err != nil {
			t.Fatalf("bootstrap save: %v", err)
		}
		before, _ := contentBackupStoredOption(t, db)

		invalid := map[string]contentbackup.Config{
			"retention too short": func() contentbackup.Config {
				cfg := contentBackupValidConfig(0)
				cfg.ContentRetentionDays = 30
				cfg.IndexRetentionDays = 3
				return cfg
			}(),
			"body above cap": func() contentbackup.Config {
				cfg := contentBackupValidConfig(0)
				cfg.MaxBodyBytes = contentbackup.MaxBodyBytesPerSide + 1
				return cfg
			}(),
			"enabled without cert pin": func() contentbackup.Config {
				cfg := contentBackupValidConfig(0)
				cfg.CertSHA256 = ""
				return cfg
			}(),
			"sqlite pool widened": func() contentbackup.Config {
				cfg := contentBackupValidConfig(0)
				cfg.DaemonSQLiteDBMaxOpen = 4
				return cfg
			}(),
			"zero config": {},
		}
		for name, cfg := range invalid {
			got, err := SaveContentBackupConfig(ctx, db, cfg, published.Version)
			if err == nil {
				t.Fatalf("%s: invalid config was saved", name)
			}
			if !errors.Is(err, contentbackup.ErrInvalidConfig) {
				t.Fatalf("%s: err = %v, want contentbackup.ErrInvalidConfig", name, err)
			}
			if errors.Is(err, ErrContentBackupConfigConflict) {
				t.Fatalf("%s: err = %v must not be reported as a version conflict", name, err)
			}
			if got != (contentbackup.Config{}) {
				t.Fatalf("%s: a rejected save returned %+v", name, got)
			}
			after, found := contentBackupStoredOption(t, db)
			if !found || after != before {
				t.Fatalf("%s: invalid config reached storage (found=%v)", name, found)
			}
			if snapshot := operation_setting.GetContentBackupConfig(); snapshot != published {
				t.Fatalf("%s: invalid config polluted the snapshot: %+v", name, snapshot)
			}
		}

		if _, err := SaveContentBackupConfig(ctx, db, contentBackupValidConfig(0), 0); err == nil {
			t.Fatal("expected_version 0 must be rejected: published versions start at 1")
		}
		if _, err := SaveContentBackupConfig(ctx, nil, contentBackupValidConfig(0), 1); err == nil {
			t.Fatal("a nil database must be reported instead of panicking")
		}
		if _, err := SaveContentBackupConfig(ctx, db, contentBackupValidConfig(0), 1); !errors.Is(err, ErrContentBackupConfigConflict) {
			t.Fatalf("err = %v, want ErrContentBackupConfigConflict after the invalid attempts", err)
		}
	})
}

func TestContentBackupConfigSaveConcurrentOnlyOneWins(t *testing.T) {
	runContentBackupConfigDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		published, err := SaveContentBackupConfig(ctx, db, contentBackupValidConfig(0), 1)
		if err != nil {
			t.Fatalf("bootstrap save: %v", err)
		}

		const writers = 8
		var wg sync.WaitGroup
		errs := make([]error, writers)
		saved := make([]contentbackup.Config, writers)
		start := make(chan struct{})
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(slot int) {
				defer wg.Done()
				cfg := contentBackupValidConfig(0)
				cfg.TargetID = "target-" + string(rune('a'+slot))
				<-start
				saved[slot], errs[slot] = SaveContentBackupConfig(ctx, db, cfg, published.Version)
			}(i)
		}
		close(start)
		wg.Wait()

		winners := 0
		for i, err := range errs {
			switch {
			case err == nil:
				winners++
				if saved[i].Version != published.Version+1 {
					t.Fatalf("winner %d published version %d, want %d", i, saved[i].Version, published.Version+1)
				}
			case errors.Is(err, ErrContentBackupConfigConflict):
			default:
				t.Fatalf("writer %d got %v, want nil or ErrContentBackupConfigConflict", i, err)
			}
		}
		if winners != 1 {
			t.Fatalf("%d concurrent writers with the same expected_version succeeded, want exactly 1", winners)
		}

		stored, found := contentBackupStoredOption(t, db)
		if !found {
			t.Fatal("the option row disappeared after concurrent saves")
		}
		var persisted contentbackup.Config
		if err := common.UnmarshalJsonStr(stored, &persisted); err != nil {
			t.Fatalf("stored blob is not valid config JSON: %v", err)
		}
		if persisted.Version != published.Version+1 {
			t.Fatalf("stored version = %d, want %d", persisted.Version, published.Version+1)
		}
		for i, err := range errs {
			if err == nil && persisted != saved[i] {
				t.Fatalf("stored config %+v does not match the winner %+v", persisted, saved[i])
			}
		}

		for i := 0; i < writers; i++ {
			if _, err := SaveContentBackupConfig(ctx, db, contentBackupValidConfig(0), published.Version); !errors.Is(err, ErrContentBackupConfigConflict) {
				t.Fatalf("round 2 writer %d: err = %v, want ErrContentBackupConfigConflict", i, err)
			}
		}
		after, _ := contentBackupStoredOption(t, db)
		if after != stored {
			t.Fatalf("replayed stale writers changed the stored config:\n before %s\n after  %s", stored, after)
		}
	})
}

func TestContentBackupConfigLoadReturnsDefaultsWhenAbsent(t *testing.T) {
	runContentBackupConfigDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		got, err := LoadContentBackupConfig(context.Background(), db)
		if err != nil {
			t.Fatalf("load without a stored row: %v", err)
		}
		if got != contentbackup.DefaultConfig() {
			t.Fatalf("loaded %+v, want contentbackup.DefaultConfig()", got)
		}
		if got.Enabled {
			t.Fatal("a site without a published config must not capture anything")
		}

		if _, err := LoadContentBackupConfig(context.Background(), nil); err == nil {
			t.Fatal("a nil database must be reported instead of panicking")
		}
	})
}

func TestContentBackupConfigLoadRejectsCorruptStoredRow(t *testing.T) {
	runContentBackupConfigDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		published, err := SaveContentBackupConfig(ctx, db, contentBackupValidConfig(0), 1)
		if err != nil {
			t.Fatalf("bootstrap save: %v", err)
		}

		for _, garbage := range []string{"", "   ", "not json", `{"version":"x"}`, `{"version":0}`} {
			if err := db.Model(&Option{}).Where(map[string]any{"key": ContentBackupSettingOptionKey}).
				Update("value", garbage).Error; err != nil {
				t.Fatalf("store garbage %q: %v", garbage, err)
			}
			got, err := LoadContentBackupConfig(ctx, db)
			if err == nil {
				t.Fatalf("garbage %q loaded as %+v", garbage, got)
			}
			if !errors.Is(err, operation_setting.ErrContentBackupConfigInvalid) {
				t.Fatalf("garbage %q: err = %v, want ErrContentBackupConfigInvalid", garbage, err)
			}
			if got != (contentbackup.Config{}) {
				t.Fatalf("garbage %q returned %+v, want the zero config so the caller keeps its last good copy", garbage, got)
			}
		}

		// Recovery stays possible: the console showed the admin the last effective
		// snapshot, so that version is the baseline while the row is unusable.
		recovered, err := SaveContentBackupConfig(ctx, db, contentBackupValidConfig(0), published.Version)
		if err != nil {
			t.Fatalf("recover from a corrupt row: %v", err)
		}
		if recovered.Version != published.Version+1 {
			t.Fatalf("recovered version = %d, want %d", recovered.Version, published.Version+1)
		}
		loaded, err := LoadContentBackupConfig(ctx, db)
		if err != nil {
			t.Fatalf("load after recovery: %v", err)
		}
		if loaded != recovered {
			t.Fatalf("loaded %+v, want the recovered %+v", loaded, recovered)
		}
	})
}

// The generic RootAuth /option entry writes the row before this module validates
// it, so a bad blob may land in the database; it must never become a snapshot.
func TestContentBackupConfigGenericOptionWriteCannotBypassValidation(t *testing.T) {
	runContentBackupConfigDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		published, err := SaveContentBackupConfig(ctx, db, contentBackupValidConfig(0), 1)
		if err != nil {
			t.Fatalf("bootstrap save: %v", err)
		}

		garbage := `{"version":99,"enabled":true,"target_id":"smuggled","ftps_password":"leaked"}`
		if err := db.Model(&Option{}).Where(map[string]any{"key": ContentBackupSettingOptionKey}).
			Update("value", garbage).Error; err != nil {
			t.Fatalf("simulate the generic /option overwrite: %v", err)
		}
		if err := updateOptionMap(ContentBackupSettingOptionKey, garbage); err == nil {
			t.Fatal("updateOptionMap accepted a config that never passed ValidateConfig")
		}
		if snapshot := operation_setting.GetContentBackupConfig(); snapshot != published {
			t.Fatalf("the generic write path published %+v, want the previous %+v", snapshot, published)
		}
		if _, err := LoadContentBackupConfig(ctx, db); !errors.Is(err, operation_setting.ErrContentBackupConfigInvalid) {
			t.Fatalf("load err = %v, want ErrContentBackupConfigInvalid", err)
		}

		if err := updateOptionMap(ContentBackupSettingOptionKey, contentBackupMustJSON(t, published)); err != nil {
			t.Fatalf("re-apply the good blob: %v", err)
		}
		if snapshot := operation_setting.GetContentBackupConfig(); snapshot != published {
			t.Fatalf("snapshot = %+v, want %+v", snapshot, published)
		}
	})
}

// 2026-09-18 起凭据存在配置整包里（与 SMTP/支付密钥同一方式）；节点身份与 spool 路径仍不进库。
func TestContentBackupConfigStoredBlobCarriesCredentialsButNoIdentity(t *testing.T) {
	runContentBackupConfigDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		cfg := contentBackupValidConfig(0)
		cfg.UploadPaused = true
		if _, err := SaveContentBackupConfig(ctx, db, cfg, 1); err != nil {
			t.Fatalf("save: %v", err)
		}
		stored, found := contentBackupStoredOption(t, db)
		if !found {
			t.Fatal("the option row was not created")
		}
		for _, forbidden := range []string{
			"site_id", "storage_node_id", "process_id", "socket", "spool_dir", "ftps_username", "ftps_password",
		} {
			if strings.Contains(stored, forbidden) {
				t.Fatalf("the options table must not carry %q, stored blob: %s", forbidden, stored)
			}
		}
		for _, expected := range []string{
			`"version":2`, `"enabled":true`, `"upload_paused":true`, `"site_label":"sitea"`, `"target_id":"target-a"`,
			`"remote_username":"backup-user"`, `"remote_password":"backup-pass"`,
			`"ftps_host":"backup.example.com"`, `"cert_sha256":"` + strings.Repeat("a", 64) + `"`,
			`"content_retention_days":30`, `"index_retention_days":30`, `"stats_retention_days":90`,
		} {
			if !strings.Contains(stored, expected) {
				t.Fatalf("stored blob is missing %s: %s", expected, stored)
			}
		}
	})
}

// GET /config 从不回显口令，所以表单整包保存时口令为空；空必须表示"沿用已存口令"，
// 否则每次改一个无关参数都会把口令清掉、上传随即全部认证失败。
func TestContentBackupConfigEmptyPasswordKeepsStoredOne(t *testing.T) {
	runContentBackupConfigDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		first := contentBackupValidConfig(0)
		saved, err := SaveContentBackupConfig(ctx, db, first, 1)
		if err != nil {
			t.Fatalf("first save: %v", err)
		}

		update := saved
		update.RemotePassword = ""
		update.UploadPaused = true
		saved2, err := SaveContentBackupConfig(ctx, db, update, saved.Version)
		if err != nil {
			t.Fatalf("second save with empty password must succeed by keeping the stored one: %v", err)
		}
		if saved2.RemotePassword != "backup-pass" || !saved2.UploadPaused {
			t.Fatalf("saved = %+v, want stored password kept and the other change applied", saved2.Redacted())
		}
		stored, _ := contentBackupStoredOption(t, db)
		if !strings.Contains(stored, `"remote_password":"backup-pass"`) {
			t.Fatalf("stored blob lost the password: %s", stored)
		}
		loaded, err := LoadContentBackupConfig(ctx, db)
		if err != nil || loaded.RemotePassword != "backup-pass" {
			t.Fatalf("loaded = %+v err=%v", loaded.Redacted(), err)
		}

		// 显式给新口令则替换。
		update = saved2
		update.RemotePassword = "rotated"
		saved3, err := SaveContentBackupConfig(ctx, db, update, saved2.Version)
		if err != nil || saved3.RemotePassword != "rotated" {
			t.Fatalf("explicit password must replace, got %+v err=%v", saved3.Redacted(), err)
		}

		// 首次保存（库里没有行）且启用态口令为空：没有可沿用的，必须拒绝。
		if err := db.Where(map[string]any{"key": ContentBackupSettingOptionKey}).Delete(&Option{}).Error; err != nil {
			t.Fatalf("clear option row: %v", err)
		}
		noPassword := contentBackupValidConfig(0)
		noPassword.RemotePassword = ""
		if _, err := SaveContentBackupConfig(ctx, db, noPassword, 1); !errors.Is(err, contentbackup.ErrInvalidConfig) {
			t.Fatalf("first enabled save without a password must be rejected, got %v", err)
		}
	})
}

func TestContentBackupConfigVersionNeverGoesBackwards(t *testing.T) {
	runContentBackupConfigDatabaseTest(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		version := int64(1)
		for i := 0; i < 5; i++ {
			saved, err := SaveContentBackupConfig(ctx, db, contentBackupValidConfig(0), version)
			if err != nil {
				t.Fatalf("save %d: %v", i, err)
			}
			if saved.Version != version+1 {
				t.Fatalf("save %d published version %d, want %d", i, saved.Version, version+1)
			}
			loaded, err := LoadContentBackupConfig(ctx, db)
			if err != nil {
				t.Fatalf("load %d: %v", i, err)
			}
			if loaded != saved {
				t.Fatalf("load %d returned %+v, want %+v", i, loaded, saved)
			}
			version = saved.Version
		}
	})
}
