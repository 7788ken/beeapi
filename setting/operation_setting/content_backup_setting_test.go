package operation_setting

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/gin-gonic/gin"
)

func contentBackupTestCertSHA256() string { return strings.Repeat("a", 64) }

func contentBackupEnabledTestConfig() contentbackup.Config {
	cfg := contentbackup.DefaultConfig()
	cfg.Enabled = true
	cfg.TargetID = "target-a"
	cfg.SiteLabel = "sitea"
	cfg.RemoteUsername = "backup-user"
	cfg.RemotePassword = "backup-pass"
	cfg.FTPSHost = "backup.example.com"
	cfg.CertSHA256 = contentBackupTestCertSHA256()
	return cfg
}

func resetContentBackupConfigSnapshot(t *testing.T) {
	t.Helper()
	contentBackupConfigSnapshot.Store(contentbackup.DefaultConfig())
	contentBackupConfigRejectMu.Lock()
	contentBackupConfigRejectLast = time.Time{}
	contentBackupConfigRejectSinceLog = 0
	contentBackupConfigRejectTotal = 0
	contentBackupConfigRejectMu.Unlock()
	t.Cleanup(func() {
		contentBackupConfigSnapshot.Store(contentbackup.DefaultConfig())
		contentBackupConfigRejectMu.Lock()
		contentBackupConfigRejectLast = time.Time{}
		contentBackupConfigRejectSinceLog = 0
		contentBackupConfigRejectTotal = 0
		contentBackupConfigRejectMu.Unlock()
	})
}

// common.SysError 自己会取 LogWriterMu 的读锁，所以这里只能换 writer，不能持写锁。
func captureContentBackupSysError(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	previous := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &buf
	defer func() { gin.DefaultErrorWriter = previous }()
	fn()
	return buf.String()
}

// 库里的整包是某个历史版本写下的：后来新增的键在旧行里不存在。2026-09-18 ai 线上实发——
// 新镜像按零值解码旧行，sftp_port=0 撞上范围校验，一份合法的已启用配置每分钟被整体拒掉，
// 进程静默回落到默认快照。缺失键必须取默认值；显式存进去的非法值仍要拒。
func TestContentBackupConfigParseFillsMissingKeysWithDefaults(t *testing.T) {
	legacy := contentBackupEnabledTestConfig()
	legacy.Version = 2
	blob, err := common.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := common.Unmarshal(blob, &raw); err != nil {
		t.Fatal(err)
	}
	for _, newer := range []string{"remote_protocol", "sftp_host", "sftp_port", "sftp_host_key_sha256", "sftp_base_dir"} {
		delete(raw, newer)
	}
	stored, err := common.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseContentBackupConfig(string(stored))
	if err != nil {
		t.Fatalf("a legacy row without the sftp keys must still parse: %v", err)
	}
	if !parsed.Enabled || parsed.Version != 2 || parsed.TargetID != "target-a" {
		t.Fatalf("stored values must survive: %+v", parsed)
	}
	if parsed.SFTPPort != 22 || parsed.RemoteProtocol != contentbackup.RemoteProtocolFTPS {
		t.Fatalf("missing keys must take DefaultConfig values, got sftp_port=%d remote_protocol=%q", parsed.SFTPPort, parsed.RemoteProtocol)
	}

	// 显式写进去的 0 不是"缺失"，照常拒绝。
	raw["sftp_port"] = 0
	explicitZero, _ := common.Marshal(raw)
	if _, err := ParseContentBackupConfig(string(explicitZero)); !errors.Is(err, ErrContentBackupConfigInvalid) {
		t.Fatalf("an explicit sftp_port=0 must still be rejected, got %v", err)
	}
}

// 旧版本保存的整包可能是"开关开着、但没有 site_label / 凭据"（当时还不是启用前置）。
// 这种行必须降级为关、保留其余字段，而不是整包拒掉让页面上的配置消失、日志每分钟一条。
func TestContentBackupConfigParseDegradesEnabledWithoutPrerequisitesToDisabled(t *testing.T) {
	legacy := contentBackupEnabledTestConfig()
	legacy.Version = 2
	legacy.SiteLabel = ""
	legacy.RemoteUsername = ""
	legacy.RemotePassword = ""
	blob, err := common.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseContentBackupConfig(string(blob))
	if err != nil {
		t.Fatalf("enabled-without-prerequisites must degrade, not fail: %v", err)
	}
	if parsed.Enabled {
		t.Fatal("the capture switch cannot be honored without its prerequisites")
	}
	if parsed.TargetID != "target-a" || parsed.FTPSHost != "backup.example.com" || parsed.Version != 2 {
		t.Fatalf("the rest of the stored config must survive: %+v", parsed.Redacted())
	}
	// 与启用无关的非法值仍然整包拒绝，不能借降级放行。
	legacy.SFTPPort = 70000
	blob, _ = common.Marshal(legacy)
	if _, err := ParseContentBackupConfig(string(blob)); !errors.Is(err, ErrContentBackupConfigInvalid) {
		t.Fatalf("an out-of-range value must still be rejected, got %v", err)
	}
}

