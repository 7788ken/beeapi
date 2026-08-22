package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTargetAuthMiddlewareTest(t *testing.T) *model.User {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}))
	previousDB := model.DB
	previousSecret := common.SessionSecret
	previousRedis := common.RedisEnabled
	model.DB = db
	common.SessionSecret = "target-auth-middleware-test-secret"
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB = previousDB
		common.SessionSecret = previousSecret
		common.RedisEnabled = previousRedis
		sqlDB, closeErr := db.DB()
		require.NoError(t, closeErr)
		require.NoError(t, sqlDB.Close())
	})
	user := &model.User{
		Username:    "target-user",
		DisplayName: "Target User",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		Group:       "default",
		AffCode:     "target-" + t.Name(),
		AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)
	return user
}

func TestDashboardAccessTokenPopulatesLiveSessionIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupTargetAuthMiddlewareTest(t)
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "test")
	require.NoError(t, err)

	router := gin.New()
	router.GET("/protected", UserAuth(), func(c *gin.Context) {
		identity, ok := GetSessionAuthIdentity(c)
		require.True(t, ok)
		require.Equal(t, user.Id, identity.UserID)
		require.Equal(t, bundle.Session.SID, identity.SessionID)
		require.Equal(t, int64(1), identity.UserAuthVersion)
		require.Equal(t, int64(1), identity.SessionVersion)
		require.False(t, c.GetBool("use_access_token"))
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+bundle.AccessToken)
	request.Header.Set("New-Api-User", strconv.Itoa(user.Id))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
}

func TestDashboardJWTFailureNeverFallsThroughToPAT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupTargetAuthMiddlewareTest(t)
	bundle, err := service.CreateLoginSession(user.Id, "password", "", "")
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).
		Update("access_token", bundle.AccessToken+"tampered").Error)

	router := gin.New()
	router.GET("/protected", UserAuth(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+bundle.AccessToken+"tampered")
	request.Header.Set("New-Api-User", strconv.Itoa(user.Id))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusUnauthorized, response.Code)
}

func TestTryUserAuthPopulatesCompleteLiveSessionIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupTargetAuthMiddlewareTest(t)
	bundle, err := service.CreateLoginSession(user.Id, "oauth", "", "")
	require.NoError(t, err)

	router := gin.New()
	router.GET("/optional", TryUserAuth(), func(c *gin.Context) {
		identity, ok := GetSessionAuthIdentity(c)
		require.True(t, ok)
		require.Equal(t, bundle.Session.SID, c.GetString("session_id"))
		require.Equal(t, identity.UserAuthVersion, c.GetInt64("auth_version"))
		require.Equal(t, identity.SessionVersion, c.GetInt64("session_version"))
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/optional", nil)
	request.Header.Set("Authorization", "Bearer "+bundle.AccessToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
}

func TestTokenOrUserAuthPopulatesCompleteLiveSessionIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupTargetAuthMiddlewareTest(t)
	bundle, err := service.CreateLoginSession(user.Id, "password", "", "")
	require.NoError(t, err)

	router := gin.New()
	router.GET("/either", TokenOrUserAuth(), func(c *gin.Context) {
		identity, ok := GetSessionAuthIdentity(c)
		require.True(t, ok)
		require.Equal(t, bundle.Session.SID, c.GetString("session_id"))
		require.Equal(t, identity.UserAuthVersion, c.GetInt64("auth_version"))
		require.Equal(t, identity.SessionVersion, c.GetInt64("session_version"))
		require.False(t, c.GetBool("use_access_token"))
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/either", nil)
	request.Header.Set("Authorization", "Bearer "+bundle.AccessToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
}

func TestTryUserAuthRejectsRevokedDashboardIdentityWithoutAnonymousDowngrade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupTargetAuthMiddlewareTest(t)
	bundle, err := service.CreateLoginSession(user.Id, "oauth", "", "")
	require.NoError(t, err)
	_, err = model.RevokeUserSession(user.Id, bundle.Session.SID, "test")
	require.NoError(t, err)

	router := gin.New()
	router.GET("/optional", TryUserAuth(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/optional", nil)
	request.Header.Set("Authorization", "Bearer "+bundle.AccessToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusUnauthorized, response.Code)
}

func TestOptionalDashboardAuthPreservesTemporarySessionDatabaseFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupTargetAuthMiddlewareTest(t)
	bundle, err := service.CreateLoginSession(user.Id, "password", "", "")
	require.NoError(t, err)
	dbErr := errors.New("temporary session lookup failure")
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").
		Register("test:dashboard-auth-query-error", func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "user_sessions" {
				tx.AddError(dbErr)
			}
		}))
	t.Cleanup(func() {
		model.DB.Callback().Query().Remove("test:dashboard-auth-query-error")
	})

	for name, auth := range map[string]gin.HandlerFunc{
		"try-user":      TryUserAuth(),
		"token-or-user": TokenOrUserAuth(),
	} {
		t.Run(name, func(t *testing.T) {
			router := gin.New()
			router.GET("/protected", auth, func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", "Bearer "+bundle.AccessToken)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusInternalServerError, response.Code)
		})
	}
}
