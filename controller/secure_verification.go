package controller

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const (
	secureVerificationMethod2FA     = "2fa"
	secureVerificationMethodPasskey = "passkey"
)

type UniversalVerifyRequest struct {
	Method string `json:"method"` // "2fa" 或 "passkey"
	Code   string `json:"code,omitempty"`
	Scope  string `json:"scope" binding:"required"`
}

type VerificationStatusResponse struct {
	Verified  bool  `json:"verified"`
	ExpiresAt int64 `json:"expires_at,omitempty"`
}

// UniversalVerify verifies 2FA and returns a scoped, short-lived Security Proof.
func UniversalVerify(c *gin.Context) {
	userId := c.GetInt("id")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "未登录",
		})
		return
	}

	var req UniversalVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, fmt.Errorf("参数错误: %v", err))
		return
	}

	// 获取用户信息
	user := &model.User{Id: userId}
	if err := user.FillUserById(); err != nil {
		common.ApiError(c, fmt.Errorf("获取用户信息失败: %v", err))
		return
	}

	if user.Status != common.UserStatusEnabled {
		common.ApiError(c, fmt.Errorf("该用户已被禁用"))
		return
	}

	// 检查用户的验证方式
	twoFA, _ := model.GetTwoFAByUserId(userId)
	has2FA := twoFA != nil && twoFA.IsEnabled

	if !has2FA {
		common.ApiError(c, fmt.Errorf("用户未启用2FA"))
		return
	}

	// 根据验证方式进行验证
	var verified bool
	var verifyMethod string

	switch req.Method {
	case "2fa":
		if !has2FA {
			common.ApiError(c, fmt.Errorf("用户未启用2FA"))
			return
		}
		if req.Code == "" {
			common.ApiError(c, fmt.Errorf("验证码不能为空"))
			return
		}
		verified = validateTwoFactorAuth(twoFA, req.Code)
		verifyMethod = "2FA"

	default:
		common.ApiError(c, fmt.Errorf("不支持的验证方式: %s", req.Method))
		return
	}

	if !verified {
		common.ApiError(c, fmt.Errorf("验证失败，请检查验证码"))
		return
	}

	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		common.ApiError(c, fmt.Errorf("登录会话状态无效"))
		return
	}
	if !allowedSecurityProofScope(req.Scope) {
		common.ApiError(c, fmt.Errorf("不支持的安全验证范围"))
		return
	}
	proof, expiresAt, err := service.IssueSecurityProof(identity, req.Method, []string{req.Scope})
	if err != nil {
		common.ApiError(c, fmt.Errorf("签发安全验证凭证失败: %v", err))
		return
	}

	// 记录日志
	model.RecordLog(userId, model.LogTypeSystem, fmt.Sprintf("通用安全验证成功 (验证方式: %s)", verifyMethod))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "验证成功",
		"data": gin.H{
			"verified":      true,
			"proof":         proof,
			"proof_type":    "SecurityProof",
			"expires_at":    expiresAt,
			"scope":         req.Scope,
			"verify_method": req.Method,
		},
	})
}

func allowedSecurityProofScope(scope string) bool {
	switch scope {
	case "channel.key.read", "passkey.register", "passkey.delete", "2fa.disable":
		return true
	default:
		return false
	}
}
