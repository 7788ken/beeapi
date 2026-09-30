package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func TestContainsApology(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"english sorry", "I'm sorry, but I cannot help with that.", true},
		{"english apologize", "I apologize for the inconvenience.", true},
		{"chinese", "抱歉，我无法回答这个问题。", true},
		{"sorry inside token window", strings.Repeat("Here is a detailed implementation plan with more context. ", 8) + "I'm sorry I could not include more charts.", true},
		{"normal answer", "The capital of France is Paris.", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ContainsApology(tc.text, nil); got != tc.want {
				t.Fatalf("ContainsApology(%q)=%v want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestContainsApologyCustomKeywords(t *testing.T) {
	t.Parallel()
	if !ContainsApology("Notice: I will not continue with this.", []string{"notice"}) {
		t.Fatal("custom keyword should match the opening window")
	}
	if ContainsApology("I'm sorry, but I cannot help with that.", []string{"i cannot and will not"}) {
		t.Fatal("custom list must replace built-in phrases")
	}
}

func TestResponseQualityAndLogic(t *testing.T) {
	prev := *operation_setting.GetResponseQualitySetting()
	t.Cleanup(func() {
		*operation_setting.GetResponseQualitySetting() = prev
	})

	cfg := operation_setting.GetResponseQualitySetting()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.ChannelSetting.BlockApologyEnabled = true
	cfg.BlockApologyEnabled = false
	cfg.ApplyAllChannels = true
	if ResponseQualityActive(nil, info) {
		t.Fatal("global off must disable the filter")
	}

	cfg.BlockApologyEnabled = true
	info.ChannelSetting.BlockApologyEnabled = false
	if !ResponseQualityActive(nil, info) {
		t.Fatal("apply-all must enable the filter without a channel switch")
	}

	cfg.ApplyAllChannels = false
	if ResponseQualityActive(nil, info) {
		t.Fatal("channel off must disable the filter when apply-all is off")
	}

	info.ChannelSetting.BlockApologyEnabled = true
	if !ResponseQualityActive(nil, info) {
		t.Fatal("both on must enable the filter")
	}
}

func TestApplyResponseQualityFilter(t *testing.T) {
	prev := *operation_setting.GetResponseQualitySetting()
	t.Cleanup(func() {
		*operation_setting.GetResponseQualitySetting() = prev
	})
	cfg := operation_setting.GetResponseQualitySetting()
	cfg.BlockApologyEnabled = true
	cfg.BlockLowTokenEnabled = true
	cfg.LowTokenThreshold = 300
	cfg.ApologyStatusCode = 502
	cfg.ApologyMessage = "custom apology copy"
	cfg.LowTokenStatusCode = 503
	cfg.LowTokenMessage = "too short {tokens}/{threshold}"

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.ChannelSetting.BlockApologyEnabled = true
	info.ChannelSetting.BlockLowTokenEnabled = true
	info.QualityInspect.Text = "I'm sorry, I cannot assist with that request."
	err := ApplyResponseQualityFilter(nil, info, &dto.Usage{CompletionTokens: 80})
	if err == nil || err.StatusCode != 502 || err.GetErrorCode() != types.ErrorCodeResponseQualityApology || err.Error() != "custom apology copy" {
		t.Fatalf("apology should use configured status/message, got %+v", err)
	}
	if !types.IsSkipRetryError(err) {
		t.Fatal("quality filter must skip channel retry so the configured response reaches the client")
	}
	cfg.RetryOnBlock = true
	err = ApplyResponseQualityFilter(nil, info, &dto.Usage{CompletionTokens: 80})
	if err == nil || types.IsSkipRetryError(err) {
		t.Fatalf("retry switch on must leave the intercept retryable, got %+v", err)
	}
	cfg.RetryOnBlock = false

	info.ResetQualityInspect()
	info.QualityInspect.Text = "A long enough normal answer that is not an apology."
	err = ApplyResponseQualityFilter(nil, info, &dto.Usage{CompletionTokens: 80})
	if err == nil || err.GetErrorCode() != types.ErrorCodeResponseQualityLowToken || err.Error() != "too short 80/300" {
		t.Fatalf("low token should use configured message, got %+v", err)
	}

	info.Request = &dto.GeneralOpenAIRequest{MaxTokens: lo.ToPtr(uint(64))}
	err = ApplyResponseQualityFilter(nil, info, &dto.Usage{CompletionTokens: 80})
	if err != nil {
		t.Fatalf("requested max_tokens below threshold must skip low-token filter: %+v", err)
	}

	info.Request = nil
	info.QualityInspect.ToolCalls = 1
	err = ApplyResponseQualityFilter(nil, info, &dto.Usage{CompletionTokens: 12})
	if err != nil {
		t.Fatalf("tool calls must skip quality filter: %+v", err)
	}

	info.ResetQualityInspect()
	info.QualityInspect.Text = "The capital of France is Paris and here is more context."
	err = ApplyResponseQualityFilter(nil, info, &dto.Usage{CompletionTokens: 400})
	if err != nil {
		t.Fatalf("normal long reply must pass: %+v", err)
	}

	info.ResetQualityInspect()
	info.QualityInspect.Text = strings.Repeat("word ", 800) + " I'm sorry"
	err = ApplyResponseQualityFilter(nil, info, &dto.Usage{CompletionTokens: 4000})
	if err != nil {
		t.Fatalf("apology past the token window must pass: %+v", err)
	}
	if CountTextToken(strings.Repeat("word ", 800), "") <= cfg.EffectiveLowTokenThreshold() {
		t.Fatal("test prefix must exceed the apology window")
	}

	info.ResetQualityInspect()
	info.QualityInspect.Text = "I'm sorry, I cannot assist. " + strings.Repeat("word ", 800)
	err = ApplyResponseQualityFilter(nil, info, &dto.Usage{CompletionTokens: 4000})
	if err == nil || err.GetErrorCode() != types.ErrorCodeResponseQualityApology {
		t.Fatalf("apology inside the token window must block, got %+v", err)
	}

	info.ResetQualityInspect()
	info.QualityInspect.Text = "The capital of France is Paris."
	info.QualityInspect.Reasoning = "I'm sorry, but I need to think this through carefully."
	err = ApplyResponseQualityFilter(nil, info, &dto.Usage{})
	if err == nil || err.GetErrorCode() != types.ErrorCodeResponseQualityLowToken {
		t.Fatalf("reasoning apology must not block as apology, got %+v", err)
	}
}

func TestQualityFilterSkipsProbes(t *testing.T) {
	prev := *operation_setting.GetResponseQualitySetting()
	t.Cleanup(func() {
		*operation_setting.GetResponseQualitySetting() = prev
	})
	cfg := operation_setting.GetResponseQualitySetting()
	cfg.BlockApologyEnabled = true
	cfg.BlockLowTokenEnabled = true
	cfg.ApplyAllChannels = true
	cfg.LowTokenThreshold = 300

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.QualityInspect.Text = "I'm sorry, I cannot assist with that request."
	usage := &dto.Usage{CompletionTokens: 20}

	info.Request = &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	if ResponseQualityActive(nil, info) {
		t.Fatal("downstream hi must disable the quality hold")
	}
	if err := ApplyResponseQualityFilter(nil, info, usage); err != nil {
		t.Fatalf("downstream hi must skip the quality filter: %+v", err)
	}

	info.Request = &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	}
	if err := ApplyResponseQualityFilter(nil, info, usage); err == nil {
		t.Fatal("hello is not the channel-test ping and must still be filtered")
	}

	info.Request = &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "please write a detailed implementation plan"}},
	}
	if err := ApplyResponseQualityFilter(nil, info, usage); err == nil {
		t.Fatal("real prompts must still be filtered")
	}

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(string(constant.ContextKeyChannelTest), true)
	if ResponseQualityActive(c, info) {
		t.Fatal("in-process channel test must disable the quality hold")
	}
	if err := ApplyResponseQualityFilter(c, info, usage); err != nil {
		t.Fatalf("in-process channel test must skip the quality filter: %+v", err)
	}

	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Set("id", model.ChannelTestUserId)
	c2.Set("token_id", 0)
	if err := ApplyResponseQualityFilter(c2, info, usage); err != nil {
		t.Fatalf("platform channel-test identity must skip the quality filter: %+v", err)
	}

	c3, _ := gin.CreateTestContext(httptest.NewRecorder())
	c3.Set("id", 9)
	c3.Set("token_id", 8)
	c3.Set("token_name", "客户测活")
	if err := ApplyResponseQualityFilter(c3, info, usage); err == nil {
		t.Fatal("a customer token named 测活 with a real prompt must still be filtered")
	}
}

