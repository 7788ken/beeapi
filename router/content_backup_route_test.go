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

// 本文件测的是「装配」不是「零件」：所有请求都打进 SetApiRouter 构造出来的真实引擎，
// 走真实的 AdminAuth -> RequireAdminPerm/RootAuth 链，用真实 access_token 鉴权。
//
// 这么写的直接原因：内容备份原本把四组路由挂在用户管理的 adminRoute 下（basePath
// /api/user），实际注册成了 /api/user/content_backup/*，而前端调的是 /api/content_backup/*，
// 整个功能在浏览器里全 404。当时 controller 包里那份测试自己手抄了一遍权限守卫、
// 自己选了路径前缀，所以一路全绿，什么都没拦住。

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
		"GET /api/content_backup/config",
		"GET /api/content_backup/jobs",
		"GET /api/content_backup/jobs/:job_id",
		"GET /api/content_backup/jobs/:job_id/download",
		"GET /api/content_backup/jobs/:job_id/preview",
		"GET /api/content_backup/nodes",
		"GET /api/content_backup/status",
		"POST /api/content_backup/jobs/retry",
		"POST /api/content_backup/jobs/search-session",
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

// TestContentBackupPermissionMatrixThroughRealMiddleware 走真实中间件验证权限矩阵
// （设计文档 7.1 / T09 任务卡）：默认 admin 不自动获得新权限；view 能读不能写；
// manage 蕴含 view；渠道开关归 channel.edit；配置与正文是 Root 专属。
//
// 注意两种「拒绝」的形态不同，这本身就是接线证据：AdminAuth 角色不够时返回 200 且
// success=false，RequireAdminPerm/RootAuth 权限不够时返回 403。
func TestContentBackupPermissionMatrixThroughRealMiddleware(t *testing.T) {
	engine := contentBackupRouteEnv(t)

	commonUser := seedContentBackupUser(t, "cbcommon", common.RoleCommonUser, "")
	plainAdmin := seedContentBackupUser(t, "cbplain", common.RoleAdminUser, model.AdminPermsNone)
	viewAdmin := seedContentBackupUser(t, "cbview", common.RoleAdminUser, model.AdminPermContentBackupView)
	manageAdmin := seedContentBackupUser(t, "cbmanage", common.RoleAdminUser, model.AdminPermContentBackupManage)
	channelAdmin := seedContentBackupUser(t, "cbchannel", common.RoleAdminUser, model.AdminPermChannelEdit)
	rootUser := seedContentBackupUser(t, "cbroot", common.RoleRootUser, "")

	const (
		statusPath   = "/api/content_backup/status"
		retryPath    = "/api/content_backup/jobs/retry"
		channelsPath = "/api/content_backup/channels/backup"
		configPath   = "/api/content_backup/config"
		searchPath   = "/api/content_backup/jobs/search-session"
		previewPath  = "/api/content_backup/jobs/any-job/preview"
		downloadPath = "/api/content_backup/jobs/any-job/download"
	)

	tests := []struct {
		name   string
		user   contentBackupTestUser
		method string
		path   string
		body   string
		want   int
	}{
		{"普通用户被 AdminAuth 挡在门外", commonUser, http.MethodGet, statusPath, "", http.StatusOK},
		{"默认 admin 无内容备份权限", plainAdmin, http.MethodGet, statusPath, "", http.StatusForbidden},
		{"view admin 可读状态", viewAdmin, http.MethodGet, statusPath, "", http.StatusOK},
		{"view admin 不能重试", viewAdmin, http.MethodPost, retryPath, `{"job_ids":["a"]}`, http.StatusForbidden},
		{"view admin 不能改渠道开关", viewAdmin, http.MethodPut, channelsPath, `{"channel_ids":[],"enabled":true}`, http.StatusForbidden},
		{"view admin 可按会话检索", viewAdmin, http.MethodPost, searchPath, "{}", http.StatusBadRequest},
		{"默认 admin 不能按会话检索", plainAdmin, http.MethodPost, searchPath, "{}", http.StatusForbidden},
		{"view admin 不能读配置", viewAdmin, http.MethodGet, configPath, "", http.StatusOK},
		// 正文是 Root 专属：元数据查看权不得顺带拿到正文预览和下载。
		{"view admin 不能预览正文", viewAdmin, http.MethodGet, previewPath, "", http.StatusOK},
		{"view admin 不能下载正文", viewAdmin, http.MethodGet, downloadPath, "", http.StatusOK},
		{"manage admin 不能预览正文", manageAdmin, http.MethodGet, previewPath, "", http.StatusOK},
		{"manage admin 不能下载正文", manageAdmin, http.MethodGet, downloadPath, "", http.StatusOK},
		{"manage admin 蕴含 view 可读状态", manageAdmin, http.MethodGet, statusPath, "", http.StatusOK},
		{"manage admin 可重试", manageAdmin, http.MethodPost, retryPath, `{"job_ids":["a"]}`, http.StatusOK},
		{"manage admin 不能改渠道开关", manageAdmin, http.MethodPut, channelsPath, `{"channel_ids":[],"enabled":true}`, http.StatusForbidden},
		{"channel.edit admin 可改渠道开关", channelAdmin, http.MethodPut, channelsPath, `{"channel_ids":[],"enabled":true}`, http.StatusBadRequest},
		{"channel.edit admin 不能读状态", channelAdmin, http.MethodGet, statusPath, "", http.StatusForbidden},
		{"root 可读状态", rootUser, http.MethodGet, statusPath, "", http.StatusOK},
		{"root 可读配置", rootUser, http.MethodGet, configPath, "", http.StatusOK},
	}

	// 上面两条 http.StatusOK 是「拒绝」而不是「放行」：普通用户被 AdminAuth 拒、
	// view admin 被 RootAuth 拒，都返回 200 + success=false，必须靠响应体区分。
	deniedByAuthHelper := map[string]bool{
		"普通用户被 AdminAuth 挡在门外": true,
		"view admin 不能读配置":     true,
		"view admin 不能预览正文":    true,
		"view admin 不能下载正文":    true,
		"manage admin 不能预览正文":  true,
		"manage admin 不能下载正文":  true,
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", test.user.token)
			request.Header.Set("New-Api-User", strconv.Itoa(test.user.id))
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			if response.Code != test.want {
				t.Fatalf("%s %s = %d, want %d (body %s)", test.method, test.path, response.Code, test.want, response.Body.String())
			}
			body := response.Body.String()
			if deniedByAuthHelper[test.name] {
				if !strings.Contains(body, `"success":false`) {
					t.Fatalf("%s %s 应被鉴权中间件拒绝（success=false），实际 body %s", test.method, test.path, body)
				}
				return
			}
			if test.want == http.StatusOK && !strings.Contains(body, `"success":true`) {
				t.Fatalf("%s %s 应放行并成功，实际 body %s", test.method, test.path, body)
			}
		})
	}
}
