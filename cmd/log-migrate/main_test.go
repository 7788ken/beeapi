package main

import "testing"

// verify-only 曾被排除在 state 锁之外（9board BE-17）。它虽然不写 state，但会读
// state.HighWater 并据此对账；与回填并发时会读到撕裂的高水位，得出错误的对账结论。
// 对账是恢复流量的硬门禁，错误的"通过"比跑不完更危险。
func TestNeedsStateLock(t *testing.T) {
	for _, tc := range []struct {
		name     string
		initOnly bool
		dryRun   bool
		want     bool
	}{
		{name: "backfill", want: true},
		{name: "verify-only", want: true},
		{name: "advance", want: true},
		{name: "init-schema-only", initOnly: true, want: false},
		{name: "dry-run", dryRun: true, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsStateLock(tc.initOnly, tc.dryRun); got != tc.want {
				t.Fatalf("needsStateLock(initOnly=%v, dryRun=%v) = %v, want %v",
					tc.initOnly, tc.dryRun, got, tc.want)
			}
		})
	}
}