func TestContentBackupConfigSnapshotDefaultsBeforeAnyLoad(t *testing.T) {
	resetContentBackupConfigSnapshot(t)

	got := GetContentBackupConfig()
	if got != contentbackup.DefaultConfig() {
		t.Fatalf("snapshot must start as contentbackup.DefaultConfig(), got %+v", got)
	}
	if got.Version != 1 {
		t.Fatalf("default config version = %d, want 1", got.Version)
	}
	if got.Enabled {
		t.Fatal("capture must stay disabled until an admin publishes a config")
	}
}

func TestContentBackupConfigSnapshotIsAValueCopy(t *testing.T) {
	resetContentBackupConfigSnapshot(t)

	old := GetContentBackupConfig()
	if old.CaptureMemoryMB != 64 {
		t.Fatalf("default capture_memory_mb = %d, want 64", old.CaptureMemoryMB)
	}

	next := old
	next.Version = 2
	next.CaptureMemoryMB = 8
	if err := SetContentBackupConfig(next); err != nil {
		t.Fatalf("publish shrunken budget: %v", err)
	}

	published := GetContentBackupConfig()
	if published.CaptureMemoryMB != 8 {
		t.Fatalf("published capture_memory_mb = %d, want 8", published.CaptureMemoryMB)
	}
	// A smaller budget only stops new admissions: whoever already holds the old
	// snapshot keeps running against it until it releases it.
	if old.CaptureMemoryMB != 64 {
		t.Fatalf("previously handed out snapshot changed to %d, want it to stay 64", old.CaptureMemoryMB)
	}
	if old.Version != 1 {
		t.Fatalf("previously handed out snapshot version changed to %d, want 1", old.Version)
	}
}

func TestContentBackupConfigRejectsInvalidWholeConfig(t *testing.T) {
	resetContentBackupConfigSnapshot(t)

	valid := contentBackupEnabledTestConfig()
	valid.Version = 7
	if err := SetContentBackupConfig(valid); err != nil {
		t.Fatalf("publish valid config: %v", err)
	}

	cases := map[string]contentbackup.Config{
		"version below one": func() contentbackup.Config { c := valid; c.Version = 0; return c }(),
		"index shorter than content": func() contentbackup.Config {
			c := valid
			c.ContentRetentionDays = 30
			c.IndexRetentionDays = 7
			return c
		}(),
		"body bytes above cap": func() contentbackup.Config {
			c := valid
			c.MaxBodyBytes = contentbackup.MaxBodyBytesPerSide + 1
			return c
		}(),
		"enabled without target": func() contentbackup.Config {
			c := valid
			c.TargetID = ""
			return c
		}(),
		"enabled without cert pin": func() contentbackup.Config {
			c := valid
			c.CertSHA256 = ""
			return c
		}(),
		"uppercase cert pin": func() contentbackup.Config {
			c := valid
			c.CertSHA256 = strings.ToUpper(contentBackupTestCertSHA256())
			return c
		}(),
	}
	for name, rejected := range cases {
		err := SetContentBackupConfig(rejected)
		if err == nil {
			t.Fatalf("%s: invalid config was published", name)
		}
		if !errors.Is(err, ErrContentBackupConfigInvalid) {
			t.Fatalf("%s: error %v must match ErrContentBackupConfigInvalid", name, err)
		}
		if !errors.Is(err, contentbackup.ErrInvalidConfig) {
			t.Fatalf("%s: error %v must match contentbackup.ErrInvalidConfig", name, err)
		}
		if got := GetContentBackupConfig(); got != valid {
			t.Fatalf("%s: rejected config polluted the snapshot: got %+v", name, got)
		}
	}

	if err := SetContentBackupConfig(contentbackup.Config{}); err == nil {
		t.Fatal("zero config must be rejected instead of publishing an all-zero budget")
	}
	if got := GetContentBackupConfig(); got != valid {
		t.Fatalf("zero config polluted the snapshot: got %+v", got)
	}
}

