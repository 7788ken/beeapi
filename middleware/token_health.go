package middleware

import (
	"context"

	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// TokenHealthRecord 记录每个请求的终态，供「令牌错误率熔断」累积统计。
//
// 必须挂在 TokenAuth 之后：
//   - 鉴权失败（无效令牌 / IP 受限 / 封禁 / 冷却 429）在 TokenAuth 内已 abort，不会进入本中间件，
//     且此时无 token_id；本中间件以 tokenId<=0 兜底跳过，避免把"非本令牌"的失败算进来。
//   - 用 c.Next() 包裹 Distribute（模型权限 / 缺模型名等 4xx）与 Relay（请求体非法 / 上游 4xx），
//     用最终 c.Writer.Status() 作信号源 —— 解决"只在 Relay() 内埋点导致中间件层 4xx 漏计"的问题。
//   - 限流 429 由 ModelRequestRateLimit 在本中间件之前 abort，不计入。
//
// feature 关闭时 service.RecordTokenStatus 内部即早退，零额外开销。
func TokenHealthRecord() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		// 总开关关闭（默认）时直接返回，不读状态码也不派生协程，保持热路径零开销。
		if !service.TokenHealthEnabled() {
			return
		}
		tokenId := c.GetInt("token_id")
		if tokenId <= 0 {
			return
		}
		// 计费/额度阶段拒绝（余额不足 403 等）不代表"请求本身错误"，跳过以免欠费令牌被误冷却。
		if c.GetBool("token_health_skip") {
			return
		}
		// 优先用 Relay 写入的权威终态状态码：realtime(WebSocket) 升级后错误经 Wss message 下发、
		// 不写 HTTP 状态码，c.Writer.Status() 会停在 200/101 不可靠；context key 缺失时回退 writer。
		status := c.GetInt("token_health_status")
		if status <= 0 {
			status = c.Writer.Status()
		}
		_ = backgroundtask.Submit("token-health-record", func(context.Context) {
			service.RecordTokenStatus(tokenId, status)
		})
	}
}
