package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoContentAuthenticationOriginBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)

	previousSecure := common.SessionCookieSecure
	previousTrustedOrigins := common.SessionCookieTrustedURLs
	common.SessionCookieSecure = true
	common.SessionCookieTrustedURLs = []string{"https://panel.example.com"}
	t.Cleanup(func() {
		common.SessionCookieSecure = previousSecure
		common.SessionCookieTrustedURLs = previousTrustedOrigins
	})

	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte(strings.Repeat("d", 32)))))
	engine.GET("/seed", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("id", 9)
		session.Set("status", common.UserStatusEnabled)
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	SetVideoRouter(engine)

	seedResponse := httptest.NewRecorder()
	engine.ServeHTTP(seedResponse, httptest.NewRequest(http.MethodGet, "/seed", nil))
	require.Equal(t, http.StatusNoContent, seedResponse.Code)
	require.Len(t, seedResponse.Result().Cookies(), 1)

	t.Run("legacy session cookie is not an authentication source", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "https://panel.example.com/v1/videos/task-1/content", nil)
		request.Host = "panel.example.com"
		request.Header.Set("Origin", "https://evil.example.com")
		request.AddCookie(seedResponse.Result().Cookies()[0])
		response := httptest.NewRecorder()

		engine.ServeHTTP(response, request)

		assert.Equal(t, http.StatusUnauthorized, response.Code)
		assert.NotContains(t, response.Body.String(), "AUTH_ORIGIN_FORBIDDEN")
	})

	t.Run("token fallback does not require browser origin", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "https://panel.example.com/v1/videos/task-1/content", nil)
		response := httptest.NewRecorder()

		engine.ServeHTTP(response, request)

		assert.Equal(t, http.StatusUnauthorized, response.Code)
		assert.NotContains(t, response.Body.String(), "AUTH_ORIGIN_FORBIDDEN")
	})
}
