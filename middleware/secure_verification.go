package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const (
	authIdentityContextKey = "auth_identity"
)

// SecurityProofRequired enforces a scoped, short-lived proof for sensitive
// dashboard operations after access-token authentication establishes identity.
func SecurityProofRequired(
	requiredScope string,
	allowedMethods []string,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !RequireSecurityProof(c, requiredScope, allowedMethods) {
			return
		}
		c.Set("secure_verified", true)
		c.Next()
	}
}

func RequireSecurityProof(
	c *gin.Context,
	requiredScope string,
	allowedMethods []string,
) bool {
	identity, ok := GetSessionAuthIdentity(c)
	if !ok {
		securityProofError(
			c,
			"SECURITY_PROOF_INVALID",
			"安全验证状态无效",
			requiredScope,
		)
		return false
	}
	raw := strings.TrimSpace(c.GetHeader("X-Security-Proof"))
	if raw == "" {
		securityProofError(
			c,
			"SECURITY_PROOF_REQUIRED",
			"需要安全验证",
			requiredScope,
		)
		return false
	}
	if _, err := service.VerifySecurityProof(
		raw,
		identity,
		requiredScope,
		allowedMethods,
	); err != nil {
		switch {
		case errors.Is(err, service.ErrAuthTokenExpired):
			securityProofError(
				c,
				"SECURITY_PROOF_EXPIRED",
				"安全验证已过期",
				requiredScope,
			)
		case errors.Is(err, service.ErrProofScope):
			securityProofError(
				c,
				"SECURITY_PROOF_SCOPE_MISMATCH",
				"安全验证范围不匹配",
				requiredScope,
			)
		case errors.Is(err, service.ErrProofMethod):
			securityProofError(
				c,
				"SECURITY_PROOF_METHOD_MISMATCH",
				"安全验证方式不匹配",
				requiredScope,
			)
		default:
			securityProofError(
				c,
				"SECURITY_PROOF_INVALID",
				"安全验证状态无效",
				requiredScope,
			)
		}
		return false
	}
	return true
}

func GetSessionAuthIdentity(c *gin.Context) (service.AuthIdentity, bool) {
	if identity, ok := GetAuthIdentity(c); ok {
		if validSessionAuthIdentity(identity) {
			return identity, true
		}
		return service.AuthIdentity{}, false
	}
	identity := service.AuthIdentity{
		UserID:          c.GetInt("id"),
		SessionID:       c.GetString("session_id"),
		UserAuthVersion: c.GetInt64("auth_version"),
		SessionVersion:  c.GetInt64("session_version"),
	}
	if !validSessionAuthIdentity(identity) {
		return service.AuthIdentity{}, false
	}
	return identity, true
}

func GetAuthIdentity(c *gin.Context) (service.AuthIdentity, bool) {
	value, ok := c.Get(authIdentityContextKey)
	if !ok {
		return service.AuthIdentity{}, false
	}
	identity, ok := value.(service.AuthIdentity)
	return identity, ok
}

func validSessionAuthIdentity(identity service.AuthIdentity) bool {
	return identity.UserID > 0 &&
		identity.SessionID != "" &&
		identity.UserAuthVersion > 0 &&
		identity.SessionVersion > 0
}

func securityProofError(c *gin.Context, code, message, scope string) {
	c.JSON(http.StatusForbidden, gin.H{
		"success": false,
		"message": message,
		"code":    code,
		"scope":   scope,
	})
	c.Abort()
}
