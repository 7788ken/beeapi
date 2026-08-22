package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// 实付倍率反推。
//
// 为什么需要：上游 /api/pricing 返回的 group_ratio 会按"发起请求者的账号"做覆盖
// （controller/pricing.go 的 GetGroupGroupRatio），匿名拿到的是公开挂牌价。
// 而该接口只认面板 access token，不认渠道的 sk- key，所以我们无法用渠道自己的身份去问价。
//
// 突破口：/dashboard/billing/usage 挂 TokenAuth，用 sk- key 就能调，返回该 key 的累计实付。
// 取两轮增量除以同窗口的基准 quota，即可反推出真实实付倍率（含上游私下给的专属折扣）。
//
// 详见 docs/2026-08-05-upstream-group-ratio-monitor.md 第十一节。

// 反推结果的合理区间：超出即判为口径不符（例如上游 usage 按 CNY 或 tokens 显示），
// 宁可不给数也不给错数。
const (
	effectiveRatioMin = 0.001
	effectiveRatioMax = 100
)

// FetchUpstreamUsage 取该 key 在上游的累计实付（USD 分）。
// 返回 ok=false 表示上游不支持该端点或响应不可解析。
func FetchUpstreamUsage(ctx context.Context, client *http.Client, panelURL, apiKey string) (float64, bool) {
	if strings.TrimSpace(apiKey) == "" {
		return 0, false
	}
	body, err := ratioMonitorGet(ctx, client, panelURL+"/dashboard/billing/usage", map[string]string{
		"Authorization": "Bearer " + strings.TrimSpace(apiKey),
	})
	if err != nil {
		return 0, false
	}
	var parsed struct {
		Object     string   `json:"object"`
		TotalUsage *float64 `json:"total_usage"`
	}
	if err := common.Unmarshal(body, &parsed); err != nil || parsed.TotalUsage == nil {
		return 0, false
	}
	return *parsed.TotalUsage, true
}

// baseQuotaForWindow 用上游自己公布的模型倍率算出"倍率=1 时应付的 quota"。
// mapping 是渠道的 model_mapping（我方模型名 → 上游模型名）：logs 记录的是我方名，
// 价目表按上游名索引，不映射就会漏算基准、把反推倍率吹大（ch8 曾算出 15.6）。
// 按次计费（quota_type=1）的模型无法用 token 反推，整条跳过并返回 skipped。
func baseQuotaForWindow(rows []model.ChannelModelUsage, ratios map[string]ModelRatio, mapping map[string]string) (base float64, skipped bool) {
	for _, r := range rows {
		name := r.ModelName
		if mapped, ok := mapping[name]; ok && mapped != "" {
			name = mapped
		}
		mr, ok := ratios[name]
		if !ok {
			// 价目表里没有这个模型，无法对账
			skipped = true
			continue
		}
		if mr.PerRequest {
			skipped = true
			continue
		}
		completionRatio := mr.CompletionRatio
		if completionRatio <= 0 {
			completionRatio = 1
		}
		base += (float64(r.PromptTokens) + float64(r.CompletionTokens)*completionRatio) * mr.Ratio
	}
	return base, skipped
}

// InferEffectiveRatio 由实付增量与基准 quota 反推实付倍率。
// 返回 nil 表示本轮无法得出可信值（无流量 / 无基准 / 结果超出合理区间）。
func InferEffectiveRatio(prevUsage, currUsage float64, rows []model.ChannelModelUsage, ratios map[string]ModelRatio, mapping map[string]string) *float64 {
	if prevUsage <= 0 || currUsage < prevUsage {
		// 首轮无基线，或上游重置过用量统计
		return nil
	}
	deltaUsd := (currUsage - prevUsage) / 100
	if deltaUsd <= 0 {
		return nil
	}
	base, skipped := baseQuotaForWindow(rows, ratios, mapping)
	if base <= 0 || skipped {
		// 窗口内有无法定价的流量（价目表缺模型/按次计费）：实付里含它们的钱、
		// 基准里却没有，算出的倍率必然虚高——宁可不给数也不给错数。
		return nil
	}
	ratio := deltaUsd * common.QuotaPerUnit / base
	if ratio < effectiveRatioMin || ratio > effectiveRatioMax {
		return nil
	}
	return &ratio
}

// LocalModelRatios 用本站倍率表构造兜底基准。
// 上游关闭 pricing（如 pomoai 匿名 401）时拿不到它的价目表，但标准 new-api 的
// model_ratio 都锚定官方价，用本站表算基准得到的就是"相对官方价的实付折扣"，
// 正是采购倍率的语义。注意：走本地表时不要再套渠道 model_mapping——本地表按我方模型名索引。
func LocalModelRatios() map[string]ModelRatio {
	mr := ratio_setting.GetModelRatioCopy()
	cr := ratio_setting.GetCompletionRatioCopy()
	out := make(map[string]ModelRatio, len(mr))
	for name, r := range mr {
		out[name] = ModelRatio{Ratio: r, CompletionRatio: cr[name]}
	}
	return out
}

// RatioDeviation 人工基准与实测值的偏差，用于抓"上游偷偷取消专属折扣"。
type RatioDeviation struct {
	ChannelId int
	Expected  float64
	Actual    float64
	Source    string // effective=实付反推 / panel=面板挂牌
	PercentUp float64
}

// CheckRatioDeviation 比较人工填写的采购倍率与实测值。
// 实付反推值优先于面板挂牌值——前者才是我们真正付的钱。
// 只在实测值"高于"预期时告警：低于预期是好事，不该打扰管理员。
func CheckRatioDeviation(channel *model.Channel, effective *float64, panelRatio *float64, thresholdPercent float64) *RatioDeviation {
	if channel.RatioExpected == nil || *channel.RatioExpected <= 0 {
		return nil
	}
	expected := *channel.RatioExpected

	var actual float64
	var source string
	switch {
	case effective != nil:
		actual, source = *effective, "effective"
	case panelRatio != nil:
		actual, source = *panelRatio, "panel"
	default:
		return nil
	}

	percentUp := (actual - expected) / expected * 100
	if percentUp <= thresholdPercent {
		return nil
	}
	return &RatioDeviation{
		ChannelId: channel.Id,
		Expected:  expected,
		Actual:    actual,
		Source:    source,
		PercentUp: percentUp,
	}
}

// BuildRatioDeviationNotification 生成偏差告警正文。
func BuildRatioDeviationNotification(items []RatioDeviation, channelNames map[int]string) string {
	content := "以下渠道的实测倍率高于你登记的采购倍率，可能是上游取消了专属折扣或上调了价格：\n\n"
	for _, d := range items {
		label := "实付反推"
		if d.Source == "panel" {
			label = "面板挂牌"
		}
		content += fmt.Sprintf("渠道 %s（#%d）登记 %g → %s %g（高 %.1f%%）\n",
			channelNames[d.ChannelId], d.ChannelId, d.Expected, label, d.Actual, d.PercentUp)
	}
	return content
}
