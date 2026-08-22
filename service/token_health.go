package service

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/go-redis/redis/v8"
)

// ─────────────────────────────────────────────────────────────────────────────
// 令牌（用户 API Key）错误率熔断
//
// 入口：
//   RecordTokenStatus  — relay 链路中间件（鉴权之后）在请求收尾时按最终 HTTP 状态码异步调用一次，
//                        覆盖 Distribute/Relay 全部终态；RecordTokenResult 为按 *NewAPIError 的兼容封装。
//   CheckTokenCooldown — 鉴权中间件放行前查询，命中则返回 429 + Retry-After。
//
// 计数：按分钟切桶 token:req:{id}:{min} / token:err:{id}:{min}，窗口内求和算错误率。
//       Redis 不可用时降级到进程内 map（单实例够用；多实例计数偏低=偏安全方向）。
// 冷却：token:cooldown:{id}，TTL=CooldownMinutes，到期自动恢复。
//
// 设计要点：
//   - 统计所有错误响应（≥400 且 ≠429，可再排除指定码）；含上游 5xx/超时。
//     若某上游故障期不想把正常 key 连坐冷却，把对应码填进 ExcludedStatusCodes 排除。
//   - CheckTokenCooldown fail-OPEN：feature 关闭或 Redis 抖动一律放行，绝不误伤。
//   - 触发时清空窗口桶：冷却到期后从全新窗口重新累积，不用陈旧错误立即复发。
// ─────────────────────────────────────────────────────────────────────────────

const (
	tokenHealthRedisOpTTL = 500 * time.Millisecond
	// 进程内桶兜底的保留分钟数（远大于任何合理窗口，限制内存）。
	thBucketRetainMinutes = 60
)

func thKeyReq(tokenId int, minute int64) string { return fmt.Sprintf("token:req:%d:%d", tokenId, minute) }
func thKeyErr(tokenId int, minute int64) string { return fmt.Sprintf("token:err:%d:%d", tokenId, minute) }
func thKeyCooldown(tokenId int) string          { return fmt.Sprintf("token:cooldown:%d", tokenId) }

func thCurrentMinute() int64 { return time.Now().Unix() / 60 }

// ── 进程内 fallback（Redis 不可用时启用）──
type thMemStore struct {
	mu        sync.Mutex
	buckets   map[string]map[int64]int64 // "req:id" / "err:id" -> minute -> count
	cooldowns map[int]int64              // tokenId -> 到期 unix nano
}

var thMem = &thMemStore{
	buckets:   make(map[string]map[int64]int64),
	cooldowns: make(map[int]int64),
}

func (m *thMemStore) incr(metricKey string, minute int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.buckets[metricKey]
	if !ok {
		b = make(map[int64]int64)
		m.buckets[metricKey] = b
	}
	b[minute]++
	for k := range b {
		if k < minute-thBucketRetainMinutes {
			delete(b, k)
		}
	}
}

func (m *thMemStore) sum(metricKey string, fromMinute, toMinute int64) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.buckets[metricKey]
	if !ok {
		return 0
	}
	var total int64
	for min := fromMinute; min <= toMinute; min++ {
		total += b[min]
	}
	return total
}

func (m *thMemStore) clear(tokenId int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.buckets, fmt.Sprintf("req:%d", tokenId))
	delete(m.buckets, fmt.Sprintf("err:%d", tokenId))
}

// setCooldownNX 原子写冷却：仅当当前无有效冷却时写入并返回 true；已在冷却中返回 false。
func (m *thMemStore) setCooldownNX(tokenId int, ttl time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if exp, ok := m.cooldowns[tokenId]; ok && exp > time.Now().UnixNano() {
		return false
	}
	m.cooldowns[tokenId] = time.Now().Add(ttl).UnixNano()
	return true
}

func (m *thMemStore) cooldownRemaining(tokenId int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.cooldowns[tokenId]
	if !ok {
		return 0
	}
	rem := exp - time.Now().UnixNano()
	if rem <= 0 {
		delete(m.cooldowns, tokenId)
		return 0
	}
	return int(rem/int64(time.Second)) + 1
}

// ── Redis 桶操作 ──
func thIncrBucketRedis(key string, ttl time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), tokenHealthRedisOpTTL)
	defer cancel()
	pipe := common.RDB.TxPipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ttl)
	_, _ = pipe.Exec(ctx)
}

func thParseInt(s string, err error) int64 {
	if err != nil || s == "" {
		return 0
	}
	v, e := strconv.ParseInt(s, 10, 64)
	if e != nil {
		return 0
	}
	return v
}

