package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	appi18n "github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAuthEntrypointTest(t *testing.T) *model.User {
	t.Helper()
	require.NoError(t, appi18n.Init())
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared&_pragma=busy_timeout(5000)"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserSession{},
		&model.AuthFlow{},
		&model.ExternalIdentityClaim{},
		&model.TwoFA{},
		&model.TwoFABackupCode{},
		&model.PasskeyCredential{},
		&model.UserOAuthBinding{},
		&model.Log{},
	))
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousSecret := common.SessionSecret
	previousRedis := common.RedisEnabled
	previousCookieSecure := common.SessionCookieSecure
	model.DB = db
	model.LOG_DB = db
	common.SessionSecret = "controller-auth-entrypoint-test-secret"
	common.RedisEnabled = false
	common.SessionCookieSecure = false
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SessionSecret = previousSecret
		common.RedisEnabled = previousRedis
		common.SessionCookieSecure = previousCookieSecure
		sqlDB, closeErr := db.DB()
		require.NoError(t, closeErr)
		require.NoError(t, sqlDB.Close())
	})
	user := &model.User{
		Username:    "entrypoint-user",
		DisplayName: "Entrypoint User",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		Group:       "default",
		AffCode:     "entrypoint",
		AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)
	return user
}

func TestRefreshCookieIsHostOnlyHttpOnlyStrictAndConfiguredSecure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupAuthEntrypointTest(t)
	common.SessionCookieSecure = true
	bundle, err := service.CreateLoginSession(user.Id, "password", "", "")
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/user/login", nil)

	writeAuthBundle(context, bundle)

	cookie := recorder.Header().Get("Set-Cookie")
	require.Contains(t, cookie, "refresh_token=")
	require.Contains(t, cookie, "Path=/api/user")
	require.Contains(t, cookie, "HttpOnly")
	require.Contains(t, cookie, "Secure")
	require.Contains(t, cookie, "SameSite=Strict")
	require.NotContains(t, cookie, "Domain=")
}

