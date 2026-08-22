package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// 通过 httptest 驱动 TokenHealthRecord 中间件，验证「中间件读最终状态码 → RecordTokenStatus
// → 触发冷却 → CheckTokenCooldown 命中」这条接线在真实 gin 请求中成立，无需起容器。

func enableTokenHealth(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := operation_setting.GetTokenHealthConfig()
	saved := *cfg
	cfg.Enabled = true
	cfg.WindowMinutes = 10
	cfg.MinRequests = 1 // 单请求即可触发，便于断言
	cfg.ErrorRateThreshold = 0.5
	cfg.CooldownMinutes = 5
	cfg.ExcludedStatusCodes = ""
	t.Cleanup(func() { *cfg = saved })
}

// buildRouter: 用一个前置中间件模拟 TokenAuth 设好 token_id，再挂 TokenHealthRecord，
// 末端 handler 写出指定状态码（并可选设置 token_health_skip）。
func fireRequest(tokenId, status int, skip bool) {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if tokenId > 0 {
			c.Set("token_id", tokenId)
		}
		c.Next()
	})
	r.Use(TokenHealthRecord())
	r.GET("/x", func(c *gin.Context) {
		if skip {
			c.Set("token_health_skip", true)
		}
		c.JSON(status, gin.H{"ok": false})
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)
}

// waitCooling 轮询等待异步记录生效（gopool.Go 异步执行）。
func waitCooling(tokenId int) bool {
	for i := 0; i < 100; i++ {
		if cooling, _ := service.CheckTokenCooldown(tokenId); cooling {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	cooling, _ := service.CheckTokenCooldown(tokenId)
	return cooling
}

func TestMiddlewareRecords4xxAndTriggers(t *testing.T) {
	enableTokenHealth(t)
	const tokenId = 910001
	fireRequest(tokenId, http.StatusBadRequest, false) // 400 客户端错误
	if !waitCooling(tokenId) {
		t.Fatalf("中间件应记录 400 并在达阈值后触发冷却")
	}
}

func TestMiddlewareSkipsBillingReject(t *testing.T) {
	enableTokenHealth(t)
	const tokenId = 910002
	fireRequest(tokenId, http.StatusForbidden, true) // 403 但带 skip 标记（模拟余额不足）
	if waitCooling(tokenId) {
		t.Fatalf("带 token_health_skip 的请求不应计入/触发冷却")
	}
}

func TestMiddlewareIgnoresNoTokenId(t *testing.T) {
	enableTokenHealth(t)
	// 无 token_id（鉴权失败场景）：不应记录，CheckTokenCooldown(0) 恒 false。
	fireRequest(0, http.StatusUnauthorized, false)
	if cooling, _ := service.CheckTokenCooldown(0); cooling {
		t.Fatalf("无 token_id 不应进入统计")
	}
}

func TestMiddleware5xxCounted(t *testing.T) {
	enableTokenHealth(t)
	const tokenId = 910003
	fireRequest(tokenId, http.StatusBadGateway, false) // 502 上游错误，现也计入
	if !waitCooling(tokenId) {
		t.Fatalf("5xx 现已计入错误率，502 应触发冷却")
	}
}

// 模拟 realtime/流式：handler 写 HTTP 200，但通过 context key 标记真实终态 400。
// 中间件应优先用 token_health_status(400) 而非 c.Writer.Status()(200) → 触发熔断。
func TestMiddlewarePrefersContextStatus(t *testing.T) {
	enableTokenHealth(t)
	const tokenId = 910004
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("token_id", tokenId); c.Next() })
	r.Use(TokenHealthRecord())
	r.GET("/x", func(c *gin.Context) {
		c.Set("token_health_status", http.StatusBadRequest) // 权威终态 400
		c.JSON(http.StatusOK, gin.H{"ok": true})            // 但 writer 停在 200
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if !waitCooling(tokenId) {
		t.Fatalf("应优先用 context token_health_status=400 触发，而非 writer 的 200")
	}
}