// thWindowCounts 求窗口内（含当前分钟，往前 windowMinutes 个桶）的总请求数与错误数。
func thWindowCounts(tokenId, windowMinutes int) (req int64, errc int64) {
	cur := thCurrentMinute()
	from := cur - int64(windowMinutes) + 1
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		pipe := common.RDB.Pipeline()
		reqCmds := make([]*redis.StringCmd, 0, windowMinutes)
		errCmds := make([]*redis.StringCmd, 0, windowMinutes)
		for m := from; m <= cur; m++ {
			reqCmds = append(reqCmds, pipe.Get(ctx, thKeyReq(tokenId, m)))
			errCmds = append(errCmds, pipe.Get(ctx, thKeyErr(tokenId, m)))
		}
		_, _ = pipe.Exec(ctx)
		for i := range reqCmds {
			req += thParseInt(reqCmds[i].Result())
			errc += thParseInt(errCmds[i].Result())
		}
		return
	}
	req = thMem.sum(fmt.Sprintf("req:%d", tokenId), from, cur)
	errc = thMem.sum(fmt.Sprintf("err:%d", tokenId), from, cur)
	return
}

// thClearBuckets 清空窗口内的请求/错误桶（触发冷却后调用）。
func thClearBuckets(tokenId, windowMinutes int) {
	cur := thCurrentMinute()
	from := cur - int64(windowMinutes) + 1
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		keys := make([]string, 0, windowMinutes*2)
		for m := from; m <= cur; m++ {
			keys = append(keys, thKeyReq(tokenId, m), thKeyErr(tokenId, m))
		}
		_ = common.RDB.Del(ctx, keys...).Err()
	}
	thMem.clear(tokenId)
}

// thSetCooldown 写冷却键。写入存储必须与 CheckTokenCooldown 的读取存储一致：
// Redis 启用时只写 Redis（写失败则记日志放弃，而不是写一个永不会被读到的内存兜底——
// 否则会出现「冷却写进内存、检查却读 Redis 读不到」的脑裂，导致熔断静默失效）。
// thSetCooldown 原子写冷却键，返回 true=本次真正写入（此前无冷却），false=已在冷却中或写失败。
// 用 SetNX 让"判断未冷却→写冷却"成为单次原子操作，消除并发首次触发时 check-then-act 的竞态
// （否则多个并发终态各自读到"未冷却"而重复写键、反复刷新 TTL 把冷却时长拖长）。
func thSetCooldown(tokenId int, ttl time.Duration) bool {
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), tokenHealthRedisOpTTL)
		defer cancel()
		ok, err := common.RDB.SetNX(ctx, thKeyCooldown(tokenId), "1", ttl).Result()
		if err != nil {
			common.SysError(fmt.Sprintf("token_health: 写冷却键失败(token_id=%d)，本次熔断未生效: %v", tokenId, err))
			return false
		}
		return ok
	}
	return thMem.setCooldownNX(tokenId, ttl)
}

// thIncrReq / thIncrErr 单次计数（Redis 或内存）。
func thIncrReq(tokenId int, minute int64, ttl time.Duration) {
	if common.RedisEnabled && common.RDB != nil {
		thIncrBucketRedis(thKeyReq(tokenId, minute), ttl)
		return
	}
	thMem.incr(fmt.Sprintf("req:%d", tokenId), minute)
}

func thIncrErr(tokenId int, minute int64, ttl time.Duration) {
	if common.RedisEnabled && common.RDB != nil {
		thIncrBucketRedis(thKeyErr(tokenId, minute), ttl)
		return
	}
	thMem.incr(fmt.Sprintf("err:%d", tokenId), minute)
}

// thIsCountedStatus 判定最终 HTTP 状态码是否计入 token 错误率：所有错误响应（≥400 且 ≠429），
// 且不在排除列表内。2xx/3xx 不计入；429（限流，含熔断自身输出）始终不计入。
// 注意：5xx/上游错误现也计入——若某上游故障期不想连坐冷却好 key，把对应码填进 ExcludedStatusCodes。
func thIsCountedStatus(statusCode int, cfg *operation_setting.TokenHealthConfig) bool {
	if statusCode < 400 {
		return false // 仅错误响应；2xx/3xx 不计入
	}
	if statusCode == 429 {
		return false // 限流不计入（熔断自身输出即 429，计入会自我延续）
	}
	if cfg.ExcludedStatusCodes != "" {
		for _, part := range strings.Split(cfg.ExcludedStatusCodes, ",") {
			if code, e := strconv.Atoi(strings.TrimSpace(part)); e == nil && code == statusCode {
				return false
			}
		}
	}
	return true
}