func TestOAuthStateEntrypointPersistsBoundTargetIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupAuthEntrypointTest(t)
	router := gin.New()
	router.GET("/state", func(c *gin.Context) {
		setAuthIdentity(c, user)
		GenerateOAuthCode(c)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/state?aff=invite-code", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Success bool   `json:"success"`
		Data    string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	var statePayload map[string]string
	flow, err := service.InspectAuthFlow(
		payload.Data,
		service.AuthFlowPurposeOAuth,
		time.Now(),
		&statePayload,
	)
	require.NoError(t, err)
	require.Equal(t, user.Id, flow.UserID)
	require.Equal(t, "entrypoint-session", flow.SessionID)
	require.Equal(t, "oauth_state", flow.Provider)
	require.Equal(t, "authorize", flow.Intent)
	require.Equal(t, "invite-code", statePayload["aff"])
}

func TestBoundOAuthCallbackRejectsDifferentDashboardSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupAuthEntrypointTest(t)
	state, _, err := service.CreateAuthFlow(service.AuthFlowSpec{
		Purpose:   service.AuthFlowPurposeOAuth,
		Provider:  "oauth_state",
		Intent:    "authorize",
		UserID:    user.Id,
		SessionID: "original-session",
	})
	require.NoError(t, err)

	router := gin.New()
	router.GET("/oauth/:provider", func(c *gin.Context) {
		c.Set("id", user.Id)
		c.Set("session_id", "different-session")
		HandleOAuth(c)
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/oauth/github?state="+url.QueryEscape(state), nil)
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"success":false`)
}

func TestGenericOAuthClaimProviderUsesStableBoundedID(t *testing.T) {
	provider := oauth.NewGenericOAuthProvider(&model.CustomOAuthProvider{
		Id:   987654,
		Slug: strings.Repeat("long-provider-slug-", 4),
	})
	require.Equal(t, "oauth:custom:987654", oauthClaimProvider(provider))
	require.LessOrEqual(t, len(oauthClaimProvider(provider)), 32)
}

func TestCustomOAuthBindingExactReplayIsIdempotent(t *testing.T) {
	user := setupAuthEntrypointTest(t)
	binding := &model.UserOAuthBinding{
		UserId:         user.Id,
		ProviderId:     7,
		ProviderUserId: "subject-1",
	}
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
		return model.EnsureUserOAuthBindingWithTx(tx, binding)
	}))
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
		return model.EnsureUserOAuthBindingWithTx(tx, &model.UserOAuthBinding{
			UserId:         user.Id,
			ProviderId:     7,
			ProviderUserId: "subject-1",
		})
	}))
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return model.EnsureUserOAuthBindingWithTx(tx, &model.UserOAuthBinding{
			UserId:         user.Id,
			ProviderId:     7,
			ProviderUserId: "subject-2",
		})
	})
	require.Error(t, err)

	var count int64
	require.NoError(t, model.DB.Model(&model.UserOAuthBinding{}).
		Where("user_id = ? AND provider_id = ?", user.Id, 7).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestWeChatBindEntrypointClaimsIdentityAtomically(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupAuthEntrypointTest(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "wechat-code", r.URL.Query().Get("code"))
		_, _ = w.Write([]byte(`{"success":true,"data":"wechat-subject"}`))
	}))
	defer upstream.Close()
	previousEnabled := common.WeChatAuthEnabled
	previousAddress := common.WeChatServerAddress
	common.WeChatAuthEnabled = true
	common.WeChatServerAddress = upstream.URL
	t.Cleanup(func() {
		common.WeChatAuthEnabled = previousEnabled
		common.WeChatServerAddress = previousAddress
	})

	router := gin.New()
	router.POST("/bind", func(c *gin.Context) {
		setAuthIdentity(c, user)
		WeChatBind(c)
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(`{"code":"wechat-code"}`))
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"success":true`)

	repeated := httptest.NewRecorder()
	repeatedRequest := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(`{"code":"wechat-code"}`))
	router.ServeHTTP(repeated, repeatedRequest)
	require.Equal(t, http.StatusOK, repeated.Code)
	require.Contains(t, repeated.Body.String(), `"success":true`)

	var reloaded model.User
	require.NoError(t, model.DB.First(&reloaded, user.Id).Error)
	require.Equal(t, "wechat-subject", reloaded.WeChatId)
	var claim model.ExternalIdentityClaim
	require.NoError(t, model.DB.Where("provider = ? AND subject = ?", "wechat", "wechat-subject").
		First(&claim).Error)
	require.Equal(t, user.Id, claim.UserID)
	var claimCount int64
	require.NoError(t, model.DB.Model(&model.ExternalIdentityClaim{}).
		Where("provider = ? AND subject = ?", "wechat", "wechat-subject").
		Count(&claimCount).Error)
	require.Equal(t, int64(1), claimCount)
}

func TestTelegramLoginEntrypointClaimsIdentityAndIssuesTargetSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupAuthEntrypointTest(t)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).
		Update("telegram_id", "telegram-subject").Error)
	previousEnabled := common.TelegramOAuthEnabled
	previousToken := common.TelegramBotToken
	common.TelegramOAuthEnabled = true
	common.TelegramBotToken = "telegram-test-token"
	t.Cleanup(func() {
		common.TelegramOAuthEnabled = previousEnabled
		common.TelegramBotToken = previousToken
	})

	values := url.Values{"id": {"telegram-subject"}, "first_name": {"Test"}}
	values.Set("hash", telegramTestHash(values, common.TelegramBotToken))
	router := gin.New()
	router.GET("/login", TelegramLogin)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/login?"+values.Encode(), nil))
	require.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.NotEmpty(t, payload.Data.AccessToken)
	require.Empty(t, payload.Data.RefreshToken)
	require.Contains(t, response.Header().Get("Set-Cookie"), "refresh_token=")
	require.Contains(t, response.Header().Get("Set-Cookie"), "HttpOnly")
	require.Contains(t, response.Header().Get("Set-Cookie"), "SameSite=Strict")
	var session model.UserSession
	require.NoError(t, model.DB.Where("user_id = ?", user.Id).First(&session).Error)
	require.Equal(t, "telegram", session.LoginMethod)
	var claim model.ExternalIdentityClaim
	require.NoError(t, model.DB.Where("provider = ? AND subject = ?", "telegram", "telegram-subject").
		First(&claim).Error)
	require.Equal(t, user.Id, claim.UserID)
}

func telegramTestHash(values url.Values, token string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "hash" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, values.Get(key)))
	}
	tokenHash := sha256.Sum256([]byte(token))
	mac := hmac.New(sha256.New, tokenHash[:])
	_, _ = mac.Write([]byte(strings.Join(parts, "\n")))
	return fmt.Sprintf("%x", mac.Sum(nil))
}

