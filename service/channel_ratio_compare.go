package service

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const ratioCompareEpsilon = 1e-9

// RatioDiff 一条倍率变化。
type RatioDiff struct {
	GroupName string
	RatioKind string
	OldValue  float64
	NewValue  float64
	Direction int
}

// ChannelRatioRoundResult 单渠道一轮抓取+比对的结果。
type ChannelRatioRoundResult struct {
	ChannelId int
	Kind      string
	Status    string
	Msg       string
	Diffs     []RatioDiff
	FirstSeen bool   // 首次建基线，不产生 diffs
	Summary   string // 当前分组倍率摘要 JSON，供列表展示
	Group     string // 实际采用的分组名（人工指定优先于自动反推）

	Usage       float64  // 本轮读到的上游累计实付（USD 分）；0=未取到
	UsageOK     bool     // 上游是否支持 /dashboard/billing/usage
	ModelRatios map[string]ModelRatio // 上游每模型倍率，供调用方算基准
	PanelRatio  *float64 // 面板口径的本渠道倍率，用于人工基准对比
}

// diffGroupRatios 比对上游本次与上次基线。
// 只有已存在基线的 (group, kind) 参与比对；新分组只建基线不报警，避免上游改分组名或新增分组被误判为涨价。
func diffGroupRatios(baselines []model.ChannelGroupRatioBaseline, samples []GroupRatioSample) (diffs []RatioDiff, upserts []model.ChannelGroupRatioBaseline) {
	type key struct {
		group string
		kind  string
	}
	known := make(map[key]float64, len(baselines))
	for _, b := range baselines {
		known[key{b.GroupName, b.RatioKind}] = b.Ratio
	}

	now := common.GetTimestamp()
	for _, s := range samples {
		k := key{s.GroupName, s.RatioKind}
		if old, ok := known[k]; ok && math.Abs(old-s.Value) > ratioCompareEpsilon {
			direction := 1
			if s.Value < old {
				direction = -1
			}
			diffs = append(diffs, RatioDiff{
				GroupName: s.GroupName,
				RatioKind: s.RatioKind,
				OldValue:  old,
				NewValue:  s.Value,
				Direction: direction,
			})
		}
		upserts = append(upserts, model.ChannelGroupRatioBaseline{
			ChannelId: 0, // 调用方填充
			GroupName: s.GroupName,
			RatioKind: s.RatioKind,
			Ratio:     s.Value,
			Extra:     s.Extra,
			UpdatedAt: now,
		})
	}
	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].GroupName != diffs[j].GroupName {
			return diffs[i].GroupName < diffs[j].GroupName
		}
		return diffs[i].RatioKind < diffs[j].RatioKind
	})
	return diffs, upserts
}

// CountBadgeDirections 只统计 group 与 resolved 两类作为角标依据。
// effective 含 sub2api 当前时段 peak 系数，进入/离开高峰时段会自然波动，计入角标会造成误报。
func CountBadgeDirections(diffs []RatioDiff) (up int, down int) {
	for _, d := range diffs {
		if d.RatioKind != RatioKindGroup && d.RatioKind != RatioKindResolved {
			continue
		}
		if d.Direction > 0 {
			up++
		} else if d.Direction < 0 {
			down++
		}
	}
	return up, down
}

// RunChannelRatioRound 抓取单渠道并落库比对结果。
func RunChannelRatioRound(ctx context.Context, client *http.Client, channel *model.Channel, batchAt int64, creds map[string]SubSiteCredential, panelCache *PanelCache) ChannelRatioRoundResult {
	result := ChannelRatioRoundResult{ChannelId: channel.Id}

	// 实付累计：/dashboard/billing/usage 认 sk- key，是唯一按 key 计的口径
	if usage, ok := FetchUpstreamUsage(ctx, client, RatioPanelURL(channel), firstEnabledKey(channel)); ok {
		result.Usage, result.UsageOK = usage, true
	}

	fetched := FetchChannelGroupRatios(ctx, client, channel, channel.RatioUpstreamKind, creds, panelCache)
	result.Kind = fetched.Kind
	if fetched.Err != nil || len(fetched.Samples) == 0 {
		result.Status = UpstreamKindUnsupported
		if fetched.Kind != UpstreamKindUnsupported {
			result.Status = "error"
		}
		if fetched.Err != nil {
			result.Msg = fetched.Err.Error()
		}
		return result
	}
	result.Status = "ok"
	// 人工指定权威，覆盖自动反推
	result.Group = fetched.Group
	if channel.RatioUpstreamGroup != nil {
		if manual := strings.TrimSpace(*channel.RatioUpstreamGroup); manual != "" {
			result.Group = manual
		}
	}
	result.Summary = buildRatioSummary(fetched.Samples, result.Group)
	result.ModelRatios = fetched.ModelRatios
	// 定位到分组时记下面板口径倍率，供与人工登记值比对
	if result.Group != "" {
		for _, s := range fetched.Samples {
			if s.GroupName == result.Group && (s.RatioKind == RatioKindResolved || s.RatioKind == RatioKindGroup) {
				v := s.Value
				result.PanelRatio = &v
				break
			}
		}
	}
	// 未定位到分组时，把原因写进 msg，前端 tooltip 会显示，管理员据此手动指定
	if result.Group == "" && fetched.GroupHint != "" {
		result.Msg = fetched.GroupHint
	}

	baselines, err := model.GetChannelGroupRatioBaselines(channel.Id)
	if err != nil {
		result.Status = "error"
		result.Msg = err.Error()
		return result
	}
	result.FirstSeen = len(baselines) == 0

	diffs, upserts := diffGroupRatios(baselines, fetched.Samples)
	for i := range upserts {
		upserts[i].ChannelId = channel.Id
	}
	if err := model.UpsertChannelGroupRatioBaselines(upserts); err != nil {
		result.Status = "error"
		result.Msg = err.Error()
		return result
	}

	// 首次建基线时全部视为已知，不产生变更记录，否则上线当天全站误报涨价。
	if result.FirstSeen {
		return result
	}

	result.Diffs = diffs
	if len(diffs) == 0 {
		return result
	}

	changes := make([]model.ChannelGroupRatioChange, 0, len(diffs))
	now := common.GetTimestamp()
	for _, d := range diffs {
		changes = append(changes, model.ChannelGroupRatioChange{
			ChannelId: channel.Id,
			BatchAt:   batchAt,
			GroupName: d.GroupName,
			RatioKind: d.RatioKind,
			OldValue:  d.OldValue,
			NewValue:  d.NewValue,
			Direction: d.Direction,
			CreatedAt: now,
		})
	}
	if err := model.CreateChannelGroupRatioChanges(changes); err != nil {
		result.Status = "error"
		result.Msg = err.Error()
	}
	return result
}

