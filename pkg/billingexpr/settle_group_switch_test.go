package billingexpr_test

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/require"
)

// 跨组重试：快照的 GroupRatio 被刷新为最终组后，结算金额必须随之改变。
// 倍率取生产上 grok-4.5 实际横跨的两组（Grok 官转 2.0 / OpenAI-Azure 2.2）。
func TestTieredSettleFollowsRefreshedGroupRatio(t *testing.T) {
	exprStr := `tier("default", p + c)`
	snap := &billingexpr.BillingSnapshot{
		BillingMode:  "tiered_expr",
		ExprString:   exprStr,
		ExprHash:     billingexpr.ExprHashString(exprStr),
		GroupRatio:   2.0,
		QuotaPerUnit: 500_000,
	}
	params := billingexpr.TokenParams{P: 1000, C: 500}

	first, err := billingexpr.ComputeTieredQuota(snap, params)
	require.NoError(t, err)
	require.Equal(t, 1500, first.ActualQuotaAfterGroup)

	// 模拟 getChannel 在重试轮把快照刷新到最终组倍率
	snap.GroupRatio = 2.2
	final, err := billingexpr.ComputeTieredQuota(snap, params)
	require.NoError(t, err)
	require.Equal(t, 1650, final.ActualQuotaAfterGroup)
}

// 首组倍率为 0 时重试落到付费组，刷新后不得仍按 0 结算成整单免费。
func TestTieredSettleZeroRatioFirstGroupDoesNotStayFree(t *testing.T) {
	exprStr := `tier("default", p + c)`
	snap := &billingexpr.BillingSnapshot{
		BillingMode:  "tiered_expr",
		ExprString:   exprStr,
		ExprHash:     billingexpr.ExprHashString(exprStr),
		GroupRatio:   0,
		QuotaPerUnit: 500_000,
	}
	params := billingexpr.TokenParams{P: 1000, C: 500}

	free, err := billingexpr.ComputeTieredQuota(snap, params)
	require.NoError(t, err)
	require.Equal(t, 0, free.ActualQuotaAfterGroup)

	snap.GroupRatio = 2.0
	paid, err := billingexpr.ComputeTieredQuota(snap, params)
	require.NoError(t, err)
	require.Equal(t, 1500, paid.ActualQuotaAfterGroup)
}
