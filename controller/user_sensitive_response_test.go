package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupUserSensitiveResponseTestDB(t *testing.T) (*gorm.DB, *model.User) {
	t.Helper()

	previousDB := model.DB
	previousRedisEnabled := common.RedisEnabled
	previousSQLite := common.UsingSQLite
	previousMySQL := common.UsingMySQL
	previousPostgreSQL := common.UsingPostgreSQL

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	common.RedisEnabled = false
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false

	accessToken := "sensitive-personal-access-token"
	user := &model.User{
		Id:          901,
		Username:    "sensitive-user",
		Password:    "sensitive-password-hash",
		AccessToken: &accessToken,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(user).Error)

	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedisEnabled
		common.UsingSQLite = previousSQLite
		common.UsingMySQL = previousMySQL
		common.UsingPostgreSQL = previousPostgreSQL
		_ = sqlDB.Close()
	})

	return db, user
}

func assertUserResponseHasNoSensitiveFields(t *testing.T, body string) {
	t.Helper()
	assert.NotContains(t, body, `"password"`)
	assert.NotContains(t, body, `"access_token"`)
	assert.NotContains(t, body, "sensitive-password-hash")
	assert.NotContains(t, body, "sensitive-personal-access-token")
}

func TestAdminUserResponsesExcludeSensitiveFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, user := setupUserSensitiveResponseTestDB(t)

	tests := []struct {
		name    string
		target  string
		handler gin.HandlerFunc
		setup   func(*gin.Context)
	}{
		{
			name:    "default list",
			target:  "/api/user/?p=1&page_size=10",
			handler: GetAllUsers,
		},
		{
			name:    "rpm sorted list",
			target:  "/api/user/?p=1&page_size=10&order_by=rpm_24h&order=desc",
			handler: GetAllUsers,
		},
		{
			name:    "search",
			target:  "/api/user/search?keyword=sensitive-user&p=1&page_size=10",
			handler: SearchUsers,
		},
		{
			name:    "detail",
			target:  "/api/user/" + strconv.Itoa(user.Id),
			handler: GetUser,
			setup: func(c *gin.Context) {
				c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(user.Id)}}
				c.Set("role", common.RoleRootUser)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodGet, test.target, nil)
			if test.setup != nil {
				test.setup(context)
			}

			test.handler(context)

			require.Equal(t, http.StatusOK, recorder.Code)
			assertUserResponseHasNoSensitiveFields(t, recorder.Body.String())
		})
	}
}

func TestRPMUserQueryDoesNotLoadSensitiveColumns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, _ = setupUserSensitiveResponseTestDB(t)

	users, total, err := listUsersOrderedByRealtimeRPM(
		context.Background(),
		&common.PageInfo{Page: 1, PageSize: 10},
		"desc",
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, users, 1)
	assert.Empty(t, users[0].Password)
	assert.Nil(t, users[0].AccessToken)
}
