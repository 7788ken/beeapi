package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// 命中后向客户端返回的 OpenAI 兼容错误信息（仅 SensitiveFilter 冷备路径使用）
const (
	sensitiveBlockMessage   = "请求内容触发了平台安全策略，已被拦截。如认为是误判请联系管理员。"
	sensitiveBlockErrorType = "content_filter"
	sensitiveBlockErrorCode = "blocked_by_sensitive_filter"
)

// LoadSensitiveWords 兼容旧调用方（main.go / controller/sensitive.go），
// 实际刷新逻辑已下沉到 service.LoadSensitiveWords()。
func LoadSensitiveWords() {
	service.LoadSensitiveWords()
}

// Deprecated: SensitiveFilter 旧版同步阻断中间件。
// 阶段 1 起改用 SensitiveCollector 异步抽查，路由不再挂载本中间件；
// 函数体保留作为应急冷备：如需回退到同步阻断，
// 在 router/relay-router.go 把 SensitiveCollector() 换回 SensitiveFilter() 即可。
func SensitiveFilter() gin.HandlerFunc {
	return func(c *gin.Context) {
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

		entries := service.SensitiveWordsSnapshot()
		if len(entries) == 0 {
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
		bodyStr := string(body)
		lowerBody := strings.ToLower(bodyStr)

		for _, e := range entries {
			snippet, hit := service.ScanSensitive(e, bodyStr, lowerBody)
			if !hit {
				continue
			}
			if e.Action == model.SensitiveActionMonitor {
				recordHit(c, e, bodyStr, snippet, false)
				continue
			}
			recordHit(c, e, bodyStr, snippet, true)
			return
		}
		c.Next()
	}
}

// recordHit 同步落库 + 可选封 token；仅 SensitiveFilter 冷备路径使用。
func recordHit(c *gin.Context, e service.CompiledSensitiveWord, body, snippet string, doBlock bool) {
	requestId := c.GetString(common.RequestIdKey)
	userId := c.GetInt("id")
	username := c.GetString("username")
	tokenId := c.GetInt("token_id")
	tokenName := c.GetString("token_name")
	channelId := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	channelName := common.GetContextKeyString(c, constant.ContextKeyChannelName)
	modelName := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
	if modelName == "" {
		modelName = c.GetString("model")
	}

	disabled := false
	if doBlock {
		disabled = disableTokenSafely(tokenId)
	}

	logEntry := &model.SensitiveBlockLog{
		RequestId:      requestId,
		UserId:         userId,
		Username:       username,
		TokenId:        tokenId,
		TokenName:      tokenName,
		ChannelId:      channelId,
		ChannelName:    channelName,
		ModelName:      modelName,
		Path:           c.Request.URL.Path,
		MatchedWordId:  e.Id,
		MatchedPattern: e.Pattern,
		IsRegex:        e.IsRegex,
		Action:         e.Action,
		MatchedSnippet: snippet,
		RequestBody:    body, // 冷备路径保留旧行为，把全量 body 写到 longtext
		Ip:             c.ClientIP(),
		UserAgent:      c.Request.Header.Get("User-Agent"),
		TokenDisabled:  disabled,
	}
	if err := logEntry.Insert(); err != nil {
		common.SysError("写入敏感词拦截记录失败：" + err.Error())
	}
	if err := model.IncrementSensitiveHit(e.Id); err != nil {
		common.SysError("递增敏感词命中计数失败 id=" + strconv.Itoa(e.Id) + " err=" + err.Error())
	}

	if !doBlock {
		return
	}
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
		"error": gin.H{
			"message":    sensitiveBlockMessage,
			"type":       sensitiveBlockErrorType,
			"code":       sensitiveBlockErrorCode,
			"request_id": requestId,
		},
	})
}

func disableTokenSafely(tokenId int) bool {
	if tokenId <= 0 {
		return false
	}
	token, err := model.GetTokenById(tokenId)
	if err != nil || token == nil {
		return false
	}
	if token.Status == common.TokenStatusDisabled {
		return true
	}
	token.Status = common.TokenStatusDisabled
	if err := token.Update(); err != nil {
		common.SysError("封禁敏感词命中 token 失败 token_id=" + strconv.Itoa(tokenId) + " err=" + err.Error())
		return false
	}
	return true
}
