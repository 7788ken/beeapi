package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/hot"
)

// 非流式断连重试短路（retry_short_circuit_setting 控制，默认关闭）。
//
// 场景：客户端在长耗时非流式请求上提前掐线（疑似 SDK 默认 600s 超时），网关把取消传播给
// 上游后上游可能已跑完照样计费；官方 SDK 随后原样自动重试同一请求，形成烧钱循环。
// 机制：失败收尾时记录"刚被客户端取消的请求"指纹；TTL 内同一请求再来，controller.Relay
// 入口直接 400 拒绝（官方 SDK 对 400 不自动重试），零计费、零渠道副作用。
//
// key = "rsc:" + sha256hex(tokenID + "|" + 模型名 + "|" + 原始请求体)；
// 存储走 cachex.HybridCache：Redis 可用时用 Redis（多实例共享），否则进程内 hot cache。
// 命中只读不续期：客户调大超时/改流式后不会被连续命中永久锁死。
const retryShortCircuitNamespace = "rsc"

type retryShortCircuitEntry struct {
	// UseTimeSeconds 记录时请求已运行的秒数（错误提示里的 N）。
	UseTimeSeconds int `json:"use_time_seconds"`
	// TTLMinutes 记录时的 TTL 配置值（错误提示里的 M）。
	TTLMinutes int `json:"ttl_minutes"`
}

// retryShortCircuitCodec 走 common.Marshal/Unmarshal（项目 Rule 1），
// 不复用 cachex.JSONCodec（其直连 encoding/json）。
type retryShortCircuitCodec struct{}

func (retryShortCircuitCodec) Encode(v retryShortCircuitEntry) (string, error) {
	b, err := common.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (retryShortCircuitCodec) Decode(s string) (retryShortCircuitEntry, error) {
	var v retryShortCircuitEntry
	if strings.TrimSpace(s) == "" {
		return v, fmt.Errorf("empty retry short circuit entry")
	}
	if err := common.UnmarshalJsonStr(s, &v); err != nil {
		return v, err
	}
	return v, nil
}

var (
	retryShortCircuitCacheOnce sync.Once
	retryShortCircuitCache     *cachex.HybridCache[retryShortCircuitEntry]
)

func getRetryShortCircuitCache() *cachex.HybridCache[retryShortCircuitEntry] {
	retryShortCircuitCacheOnce.Do(func() {
		retryShortCircuitCache = cachex.NewHybridCache[retryShortCircuitEntry](cachex.HybridCacheConfig[retryShortCircuitEntry]{
			Namespace: cachex.Namespace(retryShortCircuitNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: retryShortCircuitCodec{},
			Memory: func() *hot.HotCache[string, retryShortCircuitEntry] {
				// 指纹只在"长耗时 + 客户端断连"时产生，量级很小。
				// 实际过期以 SetWithTTL 的配置值为准（读取时惰性判定），janitor 兜底清扫内存。
				return hot.NewHotCache[string, retryShortCircuitEntry](hot.LRU, 10_000).
					WithTTL(time.Hour).
					WithJanitor().
					Build()
			},
		})
	})
	return retryShortCircuitCache
}

// retryShortCircuitKey 请求指纹：同 token + 同模型 + 同请求体 => 同 key。
// 客户改 stream=true 或调整任何参数后请求体不同，自然放行。
func retryShortCircuitKey(tokenId int, modelName string, body []byte) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%s|", tokenId, modelName)
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// requestBodyForFingerprint 从 relay 链路的 body 缓存取原始请求体。
// gin body 在 GetAndValidateRequest 阶段已被消费并缓存进 BodyStorage，绝不能重读 c.Request.Body。
// 取不到时返回 nil（真实读取失败会在主链路的 GetBodyStorage 调用处报错）。
func requestBodyForFingerprint(c *gin.Context) []byte {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil
	}
	body, err := storage.Bytes()
	if err != nil {
		return nil
	}
	return body
}

// RecordRetryShortCircuit 失败收尾钩子：非流式请求被客户端取消后记录指纹。
// 调用方（controller.Relay）保证：开关外的硬条件——非流式、下游连接已断（原始请求 ctx 已取消）。
func RecordRetryShortCircuit(c *gin.Context, tokenId int, modelName string, startTime time.Time) {
	cfg := operation_setting.GetRetryShortCircuitConfig()
	if !cfg.Enabled {
		return
	}
	if cfg.TTLMinutes <= 0 {
		return
	}
	useTime := int(time.Since(startTime).Seconds())
	if useTime < cfg.MinDurationSeconds {
		return
	}
	body := requestBodyForFingerprint(c)
	if len(body) == 0 {
		return
	}
	key := retryShortCircuitKey(tokenId, modelName, body)
	entry := retryShortCircuitEntry{UseTimeSeconds: useTime, TTLMinutes: cfg.TTLMinutes}
	if err := getRetryShortCircuitCache().SetWithTTL(key, entry, time.Duration(cfg.TTLMinutes)*time.Minute); err != nil {
		logger.LogError(c, "retry_short_circuit: record fingerprint failed: "+err.Error())
		return
	}
	logger.LogInfo(c, fmt.Sprintf("retry_short_circuit: fingerprint recorded after client cancel (model=%s, use_time=%ds, ttl=%dmin)", modelName, useTime, cfg.TTLMinutes))
}

// CheckRetryShortCircuit 入口拦截钩子：TTL 内同一请求再来 → 命中，调用方直接 400 拒绝。
// 命中不续期；存储层故障时放行（fail open），不因缓存故障阻断正常请求。
func CheckRetryShortCircuit(c *gin.Context, tokenId int, modelName string) (hit bool, canceledAfterSeconds int, retryAfterMinutes int) {
	cfg := operation_setting.GetRetryShortCircuitConfig()
	if !cfg.Enabled {
		return false, 0, 0
	}
	body := requestBodyForFingerprint(c)
	if len(body) == 0 {
		return false, 0, 0
	}
	key := retryShortCircuitKey(tokenId, modelName, body)
	entry, found, err := getRetryShortCircuitCache().Get(key)
	if err != nil {
		logger.LogError(c, "retry_short_circuit: fingerprint lookup failed: "+err.Error())
		return false, 0, 0
	}
	if !found {
		return false, 0, 0
	}
	return true, entry.UseTimeSeconds, entry.TTLMinutes
}
