package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type securityProofErrorResponse struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Scope   string `json:"scope"`
}

func useSecurityProofTestSecret(t *testing.T) {
	t.Helper()
	previous := common.SessionSecret
	common.SessionSecret = "security-proof-test-session-secret"
	t.Cleanup(func() {
		common.SessionSecret = previous
	})
}

func securityProofTestIdentity() service.AuthIdentity {
	return service.AuthIdentity{
		UserID:          42,
		SessionID:       "session-1",
		UserAuthVersion: 3,
		SessionVersion:  2,
	}
}

func requireSecurityProofErrorCode(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	code string,
	scope string,
) {
	t.Helper()
	require.Equal(t, http.StatusForbidden, recorder.Code)
	var response securityProofErrorResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.False(t, response.Success)
	require.Equal(t, code, response.Code)
	require.Equal(t, scope, response.Scope)
}

func TestRequireSecurityProofErrorContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useSecurityProofTestSecret(t)
	identity := securityProofTestIdentity()
	proof, _, err := service.IssueSecurityProof(
		identity,
		"2fa",
		[]string{"channel.key.read"},
	)
	require.NoError(t, err)

	tests := []struct {
		name           string
		identity       service.AuthIdentity
		proof          string
		requiredScope  string
		allowedMethods []string
		wantCode       string
	}{
		{
			name:           "missing live session identity",
			proof:          proof,
			requiredScope:  "channel.key.read",
			allowedMethods: []string{"2fa"},
			wantCode:       "SECURITY_PROOF_INVALID",
		},
		{
			name:           "missing proof",
			identity:       identity,
			requiredScope:  "channel.key.read",
			allowedMethods: []string{"2fa"},
			wantCode:       "SECURITY_PROOF_REQUIRED",
		},
		{
			name:           "scope mismatch",
			identity:       identity,
			proof:          proof,
			requiredScope:  "passkey.delete",
			allowedMethods: []string{"2fa"},
			wantCode:       "SECURITY_PROOF_SCOPE_MISMATCH",
		},
		{
			name:           "method mismatch",
			identity:       identity,
			proof:          proof,
			requiredScope:  "channel.key.read",
			allowedMethods: []string{"passkey"},
			wantCode:       "SECURITY_PROOF_METHOD_MISMATCH",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			if test.identity.UserID > 0 {
				context.Set(authIdentityContextKey, test.identity)
			}
			if test.proof != "" {
				context.Request.Header.Set("X-Security-Proof", test.proof)
			}
			require.False(t, RequireSecurityProof(
				context,
				test.requiredScope,
				test.allowedMethods,
			))
			requireSecurityProofErrorCode(t, recorder, test.wantCode, test.requiredScope)
			require.True(t, context.IsAborted())
		})
	}
}

func TestSecurityProofRequiredAllowsExactSessionBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useSecurityProofTestSecret(t)
	identity := securityProofTestIdentity()
	proof, _, err := service.IssueSecurityProof(
		identity,
		"passkey",
		[]string{"passkey.delete"},
	)
	require.NoError(t, err)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(authIdentityContextKey, identity)
		c.Next()
	})
	router.GET(
		"/",
		SecurityProofRequired("passkey.delete", []string{"passkey"}),
		func(c *gin.Context) {
			require.True(t, c.GetBool("secure_verified"))
			c.Status(http.StatusNoContent)
		},
	)

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Security-Proof", proof)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestGetSessionAuthIdentityRejectsPATStyleIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set(authIdentityContextKey, service.AuthIdentity{
		UserID:          42,
		UserAuthVersion: 3,
	})

	identity, ok := GetSessionAuthIdentity(context)
	require.False(t, ok)
	require.Empty(t, identity)
}

func TestGetSessionAuthIdentityReadsTargetStateContextFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	expected := securityProofTestIdentity()
	context.Set("id", expected.UserID)
	context.Set("session_id", expected.SessionID)
	context.Set("auth_version", expected.UserAuthVersion)
	context.Set("session_version", expected.SessionVersion)

	identity, ok := GetSessionAuthIdentity(context)
	require.True(t, ok)
	require.Equal(t, expected, identity)
}
