package service

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func resetURLHealth() {
	urlHealth.mu.Lock()
	urlHealth.stats = make(map[int]map[string]*urlStat)
	urlHealth.current = make(map[int]string)
	urlHealth.mu.Unlock()
}

// fastest：两个都有样本时，选 EWMA 最低的
func TestPickURLFastest(t *testing.T) {
	resetURLHealth()
	ch := 1
	urls := []string{"https://a", "https://b"}
	RecordURLResult(ch, "https://a", true, 100)
	RecordURLResult(ch, "https://b", true, 500)
	if got := PickURLForChannel(ch, urls); got != "https://a" {
		t.Fatalf("fastest should pick a(100ms), got %s", got)
	}
}

// 熔断：连续失败达阈值后该 URL 被跳过，即使它历史更快
func TestCircuitBreaker(t *testing.T) {
	resetURLHealth()
	ch := 2
	urls := []string{"https://a", "https://b"}
	RecordURLResult(ch, "https://a", true, 100)
	RecordURLResult(ch, "https://b", true, 200)
	for i := 0; i < operation_setting.GetURLHealthConfig().FailThreshold; i++ {
		RecordURLResult(ch, "https://a", false, 0)
	}
	if got := PickURLForChannel(ch, urls); got != "https://b" {
		t.Fatalf("a circuit-broken, should pick b, got %s", got)
	}
}

// EWMA：第二个样本按 alpha 平滑
func TestEWMA(t *testing.T) {
	resetURLHealth()
	ch := 3
	RecordURLResult(ch, "https://a", true, 100) // 首样本 = 100
	RecordURLResult(ch, "https://a", true, 200) // 0.2*200 + 0.8*100 = 120
	urlHealth.mu.Lock()
	got := urlHealth.stats[ch]["https://a"].ewmaTTFB
	urlHealth.mu.Unlock()
	if got < 119 || got > 121 {
		t.Fatalf("ewma expected ~120, got %v", got)
	}
}

// 迟滞：次快只快一点点（< margin）时保持当前，不横跳
func TestHysteresis(t *testing.T) {
	resetURLHealth()
	ch := 4
	urls := []string{"https://a", "https://b"}
	RecordURLResult(ch, "https://a", true, 100)
	RecordURLResult(ch, "https://b", true, 110)
	if first := PickURLForChannel(ch, urls); first != "https://a" {
		t.Fatalf("first pick should be a, got %s", first)
	}
	// b 变成只比 a 快 5ms（< max(100*0.2, 50)=50 的迟滞阈值）
	urlHealth.mu.Lock()
	urlHealth.stats[ch]["https://b"].ewmaTTFB = 95
	urlHealth.mu.Unlock()
	if second := PickURLForChannel(ch, urls); second != "https://a" {
		t.Fatalf("hysteresis should keep a (b only 5ms faster < 50ms margin), got %s", second)
	}
}

// 冷启动探索：无样本的 URL 优先被探一次，避免数据空白
func TestExplorationColdStart(t *testing.T) {
	resetURLHealth()
	ch := 5
	urls := []string{"https://a", "https://b"}
	RecordURLResult(ch, "https://a", true, 100) // a 有样本，b 无
	if got := PickURLForChannel(ch, urls); got != "https://b" {
		t.Fatalf("cold-start b should be explored first, got %s", got)
	}
}

// 全部熔断时兜底：仍返回一个 URL（冷却最早结束的），不空转
func TestAllOpenFallback(t *testing.T) {
	resetURLHealth()
	ch := 6
	urls := []string{"https://a", "https://b"}
	for i := 0; i < operation_setting.GetURLHealthConfig().FailThreshold; i++ {
		RecordURLResult(ch, "https://a", false, 0)
		RecordURLResult(ch, "https://b", false, 0)
	}
	if got := PickURLForChannel(ch, urls); got != "https://a" && got != "https://b" {
		t.Fatalf("all-open fallback should still return a url, got %q", got)
	}
}
