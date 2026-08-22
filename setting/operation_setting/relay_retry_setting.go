package operation_setting

import (
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// RelayRetryConfig 渠道重试增强配置：请求级总 deadline / 指数退避 / per-model timeout。
//
// 三项默认全部为 0（禁用），完全向后兼容；开启后联动 controller/relay.go 主循环 + task relay loop。
//
// 关联文档：Docs/2026-05-21-newapi-retry-strategy-current.md
type RelayRetryConfig struct {
	// TotalTimeoutSeconds 整条 relay 请求的总耗时预算（含全部 retry + backoff sleep）。
	// 0 = 禁用。建议 < nginx proxy_read_timeout * 0.9。
	TotalTimeoutSeconds int `json:"total_timeout_seconds"`

	// BackoffBaseMs retry 间退避起步毫秒数。0 = 禁用 backoff，失败立即重试。
	// 实际 sleep = min(base*2^retry, max) + jitter(0, that/2)。
	BackoffBaseMs int `json:"backoff_base_ms"`

	// BackoffMaxMs 单次退避上限毫秒。0 时按 base*2^retry 不封顶（仍受 TotalTimeout 约束）。
	BackoffMaxMs int `json:"backoff_max_ms"`

	// ModelTimeouts 按 model 名定制的单 channel 超时（秒）。JSON 字符串，例：
	//   {"gpt-4o-mini":15,"gpt-5-*":600,"sora-2":900}
	// 优先级：精确匹配 > 最长前缀匹配（key 以 "*" 结尾）> 全局 RelayTimeout 兜底。
	ModelTimeouts string `json:"model_timeouts"`
}

var relayRetrySetting = RelayRetryConfig{
	TotalTimeoutSeconds: 0,
	BackoffBaseMs:       0,
	BackoffMaxMs:        2000,
	ModelTimeouts:       "",
}

func init() {
	config.GlobalConfig.Register("relay_retry_setting", &relayRetrySetting)
}

func GetRelayRetryConfig() *RelayRetryConfig {
	return &relayRetrySetting
}

// ── ModelTimeouts 解析缓存（同 channel_health_setting.CountableStatusCodes 模式） ──

type modelTimeoutPrefixEntry struct {
	prefix  string
	seconds int
}

var (
	modelTimeoutMu     sync.RWMutex
	modelTimeoutRaw    string
	modelTimeoutExact  map[string]int
	modelTimeoutPrefix []modelTimeoutPrefixEntry
)

func ensureModelTimeoutsParsed() (map[string]int, []modelTimeoutPrefixEntry) {
	raw := relayRetrySetting.ModelTimeouts

	modelTimeoutMu.RLock()
	if raw == modelTimeoutRaw {
		exact, prefix := modelTimeoutExact, modelTimeoutPrefix
		modelTimeoutMu.RUnlock()
		return exact, prefix
	}
	modelTimeoutMu.RUnlock()

	modelTimeoutMu.Lock()
	defer modelTimeoutMu.Unlock()
	if raw == modelTimeoutRaw {
		return modelTimeoutExact, modelTimeoutPrefix
	}

	exact := make(map[string]int)
	var prefix []modelTimeoutPrefixEntry

	if strings.TrimSpace(raw) != "" {
		parsed := make(map[string]int)
		if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
			common.SysError("relay_retry_setting.model_timeouts parse failed: " + err.Error())
		} else {
			for k, v := range parsed {
				k = strings.TrimSpace(k)
				if k == "" || v <= 0 {
					continue
				}
				if strings.HasSuffix(k, "*") {
					prefix = append(prefix, modelTimeoutPrefixEntry{
						prefix:  strings.TrimSuffix(k, "*"),
						seconds: v,
					})
				} else {
					exact[k] = v
				}
			}
			sort.SliceStable(prefix, func(i, j int) bool {
				return len(prefix[i].prefix) > len(prefix[j].prefix)
			})
		}
	}

	modelTimeoutExact = exact
	modelTimeoutPrefix = prefix
	modelTimeoutRaw = raw

	return exact, prefix
}

// ResolveModelTimeout 返回该 model 的 per-call timeout（秒）。
// 0 表示未配置，调用方应回退到全局 common.RelayTimeout。
func ResolveModelTimeout(model string) int {
	if model == "" {
		return 0
	}
	exact, prefix := ensureModelTimeoutsParsed()
	if v, ok := exact[model]; ok {
		return v
	}
	for _, e := range prefix {
		if strings.HasPrefix(model, e.prefix) {
			return e.seconds
		}
	}
	return 0
}

// ComputeRetryBackoff 计算第 retry 次失败后到下一次 retry 之间的退避时长。
// retry 从 0 起算。base/max 为毫秒，base<=0 时返回 0 表示不退避。
// 公式：exp = min(base*2^retry, max)；返回 [exp, exp+exp/2] 的随机抖动。
func ComputeRetryBackoff(retry, baseMs, maxMs int) time.Duration {
	if baseMs <= 0 {
		return 0
	}
	if retry < 0 {
		retry = 0
	}
	exp := baseMs
	for i := 0; i < retry && exp > 0 && exp < (1<<30); i++ {
		exp *= 2
	}
	if maxMs > 0 && exp > maxMs {
		exp = maxMs
	}
	if exp <= 0 {
		return 0
	}
	jitter := rand.IntN(exp/2 + 1)
	return time.Duration(exp+jitter) * time.Millisecond
}
