package common

import (
	"errors"
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestQuotaStrictRejectsSaturation(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		kind  QuotaClampKind
	}{
		{name: "overflow", value: float64(MaxQuota) * 2, kind: QuotaClampOverflow},
		{name: "underflow", value: float64(MinQuota) * 2, kind: QuotaClampUnderflow},
		{name: "nan", value: math.NaN(), kind: QuotaClampNaN},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quota, err := QuotaFromFloatStrict(tt.value)
			require.Zero(t, quota)
			var clamp *QuotaClamp
			require.True(t, errors.As(err, &clamp))
			require.Equal(t, tt.kind, clamp.Kind)
		})
	}
}

func TestQuotaCheckedPreservesClampForAudit(t *testing.T) {
	quota, clamp := QuotaRoundChecked(float64(MaxQuota) * 2)
	require.Equal(t, MaxQuota, quota)
	require.NotNil(t, clamp)
	require.Equal(t, QuotaClampOverflow, clamp.Kind)
	require.Equal(t, MaxQuota, clamp.AuditMap()["clamped"])
}

func TestQuotaDecimalTruncateCheckedPreservesLegacyRounding(t *testing.T) {
	quota, clamp := QuotaFromDecimalTruncateChecked(decimal.RequireFromString("12.9"))
	require.Equal(t, 12, quota)
	require.Nil(t, clamp)
}