func TestContentBackupConfigRejectsRetentionTooShort(t *testing.T) {
	resetContentBackupConfigSnapshot(t)

	cfg := contentBackupEnabledTestConfig()
	cfg.ContentRetentionDays = 30
	cfg.IndexRetentionDays = 29
	err := SetContentBackupConfig(cfg)
	if !errors.Is(err, contentbackup.ErrRetentionTooShort) {
		t.Fatalf("err = %v, want contentbackup.ErrRetentionTooShort", err)
	}
	if got := GetContentBackupConfig(); got.IndexRetentionDays != contentbackup.DefaultConfig().IndexRetentionDays {
		t.Fatalf("snapshot index_retention_days = %d, want the untouched default", got.IndexRetentionDays)
	}
}

func TestContentBackupConfigUpdateFromJSONStringPublishesWholeBlob(t *testing.T) {
	resetContentBackupConfigSnapshot(t)

	cfg := contentBackupEnabledTestConfig()
	cfg.Version = 4
	cfg.UploadPaused = true
	cfg.CaptureMemoryMB = 32
	blob, err := common.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	if err := UpdateContentBackupSettingByJsonString(string(blob)); err != nil {
		t.Fatalf("apply config blob: %v", err)
	}
	got := GetContentBackupConfig()
	if got != cfg {
		t.Fatalf("snapshot = %+v, want %+v", got, cfg)
	}
	if !got.Enabled || !got.UploadPaused {
		t.Fatalf("enabled=%v upload_paused=%v, want both true", got.Enabled, got.UploadPaused)
	}
}

func TestContentBackupConfigUpdateFromJSONStringKeepsSnapshotOnGarbage(t *testing.T) {
	resetContentBackupConfigSnapshot(t)

	cfg := contentBackupEnabledTestConfig()
	cfg.Version = 9
	if err := SetContentBackupConfig(cfg); err != nil {
		t.Fatalf("publish valid config: %v", err)
	}

	for _, garbage := range []string{
		"",
		"   ",
		"not json",
		"{",
		`{"version":"not-a-number"}`,
		`[]`,
		`null`,
		`{"version":0}`,
		`{"version":3,"index_retention_days":1,"content_retention_days":30}`,
	} {
		if err := UpdateContentBackupSettingByJsonString(garbage); err == nil {
			t.Fatalf("garbage %q was accepted", garbage)
		} else if !errors.Is(err, ErrContentBackupConfigInvalid) {
			t.Fatalf("garbage %q: err = %v, want ErrContentBackupConfigInvalid", garbage, err)
		}
		if got := GetContentBackupConfig(); got != cfg {
			t.Fatalf("garbage %q polluted the snapshot: got %+v", garbage, got)
		}
	}
}

// 凭据自 2026-09-18 起就存在配置整包里（与 SMTP/支付密钥同一方式），但节点身份与 spool
// 路径仍是部署事实：从通用 /option 入口塞进来的这些键必须在解析时被丢弃，不能改写节点身份。
func TestContentBackupConfigDropsDeploymentIdentityKeys(t *testing.T) {
	resetContentBackupConfigSnapshot(t)

	cfg := contentBackupEnabledTestConfig()
	cfg.Version = 5
	blob, err := common.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	smuggled := strings.TrimSuffix(string(blob), "}") +
		`,"site_id":"renamed-site","storage_node_id":"renamed-node",` +
		`"process_id":"renamed-process","socket":"/tmp/renamed.sock","spool_dir":"/var/renamed"}`

	if err := UpdateContentBackupSettingByJsonString(smuggled); err != nil {
		t.Fatalf("unknown keys must be ignored, not rejected: %v", err)
	}

	got := GetContentBackupConfig()
	if got != cfg {
		t.Fatalf("snapshot = %+v, want the blob without the smuggled keys %+v", got, cfg)
	}
	published, err := common.Marshal(got)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if strings.Contains(string(published), "renamed") {
		t.Fatalf("published config leaked a smuggled identity key: %s", published)
	}
	// 凭据是配置的一部分，必须原样保留在快照里，上传器才连得上远端。
	if got.RemoteUsername != "backup-user" || got.RemotePassword != "backup-pass" {
		t.Fatalf("credentials must survive the round trip, got %q/%q", got.RemoteUsername, got.RemotePassword)
	}
}