func setAuthIdentity(c *gin.Context, user *model.User) service.AuthIdentity {
	identity := service.AuthIdentity{
		UserID:          user.Id,
		SessionID:       "entrypoint-session",
		UserAuthVersion: user.AuthVersion,
		SessionVersion:  1,
	}
	c.Set("id", user.Id)
	c.Set("session_id", identity.SessionID)
	c.Set("auth_version", identity.UserAuthVersion)
	c.Set("session_version", identity.SessionVersion)
	return identity
}

func TestDisable2FARequiresScopedSecurityProof(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupAuthEntrypointTest(t)
	twoFA := &model.TwoFA{
		UserId:    user.Id,
		Secret:    "test-secret",
		IsEnabled: true,
	}
	require.NoError(t, model.DB.Create(twoFA).Error)

	router := gin.New()
	router.POST("/disable", func(c *gin.Context) {
		setAuthIdentity(c, user)
		Disable2FA(c)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/disable", nil))
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Contains(t, response.Body.String(), "SECURITY_PROOF_REQUIRED")
	stored, err := model.GetTwoFAByUserId(user.Id)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.True(t, stored.IsEnabled)
}

func TestDisable2FAAcceptsExactSessionProof(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupAuthEntrypointTest(t)
	require.NoError(t, model.DB.Create(&model.TwoFA{
		UserId:    user.Id,
		Secret:    "test-secret",
		IsEnabled: true,
	}).Error)

	router := gin.New()
	router.POST("/disable", func(c *gin.Context) {
		identity := setAuthIdentity(c, user)
		proof, _, err := service.IssueSecurityProof(identity, "2fa", []string{"2fa.disable"})
		require.NoError(t, err)
		c.Request.Header.Set("X-Security-Proof", proof)
		Disable2FA(c)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/disable", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"success":true`)
	stored, err := model.GetTwoFAByUserId(user.Id)
	require.NoError(t, err)
	require.Nil(t, stored)
}

func TestPasswordAnd2FAEntrypointsIssueOneTargetSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupAuthEntrypointTest(t)
	passwordHash, err := common.Password2Hash("Password123")
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).
		Update("password", passwordHash).Error)
	require.NoError(t, model.DB.Create(&model.TwoFA{
		UserId:    user.Id,
		Secret:    "test-secret",
		IsEnabled: true,
	}).Error)
	backupCodes, err := common.GenerateBackupCodes()
	require.NoError(t, err)
	require.NotEmpty(t, backupCodes)
	require.NoError(t, model.CreateBackupCodes(user.Id, backupCodes[:1]))
	previousPasswordLogin := common.PasswordLoginEnabled
	common.PasswordLoginEnabled = true
	t.Cleanup(func() {
		common.PasswordLoginEnabled = previousPasswordLogin
	})

	router := gin.New()
	router.POST("/login", Login)
	router.POST("/login/2fa", Verify2FALogin)
	loginResponse := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(
		http.MethodPost,
		"/login",
		strings.NewReader(`{"username":"entrypoint-user","password":"Password123"}`),
	)
	loginRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(loginResponse, loginRequest)
	require.Equal(t, http.StatusOK, loginResponse.Code)
	var firstStep struct {
		Success bool `json:"success"`
		Data    struct {
			Require2FA bool   `json:"require_2fa"`
			FlowToken  string `json:"flow_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(loginResponse.Body.Bytes(), &firstStep))
	require.True(t, firstStep.Success)
	require.True(t, firstStep.Data.Require2FA)
	require.NotEmpty(t, firstStep.Data.FlowToken)

	verifyResponse := httptest.NewRecorder()
	verifyBody := fmt.Sprintf(
		`{"code":%q,"flow_token":%q}`,
		backupCodes[0],
		firstStep.Data.FlowToken,
	)
	verifyRequest := httptest.NewRequest(http.MethodPost, "/login/2fa", strings.NewReader(verifyBody))
	verifyRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(verifyResponse, verifyRequest)
	require.Equal(t, http.StatusOK, verifyResponse.Code)
	var completed struct {
		Success bool `json:"success"`
		Data    struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(verifyResponse.Body.Bytes(), &completed))
	require.True(t, completed.Success)
	require.NotEmpty(t, completed.Data.AccessToken)
	require.Empty(t, completed.Data.RefreshToken)
	require.Contains(t, verifyResponse.Header().Get("Set-Cookie"), "refresh_token=")
	require.Contains(t, verifyResponse.Header().Get("Set-Cookie"), "HttpOnly")
	require.Contains(t, verifyResponse.Header().Get("Set-Cookie"), "SameSite=Strict")

	_, err = service.InspectAuthFlow(
		firstStep.Data.FlowToken,
		service.AuthFlowPurposeTwoFA,
		time.Now(),
		nil,
	)
	require.ErrorIs(t, err, service.ErrAuthFlowConsumed)
	var activeSessions int64
	require.NoError(t, model.DB.Model(&model.UserSession{}).
		Where("user_id = ? AND status = ? AND revoked_at = 0",
			user.Id, model.UserSessionStatusActive).
		Count(&activeSessions).Error)
	require.Equal(t, int64(1), activeSessions)
}

func TestRegisterWithInvitationIssuesTargetSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	inviter := setupAuthEntrypointTest(t)
	previousRegister := common.RegisterEnabled
	previousPasswordRegister := common.PasswordRegisterEnabled
	previousEmailVerification := common.EmailVerificationEnabled
	previousDefaultToken := constant.GenerateDefaultToken
	common.RegisterEnabled = true
	common.PasswordRegisterEnabled = true
	common.EmailVerificationEnabled = false
	constant.GenerateDefaultToken = false
	t.Cleanup(func() {
		common.RegisterEnabled = previousRegister
		common.PasswordRegisterEnabled = previousPasswordRegister
		common.EmailVerificationEnabled = previousEmailVerification
		constant.GenerateDefaultToken = previousDefaultToken
	})

	router := gin.New()
	router.POST("/register", Register)
	body := `{"username":"invitee","password":"Password123","aff_code":"` + inviter.AffCode + `"}`
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			ID           int    `json:"id"`
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.NotEmpty(t, payload.Data.AccessToken)
	require.Empty(t, payload.Data.RefreshToken)
	require.Contains(t, response.Header().Get("Set-Cookie"), "refresh_token=")
	require.Contains(t, response.Header().Get("Set-Cookie"), "HttpOnly")
	require.Contains(t, response.Header().Get("Set-Cookie"), "SameSite=Strict")

	var invitee model.User
	require.NoError(t, model.DB.First(&invitee, payload.Data.ID).Error)
	require.Equal(t, inviter.Id, invitee.InviterId)
	var registrationFlow model.AuthFlow
	require.NoError(t, model.DB.Where(
		"purpose = ? AND provider = ? AND intent = ? AND user_id = ?",
		service.AuthFlowPurposeRegistration, "password", "register", invitee.Id,
	).First(&registrationFlow).Error)
	require.NotNil(t, registrationFlow.ConsumedAt)
	require.True(t, registrationFlow.ExpiresAt.After(registrationFlow.CreatedAt))
	var registrationPayload struct {
		InviterID int    `json:"inviter_id"`
		AffCode   string `json:"aff_code"`
	}
	require.NoError(t, common.UnmarshalJsonStr(registrationFlow.Payload, &registrationPayload))
	require.Equal(t, inviter.Id, registrationPayload.InviterID)
	require.Equal(t, inviter.AffCode, registrationPayload.AffCode)
	var sessions int64
	require.NoError(t, model.DB.Model(&model.UserSession{}).
		Where("user_id = ? AND status = ? AND revoked_at = 0",
			invitee.Id, model.UserSessionStatusActive).
		Count(&sessions).Error)
	require.Equal(t, int64(1), sessions)
}

func TestRegisterEntrypointRollsBackUserAndFlowOnDatabaseFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupAuthEntrypointTest(t)
	previousRegister := common.RegisterEnabled
	previousPasswordRegister := common.PasswordRegisterEnabled
	previousEmailVerification := common.EmailVerificationEnabled
	previousDefaultToken := constant.GenerateDefaultToken
	common.RegisterEnabled = true
	common.PasswordRegisterEnabled = true
	common.EmailVerificationEnabled = false
	constant.GenerateDefaultToken = false
	t.Cleanup(func() {
		common.RegisterEnabled = previousRegister
		common.PasswordRegisterEnabled = previousPasswordRegister
		common.EmailVerificationEnabled = previousEmailVerification
		constant.GenerateDefaultToken = previousDefaultToken
	})
	dbErr := errors.New("auth flow insert failed")
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register(
		"test:registration-auth-flow-error",
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "auth_flows" {
				tx.AddError(dbErr)
			}
		},
	))

	router := gin.New()
	router.POST("/register", Register)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(
		`{"username":"rolled-back","password":"Password123"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"success":false`)

	var users, flows int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("username = ?", "rolled-back").Count(&users).Error)
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("purpose = ?", service.AuthFlowPurposeRegistration).Count(&flows).Error)
	require.Zero(t, users)
	require.Zero(t, flows)
}

func TestRefreshDashboardSessionUsesTerminalAndTemporaryHTTPStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupAuthEntrypointTest(t)
	router := gin.New()
	router.POST("/refresh", RefreshDashboardSession)

	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, httptest.NewRequest(http.MethodPost, "/refresh", nil))
	require.Equal(t, http.StatusUnauthorized, missing.Code)

	invalid := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	invalidRequest.AddCookie(&http.Cookie{Name: "refresh_token", Value: "invalid"})
	router.ServeHTTP(invalid, invalidRequest)
	require.Equal(t, http.StatusUnauthorized, invalid.Code)

	bundle, err := service.CreateLoginSession(user.Id, "password", "", "")
	require.NoError(t, err)

	bodyOnly := httptest.NewRecorder()
	bodyOnlyRequest := httptest.NewRequest(
		http.MethodPost,
		"/refresh",
		strings.NewReader(fmt.Sprintf(`{"refresh_token":%q}`, bundle.RefreshToken)),
	)
	bodyOnlyRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(bodyOnly, bodyOnlyRequest)
	require.Equal(t, http.StatusUnauthorized, bodyOnly.Code)

	dbErr := errors.New("temporary session database failure")
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(
		"test:refresh-session-database-error",
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "user_sessions" {
				tx.AddError(dbErr)
			}
		},
	))
	t.Cleanup(func() {
		model.DB.Callback().Query().Remove("test:refresh-session-database-error")
	})

	temporary := httptest.NewRecorder()
	temporaryRequest := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	temporaryRequest.AddCookie(&http.Cookie{
		Name:  "refresh_token",
		Value: bundle.RefreshToken,
	})
	router.ServeHTTP(temporary, temporaryRequest)
	require.Equal(t, http.StatusInternalServerError, temporary.Code)
}

func TestLogoutOnlyAcceptsRefreshCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupAuthEntrypointTest(t)
	bundle, err := service.CreateLoginSession(user.Id, "password", "", "")
	require.NoError(t, err)

	router := gin.New()
	router.POST("/logout", Logout)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/logout",
		strings.NewReader(fmt.Sprintf(`{"refresh_token":%q}`, bundle.RefreshToken)),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)

	_, err = service.RefreshLoginSession(bundle.RefreshToken)
	require.NoError(t, err)
}

func TestPasskeyRegisterFinishCommitConsumesOnce(t *testing.T) {
	user := setupAuthEntrypointTest(t)
	token, _, err := service.CreateAuthFlow(service.AuthFlowSpec{
		Purpose:  service.AuthFlowPurposeRegistration,
		Provider: "passkey",
		Intent:   "register",
		UserID:   user.Id,
	})
	require.NoError(t, err)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			errs <- commitPasskeyRegistration(
				token,
				user.Id,
				testPasskeyCredential(user.Id, fmt.Sprintf("credential-%d", index)),
				time.Now(),
			)
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	var succeeded, consumed int
	for commitErr := range errs {
		switch {
		case commitErr == nil:
			succeeded++
		case errors.Is(commitErr, service.ErrAuthFlowConsumed):
			consumed++
		default:
			require.NoError(t, commitErr)
		}
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, consumed)
	var credentials int64
	require.NoError(t, model.DB.Model(&model.PasskeyCredential{}).
		Where("user_id = ?", user.Id).Count(&credentials).Error)
	require.Equal(t, int64(1), credentials)
	var reloaded model.User
	require.NoError(t, model.DB.First(&reloaded, user.Id).Error)
	require.Equal(t, int64(2), reloaded.AuthVersion)
}

func TestPasskeyRegisterFinishCommitRejectsExpiredAndReplay(t *testing.T) {
	user := setupAuthEntrypointTest(t)
	expiredToken, _, err := service.CreateAuthFlow(service.AuthFlowSpec{
		Purpose:  service.AuthFlowPurposeRegistration,
		Provider: "passkey",
		Intent:   "register",
		UserID:   user.Id,
		TTL:      time.Second,
	})
	require.NoError(t, err)
	err = commitPasskeyRegistration(
		expiredToken,
		user.Id,
		testPasskeyCredential(user.Id, "expired"),
		time.Now().Add(2*time.Second),
	)
	require.ErrorIs(t, err, service.ErrAuthFlowExpired)

	token, _, err := service.CreateAuthFlow(service.AuthFlowSpec{
		Purpose:  service.AuthFlowPurposeRegistration,
		Provider: "passkey",
		Intent:   "register",
		UserID:   user.Id,
	})
	require.NoError(t, err)
	require.NoError(t, commitPasskeyRegistration(
		token,
		user.Id,
		testPasskeyCredential(user.Id, "winner"),
		time.Now(),
	))
	err = commitPasskeyRegistration(
		token,
		user.Id,
		testPasskeyCredential(user.Id, "replay"),
		time.Now(),
	)
	require.ErrorIs(t, err, service.ErrAuthFlowConsumed)

	var stored model.PasskeyCredential
	require.NoError(t, model.DB.Where("user_id = ?", user.Id).First(&stored).Error)
	require.Equal(t, "winner", stored.CredentialID)
}

func TestPasskeyRegisterFinishCommitRollsBackFlowOnCredentialFailure(t *testing.T) {
	user := setupAuthEntrypointTest(t)
	token, _, err := service.CreateAuthFlow(service.AuthFlowSpec{
		Purpose:  service.AuthFlowPurposeRegistration,
		Provider: "passkey",
		Intent:   "register",
		UserID:   user.Id,
	})
	require.NoError(t, err)
	dbErr := errors.New("passkey insert failed")
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register(
		"test:passkey-credential-error",
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "passkey_credentials" {
				tx.AddError(dbErr)
			}
		},
	))
	t.Cleanup(func() {
		model.DB.Callback().Create().Remove("test:passkey-credential-error")
	})

	err = commitPasskeyRegistration(
		token,
		user.Id,
		testPasskeyCredential(user.Id, "rollback"),
		time.Now(),
	)
	require.Error(t, err)
	_, err = service.InspectAuthFlow(
		token,
		service.AuthFlowPurposeRegistration,
		time.Now(),
		nil,
	)
	require.NoError(t, err)
	var credentials int64
	require.NoError(t, model.DB.Model(&model.PasskeyCredential{}).
		Where("user_id = ?", user.Id).Count(&credentials).Error)
	require.Zero(t, credentials)
}

func testPasskeyCredential(userID int, credentialID string) *model.PasskeyCredential {
	return &model.PasskeyCredential{
		UserID:       userID,
		CredentialID: credentialID,
		PublicKey:    "test-public-key",
	}
}

func TestPasskeyDeleteAndTwoFAMutationsBumpAuthVersion(t *testing.T) {
	user := setupAuthEntrypointTest(t)
	require.NoError(t, model.DB.Create(testPasskeyCredential(user.Id, "delete-me")).Error)
	require.NoError(t, commitPasskeyDeletion(user.Id))
	var credentials int64
	require.NoError(t, model.DB.Model(&model.PasskeyCredential{}).
		Where("user_id = ?", user.Id).Count(&credentials).Error)
	require.Zero(t, credentials)

	twoFA := &model.TwoFA{
		UserId: user.Id,
		Secret: "test-secret",
	}
	require.NoError(t, model.DB.Create(twoFA).Error)
	require.NoError(t, twoFA.Enable())
	require.NoError(t, model.DisableTwoFA(user.Id))

	var reloaded model.User
	require.NoError(t, model.DB.First(&reloaded, user.Id).Error)
	require.Equal(t, int64(4), reloaded.AuthVersion)
}

func TestDeleteDisabledTwoFASetupDoesNotUseDisableFlow(t *testing.T) {
	user := setupAuthEntrypointTest(t)
	twoFA := &model.TwoFA{
		UserId:    user.Id,
		Secret:    "pending-secret",
		IsEnabled: false,
	}
	require.NoError(t, model.DB.Create(twoFA).Error)
	require.NoError(t, twoFA.Delete())

	var records int64
	require.NoError(t, model.DB.Model(&model.TwoFA{}).
		Where("user_id = ?", user.Id).Count(&records).Error)
	require.Zero(t, records)

	var reloaded model.User
	require.NoError(t, model.DB.First(&reloaded, user.Id).Error)
	require.Equal(t, int64(1), reloaded.AuthVersion)
}
