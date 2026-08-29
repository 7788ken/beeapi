package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// RequireAdminPerm 管理员细粒度权限闸门，必须挂在 AdminAuth() 之后。
// 命中任意一个 perm 即放行；root 在 model.HasAdminPermFor 里恒通过。
// 权限现读库不走用户缓存：收回权限要立刻生效，而管理端接口本身低频。
func RequireAdminPerm(perms ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if len(perms) == 0 {
			c.Next()
			return
		}
		if c.GetInt("role") >= common.RoleRootUser {
			c.Next()
			return
		}
		allowed, err := model.UserHasAnyAdminPerm(c.GetInt("id"), c.GetInt("role"), perms...)
		if err != nil {
			common.SysLog("RequireAdminPerm: failed to load admin perms: " + err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": common.TranslateMessage(c, i18n.MsgDatabaseError),
			})
			c.Abort()
			return
		}
		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": common.TranslateMessage(c, i18n.MsgAuthAdminPermDenied),
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
