package common

import (
	"fmt"
	"math"

	"github.com/shopspring/decimal"
)

// Quota conversions are centralized here so every billing path shares one
// saturation policy. Quota columns (user/token/log) are 32-bit integers in the
// database, so an oversized product must clamp to the int32 range instead of
// wrapping around and turning a charge into a credit.
const (
	MaxQuota = math.MaxInt32
	MinQuota = math.MinInt32
)

type QuotaClampKind string

const (
	QuotaClampOverflow  QuotaClampKind = "overflow"
	QuotaClampUnderflow QuotaClampKind = "underflow"
	QuotaClampNaN       QuotaClampKind = "nan"
)

// QuotaClamp describes a quota conversion that could not be represented by
// the int32-backed quota columns. Settlement may retain it for audit logs;
// pre-consume paths return it as an error before deducting quota.
type QuotaClamp struct {
	Op       string         `json:"op"`
	Kind     QuotaClampKind `json:"kind"`
	Original float64        `json:"original"`
	Clamped  int            `json:"clamped"`
}

func (c *QuotaClamp) Error() string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf("quota conversion (%s) %s: original=%g, clamped=%d", c.Op, c.Kind, c.Original, c.Clamped)
}

func (c *QuotaClamp) AuditMap() map[string]interface{} {
	if c == nil {
		return nil
	}
	return map[string]interface{}{
		"op":       c.Op,
		"kind":     c.Kind,
		"original": c.Original,
		"clamped":  c.Clamped,
	}
}

func saturateQuota(value float64, op string) (int, *QuotaClamp) {
	var clamp *QuotaClamp
	switch {
	case math.IsNaN(value):
		clamp = &QuotaClamp{Op: op, Kind: QuotaClampNaN, Original: value, Clamped: 0}
	case value >= MaxQuota:
		clamp = &QuotaClamp{Op: op, Kind: QuotaClampOverflow, Original: value, Clamped: MaxQuota}
	case value <= MinQuota:
		clamp = &QuotaClamp{Op: op, Kind: QuotaClampUnderflow, Original: value, Clamped: MinQuota}
	default:
		return int(value), nil
	}
	SysError(clamp.Error())
	return clamp.Clamped, clamp
}

func strictQuota(quota int, clamp *QuotaClamp) (int, error) {
	if clamp != nil {
		return 0, clamp
	}
	return quota, nil
}

// QuotaFromFloat converts a computed quota value to int, truncating toward
// zero, with saturation. Quota products can include user-controlled
// multipliers (image n, video seconds, resolution ratios); an oversized
// product must never wrap around and turn a charge into a credit. The bound is
// int32 because quota columns (user/token/log) are 32-bit integers in the
// database.
func QuotaFromFloat(value float64) int {
	quota, _ := QuotaFromFloatChecked(value)
	return quota
}

func QuotaFromFloatChecked(value float64) (int, *QuotaClamp) {
	return saturateQuota(value, "QuotaFromFloat")
}

func QuotaFromFloatStrict(value float64) (int, error) {
	return strictQuota(QuotaFromFloatChecked(value))
}

func QuotaRound(value float64) int {
	quota, _ := QuotaRoundChecked(value)
	return quota
}

func QuotaRoundChecked(value float64) (int, *QuotaClamp) {
	return saturateQuota(math.Round(value), "QuotaRound")
}

func QuotaRoundStrict(value float64) (int, error) {
	return strictQuota(QuotaRoundChecked(value))
}

// QuotaFromDecimal converts a computed quota decimal to int with saturation.
// The decimal is rounded (half away from zero) before conversion. Use for
// decimal products in billing math where an oversized multiplier could
// otherwise wrap around.
func QuotaFromDecimal(d decimal.Decimal) int {
	quota, _ := QuotaFromDecimalChecked(d)
	return quota
}

func QuotaFromDecimalChecked(d decimal.Decimal) (int, *QuotaClamp) {
	f, _ := d.Round(0).Float64()
	return saturateQuota(f, "QuotaFromDecimal")
}

// QuotaFromDecimalTruncate converts a computed quota decimal to int with
// saturation, truncating toward zero (matching the legacy decimal.IntPart()
// behavior). Use where the original billing path truncated rather than
// rounded, to preserve the existing charge口径 while still guarding overflow.
func QuotaFromDecimalTruncate(d decimal.Decimal) int {
	f, _ := d.Truncate(0).Float64()
	quota, _ := saturateQuota(f, "QuotaFromDecimalTruncate")
	return quota
}

func QuotaFromDecimalTruncateChecked(d decimal.Decimal) (int, *QuotaClamp) {
	f, _ := d.Truncate(0).Float64()
	return saturateQuota(f, "QuotaFromDecimalTruncate")
}
