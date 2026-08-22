package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
)

func usageRows(model_ string, pt, ct int64) []model.ChannelModelUsage {
	return []model.ChannelModelUsage{{ChannelId: 1, ModelName: model_, PromptTokens: pt, CompletionTokens: ct}}
}

// 反推口径：实付增量 / 基准 quota。用 aixor 实测形态（gpt-5.4 mr=1.25 cr=6）。
func TestInferEffectiveRatioBasic(t *testing.T) {
	ratios := map[string]ModelRatio{"gpt-5.4": {Ratio: 1.25, CompletionRatio: 6}}
	// 基准 = (1_000_000 + 100_000*6) * 1.25 = 2_000_000 quota
	rows := usageRows("gpt-5.4", 1_000_000, 100_000)
	// 上游多收了 1 USD = 500_000 quota → 倍率 0.25
	got := InferEffectiveRatio(1000, 1100, rows, ratios, nil)
	if got == nil {
		t.Fatal("want a ratio, got nil")
	}
	if *got < 0.2499 || *got > 0.2501 {
		t.Fatalf("ratio = %v, want 0.25", *got)
	}
}

// 首轮没有上轮快照，不能拿"全历史实付"除"本窗口用量"——那会得出离谱的高值。
func TestInferEffectiveRatioFirstRoundReturnsNil(t *testing.T) {
	ratios := map[string]ModelRatio{"m": {Ratio: 1, CompletionRatio: 1}}
	if got := InferEffectiveRatio(0, 5000, usageRows("m", 1000, 100), ratios, nil); got != nil {
		t.Fatalf("first round must return nil, got %v", *got)
	}
}

// 上游重置用量统计（当前值小于快照）时不能算出负数或乱值。
func TestInferEffectiveRatioHandlesUsageReset(t *testing.T) {
	ratios := map[string]ModelRatio{"m": {Ratio: 1, CompletionRatio: 1}}
	if got := InferEffectiveRatio(9000, 100, usageRows("m", 1000, 100), ratios, nil); got != nil {
		t.Fatalf("usage reset must return nil, got %v", *got)
	}
}

// 窗口内无流量 → 基准为 0，不能除零。
func TestInferEffectiveRatioNoTrafficReturnsNil(t *testing.T) {
	ratios := map[string]ModelRatio{"m": {Ratio: 1, CompletionRatio: 1}}
	if got := InferEffectiveRatio(1000, 2000, nil, ratios, nil); got != nil {
		t.Fatalf("no traffic must return nil, got %v", *got)
	}
}

// 上游 usage 若按 CNY 或 tokens 显示，算出的值会离谱；宁可不给数也不给错数。
func TestInferEffectiveRatioRejectsImplausible(t *testing.T) {
	ratios := map[string]ModelRatio{"m": {Ratio: 1, CompletionRatio: 1}}
	// 实付增量巨大 → 倍率远超合理上限
	if got := InferEffectiveRatio(0.0001, 1e9, usageRows("m", 10, 0), ratios, nil); got != nil {
		t.Fatalf("implausible ratio must be rejected, got %v", *got)
	}
}

// 按次计费的模型无法用 token 反推，必须跳过而不是当成 0 倍率。
func TestBaseQuotaSkipsPerRequestModels(t *testing.T) {
	ratios := map[string]ModelRatio{
		"img":  {Ratio: 0, PerRequest: true},
		"chat": {Ratio: 2, CompletionRatio: 1},
	}
	rows := []model.ChannelModelUsage{
		{ModelName: "img", PromptTokens: 999999, CompletionTokens: 999999},
		{ModelName: "chat", PromptTokens: 100, CompletionTokens: 100},
	}
	base, skipped := baseQuotaForWindow(rows, ratios, nil)
	if !skipped {
		t.Error("per-request model must be flagged as skipped")
	}
	if base != 400 { // (100+100*1)*2
		t.Fatalf("base = %v, want 400 (img excluded)", base)
	}
}

