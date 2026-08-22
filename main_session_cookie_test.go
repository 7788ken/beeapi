package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

func TestSessionCookieOptions(t *testing.T) {
	originalSecure := common.SessionCookieSecure
	t.Cleanup(func() {
		common.SessionCookieSecure = originalSecure
	})

	for _, secure := range []bool{false, true} {
		common.SessionCookieSecure = secure
		options := sessionCookieOptions()

		if options.Path != "/" {
			t.Fatalf("Path = %q, want /", options.Path)
		}
		if options.Domain != "" {
			t.Fatalf("Domain = %q, want empty host-only cookie domain", options.Domain)
		}
		if !options.HttpOnly {
			t.Fatal("HttpOnly = false, want true")
		}
		if options.Secure != secure {
			t.Fatalf("Secure = %v, want %v", options.Secure, secure)
		}
		if options.SameSite != http.SameSiteStrictMode {
			t.Fatalf("SameSite = %v, want Strict", options.SameSite)
		}
	}
}

func TestSessionCookieHeader(t *testing.T) {
	originalSecure := common.SessionCookieSecure
	t.Cleanup(func() {
		common.SessionCookieSecure = originalSecure
	})

	for _, secure := range []bool{false, true} {
		common.SessionCookieSecure = secure
		store := cookie.NewStore([]byte(strings.Repeat("a", 32)))
		store.Options(sessionCookieOptions())

		router := gin.New()
		router.Use(sessions.Sessions("session", store))
		router.GET("/", func(c *gin.Context) {
			session := sessions.Default(c)
			session.Set("user_id", 1)
			if err := session.Save(); err != nil {
				c.AbortWithStatus(http.StatusInternalServerError)
				return
			}
			c.Status(http.StatusNoContent)
		})

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
		}

		cookies := recorder.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("cookie count = %d, want 1", len(cookies))
		}
		sessionCookie := cookies[0]
		if sessionCookie.Domain != "" {
			t.Fatalf("Domain = %q, want empty host-only cookie domain", sessionCookie.Domain)
		}
		if !sessionCookie.HttpOnly {
			t.Fatal("HttpOnly = false, want true")
		}
		if sessionCookie.Secure != secure {
			t.Fatalf("Secure = %v, want %v", sessionCookie.Secure, secure)
		}
		if sessionCookie.SameSite != http.SameSiteStrictMode {
			t.Fatalf("SameSite = %v, want Strict", sessionCookie.SameSite)
		}
	}
}
