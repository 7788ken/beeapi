package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// ContentBackupConsoleAuth 只放行独立管理端的令牌。
// 缺失、错误、或服务器未配置令牌，都返回 404，不区分原因。
// 普通管理员权限和 root 浏览器会话不能代替这把令牌。
func ContentBackupConsoleAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !contentBackupConsoleTokenMatches(common.ContentBackupConsoleToken, contentBackupConsoleTokenFrom(c)) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

func contentBackupConsoleTokenFrom(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func contentBackupConsoleTokenMatches(expected, got string) bool {
	if expected == "" {
		return false
	}
	sumExpected := sha256.Sum256([]byte(expected))
	sumGot := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(sumExpected[:], sumGot[:]) == 1
}
