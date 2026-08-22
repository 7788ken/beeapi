package service

import (
	"sync"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// URL 健康/延迟状态：渠道内多 base_url 的"又稳又快"路由依据。
//   稳：连续失败熔断 + 冷却自愈；
//   快：TTFB 指数移动平均 + fastest 择优 + 迟滞防横跳 + 保底探索保持数据新鲜。
// 纯内存、单实例（.76 单机），各 relay 副本独立维护；如需多副本强一致可后续接 Redis。
//
// 路由参数（熔断阈值/冷却/EWMA α/迟滞/探索间隔）由 admin 后台可调、实时生效，
// 见 setting/operation_setting/url_health_setting.go。

type urlStat struct {
	consecFails   int
	openUntil     time.Time // 非零且 now<openUntil 表示熔断中
	ewmaTTFB      float64   // 首字节时间（毫秒）指数移动平均
	sampleCount   int64
	lastSampledAt time.Time
}

type urlHealthRegistry struct {
	mu      sync.Mutex
	stats   map[int]map[string]*urlStat // channelId -> url -> stat
	current map[int]string              // channelId -> 当前选中的 URL（迟滞用）
}

var urlHealth = &urlHealthRegistry{
	stats:   make(map[int]map[string]*urlStat),
	current: make(map[int]string),
}

func (r *urlHealthRegistry) stat(channelId int, url string) *urlStat {
	m := r.stats[channelId]
	if m == nil {
		m = make(map[string]*urlStat)
		r.stats[channelId] = m
	}
	s := m[url]
	if s == nil {
		s = &urlStat{}
		m[url] = s
	}
	return s
}

// PickURLForChannel 从该渠道的候选 base_url 列表里选一个用于本次请求：
// 跳过熔断中的 URL → 保底探索久未采样的 → fastest（EWMA 最低）+ 迟滞防横跳。
// 全部熔断时挑冷却最早结束的去半开试探。urls 为空返回 ""。
func PickURLForChannel(channelId int, urls []string) string {
	if len(urls) == 0 {
		return ""
	}
	if len(urls) == 1 {
		return urls[0]
	}
	r := urlHealth
	cfg := operation_setting.GetURLHealthConfig()
	explorationGap := time.Duration(cfg.ExplorationGapSeconds) * time.Second
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()

	// 1) 候选 = 未在熔断冷却中的 URL
	healthy := make([]string, 0, len(urls))
	for _, u := range urls {
		s := r.stat(channelId, u)
		if !s.openUntil.IsZero() && now.Before(s.openUntil) {
			continue // 熔断中跳过
		}
		healthy = append(healthy, u)
	}
	// 全部熔断：挑冷却最早结束的去半开试探（至少给客户端一个明确的上游错误，而非空转）
	if len(healthy) == 0 {
		best := urls[0]
		bestUntil := r.stat(channelId, urls[0]).openUntil
		for _, u := range urls[1:] {
			if t := r.stat(channelId, u).openUntil; t.Before(bestUntil) {
				best, bestUntil = u, t
			}
		}
		return best
	}

	// 2) 保底探索：冷启动或久未采样的健康 URL 优先探一次（fastest 下常态只喂最快 URL，须防其余数据过期）
	for _, u := range healthy {
		s := r.stat(channelId, u)
		if s.sampleCount == 0 || now.Sub(s.lastSampledAt) > explorationGap {
			return u
		}
	}

	// 3) fastest：选 EWMA 最低
	best := healthy[0]
	for _, u := range healthy[1:] {
		if r.stat(channelId, u).ewmaTTFB < r.stat(channelId, best).ewmaTTFB {
			best = u
		}
	}
	// 4) 迟滞：当前选中的仍健康、且 best 没有"明显更快"时保持当前，避免两个延迟接近的 URL 来回横跳
	if cur := r.current[channelId]; cur != "" && cur != best && urlSliceContains(healthy, cur) {
		curEwma := r.stat(channelId, cur).ewmaTTFB
		bestEwma := r.stat(channelId, best).ewmaTTFB
		margin := curEwma * cfg.HysteresisRatio
		if margin < cfg.HysteresisMinMs {
			margin = cfg.HysteresisMinMs
		}
		if curEwma-bestEwma < margin {
			return cur
		}
	}
	r.current[channelId] = best
	return best
}

// RecordURLResult 记录一次请求结果，更新熔断与 TTFB。
// ttfbMs 仅在 success 时有意义（失败时无有效首字节，传 0 即可）。
// 仅"节点无响应"类失败应以 success=false 上报；上游业务错误（4xx/5xx）不算节点失败，不上报。
func RecordURLResult(channelId int, url string, success bool, ttfbMs float64) {
	if url == "" {
		return
	}
	r := urlHealth
	cfg := operation_setting.GetURLHealthConfig()
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.stat(channelId, url)
	s.lastSampledAt = time.Now()
	if success {
		s.consecFails = 0
		s.openUntil = time.Time{} // 解除熔断
		if s.sampleCount == 0 || s.ewmaTTFB == 0 {
			s.ewmaTTFB = ttfbMs
		} else {
			s.ewmaTTFB = cfg.EwmaAlpha*ttfbMs + (1-cfg.EwmaAlpha)*s.ewmaTTFB
		}
		s.sampleCount++
		return
	}
	s.consecFails++
	if s.consecFails >= cfg.FailThreshold {
		s.openUntil = s.lastSampledAt.Add(time.Duration(cfg.CooldownSeconds) * time.Second)
	}
}

func urlSliceContains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
