package controller

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type adminDeleteUserResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func setupAdminDeleteUserTestDB(t *testing.T) (*gorm.DB, *model.User) {
	t.Helper()

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousSQLite := common.UsingSQLite
	previousMySQL := common.UsingMySQL
	previousPostgreSQL := common.UsingPostgreSQL

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open admin delete user test db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.BatchUpdateDeleteLedger{},
		// 硬删除用户会连带清理认证凭据，缺表会让整个删除事务失败
		&model.TwoFA{},
		&model.TwoFABackupCode{},
		&model.PasskeyCredential{},
		&model.UserSession{},
		&model.AuthFlow{},
		&model.ExternalIdentityClaim{},
		&model.UserOAuthBinding{},
		// 硬删除用户会把在途工作项置终态，缺表会让整个删除事务失败
		&model.WalletPreConsumeRecord{},
		&model.Task{},
		&model.Midjourney{},
	); err != nil {
		t.Fatalf("migrate admin delete user test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get admin delete user sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	gin.SetMode(gin.TestMode)

	user := &model.User{
		Username: "admin-delete-target",
		Password: "password123",
		AffCode:  "admin-delete-aff",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create admin delete target: %v", err)
	}

	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.UsingSQLite = previousSQLite
		common.UsingMySQL = previousMySQL
		common.UsingPostgreSQL = previousPostgreSQL
		_ = sqlDB.Close()
	})

	return db, user
}

func invokeAdminDeleteUser(
	t *testing.T,
	userID int,
) (*httptest.ResponseRecorder, adminDeleteUserResponse) {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodDelete, "/api/user/"+strconv.Itoa(userID), nil)
	context.Params = gin.Params{{Key: "id", Value: strconv.Itoa(userID)}}
	context.Set("role", common.RoleRootUser)

	DeleteUser(context)

	var response adminDeleteUserResponse
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode admin delete user response %q: %v", recorder.Body.String(), err)
	}
	return recorder, response
}

func TestAdminDeleteUserReturnsSuccessJSONAndDurableLedgers(t *testing.T) {
	db, user := setupAdminDeleteUserTestDB(t)

	recorder, response := invokeAdminDeleteUser(t, user.Id)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin delete status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !response.Success || response.Message != "" {
		t.Fatalf("admin delete response = %#v, want success with empty message", response)
	}
	if err := db.Unscoped().First(&model.User{}, user.Id).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("admin-deleted user lookup error = %v, want record not found", err)
	}

	var ledgers []model.BatchUpdateDeleteLedger
	if err := db.Where("entity_id = ?", user.Id).Order("kind").Find(&ledgers).Error; err != nil {
		t.Fatalf("load admin delete ledgers: %v", err)
	}
	wantKinds := []int{
		model.BatchUpdateTypeUserQuota,
		model.BatchUpdateTypeUsedQuota,
		model.BatchUpdateTypeRequestCount,
	}
	if len(ledgers) != len(wantKinds) {
		t.Fatalf("admin delete ledger count = %d, want %d", len(ledgers), len(wantKinds))
	}
	for index, ledger := range ledgers {
		if ledger.Kind != wantKinds[index] ||
			ledger.EntityId != user.Id ||
			ledger.DeletedAt <= 0 ||
			ledger.DiscardedDelta != 0 ||
			ledger.DiscardedCount != 0 {
			t.Fatalf("admin delete ledger[%d] = %#v", index, ledger)
		}
	}
}

func TestAdminDeleteUserReturnsErrorJSONAndRollsBackOnLedgerConflict(t *testing.T) {
	db, user := setupAdminDeleteUserTestDB(t)
	if err := db.Create(&model.BatchUpdateDeleteLedger{
		Kind:      model.BatchUpdateTypeUserQuota,
		EntityId:  user.Id,
		DeletedAt: 1,
	}).Error; err != nil {
		t.Fatalf("seed conflicting user delete ledger: %v", err)
	}

	recorder, response := invokeAdminDeleteUser(t, user.Id)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin delete error status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if response.Success {
		t.Fatalf("admin delete error response reported success: %#v", response)
	}
	if response.Message == "" {
		t.Fatal("admin delete error response has empty message")
	}
	if err := db.First(&model.User{}, user.Id).Error; err != nil {
		t.Fatalf("user was deleted after ledger conflict: %v", err)
	}
	var ledgerCount int64
	if err := db.Model(&model.BatchUpdateDeleteLedger{}).Where("entity_id = ?", user.Id).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("count ledgers after conflict: %v", err)
	}
	if ledgerCount != 1 {
		t.Fatalf("ledger count after rolled-back delete = %d, want 1", ledgerCount)
	}
}

func TestManageUserDeleteReturnsBeforeUpdateAndCacheRevival(t *testing.T) {
	db, user := setupAdminDeleteUserTestDB(t)
	var userUpdateCalls atomic.Int64
	unexpectedUpdate := errors.New("deleted user must not reach Update")
	callbackName := "test:manage-user-delete-no-update"
	if err := db.Callback().Update().
		Before("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "users" {
				userUpdateCalls.Add(1)
				tx.AddError(unexpectedUpdate)
			}
		}); err != nil {
		t.Fatalf("register user update guard: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Update().Remove(callbackName)
	})

	body, err := common.Marshal(ManageRequest{Id: user.Id, Action: "delete"})
	if err != nil {
		t.Fatalf("marshal manage delete request: %v", err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPut, "/api/user/manage", bytes.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set("id", 1)
	context.Set("role", common.RoleRootUser)

	ManageUser(context)

	var response adminDeleteUserResponse
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode manage delete response %q: %v", recorder.Body.String(), err)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("manage delete status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !response.Success || response.Message != "" {
		t.Fatalf("manage delete response = %#v, want success with empty message", response)
	}
	if got := userUpdateCalls.Load(); got != 0 {
		t.Fatalf("ManageUser delete reached User.Update %d times, want 0", got)
	}
	if err := db.First(&model.User{}, user.Id).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("manage-deleted user scoped lookup error = %v, want record not found", err)
	}
	var deleted model.User
	if err := db.Unscoped().First(&deleted, user.Id).Error; err != nil {
		t.Fatalf("load manage-deleted user: %v", err)
	}
	if !deleted.DeletedAt.Valid {
		t.Fatal("ManageUser delete did not soft-delete user")
	}
	var ledgers []model.BatchUpdateDeleteLedger
	if err := db.Where("entity_id = ?", user.Id).Order("kind").Find(&ledgers).Error; err != nil {
		t.Fatalf("load manage delete ledgers: %v", err)
	}
	wantKinds := []int{
		model.BatchUpdateTypeUserQuota,
		model.BatchUpdateTypeUsedQuota,
		model.BatchUpdateTypeRequestCount,
	}
	if len(ledgers) != len(wantKinds) {
		t.Fatalf("manage delete ledger count = %d, want %d", len(ledgers), len(wantKinds))
	}
	for index, ledger := range ledgers {
		if ledger.Kind != wantKinds[index] ||
			ledger.EntityId != user.Id ||
			ledger.DeletedAt <= 0 ||
			ledger.DiscardedDelta != 0 ||
			ledger.DiscardedCount != 0 {
			t.Fatalf("manage delete ledger[%d] = %#v", index, ledger)
		}
	}
}
