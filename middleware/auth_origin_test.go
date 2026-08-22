package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func withSessionCookieOriginSettings(t *testing.T, secure bool, trustedOrigins []string) {
	t.Helper()
	previousSecure := common.SessionCookieSecure
	previousTrustedOrigins := common.SessionCookieTrustedURLs
	common.SessionCookieSecure = secure
	common.SessionCookieTrustedURLs = trustedOrigins
	t.Cleanup(func() {
		common.SessionCookieSecure = previousSecure
		common.SessionCookieTrustedURLs = previousTrustedOrigins
	})
}

func runOriginGuardRequest(t *testing.T, origin, referer string, configure func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	router := gin.New()
	router.GET("/guarded", SessionCookieOriginGuard(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "https://panel.example.com/guarded", nil)
	request.Host = "panel.example.com"
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	if configure != nil {
		configure(request)
	}

	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestSessionCookieOriginGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withSessionCookieOriginSettings(t, true, []string{"https://trusted.example.com"})

	tests := []struct {
		name      string
		origin    string
		referer   string
		configure func(*http.Request)
		expected  int
	}{
		{name: "same origin", origin: "https://panel.example.com", expected: http.StatusNoContent},
		{name: "trusted exact origin", origin: "https://trusted.example.com", expected: http.StatusNoContent},
		{name: "referer fallback", referer: "https://panel.example.com/profile", expected: http.StatusNoContent},
		{name: "missing both", expected: http.StatusForbidden},
		{name: "null origin", origin: "null", expected: http.StatusForbidden},
		{name: "suffix attack", origin: "https://trusted.example.com.evil.test", expected: http.StatusForbidden},
		{name: "scheme mismatch", origin: "http://panel.example.com", expected: http.StatusForbidden},
		{name: "path in origin", origin: "https://panel.example.com/profile", expected: http.StatusForbidden},
		{name: "comma separated origins", origin: "https://panel.example.com,https://trusted.example.com", expected: http.StatusForbidden},
		{
			name:   "multiple origin headers",
			origin: "https://panel.example.com",
			configure: func(request *http.Request) {
				request.Header.Add("Origin", "https://trusted.example.com")
			},
			expected: http.StatusForbidden,
		},
		{
			name:    "multiple referer headers",
			referer: "https://panel.example.com/profile",
			configure: func(request *http.Request) {
				request.Header.Add("Referer", "https://trusted.example.com/profile")
			},
			expected: http.StatusForbidden,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := runOriginGuardRequest(t, test.origin, test.referer, test.configure)
			assert.Equal(t, test.expected, response.Code)
			assert.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
			if test.expected == http.StatusForbidden {
				assert.Contains(t, response.Body.String(), "AUTH_ORIGIN_FORBIDDEN")
			}
		})
	}
}

func TestSessionCookieOriginGuardDevelopmentCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withSessionCookieOriginSettings(t, false, nil)

	response := runOriginGuardRequest(t, "http://localhost:3001", "", nil)

	assert.Equal(t, http.StatusNoContent, response.Code)
}