// BuildRatioChangeNotification 生成管理员通知正文。
func BuildRatioChangeNotification(results []ChannelRatioRoundResult, channelNames map[int]string) string {
	content := "检测到上游分组倍率变化：\n\n"
	for _, r := range results {
		up, down := CountBadgeDirections(r.Diffs)
		if up == 0 && down == 0 {
			continue
		}
		content += fmt.Sprintf("渠道 %s（#%d，%s）涨 %d 降 %d\n", channelNames[r.ChannelId], r.ChannelId, r.Kind, up, down)
		for _, d := range r.Diffs {
			if d.RatioKind != RatioKindGroup && d.RatioKind != RatioKindResolved {
				continue
			}
			arrow := "↑"
			if d.Direction < 0 {
				arrow = "↓"
			}
			content += fmt.Sprintf("  %s %s：%g → %g\n", arrow, d.GroupName, d.OldValue, d.NewValue)
		}
		content += "\n"
	}
	content += fmt.Sprintf("检测时间：%s\n", time.Now().Format("2006-01-02 15:04:05"))
	return content
}

// RatioSummary 当前分组倍率摘要，供渠道列表直接展示"此刻的倍率"。
type RatioSummary struct {
	N     int     `json:"n"`               // 参与统计的分组数
	Min   float64 `json:"min"`             // 最低倍率
	Max   float64 `json:"max"`             // 最高倍率
	Group string  `json:"g,omitempty"`     // 已定位到我方 key 所属分组时的分组名
}

// buildRatioSummary 从本轮样本算出展示摘要。
// 优先用 resolved（sub2api 含专属折扣的实付倍率），没有则用 group。
// effective 含时段 peak 系数不稳定、api_rate 不是倍率，均排除。
func buildRatioSummary(samples []GroupRatioSample, group string) string {
	// 已定位到我方 key 所属分组时，只展示该组的倍率——这才是我们真正在付的价
	if group != "" {
		// resolved 必须优先于 group：sub2api 上游管理员可给我们设专属倍率
		// （resolved=user_rate_multiplier），group 只是公共挂牌价。
		// 曾因按样本顺序取第一个命中，把 allincoding 的专属价 0.15 显示成挂牌价 3.46。
		for _, kind := range []string{RatioKindResolved, RatioKindGroup} {
			for _, s := range samples {
				if s.GroupName != group || s.RatioKind != kind {
					continue
				}
				encoded, err := common.Marshal(RatioSummary{N: 1, Min: s.Value, Max: s.Value, Group: group})
				if err == nil {
					return string(encoded)
				}
			}
		}
	}

	values := make([]float64, 0, len(samples))
	for _, s := range samples {
		if s.RatioKind == RatioKindResolved {
			values = append(values, s.Value)
		}
	}
	if len(values) == 0 {
		for _, s := range samples {
			if s.RatioKind == RatioKindGroup {
				values = append(values, s.Value)
			}
		}
	}
	if len(values) == 0 {
		return ""
	}

	summary := RatioSummary{N: len(values), Min: values[0], Max: values[0]}
	for _, v := range values[1:] {
		if v < summary.Min {
			summary.Min = v
		}
		if v > summary.Max {
			summary.Max = v
		}
	}
	encoded, err := common.Marshal(summary)
	if err != nil {
		return ""
	}
	return string(encoded)
}
