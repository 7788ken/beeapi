package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupManageQuotaTestDB 起一个只装 ManageUser 额度路径所需表的内存库。
func setupManageQuotaTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousSQLite := common.UsingSQLite

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open manage quota test db: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Log{}); err != nil {
		t.Fatalf("migrate manage quota test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get manage quota sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.UsingSQLite = true
	gin.SetMode(gin.TestMode)

	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.UsingSQLite = previousSQLite
		_ = sqlDB.Close()
	})
	return db
}

func createQuotaTestUser(t *testing.T, db *gorm.DB, name string, role int, quota int, perms string) *model.User {
	t.Helper()
	user := &model.User{
		Username:   name,
		Password:   "password123",
		AffCode:    "aff-" + name,
		Role:       role,
		Status:     common.UserStatusEnabled,
		Quota:      quota,
		AdminPerms: perms,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return user
}

func invokeManageUser(t *testing.T, operator *model.User, req ManageRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := common.Marshal(req)
	if err != nil {
		t.Fatalf("marshal manage request: %v", err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/manage", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", operator.Id)
	c.Set("role", operator.Role)
	c.Set("username", operator.Username)
	ManageUser(c)
	return recorder
}

func quotaOf(t *testing.T, db *gorm.DB, id int) int {
	t.Helper()
	var user model.User
	if err := db.First(&user, id).Error; err != nil {
		t.Fatalf("reload user %d: %v", id, err)
	}
	return user.Quota
}

func TestManageUserAddQuotaDeniedWithoutGrantPerm(t *testing.T) {
	db := setupManageQuotaTestDB(t)
	admin := createQuotaTestUser(t, db, "no-grant-admin", common.RoleAdminUser, 0, "channel.view")
	target := createQuotaTestUser(t, db, "grant-target", common.RoleCommonUser, 0, "")

	recorder := invokeManageUser(t, admin, ManageRequest{Id: target.Id, Action: "add_quota", Mode: "add", Value: 100})
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (body=%s)", recorder.Code, http.StatusForbidden, recorder.Body.String())
	}
	if got := quotaOf(t, db, target.Id); got != 0 {
		t.Fatalf("target quota = %d, want 0", got)
	}
}

func TestManageUserAdminCannotSubtractOrOverrideQuota(t *testing.T) {
	db := setupManageQuotaTestDB(t)
	admin := createQuotaTestUser(t, db, "grant-admin", common.RoleAdminUser, 0, "quota.grant")
	target := createQuotaTestUser(t, db, "keep-quota-target", common.RoleCommonUser, 500, "")

	for _, mode := range []string{"subtract", "override"} {
		recorder := invokeManageUser(t, admin, ManageRequest{Id: target.Id, Action: "add_quota", Mode: mode, Value: 0})
		if !strings.Contains(recorder.Body.String(), "\"success\":false") {
			t.Fatalf("mode %s should be rejected, body=%s", mode, recorder.Body.String())
		}
	}
	if got := quotaOf(t, db, target.Id); got != 500 {
		t.Fatalf("target quota = %d, want 500 (归零/扣减必须被拦住)", got)
	}
}

func TestManageUserAdminCannotAdjustAnotherAdminQuota(t *testing.T) {
	db := setupManageQuotaTestDB(t)
	admin := createQuotaTestUser(t, db, "granting-admin", common.RoleAdminUser, 0, "quota.grant")
	// 目标必须是普通用户；同级管理员会先被 MsgUserNoPermissionHigherLevel 拦住
	target := createQuotaTestUser(t, db, "peer-admin", common.RoleAdminUser, 10, "")

	recorder := invokeManageUser(t, admin, ManageRequest{Id: target.Id, Action: "add_quota", Mode: "add", Value: 100})
	if !strings.Contains(recorder.Body.String(), "\"success\":false") {
		t.Fatalf("adjusting a peer admin should be rejected, body=%s", recorder.Body.String())
	}
	if got := quotaOf(t, db, target.Id); got != 10 {
		t.Fatalf("peer admin quota = %d, want 10", got)
	}
}

func TestManageUserAddQuotaDeductsFromAdminWhenConfigured(t *testing.T) {
	db := setupManageQuotaTestDB(t)
	admin := createQuotaTestUser(t, db, "reseller-admin", common.RoleAdminUser, 1000, "quota.grant,quota.deduct_self")
	target := createQuotaTestUser(t, db, "reseller-target", common.RoleCommonUser, 20, "")

	recorder := invokeManageUser(t, admin, ManageRequest{Id: target.Id, Action: "add_quota", Mode: "add", Value: 300})
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "\"success\":true") {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if got := quotaOf(t, db, admin.Id); got != 700 {
		t.Fatalf("admin quota = %d, want 700", got)
	}
	if got := quotaOf(t, db, target.Id); got != 320 {
		t.Fatalf("target quota = %d, want 320", got)
	}
}

func TestManageUserAddQuotaRejectedWhenAdminBalanceInsufficient(t *testing.T) {
	db := setupManageQuotaTestDB(t)
	admin := createQuotaTestUser(t, db, "broke-admin", common.RoleAdminUser, 50, "quota.grant,quota.deduct_self")
	target := createQuotaTestUser(t, db, "broke-target", common.RoleCommonUser, 0, "")

	recorder := invokeManageUser(t, admin, ManageRequest{Id: target.Id, Action: "add_quota", Mode: "add", Value: 100})
	if !strings.Contains(recorder.Body.String(), "\"success\":false") {
		t.Fatalf("insufficient admin quota should fail, body=%s", recorder.Body.String())
	}
	if got := quotaOf(t, db, admin.Id); got != 50 {
		t.Fatalf("admin quota = %d, want 50", got)
	}
	if got := quotaOf(t, db, target.Id); got != 0 {
		t.Fatalf("target quota = %d, want 0 (失败必须整笔回滚)", got)
	}
}

func TestManageUserAddQuotaDoesNotTouchAdminWithoutDeductSelf(t *testing.T) {
	db := setupManageQuotaTestDB(t)
	admin := createQuotaTestUser(t, db, "plain-admin", common.RoleAdminUser, 1000, "quota.grant")
	target := createQuotaTestUser(t, db, "plain-target", common.RoleCommonUser, 0, "")

	recorder := invokeManageUser(t, admin, ManageRequest{Id: target.Id, Action: "add_quota", Mode: "add", Value: 100})
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "\"success\":true") {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if got := quotaOf(t, db, admin.Id); got != 1000 {
		t.Fatalf("admin quota = %d, want 1000", got)
	}
	if got := quotaOf(t, db, target.Id); got != 100 {
		t.Fatalf("target quota = %d, want 100", got)
	}
}

func TestManageUserNonQuotaActionRequiresUserManagePerm(t *testing.T) {
	db := setupManageQuotaTestDB(t)
	admin := createQuotaTestUser(t, db, "quota-only-admin", common.RoleAdminUser, 0, "quota.grant")
	target := createQuotaTestUser(t, db, "disable-target", common.RoleCommonUser, 0, "")

	recorder := invokeManageUser(t, admin, ManageRequest{Id: target.Id, Action: "disable"})
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (body=%s)", recorder.Code, http.StatusForbidden, recorder.Body.String())
	}
}

func TestManageUserRootKeepsFullQuotaControl(t *testing.T) {
	db := setupManageQuotaTestDB(t)
	root := createQuotaTestUser(t, db, "root-user", common.RoleRootUser, 1000, "")
	target := createQuotaTestUser(t, db, "root-target", common.RoleCommonUser, 500, "")

	recorder := invokeManageUser(t, root, ManageRequest{Id: target.Id, Action: "add_quota", Mode: "override", Value: 0})
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "\"success\":true") {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if got := quotaOf(t, db, target.Id); got != 0 {
		t.Fatalf("target quota = %d, want 0", got)
	}
	// root 充值不扣自己
	if got := quotaOf(t, db, root.Id); got != 1000 {
		t.Fatalf("root quota = %d, want 1000", got)
	}
}
