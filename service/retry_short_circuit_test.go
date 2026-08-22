package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// newRSCTestContext 构造带缓存请求体的 gin 测试上下文（走 KeyRequestBody 缓存路径，不读 Request.Body）。
func newRSCTestContext(body []byte) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Set(common.KeyRequestBody, body)
	return c
}

// withRSCConfig 临时改写全局配置，测试结束后恢复。
func withRSCConfig(t *testing.T, mutate func(cfg *operation_setting.RetryShortCircuitConfig)) {
	t.Helper()
	cfg := operation_setting.GetRetryShortCircuitConfig()
	orig := *cfg
	t.Cleanup(func() { *cfg = orig })
	mutate(cfg)
}

func TestRetryShortCircuitKeyDeterminism(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}]}`)
	k1 := retryShortCircuitKey(42, "claude-sonnet-4", body)
	k2 := retryShortCircuitKey(42, "claude-sonnet-4", body)
	if k1 != k2 {
		t.Errorf("same input must yield same key: %s vs %s", k1, k2)
	}
	if len(k1) != 64 {
		t.Errorf("key must be sha256 hex (64 chars), got %d", len(k1))
	}
	if k := retryShortCircuitKey(42, "claude-sonnet-4", []byte(`{"other":true}`)); k == k1 {
		t.Error("different body must yield different key")
	}
	if k := retryShortCircuitKey(42, "gpt-4o", body); k == k1 {
		t.Error("different model must yield different key")
	}
	if k := retryShortCircuitKey(7, "claude-sonnet-4", body); k == k1 {
		t.Error("different token must yield different key")
	}
}

func TestRetryShortCircuitDisabledNoOp(t *testing.T) {
	withRSCConfig(t, func(cfg *operation_setting.RetryShortCircuitConfig) {
		cfg.Enabled = false
		cfg.MinDurationSeconds = 300
		cfg.TTLMinutes = 15
	})
	body := []byte(`{"model":"m","messages":["disabled-case"]}`)

	// 记录钩子 no-op：即便满足时长条件也不落指纹
	RecordRetryShortCircuit(newRSCTestContext(body), 1, "m", time.Now().Add(-620*time.Second))
	if _, found, err := getRetryShortCircuitCache().Get(retryShortCircuitKey(1, "m", body)); err != nil || found {
		t.Errorf("disabled record must be no-op, found=%v err=%v", found, err)
	}

	// 拦截钩子 no-op：即便缓存里有指纹也不命中
	if err := getRetryShortCircuitCache().SetWithTTL(retryShortCircuitKey(1, "m", body), retryShortCircuitEntry{UseTimeSeconds: 600, TTLMinutes: 15}, time.Minute); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if hit, _, _ := CheckRetryShortCircuit(newRSCTestContext(body), 1, "m"); hit {
		t.Error("disabled check must be no-op even when fingerprint exists")
	}
}

func TestRetryShortCircuitRecordAndHit(t *testing.T) {
	withRSCConfig(t, func(cfg *operation_setting.RetryShortCircuitConfig) {
		cfg.Enabled = true
		cfg.MinDurationSeconds = 300
		cfg.TTLMinutes = 15
	})
	body := []byte(`{"model":"m","messages":["record-hit-case"],"stream":false}`)

	RecordRetryShortCircuit(newRSCTestContext(body), 42, "claude-x", time.Now().Add(-620*time.Second))

	hit, canceledAfter, retryAfterMin := CheckRetryShortCircuit(newRSCTestContext(body), 42, "claude-x")
	if !hit {
		t.Fatal("identical request must hit fingerprint")
	}
	if canceledAfter < 620 || canceledAfter > 622 {
		t.Errorf("canceledAfter = %d, want ~620", canceledAfter)
	}
	if retryAfterMin != 15 {
		t.Errorf("retryAfterMin = %d, want 15", retryAfterMin)
	}

	if hit, _, _ := CheckRetryShortCircuit(newRSCTestContext([]byte(`{"model":"m","messages":["record-hit-case"],"stream":true}`)), 42, "claude-x"); hit {
		t.Error("different body (e.g. stream=true) must not hit")
	}
	if hit, _, _ := CheckRetryShortCircuit(newRSCTestContext(body), 7, "claude-x"); hit {
		t.Error("different token must not hit")
	}
}

func TestRetryShortCircuitBelowMinDurationNotRecorded(t *testing.T) {
	withRSCConfig(t, func(cfg *operation_setting.RetryShortCircuitConfig) {
		cfg.Enabled = true
		cfg.MinDurationSeconds = 300
		cfg.TTLMinutes = 15
	})
	body := []byte(`{"model":"m","messages":["short-run-case"]}`)

	RecordRetryShortCircuit(newRSCTestContext(body), 42, "m", time.Now().Add(-10*time.Second))
	if hit, _, _ := CheckRetryShortCircuit(newRSCTestContext(body), 42, "m"); hit {
		t.Error("request canceled before min_duration_seconds must not be recorded")
	}
}

func TestRetryShortCircuitEmptyBodyNotRecorded(t *testing.T) {
	withRSCConfig(t, func(cfg *operation_setting.RetryShortCircuitConfig) {
		cfg.Enabled = true
		cfg.MinDurationSeconds = 300
		cfg.TTLMinutes = 15
	})

	RecordRetryShortCircuit(newRSCTestContext([]byte{}), 42, "m", time.Now().Add(-620*time.Second))
	if _, found, err := getRetryShortCircuitCache().Get(retryShortCircuitKey(42, "m", nil)); err != nil || found {
		t.Errorf("empty body must not be recorded, found=%v err=%v", found, err)
	}
	if hit, _, _ := CheckRetryShortCircuit(newRSCTestContext([]byte{}), 42, "m"); hit {
		t.Error("empty body check must not hit")
	}
}

func TestRetryShortCircuitMemoryExpiry(t *testing.T) {
	// 无 Redis（测试环境 RDB 为 nil）时走进程内 hot cache：过期后读取必须 miss
	key := retryShortCircuitKey(9, "expiry-model", []byte("expiry-body"))
	if err := getRetryShortCircuitCache().SetWithTTL(key, retryShortCircuitEntry{UseTimeSeconds: 601, TTLMinutes: 1}, 40*time.Millisecond); err != nil {
		t.Fatalf("SetWithTTL: %v", err)
	}
	if entry, found, err := getRetryShortCircuitCache().Get(key); err != nil || !found || entry.UseTimeSeconds != 601 {
		t.Fatalf("fresh entry must be readable, entry=%+v found=%v err=%v", entry, found, err)
	}
	time.Sleep(120 * time.Millisecond)
	if _, found, err := getRetryShortCircuitCache().Get(key); err != nil || found {
		t.Errorf("expired entry must miss, found=%v err=%v", found, err)
	}
}
