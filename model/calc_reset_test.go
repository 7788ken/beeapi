package model

import (
	"testing"
	"time"
)

func TestCalcNextResetTimeBeijingDaily(t *testing.T) {
	plan := &SubscriptionPlan{QuotaResetPeriod: SubscriptionResetDaily}
	// 一个 UTC 时刻：2026-05-16 03:00:00Z = 北京 2026-05-16 11:00 → 下一次北京 00:00 = 2026-05-17 00:00 CST = 2026-05-16 16:00 UTC
	base := time.Date(2026, 5, 16, 3, 0, 0, 0, time.UTC)
	got := calcNextResetTime(base, plan, 0)
	want := time.Date(2026, 5, 16, 16, 0, 0, 0, time.UTC).Unix()
	if got != want {
		t.Fatalf("daily: got=%d (%s) want=%d (%s)", got, time.Unix(got, 0).UTC(), want, time.Unix(want, 0).UTC())
	}
}

func TestCalcNextResetTimeBeijingDailyAcrossMidnight(t *testing.T) {
	plan := &SubscriptionPlan{QuotaResetPeriod: SubscriptionResetDaily}
	// 北京 2026-05-16 23:30 = UTC 15:30 → 下一次北京 2026-05-17 00:00 = UTC 16:00
	base := time.Date(2026, 5, 16, 15, 30, 0, 0, time.UTC)
	got := calcNextResetTime(base, plan, 0)
	want := time.Date(2026, 5, 16, 16, 0, 0, 0, time.UTC).Unix()
	if got != want {
		t.Fatalf("near-midnight: got=%d (%s) want=%d (%s)", got, time.Unix(got, 0).UTC(), want, time.Unix(want, 0).UTC())
	}
}

func TestCalcNextResetTimeBeijingWeekly(t *testing.T) {
	plan := &SubscriptionPlan{QuotaResetPeriod: SubscriptionResetWeekly}
	cases := []struct {
		name string
		base time.Time // UTC 输入
		want time.Time // 期望下次重置（UTC）
	}{
		{
			// 2026-05-16 是周六（北京 11:00），下次 = 周一 2026-05-18 00:00 CST = 2026-05-17 16:00 UTC
			name: "saturday-noon",
			base: time.Date(2026, 5, 16, 3, 0, 0, 0, time.UTC),
			want: time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
		},
		{
			// 北京周日 23:30 (UTC 周日 15:30) → 下次周一 00:00 CST = UTC 16:00
			name: "sunday-late-night-beijing",
			base: time.Date(2026, 5, 17, 15, 30, 0, 0, time.UTC),
			want: time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
		},
		{
			// 北京周一 09:00 (UTC 01:00) → 下次下周一 00:00 CST = 周日 UTC 16:00（7 天后）
			name: "monday-morning-beijing",
			base: time.Date(2026, 5, 18, 1, 0, 0, 0, time.UTC),
			want: time.Date(2026, 5, 24, 16, 0, 0, 0, time.UTC),
		},
		{
			// UTC 周日 18:00 = 北京周一 02:00 → 下次下周一 00:00 CST = UTC 周日 16:00（6 天后）
			name: "utc-sunday-evening-is-beijing-monday",
			base: time.Date(2026, 5, 17, 18, 0, 0, 0, time.UTC),
			want: time.Date(2026, 5, 24, 16, 0, 0, 0, time.UTC),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := calcNextResetTime(c.base, plan, 0)
			if got != c.want.Unix() {
				t.Fatalf("weekly %s: got=%d (%s) want=%d (%s)", c.name,
					got, time.Unix(got, 0).UTC(), c.want.Unix(), c.want)
			}
		})
	}
}

func TestCalcNextResetTimeBeijingMonthly(t *testing.T) {
	plan := &SubscriptionPlan{QuotaResetPeriod: SubscriptionResetMonthly}
	// 北京 2026-05-16 11:00 → 下一次北京 2026-06-01 00:00 = UTC 2026-05-31 16:00
	base := time.Date(2026, 5, 16, 3, 0, 0, 0, time.UTC)
	got := calcNextResetTime(base, plan, 0)
	want := time.Date(2026, 5, 31, 16, 0, 0, 0, time.UTC).Unix()
	if got != want {
		t.Fatalf("monthly: got=%d want=%d", got, want)
	}
}