func TestIsResponseQualityFilterError(t *testing.T) {
	err := types.NewErrorWithStatusCode(fmt.Errorf("low token"), types.ErrorCodeResponseQualityLowToken, http.StatusServiceUnavailable)
	if !types.IsResponseQualityFilterError(err) {
		t.Fatal("low token must be recognized")
	}
	if types.IsResponseQualityFilterError(types.NewError(nil, types.ErrorCodeEmptyResponse)) {
		t.Fatal("empty response is not a quality filter error")
	}
}

func TestQualityChargeUsage(t *testing.T) {
	t.Parallel()
	info := &relaycommon.RelayInfo{}
	info.SetEstimatePromptTokens(1200)
	info.QualityInspect.Text = strings.Repeat("token ", 40)

	if got := qualityChargeUsage(info, &dto.Usage{PromptTokens: 800, CompletionTokens: 141, TotalTokens: 941}); got == nil || got.PromptTokens != 800 || got.CompletionTokens != 141 {
		t.Fatalf("upstream usage must be used as-is, got %+v", got)
	}

	if got := qualityChargeUsage(nil, nil); got != nil {
		t.Fatalf("nil info and usage must not invent a bill, got %+v", got)
	}

	got := qualityChargeUsage(info, nil)
	if got == nil || got.PromptTokens != 1200 || got.CompletionTokens <= 0 {
		t.Fatalf("missing usage should fall back to estimate + counted completion, got %+v", got)
	}
}

