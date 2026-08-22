package service

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// 这些测试在无 Redis 环境运行（common.RedisEnabled 默认 false），走进程内 thMem 兜底，
// 直接验证「按状态码计数 → 达阈值写冷却 → CheckTokenCooldown 命中」这条链路。

func withTokenHealthConfig(t *testing.T, mutate func(c *operation_setting.TokenHealthConfig)) {
	t.Helper()
	cfg := operation_setting.GetTokenHealthConfig()
	saved := *cfg
	mutate(cfg)
	t.Cleanup(func() { *cfg = saved })
}

func TestThIsCountedStatus(t *testing.T) {
	cfg := &operation_setting.TokenHealthConfig{ExcludedStatusCodes: "401,403"}
	cases := []struct {
		status int
		want   bool
	}{
		{200, false}, // 成功不计
		{301, false}, // 3xx 不计
		{400, true},  // 客户端错误计入
		{404, true},
		{413, true},
		{422, true},
		{429, false}, // 限流始终不计
		{401, false}, // 在排除列表
		{403, false}, // 在排除列表
		{500, true}, // 5xx 现也计入
		{502, true},
		{0, false}, // 未写状态码视为成功
	}
	for _, c := range cases {
		if got := thIsCountedStatus(c.status, cfg); got != c.want {
			t.Errorf("thIsCountedStatus(%d) = %v, want %v", c.status, got, c.want)
		}
	}

	// 5xx 也能通过排除列表排除（上游故障期的逃生舱）。
	cfg5xx := &operation_setting.TokenHealthConfig{ExcludedStatusCodes: "502,503"}
	if thIsCountedStatus(503, cfg5xx) {
		t.Errorf("503 在排除列表内应不计入")
	}
	if !thIsCountedStatus(500, cfg5xx) {
		t.Errorf("500 不在排除列表应计入")
	}
}

func TestRecordTokenStatusTriggersCooldown(t *testing.T) {
	withTokenHealthConfig(t, func(c *operation_setting.TokenHealthConfig) {
		c.Enabled = true
		c.WindowMinutes = 10
		c.MinRequests = 5
		c.ErrorRateThreshold = 0.8
		c.CooldownMinutes = 5
		c.ExcludedStatusCodes = ""
	})

	const tokenId = 900001
	thMem.clear(tokenId)

	// 5 次请求，4 次 400（错误率 80% ≥ 阈值 0.8），应触发冷却。
	for i := 0; i < 4; i++ {
		RecordTokenStatus(tokenId, 400)
	}
	RecordTokenStatus(tokenId, 200)

	cooling, remaining := CheckTokenCooldown(tokenId)
	if !cooling {
		t.Fatalf("达阈值后应进入冷却，但 CheckTokenCooldown 返回未冷却")
	}
	if remaining <= 0 {
		t.Errorf("冷却剩余秒数应 > 0，得到 %d", remaining)
	}
}

func TestRecordTokenStatusBelowMinRequests(t *testing.T) {
	withTokenHealthConfig(t, func(c *operation_setting.TokenHealthConfig) {
		c.Enabled = true
		c.WindowMinutes = 10
		c.MinRequests = 50
		c.ErrorRateThreshold = 0.8
		c.CooldownMinutes = 5
	})

	const tokenId = 900002
	thMem.clear(tokenId)

	// 全是 400，但请求数 < MinRequests(50)，不应触发。
	for i := 0; i < 10; i++ {
		RecordTokenStatus(tokenId, 400)
	}
	if cooling, _ := CheckTokenCooldown(tokenId); cooling {
		t.Fatalf("请求数未达 MinRequests 不应触发冷却")
	}
}

func TestRecordTokenStatus5xxCounted(t *testing.T) {
	withTokenHealthConfig(t, func(c *operation_setting.TokenHealthConfig) {
		c.Enabled = true
		c.WindowMinutes = 10
		c.MinRequests = 5
		c.ErrorRateThreshold = 0.5
		c.CooldownMinutes = 5
		c.ExcludedStatusCodes = ""
	})

	const tokenId = 900003
	thMem.clear(tokenId)

	// 全是 500（上游/内部错误）现在也计入错误率，应触发冷却。
	for i := 0; i < 20; i++ {
		RecordTokenStatus(tokenId, 500)
	}
	if cooling, _ := CheckTokenCooldown(tokenId); !cooling {
		t.Fatalf("5xx 现已计入错误率，20 次 500 应触发冷却")
	}
}

func TestRecordTokenStatusDisabledNoop(t *testing.T) {
	withTokenHealthConfig(t, func(c *operation_setting.TokenHealthConfig) {
		c.Enabled = false
	})

	const tokenId = 900004
	thMem.clear(tokenId)

	for i := 0; i < 100; i++ {
		RecordTokenStatus(tokenId, 400)
	}
	if cooling, _ := CheckTokenCooldown(tokenId); cooling {
		t.Fatalf("feature 关闭时不应记录或触发冷却")
	}
}
