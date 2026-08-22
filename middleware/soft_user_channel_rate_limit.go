package middleware

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// SoftUserChannelRateLimit 软 RPM 限流：按 (user_id, channel_id) 配额限速 +
// 配额内随机延迟，让客户端从外部看不出是固定额度。
//
// 调用点：在 Distribute() 选完 channel、SetupContextForSelectedChannel 之后、
// c.Next() 之前。详见 distributor.go。
//
// 失败策略：
//   - 规则查询出错 / Redis 出错 → fail-open（记 warn，继续转发）
//   - 抖动延迟出错 → fail-open
//
// 注意：调用方必须保证已设置 ContextKeyUserId / ContextKeyChannelId，否则跳过。
func SoftUserChannelRateLimit(c *gin.Context) {
	userId := int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
	channelId := int64(common.GetContextKeyInt(c, constant.ContextKeyChannelId))
	if userId <= 0 || channelId <= 0 {
		return
	}

	rule, err := model.GetUserChannelRateLimit(userId, channelId)
	if err != nil {
		common.SysError(fmt.Sprintf("soft_rpm: lookup rule failed user=%d channel=%d: %s", userId, channelId, err.Error()))
		return // fail-open
	}
	if rule == nil || !rule.Enabled || rule.BaseRpm <= 0 {
		return
	}

	minuteBucket := time.Now().Unix() / 60
	effectiveRpm := computeEffectiveRpm(rule.BaseRpm, rule.JitterPct, userId, channelId, minuteBucket)
	if effectiveRpm <= 0 {
		effectiveRpm = 1 // 保底，避免 jitter 把 base=1 抖到 0
	}

	count, ok := softRpmIncr(c, userId, channelId, minuteBucket)
	if !ok {
		return // Redis 故障 → fail-open
	}

	if count > int64(effectiveRpm) {
		writeSoftRateLimit429(c)
		return
	}

	// 通过限流：注入随机延迟，掩盖固定额度指纹
	if rule.MaxDelayMs > 0 {
		mn := rule.MinDelayMs
		mx := rule.MaxDelayMs
		if mn > mx {
			mn, mx = mx, mn
		}
		var delay int
		if mn == mx {
			delay = mn
		} else {
			delay = mn + rand.Intn(mx-mn+1)
		}
		if delay > 0 {
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}
	}
}

// computeEffectiveRpm 单分钟内同 (user, channel) 稳定的 RPM：
// seed = fnv(user, channel, minuteBucket) → rand in [-jitter, +jitter] → base*(1+r)。
func computeEffectiveRpm(baseRpm int, jitterPct float32, userId, channelId, minuteBucket int64) int {
	if jitterPct <= 0 {
		return baseRpm
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(fmt.Sprintf("%d|%d|%d", userId, channelId, minuteBucket)))
	r := rand.New(rand.NewSource(int64(h.Sum64())))
	delta := (r.Float64()*2 - 1) * float64(jitterPct) // [-j, +j]
	eff := float64(baseRpm) * (1 + delta)
	return int(math.Round(eff))
}

// softRpmIncr Redis INCR + EXPIRE 70s；返回 (count, ok)。
// Redis 不可用时返回 (0, false) → fail-open。
func softRpmIncr(c *gin.Context, userId, channelId, minuteBucket int64) (int64, bool) {
	if !common.RedisEnabled || common.RDB == nil {
		// 内存退化方案：极简实现，进程内分桶；fail-open（不限流）。
		// 软限流场景下，没有 Redis 就视为放行，避免单机内存计数与"看不出来"目标冲突。
		return 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	key := fmt.Sprintf("softrl:%d:%d:%d", userId, channelId, minuteBucket)
	count, err := common.RDB.Incr(ctx, key).Result()
	if err != nil {
		common.SysError("soft_rpm: redis incr failed: " + err.Error())
		return 0, false
	}
	if count == 1 {
		// 首次自增设置 TTL，70s 留 10s 容错（分钟桶切换瞬间避免立刻过期）
		if err := common.RDB.Expire(ctx, key, 70*time.Second).Err(); err != nil {
			common.SysError("soft_rpm: redis expire failed: " + err.Error())
		}
	}
	return count, true
}

// writeSoftRateLimit429 按请求路径选择 429 响应格式：
//   - /v1/messages → Anthropic 风格
//   - 其他 → OpenAI 风格
//
// Retry-After=1 让客户端"觉得"是上游临时限流，1s 后重试即可。
func writeSoftRateLimit429(c *gin.Context) {
	c.Header("Retry-After", "1")
	path := c.Request.URL.Path
	if strings.Contains(path, "/v1/messages") {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "rate_limit_error",
				"message": "Number of requests has exceeded your rate limit. Please retry after a few seconds.",
			},
		})
	} else {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": gin.H{
				"message": "Rate limit reached for requests. Please try again in 1s.",
				"type":    "requests",
				"param":   nil,
				"code":    "rate_limit_exceeded",
			},
		})
	}
	c.Abort()
}
