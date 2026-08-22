package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestAnonymousPostRoutesRejectOversizeBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalLimit := constant.AnonymousRequestBodyLimitKB
	originalGlobalLimit := common.GlobalApiRateLimitEnable
	originalCriticalLimit := common.CriticalRateLimitEnable
	constant.AnonymousRequestBodyLimitKB = 1
	common.GlobalApiRateLimitEnable = false
	common.CriticalRateLimitEnable = false
	t.Cleanup(func() {
		constant.AnonymousRequestBodyLimitKB = originalLimit
		common.GlobalApiRateLimitEnable = originalGlobalLimit
		common.CriticalRateLimitEnable = originalCriticalLimit
	})

	engine := gin.New()
	SetApiRouter(engine)

	paths := []string{
		"/api/setup",
		"/api/user/reset",
		"/api/oauth/email/bind",
		"/api/oauth/wechat/bind",
		"/api/stripe/webhook",
		"/api/creem/webhook",
		"/api/waffo/webhook",
		"/api/waffo-pancake/webhook",
		"/api/cryptomus/webhook",
		"/api/sfpay/notify",
		"/api/user/register",
		"/api/user/login",
		"/api/user/login/2fa",
		"/api/user/passkey/login/begin",
		"/api/user/passkey/login/finish",
		"/api/user/epay/notify",
		"/api/subscription/epay/notify",
		"/api/subscription/epay/return",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				path,
				strings.NewReader(strings.Repeat("x", 1025)),
			)

			engine.ServeHTTP(recorder, request)

			assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
		})
	}
}
