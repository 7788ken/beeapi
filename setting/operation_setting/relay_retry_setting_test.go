package operation_setting

import (
	"testing"
	"time"
)

func TestComputeRetryBackoff(t *testing.T) {
	t.Run("base 0 disables", func(t *testing.T) {
		if got := ComputeRetryBackoff(0, 0, 1000); got != 0 {
			t.Fatalf("base=0 must return 0, got %v", got)
		}
	})

	t.Run("base 200ms grows then caps at max", func(t *testing.T) {
		base, max := 200, 1500
		// retry=0 → exp=200, allow [200, 300]
		// retry=1 → exp=400, allow [400, 600]
		// retry=2 → exp=800, allow [800, 1200]
		// retry=3 → exp=1500 (capped), allow [1500, 2250]
		for i, want := range []struct{ minMs, maxMs int }{
			{200, 300},
			{400, 600},
			{800, 1200},
			{1500, 2250},
			{1500, 2250},
		} {
			got := ComputeRetryBackoff(i, base, max)
			ms := int(got / time.Millisecond)
			if ms < want.minMs || ms > want.maxMs {
				t.Errorf("retry=%d: got %dms, want in [%d, %d]", i, ms, want.minMs, want.maxMs)
			}
		}
	})

	t.Run("negative retry treated as 0", func(t *testing.T) {
		got := ComputeRetryBackoff(-5, 100, 0)
		ms := int(got / time.Millisecond)
		if ms < 100 || ms > 150 {
			t.Errorf("negative retry should fall back to retry=0; got %dms", ms)
		}
	})
}

func TestResolveModelTimeout(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		model  string
		expect int
	}{
		{"empty config", "", "gpt-4o-mini", 0},
		{"exact hit", `{"gpt-4o-mini":15}`, "gpt-4o-mini", 15},
		{"miss exact only", `{"gpt-4o-mini":15}`, "gpt-4o", 0},
		{"prefix hit", `{"gpt-5-*":600}`, "gpt-5-thinking", 600},
		{"prefix miss", `{"gpt-5-*":600}`, "gpt-4o", 0},
		{"exact wins over prefix", `{"gpt-5-*":600,"gpt-5-codex":120}`, "gpt-5-codex", 120},
		{"longest prefix wins", `{"gpt-*":300,"gpt-5-*":600}`, "gpt-5-mini", 600},
		{"invalid JSON ignored", `{not-json`, "gpt-4o-mini", 0},
		{"non-positive ignored", `{"gpt-4o-mini":0,"gpt-4o":-5,"claude":10}`, "claude", 10},
		{"empty model returns 0", `{"gpt-4o-mini":15}`, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Mutate the package-level config and invalidate the cache.
			GetRelayRetryConfig().ModelTimeouts = tc.raw
			modelTimeoutMu.Lock()
			modelTimeoutRaw = ""
			modelTimeoutMu.Unlock()

			got := ResolveModelTimeout(tc.model)
			if got != tc.expect {
				t.Errorf("raw=%q model=%q: got %d, want %d", tc.raw, tc.model, got, tc.expect)
			}
		})
	}
}