func TestSessionCookieOriginGuardDoesNotTrustForwardedProto(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withSessionCookieOriginSettings(t, true, nil)

	router := gin.New()
	router.POST("/guarded", SessionCookieOriginGuard(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "http://panel.example.com/guarded", nil)
	request.Host = "panel.example.com"
	request.Header.Set("Origin", "https://panel.example.com")
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestSessionCookieOriginGuardAllowsConfiguredOriginBehindTLSProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withSessionCookieOriginSettings(t, true, []string{"https://panel.example.com"})

	router := gin.New()
	router.POST("/guarded", SessionCookieOriginGuard(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "http://panel.example.com/guarded", nil)
	request.Host = "panel.example.com"
	request.Header.Set("Origin", "https://panel.example.com")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
}

func newSessionAuthenticatedRouter(t *testing.T) (*gin.Engine, *http.Cookie) {
	t.Helper()
	store := cookie.NewStore([]byte(strings.Repeat("a", 32)))
	router := gin.New()
	router.Use(sessions.Sessions("session", store))
	router.GET("/seed", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("id", 7)
		session.Set("username", "session-user")
		session.Set("role", common.RoleCommonUser)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	router.GET("/protected", UserAuth(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.GET("/mixed", TokenOrUserAuth(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	seedResponse := httptest.NewRecorder()
	router.ServeHTTP(seedResponse, httptest.NewRequest(http.MethodGet, "/seed", nil))
	require.Equal(t, http.StatusNoContent, seedResponse.Code)
	cookies := seedResponse.Result().Cookies()
	require.Len(t, cookies, 1)
	return router, cookies[0]
}

func TestLegacySessionCookieCannotAuthenticateDashboard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withSessionCookieOriginSettings(t, true, []string{"https://trusted.example.com"})
	router, sessionCookie := newSessionAuthenticatedRouter(t)

	tests := []struct {
		name     string
		origin   string
		referer  string
		expected int
	}{
		{name: "same origin", origin: "https://panel.example.com", expected: http.StatusUnauthorized},
		{name: "trusted origin", origin: "https://trusted.example.com", expected: http.StatusUnauthorized},
		{name: "referer fallback", referer: "https://panel.example.com/profile", expected: http.StatusUnauthorized},
		{name: "missing origin and referer", expected: http.StatusUnauthorized},
		{name: "untrusted origin", origin: "https://evil.example.com", expected: http.StatusUnauthorized},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://panel.example.com/protected", nil)
			request.Host = "panel.example.com"
			request.Header.Set("New-Api-User", "7")
			request.AddCookie(sessionCookie)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.referer != "" {
				request.Header.Set("Referer", test.referer)
			}
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			assert.Equal(t, test.expected, response.Code)
		})
	}
}

func TestLegacySessionCookieCannotAuthenticateInDevelopment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withSessionCookieOriginSettings(t, false, nil)
	router, sessionCookie := newSessionAuthenticatedRouter(t)

	request := httptest.NewRequest(http.MethodGet, "http://localhost/protected", nil)
	request.Header.Set("New-Api-User", "7")
	request.AddCookie(sessionCookie)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusUnauthorized, response.Code)
}

func TestTokenOrUserAuthRejectsLegacySessionCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withSessionCookieOriginSettings(t, true, []string{"https://trusted.example.com"})
	router, sessionCookie := newSessionAuthenticatedRouter(t)

	tests := []struct {
		name     string
		origin   string
		expected int
	}{
		{name: "same origin", origin: "https://panel.example.com", expected: http.StatusUnauthorized},
		{name: "trusted origin", origin: "https://trusted.example.com", expected: http.StatusUnauthorized},
		{name: "missing origin", expected: http.StatusUnauthorized},
		{name: "untrusted origin", origin: "https://evil.example.com", expected: http.StatusUnauthorized},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://panel.example.com/mixed", nil)
			request.Host = "panel.example.com"
			request.AddCookie(sessionCookie)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			assert.Equal(t, test.expected, response.Code)
		})
	}
}

func TestTokenOrUserAuthRejectsLegacySessionCookieInDevelopment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withSessionCookieOriginSettings(t, false, nil)
	router, sessionCookie := newSessionAuthenticatedRouter(t)

	request := httptest.NewRequest(http.MethodGet, "http://localhost/mixed", nil)
	request.AddCookie(sessionCookie)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusUnauthorized, response.Code)
}

func TestAccessTokenAuthenticationDoesNotRequireBrowserOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withSessionCookieOriginSettings(t, true, []string{"https://panel.example.com"})

	previousDB := model.DB
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})

	accessToken := "dashboard-access-token"
	user := &model.User{
		Id:          7,
		Username:    "token-user",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AccessToken: &accessToken,
	}
	require.NoError(t, db.Create(user).Error)

	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte(strings.Repeat("b", 32)))))
	router.GET("/protected", UserAuth(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "https://panel.example.com/protected", nil)
	request.Header.Set("Authorization", "Bearer dashboard-access-token")
	request.Header.Set("New-Api-User", "7")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
}

func TestTokenOrUserAuthTokenFallbackDoesNotRequireBrowserOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withSessionCookieOriginSettings(t, true, []string{"https://panel.example.com"})

	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte(strings.Repeat("c", 32)))))
	router.GET("/mixed", TokenOrUserAuth(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "https://panel.example.com/mixed", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.NotContains(t, response.Body.String(), "AUTH_ORIGIN_FORBIDDEN")
}