// 整包 JSON 不得带节点身份/spool 路径；凭据键只允许 remote_username / remote_password 这一对，
// 且 Redacted() 后口令为空——GET /config 只能下发 Redacted 版本。
func TestContentBackupConfigJSONCarriesNoDeploymentIdentity(t *testing.T) {
	resetContentBackupConfigSnapshot(t)

	if err := SetContentBackupConfig(contentBackupEnabledTestConfig()); err != nil {
		t.Fatalf("publish enabled config: %v", err)
	}
	roundTripped := ContentBackupConfigJSONString()
	for _, forbidden := range []string{
		"site_id", "storage_node_id", "process_id", "socket", "spool_dir", "ftps_username", "ftps_password",
	} {
		if strings.Contains(roundTripped, forbidden) {
			t.Fatalf("published config JSON must not carry %q: %s", forbidden, roundTripped)
		}
	}
	if !strings.Contains(roundTripped, `"remote_password":"backup-pass"`) {
		t.Fatalf("the stored blob is where the password lives: %s", roundTripped)
	}
	redacted, err := common.Marshal(GetContentBackupConfig().Redacted())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(redacted), "backup-pass") || !strings.Contains(string(redacted), `"remote_password":""`) {
		t.Fatalf("Redacted config must blank the password: %s", redacted)
	}
}

func TestContentBackupConfigRejectLoggingIsRateLimited(t *testing.T) {
	resetContentBackupConfigSnapshot(t)
	previousInterval := contentBackupConfigRejectLogInterval
	contentBackupConfigRejectLogInterval = time.Minute
	t.Cleanup(func() { contentBackupConfigRejectLogInterval = previousInterval })

	logged := captureContentBackupSysError(t, func() {
		for i := 0; i < 5; i++ {
			if err := UpdateContentBackupSettingByJsonString("not json"); err == nil {
				t.Fatalf("reject %d: garbage was accepted", i)
			}
		}
	})
	if got := ContentBackupConfigRejectCount(); got != 5 {
		t.Fatalf("reject count = %d, want 5", got)
	}
	if occurrences := strings.Count(logged, "[SYS]"); occurrences != 1 {
		t.Fatalf("rate limited log emitted %d lines within one interval, want exactly 1: %q", occurrences, logged)
	}

	contentBackupConfigRejectLast = time.Now().Add(-2 * contentBackupConfigRejectLogInterval)
	logged = captureContentBackupSysError(t, func() {
		if err := UpdateContentBackupSettingByJsonString("still not json"); err == nil {
			t.Fatal("garbage was accepted after the rate limit window")
		}
	})
	if got := ContentBackupConfigRejectCount(); got != 6 {
		t.Fatalf("reject count = %d, want 6", got)
	}
	if !strings.Contains(logged, "content backup config") {
		t.Fatalf("a rejection after the window must be logged again, got %q", logged)
	}
}

func TestContentBackupConfigSnapshotHasNoDataRace(t *testing.T) {
	resetContentBackupConfigSnapshot(t)

	blob, err := common.Marshal(contentBackupEnabledTestConfig())
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snapshot := GetContentBackupConfig()
				_ = snapshot.CaptureMemoryMB
				_ = ContentBackupConfigJSONString()
			}
		}()
	}
	for i := 0; i < 200; i++ {
		if err := UpdateContentBackupSettingByJsonString(string(blob)); err != nil {
			t.Fatalf("publish during concurrent reads: %v", err)
		}
	}
	close(stop)
	wg.Wait()

	if got := GetContentBackupConfig(); got.CaptureMemoryMB != contentbackup.DefaultConfig().CaptureMemoryMB {
		t.Fatalf("snapshot capture_memory_mb = %d after concurrent publishing", got.CaptureMemoryMB)
	}
}

func TestContentBackupConfigParseRejectsPartialFieldWrites(t *testing.T) {
	resetContentBackupConfigSnapshot(t)

	cfg := contentBackupEnabledTestConfig()
	cfg.Version = 2
	if err := SetContentBackupConfig(cfg); err != nil {
		t.Fatalf("publish valid config: %v", err)
	}

	// Half a config: someone stored only the fields they cared about.
	partial := `{"version":3,"enabled":true}`
	if _, err := ParseContentBackupConfig(partial); err == nil {
		t.Fatal("a partial blob must not become a publishable config")
	}
	if got := GetContentBackupConfig(); got != cfg {
		t.Fatalf("partial blob polluted the snapshot: got %+v", got)
	}

	var validation *contentbackup.ValidationError
	full := cfg
	full.CertSHA256 = "zz"
	if _, err := ParseContentBackupConfig(mustContentBackupJSON(t, full)); !errors.As(err, &validation) {
		t.Fatalf("err = %v, want a *contentbackup.ValidationError with field details", err)
	}
	if validation.Field != "cert_sha256" {
		t.Fatalf("validation field = %q, want cert_sha256", validation.Field)
	}
}

func mustContentBackupJSON(t *testing.T, cfg contentbackup.Config) string {
	t.Helper()
	blob, err := common.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return string(blob)
}