func TestChargeQualityFilterIfNeeded(t *testing.T) {
	prev := qualityConsumeQuota
	t.Cleanup(func() { qualityConsumeQuota = prev })

	var charged *dto.Usage
	var extra []string
	qualityConsumeQuota = func(_ *gin.Context, _ *relaycommon.RelayInfo, usage *dto.Usage, extraContent []string) {
		charged = usage
		extra = extraContent
	}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	usage := &dto.Usage{PromptTokens: 900, CompletionTokens: 151, TotalTokens: 1051}
	qerr := types.NewErrorWithStatusCode(fmt.Errorf("too short"), types.ErrorCodeResponseQualityLowToken, http.StatusServiceUnavailable)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	ChargeQualityFilterIfNeeded(c, info, usage, qerr)
	if charged != nil {
		t.Fatal("default channel must refund quality hits, not charge")
	}

	info.ChannelSetting.DisableNoOutputRefund = true
	ChargeQualityFilterIfNeeded(nil, info, usage, qerr)
	if charged != nil {
		t.Fatal("nil gin context must not charge")
	}

	ChargeQualityFilterIfNeeded(c, info, usage, qerr)
	if charged != usage {
		t.Fatalf("disable_no_output_refund must settle upstream usage, got %+v", charged)
	}
	if len(extra) != 1 || !strings.Contains(extra[0], "低token") || !strings.Contains(extra[0], "0 输出不免单") {
		t.Fatalf("consume log must name the intercept, got %v", extra)
	}

	charged = nil
	ChargeQualityFilterIfNeeded(c, info, nil, types.NewErrorWithStatusCode(fmt.Errorf("apology"), types.ErrorCodeResponseQualityApology, http.StatusBadGateway))
	if charged != nil {
		t.Fatal("empty usage and empty inspect text must skip charge")
	}
}

func TestChargeOrDeferQualityFilter(t *testing.T) {
	prevConsume := qualityConsumeQuota
	prevCfg := *operation_setting.GetResponseQualitySetting()
	t.Cleanup(func() {
		qualityConsumeQuota = prevConsume
		*operation_setting.GetResponseQualitySetting() = prevCfg
	})

	var charges int
	qualityConsumeQuota = func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage, _ []string) {
		charges++
	}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	usage := &dto.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}
	qerr := types.NewErrorWithStatusCode(fmt.Errorf("apology"), types.ErrorCodeResponseQualityApology, http.StatusServiceUnavailable)
	other := types.NewErrorWithStatusCode(fmt.Errorf("upstream"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.ChannelSetting.DisableNoOutputRefund = true

	cfg := operation_setting.GetResponseQualitySetting()
	cfg.RetryOnBlock = false
	ChargeOrDeferQualityFilter(c, info, usage, qerr)
	if charges != 1 || info.QualityChargePending != nil {
		t.Fatalf("retry off must charge immediately, charges=%d pending=%v", charges, info.QualityChargePending)
	}

	charges = 0
	cfg.RetryOnBlock = true
	ChargeOrDeferQualityFilter(c, info, usage, qerr)
	if charges != 0 || info.QualityChargePending == nil || info.QualityChargePending.CompletionTokens != 20 {
		t.Fatalf("retry on must defer, charges=%d pending=%+v", charges, info.QualityChargePending)
	}

	SettleDeferredQualityCharge(c, info, other)
	if charges != 0 || info.QualityChargePending == nil {
		t.Fatal("a later non-quality error must not settle the deferred intercept")
	}

	SettleDeferredQualityCharge(c, info, qerr)
	if charges != 1 || info.QualityChargePending != nil {
		t.Fatalf("final quality error must settle once, charges=%d pending=%v", charges, info.QualityChargePending)
	}
}
