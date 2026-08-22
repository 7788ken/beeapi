package controller

import (
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

func setupAdminResetPasskeyTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	previousDB := model.DB
	previousSQLite := common.UsingSQLite
	previousMySQL := common.UsingMySQL
	previousPostgreSQL := common.UsingPostgreSQL

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.PasskeyCredential{}))
	model.DB = db
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false

	t.Cleanup(func() {
		model.DB = previousDB
		common.UsingSQLite = previousSQLite
		common.UsingMySQL = previousMySQL
		common.UsingPostgreSQL = previousPostgreSQL
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func TestAdminResetPasskeyEnforcesRoleHierarchy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupAdminResetPasskeyTestDB(t)

	tests := []struct {
		name       string
		actorRole  int
		targetRole int
		allowed    bool
	}{
		{
			name:       "admin resets common user",
			actorRole:  common.RoleAdminUser,
			targetRole: common.RoleCommonUser,
			allowed:    true,
		},
		{
			name:       "admin cannot reset peer",
			actorRole:  common.RoleAdminUser,
			targetRole: common.RoleAdminUser,
			allowed:    false,
		},
		{
			name:       "admin cannot reset root",
			actorRole:  common.RoleAdminUser,
			targetRole: common.RoleRootUser,
			allowed:    false,
		},
		{
			name:       "root can reset root",
			actorRole:  common.RoleRootUser,
			targetRole: common.RoleRootUser,
			allowed:    true,
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := &model.User{
				Id:       100 + index,
				Username: fmt.Sprintf("passkey-target-%d", index),
				Password: "password-placeholder",
				AffCode:  fmt.Sprintf("passkey-%d", index),
				Role:     test.targetRole,
				Status:   common.UserStatusEnabled,
			}
			require.NoError(t, db.Create(target).Error)
			require.NoError(t, db.Create(&model.PasskeyCredential{
				UserID:       target.Id,
				CredentialID: fmt.Sprintf("credential-%d", index),
				PublicKey:    fmt.Sprintf("public-key-%d", index),
			}).Error)

			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
			context.Params = gin.Params{{Key: "id", Value: strconv.Itoa(target.Id)}}
			context.Set("role", test.actorRole)

			AdminResetPasskey(context)

			require.Equal(t, http.StatusOK, recorder.Code)
			var response struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, test.allowed, response.Success)

			_, err := model.GetPasskeyByUserID(target.Id)
			if test.allowed {
				assert.ErrorIs(t, err, model.ErrPasskeyNotFound)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