// thIsCountedError 判定错误是否计入 token 错误率（按 NewAPIError 的 StatusCode）。
func thIsCountedError(err *types.NewAPIError, cfg *operation_setting.TokenHealthConfig) bool {
	if err == nil {
		return false
	}
	return thIsCountedStatus(err.StatusCode, cfg)
}

// RecordTokenResult 记录一次用户请求终态（按 *NewAPIError）。err==nil 表示成功。
// 保留以兼容按错误对象记录的调用方；内部委托给 RecordTokenStatus。
func RecordTokenResult(tokenId int, err *types.NewAPIError) {
	status := http.StatusOK
	if err != nil {
		status = err.StatusCode
	}
	RecordTokenStatus(tokenId, status)
}

// RecordTokenStatus 记录一次用户请求终态（按最终 HTTP 状态码）。statusCode<400 视为成功。
// 埋点在 relay 链路中间件（鉴权之后）：覆盖 Distribute（模型权限/缺模型名等 4xx）与
// Relay（请求体非法/上游 4xx）的全部终态；鉴权失败（无 token_id）与限流 429 不进入本路径。
// 应在 goroutine 内调用；内部已 recover，但 Redis 抖动仍可能短暂耗时。
func RecordTokenStatus(tokenId int, statusCode int) {
	if tokenId <= 0 {
		return
	}
	cfg := operation_setting.GetTokenHealthConfig()
	if !cfg.Enabled {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("token_health: panic recovered (token_id=%d): %v", tokenId, r))
		}
	}()

	window := cfg.WindowMinutes
	if window <= 0 {
		window = 10
	}
	if window > thBucketRetainMinutes {
		window = thBucketRetainMinutes // 上限钳制：防误配大值导致每请求海量 Redis GET/DEL
	}
	bucketTTL := time.Duration(window+2) * time.Minute
	minute := thCurrentMinute()

	thIncrReq(tokenId, minute, bucketTTL)
	if thIsCountedStatus(statusCode, cfg) {
		thIncrErr(tokenId, minute, bucketTTL)
	}

	// 阈值非法（<=0 或 >1）时不触发，避免误配置导致秒封。
	threshold := cfg.ErrorRateThreshold
	if threshold <= 0 || threshold > 1 {
		return
	}
	minReq := cfg.MinRequests
	if minReq < 1 {
		minReq = 1
	}

	req, errc := thWindowCounts(tokenId, window)
	if req < int64(minReq) {
		return
	}
	if float64(errc)/float64(req) < threshold {
		return
	}
	cooldown := cfg.CooldownMinutes
	if cooldown <= 0 {
		cooldown = 5
	}
	// SetNX 原子写入：仅本次真正触发（此前未冷却）才清桶+记日志，避免并发重复刷新 TTL 变相延长冷却。
	if !thSetCooldown(tokenId, time.Duration(cooldown)*time.Minute) {
		return
	}
	thClearBuckets(tokenId, window) // 冷却到期后从全新窗口重新累积
	common.SysLog(fmt.Sprintf(
		"token_health: token_id=%d 触发错误率熔断（窗口 %dmin 内 %d/%d=%.0f%% ≥ %.0f%%），冷却 %dmin",
		tokenId, window, errc, req, float64(errc)/float64(req)*100, threshold*100, cooldown))
}

// TokenHealthEnabled 总开关快速查询：供 relay 中间件在派生记录协程前短路，关闭时零额外开销。
func TokenHealthEnabled() bool {
	return operation_setting.GetTokenHealthConfig().Enabled
}

// CheckTokenCooldown 查询 token 是否处于冷却。返回 (是否冷却中, 剩余秒数)。
// fail-OPEN：feature 关闭或 Redis 异常时一律返回 (false, 0)，绝不拦截正常请求。
func CheckTokenCooldown(tokenId int) (bool, int) {
	if tokenId <= 0 {
		return false, 0
	}
	cfg := operation_setting.GetTokenHealthConfig()
	if !cfg.Enabled {
		return false, 0
	}
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), tokenHealthRedisOpTTL)
		defer cancel()
		ttl, err := common.RDB.TTL(ctx, thKeyCooldown(tokenId)).Result()
		if err != nil {
			return false, 0 // fail-open
		}
		if ttl > 0 {
			return true, int(ttl.Seconds()) + 1
		}
		return false, 0
	}
	rem := thMem.cooldownRemaining(tokenId)
	return rem > 0, rem
}
