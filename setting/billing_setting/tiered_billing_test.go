package billing_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/require"
)

func TestGPT56AliasInheritsSolTieredBilling(t *testing.T) {
	previous := billingSetting
	t.Cleanup(func() {
		billingSetting = previous
	})

	const solExpr = `tier("standard", p * 5 + c * 30 + cr * 0.5 + cc * 6.25)`
	billingSetting = BillingSetting{
		BillingMode: map[string]string{
			"gpt-5.6-sol": BillingModeTieredExpr,
		},
		BillingExpr: map[string]string{
			"gpt-5.6-sol": solExpr,
		},
	}

	require.Equal(t, BillingModeTieredExpr, GetBillingMode("gpt-5.6"))
	expr, ok := GetBillingExpr("gpt-5.6")
	require.True(t, ok)
	require.Equal(t, solExpr, expr)
}

func TestGPT56DefaultExpressionsMatchOfficialCachePricing(t *testing.T) {
	tests := []struct {
		name          string
		expr          string
		shortC        float64
		longC         float64
		shortCR       float64
		shortCC       float64
		longCR        float64
		longCC        float64
		priorityInput float64
	}{
		{
			name:          "sol",
			expr:          gpt56SolExpr,
			shortC:        20,
			longC:         30,
			shortCR:       0.4,
			shortCC:       5,
			longCR:        0.8,
			longCC:        10,
			priorityInput: 8,
		},
		{
			name:          "terra",
			expr:          gpt56TerraExpr,
			shortC:        12,
			longC:         18,
			shortCR:       0.2,
			shortCC:       2.5,
			longCR:        0.4,
			longCC:        5,
			priorityInput: 4,
		},
		{
			name:          "luna",
			expr:          gpt56LunaExpr,
			shortC:        1.2,
			longC:         1.8,
			shortCR:       0.02,
			shortCC:       0.25,
			longCR:        0.04,
			longCC:        0.5,
			priorityInput: 0.4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shortC, _, err := billingexpr.RunExpr(tt.expr, billingexpr.TokenParams{Len: 1, C: 1})
			require.NoError(t, err)
			require.Equal(t, tt.shortC, shortC)

			// 长上下文档输出价 = 标准档 × 1.5（输入是 × 2，别写混）
			longC, _, err := billingexpr.RunExpr(tt.expr, billingexpr.TokenParams{Len: 272001, C: 1})
			require.NoError(t, err)
			require.Equal(t, tt.longC, longC)
			// 浮点：1.2*1.5 不等于字面量 1.8，用容差比
			require.InDelta(t, tt.shortC*1.5, longC, 1e-9)

			shortCR, _, err := billingexpr.RunExpr(tt.expr, billingexpr.TokenParams{Len: 1, CR: 1})
			require.NoError(t, err)
			require.Equal(t, tt.shortCR, shortCR)

			shortCC, _, err := billingexpr.RunExpr(tt.expr, billingexpr.TokenParams{Len: 1, CC: 1})
			require.NoError(t, err)
			require.Equal(t, tt.shortCC, shortCC)

			longCR, _, err := billingexpr.RunExpr(tt.expr, billingexpr.TokenParams{Len: 272001, CR: 1})
			require.NoError(t, err)
			require.Equal(t, tt.longCR, longCR)

			longCC, _, err := billingexpr.RunExpr(tt.expr, billingexpr.TokenParams{Len: 272001, CC: 1})
			require.NoError(t, err)
			require.Equal(t, tt.longCC, longCC)

			priorityInput, _, err := billingexpr.RunExprWithRequest(
				tt.expr,
				billingexpr.TokenParams{Len: 1, P: 1},
				billingexpr.RequestInput{Body: []byte(`{"service_tier":"priority"}`)},
			)
			require.NoError(t, err)
			require.Equal(t, tt.priorityInput, priorityInput)

			// service_tier 于 2026-07-30 更名 Fast mode，两个取值都必须走 2x
			fastInput, _, err := billingexpr.RunExprWithRequest(
				tt.expr,
				billingexpr.TokenParams{Len: 1, P: 1},
				billingexpr.RequestInput{Body: []byte(`{"service_tier":"fast"}`)},
			)
			require.NoError(t, err)
			require.Equal(t, tt.priorityInput, fastInput)
		})
	}
}
