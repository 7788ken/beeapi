package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

// SensitiveCollector 不良监控异步采集中间件。
//
//   - 不阻断请求；命中采样后异步把 body 落 dump 文件并投递审计任务
//   - 仅扫白名单路径前缀（service.IsSensitiveGuardedPath）+ POST + JSON
//   - 任何错误（开关关、未采样、body 读不到、queue 满）一律放行
//   - 与旧 SensitiveFilter（同步阻断版）互斥使用：旧版保留代码作冷备，不再挂载
func SensitiveCollector() gin.HandlerFunc {
	return sensitiveCollector(service.SubmitSensitiveAudit)
}

func sensitiveCollector(submit func(service.SensitiveAuditJob, []byte) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !setting.GetSensitiveAsyncEnabled() {
			c.Next()
			return
		}
		if c.Request.Method != http.MethodPost {
			c.Next()
			return
		}
		if !service.IsSensitiveGuardedPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		contentType := c.Request.Header.Get("Content-Type")
		if !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
			c.Next()
			return
		}

		userId := c.GetInt("id")
		if !service.ShouldSampleSensitive(userId) {
			c.Next()
			return
		}

		storage, err := common.GetBodyStorage(c)
		if err != nil {
			c.Next()
			return
		}
		body, err := storage.Bytes()
		if err != nil || len(body) == 0 {
			c.Next()
			return
		}

		// Submit 在返回前同步登记 producer 并复制 body，实际 dump 写盘在
		// pipeline 内异步执行；这里不把 *gin.Context 传入异步边界。
		meta := service.SensitiveAuditJob{
			RequestID:   c.GetString(common.RequestIdKey),
			UserID:      userId,
			Username:    c.GetString("username"),
			TokenID:     c.GetInt("token_id"),
			TokenName:   c.GetString("token_name"),
			ChannelID:   common.GetContextKeyInt(c, constant.ContextKeyChannelId),
			ChannelName: common.GetContextKeyString(c, constant.ContextKeyChannelName),
			ModelName:   firstNonEmpty(common.GetContextKeyString(c, constant.ContextKeyOriginalModel), c.GetString("model")),
			Path:        c.Request.URL.Path,
			IP:          c.ClientIP(),
			UserAgent:   c.Request.Header.Get("User-Agent"),
		}
		if err := submit(meta, body); err != nil {
			common.SysError("[sensitive] audit admission failed request_id=" + meta.RequestID + " err=" + err.Error())
		}

		c.Next()
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
