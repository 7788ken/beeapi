package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComputeLevelFromStreak(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		streak   int64
		base     int
		step     int
		maxLevel int
		want     int
	}{
		{"below base", 4, 5, 5, 10, 0},
		{"at base", 5, 5, 5, 10, 1},
		{"one step above", 10, 5, 5, 10, 2},
		{"two steps above", 15, 5, 5, 10, 3},
		{"capped at max", 100, 5, 5, 10, 10},
		{"base=2 step=3 streak=2", 2, 2, 3, 10, 1},
		{"base=2 step=3 streak=5", 5, 2, 3, 10, 2},
		{"base=2 step=3 streak=8", 8, 2, 3, 10, 3},
		{"zero streak", 0, 5, 5, 10, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeLevelFromStreak(tc.streak, tc.base, tc.step, tc.maxLevel)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestDegradeFactors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		level      int
		maxLevel   int
		minFactor  float64
		wantFactor float64
		wantOffset int
	}{
		{"L0", 0, 10, 0.05, 1.0, 0},
		{"L1", 1, 10, 0.05, 0.905, -1},
		{"L2", 2, 10, 0.05, 0.81, -2},
		{"L5", 5, 10, 0.05, 0.525, -5},
		{"L10 hits min", 10, 10, 0.05, 0.05, -10},
		{"beyond max clamped", 15, 10, 0.05, 0.05, -15},
		{"negative level", -1, 10, 0.05, 1.0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			factor, offset := DegradeFactors(tc.level, tc.maxLevel, tc.minFactor)
			require.InDelta(t, tc.wantFactor, factor, 0.001, "factor")
			require.Equal(t, tc.wantOffset, offset, "offset")
		})
	}
}
