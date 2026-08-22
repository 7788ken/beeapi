package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestAuthenticationSessionRoutesRejectUntrustedOrigins(t *testing.T) {
	gin.SetMode(gin.TestMode)

	previousSecure := common.SessionCookieSecure
	previousTrustedOrigins := common.SessionCookieTrustedURLs
	previousGlobalLimit := common.GlobalApiRateLimitEnable
	previousCriticalLimit := common.CriticalRateLimitEnable
	common.SessionCookieSecure = true
	common.SessionCookieTrustedURLs = []string{"https://panel.example.com"}
	common.GlobalApiRateLimitEnable = false
	common.CriticalRateLimitEnable = false
	t.Cleanup(func() {
		common.SessionCookieSecure = previousSecure
		common.SessionCookieTrustedURLs = previousTrustedOrigins
		common.GlobalApiRateLimitEnable = previousGlobalLimit
		common.CriticalRateLimitEnable = previousCriticalLimit
	})

	engine := gin.New()
	SetApiRouter(engine)

	routes := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/oauth/state"},
		{method: http.MethodPost, path: "/api/oauth/email/bind"},
		{method: http.MethodGet, path: "/api/oauth/wechat"},
		{method: http.MethodPost, path: "/api/oauth/wechat/bind"},
		{method: http.MethodGet, path: "/api/oauth/telegram/login"},
		{method: http.MethodGet, path: "/api/oauth/telegram/bind"},
		{method: http.MethodGet, path: "/api/oauth/github"},
		{method: http.MethodPost, path: "/api/user/register"},
		{method: http.MethodPost, path: "/api/user/login"},
		{method: http.MethodPost, path: "/api/user/login/2fa"},
		{method: http.MethodPost, path: "/api/user/refresh"},
		{method: http.MethodPost, path: "/api/user/logout"},
		{method: http.MethodPost, path: "/api/user/passkey/login/begin"},
		{method: http.MethodPost, path: "/api/user/passkey/login/finish"},
	}

	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			request := httptest.NewRequest(route.method, route.path, strings.NewReader("{}"))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "https://evil.example.com")
			response := httptest.NewRecorder()

			engine.ServeHTTP(response, request)

			assert.Equal(t, http.StatusForbidden, response.Code)
			assert.Contains(t, response.Body.String(), "AUTH_ORIGIN_FORBIDDEN")
		})
	}
}

func TestExternalIdentityBindEntrypointsRequireDashboardIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	previousSecure := common.SessionCookieSecure
	previousTrustedOrigins := common.SessionCookieTrustedURLs
	previousGlobalLimit := common.GlobalApiRateLimitEnable
	previousCriticalLimit := common.CriticalRateLimitEnable
	common.SessionCookieSecure = true
	common.SessionCookieTrustedURLs = []string{"https://panel.example.com"}
	common.GlobalApiRateLimitEnable = false
	common.CriticalRateLimitEnable = false
	t.Cleanup(func() {
		common.SessionCookieSecure = previousSecure
		common.SessionCookieTrustedURLs = previousTrustedOrigins
		common.GlobalApiRateLimitEnable = previousGlobalLimit
		common.CriticalRateLimitEnable = previousCriticalLimit
	})

	engine := gin.New()
	SetApiRouter(engine)
	for _, route := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/oauth/email/bind"},
		{method: http.MethodPost, path: "/api/oauth/wechat/bind"},
		{method: http.MethodGet, path: "/api/oauth/telegram/bind"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			request := httptest.NewRequest(route.method, route.path, strings.NewReader("{}"))
			request.Host = "panel.example.com"
			request.Header.Set("Origin", "https://panel.example.com")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			engine.ServeHTTP(response, request)

			assert.Equal(t, http.StatusUnauthorized, response.Code)
		})
	}
}
