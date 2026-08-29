package common

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
)

// MarkAdminRejectReason 写入上游拒绝/拦截标记，first-write-wins：
// 该键参与免单计费判定，先到的拒绝证据不被后续弱信号（如 empty_candidates）覆盖，
// 语义对齐 StreamStatus.SetEndReason。每轮渠道重试由 controller/relay.go 统一清空后重新累积。
func MarkAdminRejectReason(c *gin.Context, reason string) {
	if c == nil || reason == "" {
		return
	}
	if common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason) != "" {
		return
	}
	common.SetContextKey(c, constant.ContextKeyAdminRejectReason, reason)
}
