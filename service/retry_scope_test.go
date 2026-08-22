package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func scopeCtx(strategy int, policy string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyOriginRetryStrategy, strategy)
	common.SetContextKey(c, constant.ContextKeyTokenRelayRetryPolicy, policy)
	return c
}

// TestEffectiveRetryScope 覆盖渠道级 RetryStrategy 与 Key 级 relay_retry_policy 的合并真值表：
// 取最保守者（min），inherit/system 不约束，显式宽松不能放开另一轴的收紧。
func TestEffectiveRetryScope(t *testing.T) {
	cases := []struct {
		name     string
		strategy int
		policy   string
		want     int
	}{
		// 单轴：inherit/system 不约束，另一轴直接生效
		{"both inherit/system", model.RetryStrategyInherit, model.TokenRelayRetryPolicySystem, retryScopeUnconstrained},
		{"channel cost_guard only", model.RetryStrategyCostGuard, model.TokenRelayRetryPolicySystem, RetryScopeNoCrossChannel},
		{"token disabled only", model.RetryStrategyInherit, model.TokenRelayRetryPolicyDisabled, RetryScopeNoCrossChannel},
		{"channel same_domain only", model.RetryStrategySameDomain, model.TokenRelayRetryPolicySystem, RetryScopeSameDomain},
		{"token cache_domain_only only", model.RetryStrategyInherit, model.TokenRelayRetryPolicyCacheDomainOnly, RetryScopeSameDomain},
		{"channel cross_channel only", model.RetryStrategyCrossChannel, model.TokenRelayRetryPolicySystem, RetryScopeCrossChannel},
		{"token allow_cross_channel only", model.RetryStrategyInherit, model.TokenRelayRetryPolicyAllowCrossChannel, RetryScopeCrossChannel},
		// 双轴 min()：最保守者胜
		{"cost_guard vs allow_cross -> channel tightens", model.RetryStrategyCostGuard, model.TokenRelayRetryPolicyAllowCrossChannel, RetryScopeNoCrossChannel},
		{"cross_channel vs cache_domain_only -> token tightens", model.RetryStrategyCrossChannel, model.TokenRelayRetryPolicyCacheDomainOnly, RetryScopeSameDomain},
		{"cross_channel vs disabled -> token tightens", model.RetryStrategyCrossChannel, model.TokenRelayRetryPolicyDisabled, RetryScopeNoCrossChannel},
		{"same_domain vs allow_cross -> channel tightens", model.RetryStrategySameDomain, model.TokenRelayRetryPolicyAllowCrossChannel, RetryScopeSameDomain},
		{"cost_guard vs cache_domain_only -> channel tightens", model.RetryStrategyCostGuard, model.TokenRelayRetryPolicyCacheDomainOnly, RetryScopeNoCrossChannel},
		{"same_domain vs disabled -> token tightens", model.RetryStrategySameDomain, model.TokenRelayRetryPolicyDisabled, RetryScopeNoCrossChannel},
		// 未知值按不约束处理
		{"garbage policy -> unconstrained", model.RetryStrategyInherit, "garbage", retryScopeUnconstrained},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EffectiveRetryScope(scopeCtx(tc.strategy, tc.policy))
			if got != tc.want {
				t.Fatalf("strategy=%d policy=%q: got scope %d, want %d", tc.strategy, tc.policy, got, tc.want)
			}
		})
	}
}
