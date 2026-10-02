package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 本文件测的是「装配」不是「零件」：所有请求都打进 SetApiRouter 构造出来的真实引擎。
// 内容备份只认 CONTENT_BACKUP_CONSOLE_TOKEN。管理员 access_token 和 root 会话都不能代替。
//
// 这么写的直接原因：内容备份原本把四组路由挂在用户管理的 adminRoute 下（basePath
// /api/user），实际注册成了 /api/user/content_backup/*，而调用方用的是 /api/content_backup/*。

// contentBackupRouteEnv 准备一套能跑完整鉴权链的最小环境：独立 SQLite、迁移 User 与
// 内容备份四张表、关掉两个限流开关（必须在 SetApiRouter 之前设，限流中间件在注册时
// 就读死了开关），最后返回真实引擎。
func contentBackupRouteEnv(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(
		sqlite.Open(t.TempDir()+"/content-backup-route.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(append(model.ContentBackupModels(), &model.User{})...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })

	previousGlobalLimit := common.GlobalApiRateLimitEnable
	previousCriticalLimit := common.CriticalRateLimitEnable
	common.GlobalApiRateLimitEnable = false
	common.CriticalRateLimitEnable = false
	t.Cleanup(func() {
		common.GlobalApiRateLimitEnable = previousGlobalLimit
		common.CriticalRateLimitEnable = previousCriticalLimit
	})

	t.Setenv("CONTENT_BACKUP_SITE_ID", "route-site")
	if err := operation_setting.SetContentBackupConfig(contentbackup.DefaultConfig()); err != nil {
		t.Fatalf("reset content backup config: %v", err)
	}

	engine := gin.New()
	SetApiRouter(engine)
	return engine
}

// contentBackupTestUser 是一条真实的 users 行 + 它的 access_token，用来走 authHelper
// 的 access token 分支（32 位十六进制不是 JWT，不会被当成内部会话令牌）。
type contentBackupTestUser struct {
	id    int
	token string
}

func seedContentBackupUser(t *testing.T, name string, role int, adminPerms string) contentBackupTestUser {
	t.Helper()
	token := fmt.Sprintf("%-32s", name)[:32]
	token = strings.ReplaceAll(token, " ", "0")
	user := model.User{
		Username:    name,
		Password:    "content-backup-route-test-hash",
		Role:        role,
		Status:      common.UserStatusEnabled,
		AdminPerms:  adminPerms,
		AccessToken: &token,
		// aff_code 建了唯一索引，留空会让第二个用户插入失败。
		AffCode: name,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("seed user %s: %v", name, err)
	}
	return contentBackupTestUser{id: user.Id, token: token}
}

// TestContentBackupRoutesRegisteredUnderApiPrefix 锁死对外路径。设计文档 §6 规定统一
// 前缀 /api/content_backup，前端 features/content-backup/api.ts 的 BASE 与之一致；
// 任何把这组路由挂错父组的改动都会让这里变红。
func TestContentBackupRoutesRegisteredUnderApiPrefix(t *testing.T) {
	engine := contentBackupRouteEnv(t)

	var got []string
	for _, route := range engine.Routes() {
		if strings.Contains(route.Path, "content_backup") {
			got = append(got, route.Method+" "+route.Path)
		}
	}
	sort.Strings(got)

	want := []string{
		"GET /api/content_backup/channels",
		"GET /api/content_backup/config",
		"GET /api/content_backup/jobs",
		"GET /api/content_backup/jobs/:job_id",
		"GET /api/content_backup/jobs/:job_id/download",
		"GET /api/content_backup/jobs/:job_id/preview",
		"GET /api/content_backup/nodes",
		"GET /api/content_backup/status",
		"POST /api/content_backup/jobs/retry",
		"POST /api/content_backup/jobs/search-session",
		"POST /api/content_backup/notify/test",
		"POST /api/content_backup/test_connection",
		"PUT /api/content_backup/channels/backup",
		"PUT /api/content_backup/config",
	}
	sort.Strings(want)

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("内容备份路由表不符：\n实际:\n%s\n期望:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestContentBackupRoutesAbsentWhenModuleOff 锁死部署级开关：CONTENT_BACKUP_MODULE=off 时
// /api/content_backup 整组不注册，连 Root 调也是 404，而不是被鉴权拒绝。
func TestContentBackupRoutesAbsentWhenModuleOff(t *testing.T) {
	previous := common.ContentBackupModuleEnabled
	common.ContentBackupModuleEnabled = false
	t.Cleanup(func() { common.ContentBackupModuleEnabled = previous })
	engine := contentBackupRouteEnv(t)

	for _, route := range engine.Routes() {
		if strings.Contains(route.Path, "content_backup") {
			t.Fatalf("模块关闭时不应注册内容备份路由，实际注册了 %s %s", route.Method, route.Path)
		}
	}

	rootUser := seedContentBackupUser(t, "cboffroot", common.RoleRootUser, "")
	for _, path := range []string{"/api/content_backup/status", "/api/content_backup/config"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", rootUser.token)
		request.Header.Set("New-Api-User", strconv.Itoa(rootUser.id))
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404 (body %s)", path, response.Code, response.Body.String())
		}
	}
}

// TestContentBackupConsoleTokenRequired 锁死：没有控制台令牌、令牌错误、
// 以及 root 的浏览器 access_token，都是 404。正确的 Bearer 令牌才能进到处理函数。
func TestContentBackupConsoleTokenRequired(t *testing.T) {
	engine := contentBackupRouteEnv(t)
	const token = "route-test-console-token"
	previous := common.ContentBackupConsoleToken
	common.ContentBackupConsoleToken = token
	t.Cleanup(func() { common.ContentBackupConsoleToken = previous })

	if err := model.DB.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatalf("migrate channels: %v", err)
	}
	rootUser := seedContentBackupUser(t, "cbroot", common.RoleRootUser, "")

	tests := []struct {
		name   string
		auth   string
		method string
		path   string
		want   int
	}{
		{"没有令牌", "", http.MethodGet, "/api/content_backup/status", http.StatusNotFound},
		{"令牌错误", "Bearer wrong-token", http.MethodGet, "/api/content_backup/config", http.StatusNotFound},
		{"root 浏览器令牌不能代替", rootUser.token, http.MethodGet, "/api/content_backup/status", http.StatusNotFound},
		{"root Bearer 会话也不能代替", "Bearer " + rootUser.token, http.MethodGet, "/api/content_backup/jobs/any-job/preview", http.StatusNotFound},
		{"正确令牌可读状态", "Bearer " + token, http.MethodGet, "/api/content_backup/status", http.StatusOK},
		{"正确令牌可读配置", "Bearer " + token, http.MethodGet, "/api/content_backup/config", http.StatusOK},
		{"正确令牌可列渠道", "Bearer " + token, http.MethodGet, "/api/content_backup/channels", http.StatusOK},
		{"没有令牌不能发测试邮件", "", http.MethodPost, "/api/content_backup/notify/test", http.StatusNotFound},
		{"令牌错误不能发测试邮件", "Bearer wrong-token", http.MethodPost, "/api/content_backup/notify/test", http.StatusNotFound},
		{"root 浏览器令牌不能发测试邮件", "Bearer " + rootUser.token, http.MethodPost, "/api/content_backup/notify/test", http.StatusNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			if test.auth != "" {
				request.Header.Set("Authorization", test.auth)
			}
			if test.name == "root 浏览器令牌不能代替" {
				request.Header.Set("New-Api-User", strconv.Itoa(rootUser.id))
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("%s %s = %d, want %d (body %s)", test.method, test.path, response.Code, test.want, response.Body.String())
			}
			if test.want == http.StatusOK && !strings.Contains(response.Body.String(), `"success":true`) {
				t.Fatalf("%s 应放行，实际 body %s", test.path, response.Body.String())
			}
		})
	}

	// 正确令牌进到测试邮件处理函数：默认配置没有收件人，回的是它自己的 400 no_recipients，
	// 不是路由层的 404，也不会真去发信。
	t.Run("正确令牌进到测试邮件处理函数", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/content_backup/notify/test", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"no_recipients"`) {
			t.Fatalf("status = %d body %s, want 400 no_recipients", response.Code, response.Body.String())
		}
	})

	t.Run("服务器未配置令牌时正确口令也是 404", func(t *testing.T) {
		common.ContentBackupConsoleToken = ""
		request := httptest.NewRequest(http.MethodGet, "/api/content_backup/status", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", response.Code)
		}
	})
}
