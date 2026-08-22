package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupSubscriptionResetControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousSQLite := common.UsingSQLite
	previousMySQL := common.UsingMySQL
	previousPostgreSQL := common.UsingPostgreSQL

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.SubscriptionPlan{},
		&model.UserSubscription{},
		&model.Log{},
	))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.UsingSQLite = previousSQLite
		common.UsingMySQL = previousMySQL
		common.UsingPostgreSQL = previousPostgreSQL
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func invokeAdminResetUserSubscriptions(
	t *testing.T,
	userId int,
	planId int,
	requestId string,
) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(gin.H{
		"plan_id":            planId,
		"advance_reset_time": true,
	})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/subscription/admin/users/%d/subscriptions/reset", userId),
		bytes.NewReader(body),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	context.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", userId)}}
	context.Set("id", 1)
	context.Set("username", "root")
	context.Set(common.RequestIdKey, requestId)

	AdminResetUserSubscriptionsByPlan(context)
	return recorder
}

func TestAdminResetUserSubscriptionsByPlan_WritesCompleteAudit(t *testing.T) {
	db := setupSubscriptionResetControllerTestDB(t)
	now := time.Now().Unix()
	user := &model.User{
		Id:       711,
		Username: "reset-controller-user",
		Password: "testpass123",
		Email:    "reset-controller-user@test.local",
		Group:    "auto",
		AffCode:  "reset-controller-user",
	}
	require.NoError(t, db.Create(user).Error)
	plan := &model.SubscriptionPlan{
		Id:                      712,
		Title:                   "controller-reset-plan",
		PriceAmount:             1,
		Currency:                "USD",
		DurationUnit:            model.SubscriptionDurationMonth,
		DurationValue:           1,
		Enabled:                 true,
		TotalAmount:             1000,
		UpgradeGroup:            "codex",
		QuotaResetPeriod:        model.SubscriptionResetCustom,
		QuotaResetCustomSeconds: 3600,
	}
	require.NoError(t, db.Create(plan).Error)
	subscription := &model.UserSubscription{
		Id:                713,
		UserId:            user.Id,
		PlanId:            plan.Id,
		AmountTotal:       1000,
		AmountUsed:        1000,
		StartTime:         now - 3600,
		EndTime:           now + 7200,
		Status:            "exhausted",
		Source:            "order",
		ExhaustNotifiedAt: now,
		UpgradeGroup:      "codex",
	}
	require.NoError(t, db.Create(subscription).Error)

	recorder := invokeAdminResetUserSubscriptions(t, user.Id, plan.Id, "reset-request-success")
	assert.Equal(t, http.StatusOK, recorder.Code)

	var response struct {
		Success bool                          `json:"success"`
		Data    model.SubscriptionResetResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.Equal(t, 1, response.Data.ResetCount)

	var audit model.Log
	require.NoError(t, db.Where("request_id = ?", "reset-request-success").First(&audit).Error)
	assert.Equal(t, user.Id, audit.UserId)
	assert.Equal(t, model.LogTypeManage, audit.Type)
	assert.Equal(t, "reset-request-success", audit.RequestId)

	var other struct {
		AdminInfo struct {
			Action           string                        `json:"action"`
			AdminId          int                           `json:"admin_id"`
			RequestId        string                        `json:"request_id"`
			PlanId           int                           `json:"plan_id"`
			ResetCount       int                           `json:"reset_count"`
			AdvanceResetTime bool                          `json:"advance_reset_time"`
			BeforeAfter      []model.SubscriptionResetItem `json:"before_after"`
			FailureReason    string                        `json:"failure_reason"`
		} `json:"admin_info"`
	}
	require.NoError(t, json.Unmarshal([]byte(audit.Other), &other))
	assert.Equal(t, "subscription_usage_reset", other.AdminInfo.Action)
	assert.Equal(t, 1, other.AdminInfo.AdminId)
	assert.Equal(t, "reset-request-success", other.AdminInfo.RequestId)
	assert.Equal(t, plan.Id, other.AdminInfo.PlanId)
	assert.Equal(t, 1, other.AdminInfo.ResetCount)
	assert.True(t, other.AdminInfo.AdvanceResetTime)
	assert.Empty(t, other.AdminInfo.FailureReason)
	require.Len(t, other.AdminInfo.BeforeAfter, 1)
	assert.EqualValues(t, 1000, other.AdminInfo.BeforeAfter[0].AmountUsedBefore)
	assert.EqualValues(t, 0, other.AdminInfo.BeforeAfter[0].AmountUsedAfter)
	assert.Equal(t, "exhausted", other.AdminInfo.BeforeAfter[0].StatusBefore)
	assert.Equal(t, "active", other.AdminInfo.BeforeAfter[0].StatusAfter)
	assert.Equal(t, "codex", other.AdminInfo.BeforeAfter[0].GroupRestoredTo)
}

func TestAdminResetUserSubscriptionsByPlan_AuditsFailureReason(t *testing.T) {
	db := setupSubscriptionResetControllerTestDB(t)
	user := &model.User{
		Id:       721,
		Username: "reset-failure-user",
		Password: "testpass123",
		Group:    "auto",
		AffCode:  "reset-failure-user",
	}
	require.NoError(t, db.Create(user).Error)
	plan := &model.SubscriptionPlan{
		Id:            722,
		Title:         "reset-failure-plan",
		PriceAmount:   1,
		Currency:      "USD",
		DurationUnit:  model.SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   1000,
	}
	require.NoError(t, db.Create(plan).Error)

	recorder := invokeAdminResetUserSubscriptions(t, user.Id, plan.Id, "reset-request-failure")
	assert.Equal(t, http.StatusOK, recorder.Code)

	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)

	var audit model.Log
	require.NoError(t, db.Where("request_id = ?", "reset-request-failure").First(&audit).Error)
	var other map[string]map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(audit.Other), &other))
	assert.Contains(t, other["admin_info"]["failure_reason"], "没有有效")
	assert.Equal(t, float64(0), other["admin_info"]["reset_count"])
}
