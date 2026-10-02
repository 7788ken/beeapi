package middleware

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const RouteTagKey = "route_tag"

// 访问日志原样记录查询串，这些参数可能携带令牌、渠道密钥（渠道搜索按密钥匹配）、登录码或可重放的 Telegram 登录签名，落盘前只保留参数名
var credentialQueryParams = map[string]struct{}{
	"key":          {},
	"token":        {},
	"keyword":      {},
	"code":         {},
	"hash":         {},
	"api_key":      {},
	"access_token": {},
}

func RouteTag(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RouteTagKey, tag)
		c.Next()
	}
}

func SetUpLogger(server *gin.Engine) {
	server.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		var requestID string
		if param.Keys != nil {
			requestID, _ = param.Keys[common.RequestIdKey].(string)
		}
		tag, _ := param.Keys[RouteTagKey].(string)
		if tag == "" {
			tag = "web"
		}
		return fmt.Sprintf("[GIN] %s | %s | %s | %3d | %13v | %15s | %7s %s\n",
			param.TimeStamp.Format("2006/01/02 - 15:04:05"),
			tag,
			requestID,
			param.StatusCode,
			param.Latency,
			param.ClientIP,
			param.Method,
			redactCredentialQuery(param.Path),
		)
	}))
}

func redactCredentialQuery(path string) string {
	base, query, found := strings.Cut(path, "?")
	if !found {
		return path
	}
	pairs := strings.Split(query, "&")
	for i, pair := range pairs {
		rawName, value, _ := strings.Cut(pair, "=")
		if value == "" {
			continue
		}
		// 解码失败的参数会被 url.ParseQuery 整对丢弃，业务读不到
		name, err := url.QueryUnescape(rawName)
		if err != nil {
			continue
		}
		if _, ok := credentialQueryParams[strings.ToLower(name)]; ok {
			pairs[i] = rawName + "=[REDACTED]"
		}
	}
	return base + "?" + strings.Join(pairs, "&")
}