// 上游价目表没有的模型（例如我们做了 model_mapping 改名）不能计入基准。
func TestBaseQuotaSkipsUnknownModels(t *testing.T) {
	ratios := map[string]ModelRatio{"known": {Ratio: 1, CompletionRatio: 1}}
	rows := []model.ChannelModelUsage{
		{ModelName: "unknown", PromptTokens: 1000, CompletionTokens: 1000},
		{ModelName: "known", PromptTokens: 10, CompletionTokens: 0},
	}
	base, skipped := baseQuotaForWindow(rows, ratios, nil)
	if !skipped || base != 10 {
		t.Fatalf("base=%v skipped=%v, want 10/true", base, skipped)
	}
}

// completion_ratio 缺省（上游没给）时按 1 处理，不能当 0 让基准偏低。
func TestBaseQuotaDefaultsCompletionRatio(t *testing.T) {
	ratios := map[string]ModelRatio{"m": {Ratio: 1, CompletionRatio: 0}}
	base, _ := baseQuotaForWindow(usageRows("m", 100, 50), ratios, nil)
	if base != 150 {
		t.Fatalf("base = %v, want 150", base)
	}
}

// 实付反推值优先于面板挂牌值——前者才是真正付的钱。
func TestCheckRatioDeviationPrefersEffective(t *testing.T) {
	expected := 0.11
	ch := &model.Channel{Id: 84, RatioExpected: &expected}
	eff := 0.24
	panel := 1.0
	dev := CheckRatioDeviation(ch, &eff, &panel, 10)
	if dev == nil {
		t.Fatal("want deviation")
	}
	if dev.Source != "effective" || dev.Actual != 0.24 {
		t.Fatalf("got source=%s actual=%v, want effective/0.24", dev.Source, dev.Actual)
	}
}

// 实测低于登记价是好事，不该告警。
func TestCheckRatioDeviationIgnoresCheaper(t *testing.T) {
	expected := 1.0
	ch := &model.Channel{Id: 1, RatioExpected: &expected}
	eff := 0.5
	if dev := CheckRatioDeviation(ch, &eff, nil, 10); dev != nil {
		t.Fatalf("cheaper than expected must not alert, got %+v", dev)
	}
}

// 阈值内的小幅波动不告警，避免噪音。
func TestCheckRatioDeviationRespectsThreshold(t *testing.T) {
	expected := 1.0
	ch := &model.Channel{Id: 1, RatioExpected: &expected}
	within := 1.05
	if dev := CheckRatioDeviation(ch, &within, nil, 10); dev != nil {
		t.Fatalf("5%% rise under 10%% threshold must not alert, got %+v", dev)
	}
	beyond := 1.2
	if dev := CheckRatioDeviation(ch, &beyond, nil, 10); dev == nil {
		t.Fatal("20% rise over 10% threshold must alert")
	}
}

// 没登记采购价的渠道不参与告警。
func TestCheckRatioDeviationSkipsUnset(t *testing.T) {
	eff := 99.0
	if dev := CheckRatioDeviation(&model.Channel{Id: 1}, &eff, nil, 10); dev != nil {
		t.Fatalf("unset expected must not alert, got %+v", dev)
	}
}

// 渠道 model_mapping 必须生效：logs 记我方名、价目表按上游名索引。
// ch8 曾因映射缺失把倍率吹到 15.6。
func TestBaseQuotaAppliesModelMapping(t *testing.T) {
	ratios := map[string]ModelRatio{"upstream-name": {Ratio: 2, CompletionRatio: 1}}
	mapping := map[string]string{"our-name": "upstream-name"}
	base, skipped := baseQuotaForWindow(usageRows("our-name", 100, 100), ratios, mapping)
	if skipped || base != 400 {
		t.Fatalf("base=%v skipped=%v, want 400/false", base, skipped)
	}
}

// 窗口内含无法定价的流量时必须整体放弃：实付里有它们的钱、基准里没有，
// 出数必然虚高。
func TestInferEffectiveRatioNilOnSkippedModels(t *testing.T) {
	ratios := map[string]ModelRatio{"known": {Ratio: 1, CompletionRatio: 1}}
	rows := []model.ChannelModelUsage{
		{ModelName: "known", PromptTokens: 1000, CompletionTokens: 0},
		{ModelName: "unknown", PromptTokens: 999999, CompletionTokens: 0},
	}
	if got := InferEffectiveRatio(1000, 1100, rows, ratios, nil); got != nil {
		t.Fatalf("skipped traffic must yield nil, got %v", *got)
	}
}
