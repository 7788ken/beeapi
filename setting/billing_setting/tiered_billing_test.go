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
		shortCR       float64
		shortCC       float64
		longCR        float64
		longCC        float64
		priorityInput float64
	}{
		{
			name:          "sol",
			expr:          gpt56SolExpr,
			shortCR:       0.5,
			shortCC:       6.25,
			longCR:        1,
			longCC:        12.5,
			priorityInput: 10,
		},
		{
			name:          "terra",
			expr:          gpt56TerraExpr,
			shortCR:       0.25,
			shortCC:       3.125,
			longCR:        0.5,
			longCC:        6.25,
			priorityInput: 5,
		},
		{
			name:          "luna",
			expr:          gpt56LunaExpr,
			shortCR:       0.1,
			shortCC:       1.25,
			longCR:        0.2,
			longCC:        2.5,
			priorityInput: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
		})
	}
}
