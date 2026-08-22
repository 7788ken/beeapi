package types

import (
	"errors"
	"testing"
)

// 「分组可用性」错误归属契约。改动这里等于改动指标口径，历史数据将不可比。
func TestClassifyErrorAttribution(t *testing.T) {
	cases := []struct {
		name string
		err  *NewAPIError
		want ErrorAttribution
	}{
		// 上游故障
		{"5xx", NewErrorWithStatusCode(errors.New("boom"), ErrorCodeBadResponseStatusCode, 500), AttributionUpstream},
		{"502", NewErrorWithStatusCode(errors.New("bad gw"), ErrorCodeBadResponseStatusCode, 502), AttributionUpstream},
		{"401 key 失效", NewErrorWithStatusCode(errors.New("unauthorized"), ErrorCodeBadResponseStatusCode, 401), AttributionUpstream},
		{"403 被封", NewErrorWithStatusCode(errors.New("forbidden"), ErrorCodeBadResponseStatusCode, 403), AttributionUpstream},
		{"429 限流", NewErrorWithStatusCode(errors.New("rate limited"), ErrorCodeBadResponseStatusCode, 429), AttributionUpstream},
		{"上游 404 模型下线", NewErrorWithStatusCode(errors.New("model gone"), ErrorCodeBadResponseStatusCode, 404), AttributionUpstream},
		{"连不上", NewError(errors.New("dial fail"), ErrorCodeDoRequestFailed), AttributionUpstream},
		{"无响应", NewError(errors.New("no resp"), ErrorCodeUpstreamNoResponse), AttributionUpstream},
		{"上游超时", NewError(errors.New("timeout"), ErrorCodeUpstreamTimeout), AttributionUpstream},
		{"空响应", NewError(errors.New("empty"), ErrorCodeEmptyResponse), AttributionUpstream},
		{"channel:key 用尽", NewError(errors.New("no key"), ErrorCodeChannelNoAvailableKey), AttributionUpstream},
		{"channel:key 无效", NewError(errors.New("bad key"), ErrorCodeChannelInvalidKey), AttributionUpstream},

		// 客户端错误 —— 不该拖累可用率
		{"上游 400 拒非法参数", NewErrorWithStatusCode(errors.New("bad param"), ErrorCodeBadResponseStatusCode, 400), AttributionClient},
		{"余额不足", NewError(errors.New("no quota"), ErrorCodeInsufficientUserQuota), AttributionClient},
		{"敏感词", NewError(errors.New("blocked"), ErrorCodeSensitiveWordsDetected), AttributionClient},
		{"无权限", NewError(errors.New("denied"), ErrorCodeAccessDenied), AttributionClient},
		{"请求转换失败", NewError(errors.New("convert"), ErrorCodeConvertRequestFailed), AttributionClient},
		{"token 熔断", NewError(errors.New("breaker"), ErrorCodeTokenErrorRateLimited), AttributionClient},
		{"网关侧模型未配置", NewError(errors.New("no model"), ErrorCodeModelNotFound), AttributionClient},

		// 网关自身
		{"计价配置错", NewError(errors.New("price"), ErrorCodeModelPriceError), AttributionGateway},
		{"DB 查询错", NewError(errors.New("sql"), ErrorCodeQueryDataError), AttributionGateway},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyErrorAttribution(tc.err); got != tc.want {
				t.Fatalf("attribution = %v, want %v", got, tc.want)
			}
		})
	}
}

// 只有上游故障进可用率分母。
func TestIsUpstreamAttributedError(t *testing.T) {
	if !IsUpstreamAttributedError(NewErrorWithStatusCode(errors.New("x"), ErrorCodeBadResponseStatusCode, 503)) {
		t.Fatal("503 must count toward availability")
	}
	if IsUpstreamAttributedError(NewError(errors.New("x"), ErrorCodeInsufficientUserQuota)) {
		t.Fatal("insufficient quota must NOT count toward availability")
	}
	if IsUpstreamAttributedError(NewError(errors.New("x"), ErrorCodeModelPriceError)) {
		t.Fatal("gateway pricing bug must NOT count toward availability")
	}
}
