package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 上游分组倍率监控：抓取三类上游的分组倍率，供比对与角标展示。
// 详见 docs/2026-08-05-upstream-group-ratio-monitor.md

const sub2apiKeyGroupName = "(key group)"

const (
	ratioMonitorTimeout     = 15 * time.Second
	ratioMonitorMaxBodySize = 10 << 20
)

// 上游类型
const (
	UpstreamKindNewAPI      = "newapi"
	UpstreamKindSub2API     = "sub2api"
	UpstreamKindDoneHub     = "donehub"
	UpstreamKindUnsupported = "unsupported"
)

// 倍率种类别名：单一事实源在 model，避免两处定义漂移后静默错配。
const (
	RatioKindGroup     = model.RatioKindGroup
	RatioKindResolved  = model.RatioKindResolved
	RatioKindEffective = model.RatioKindEffective
	RatioKindAPIRate   = model.RatioKindAPIRate
)

// GroupRatioSample 一个分组的一项倍率观测值。
type GroupRatioSample struct {
	GroupName string
	RatioKind string
	Value     float64
	Extra     string
	Models    map[string]struct{} // 该分组启用的模型集合，仅 new-api 侧用于反推我方 key 所属分组
}

// UpstreamRatioResult 单个渠道一次抓取的结果。
type UpstreamRatioResult struct {
	Kind        string
	Samples     []GroupRatioSample
	Group       string // 我方 key 所属分组；空=未能定位
	GroupHint   string // 未能定位时说明原因，供管理员判断该填哪个分组名
	ModelRatios map[string]ModelRatio // 上游每模型倍率，用于反推实付倍率的基准计算
	Err         error
}

// ModelRatio 上游对单个模型的计费倍率。
type ModelRatio struct {
	Ratio           float64 // model_ratio
	CompletionRatio float64 // completion_ratio；0 表示上游未给，按 1 处理
	PerRequest      bool    // quota_type=1 按次计费，无法用 token 反推，跳过
}

func RatioMonitorClient() *http.Client {
	transport := &http.Transport{
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	}
	if common.TLSInsecureSkipVerify {
		transport.TLSClientConfig = common.InsecureTLSConfig
	}
	return &http.Client{
		Transport: transport,
		Timeout:   ratioMonitorTimeout,
		// 面板地址由管理员填写，限制重定向跳数避免被当作盲 SSRF 探针；
		// Go 跨域重定向本身会剥离 Authorization，这里再收紧跳数。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 2 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

// RatioPanelURL 面板域名优先于 base_url。
// ai-wave 实测：渠道 base_url 是 api2.ai-wave.org（纯 relay，/api/* 全 404），面板在 www.ai-wave.org。
func RatioPanelURL(channel *model.Channel) string {
	if channel.RatioPanelUrl != nil {
		if trimmed := strings.TrimRight(strings.TrimSpace(*channel.RatioPanelUrl), "/"); trimmed != "" {
			return trimmed
		}
	}
	// 只认显式配置的 base_url：GetBaseURL() 在为空时会回落到官方域名（constant.ChannelBaseURLs），
	// 那会让我们带着渠道 key 去打 api.openai.com 之类的不存在路径。
	if channel.BaseURL == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(*channel.BaseURL), "/")
}

func ratioMonitorGet(ctx context.Context, client *http.Client, url string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, ratioMonitorMaxBodySize))
}

// FetchDoneHubGroupRatios donehub(one-hub 系) /api/user_group_map。
// 该端点挂 TrySetUserBySession，只读 session cookie，匿名可访问；带 Bearer token 无额外收益。
func FetchDoneHubGroupRatios(ctx context.Context, client *http.Client, panelURL string) ([]GroupRatioSample, error) {
	body, err := ratioMonitorGet(ctx, client, panelURL+"/api/user_group_map", nil)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Success bool `json:"success"`
		Data    map[string]struct {
			Symbol  string  `json:"symbol"`
			Ratio   float64 `json:"ratio"`
			APIRate int     `json:"api_rate"`
			Public  bool    `json:"public"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if !parsed.Success || len(parsed.Data) == 0 {
		return nil, fmt.Errorf("empty user_group_map")
	}
	samples := make([]GroupRatioSample, 0, len(parsed.Data)*2)
	for name, g := range parsed.Data {
		samples = append(samples, GroupRatioSample{GroupName: name, RatioKind: RatioKindGroup, Value: g.Ratio})
		if g.APIRate > 0 {
			samples = append(samples, GroupRatioSample{GroupName: name, RatioKind: RatioKindAPIRate, Value: float64(g.APIRate)})
		}
	}
	return samples, nil
}

// FetchSub2APIGroupRatios sub2api /v1/sub2api/billing，用渠道 API key 鉴权。
// 只返回该 key 所属分组的倍率，含上游给我们的专属折扣（user_rate_multiplier）。
func FetchSub2APIGroupRatios(ctx context.Context, client *http.Client, panelURL, apiKey string) ([]GroupRatioSample, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("missing api key")
	}
	body, err := ratioMonitorGet(ctx, client, panelURL+"/v1/sub2api/billing", map[string]string{
		"Authorization": "Bearer " + strings.TrimSpace(apiKey),
	})
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Object                  string   `json:"object"`
		GroupRateMultiplier     float64  `json:"group_rate_multiplier"`
		ResolvedRateMultiplier  float64  `json:"resolved_rate_multiplier"`
		EffectiveRateMultiplier float64  `json:"effective_rate_multiplier"`
		PeakRateEnabled         bool     `json:"peak_rate_enabled"`
		PeakStart               *string  `json:"peak_start"`
		PeakEnd                 *string  `json:"peak_end"`
		PeakRateMultiplier      *float64 `json:"peak_rate_multiplier"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Object != "sub2api.key_billing" {
		return nil, fmt.Errorf("unexpected object %q", parsed.Object)
	}

	// sub2api 的 billing 端点不返回分组名，用固定键代表"本 key 所属分组"。
	const name = sub2apiKeyGroupName
	peak := ""
	if parsed.PeakRateEnabled {
		peakInfo := map[string]any{"start": parsed.PeakStart, "end": parsed.PeakEnd, "multiplier": parsed.PeakRateMultiplier}
		if encoded, err := json.Marshal(peakInfo); err == nil {
			peak = string(encoded)
		}
	}
	// resolved 是比对基准（稳定值）；effective 含当前时段 peak，进入高峰时段不应被误判为涨价。
	return []GroupRatioSample{
		{GroupName: name, RatioKind: RatioKindGroup, Value: parsed.GroupRateMultiplier},
		{GroupName: name, RatioKind: RatioKindResolved, Value: parsed.ResolvedRateMultiplier, Extra: peak},
		{GroupName: name, RatioKind: RatioKindEffective, Value: parsed.EffectiveRateMultiplier},
	}, nil
}

// FetchNewAPIGroupRatios new-api /api/pricing 的 group_ratio。
// 注意：/api/ratio_config 的 GetExposedData 写死 5 个字段，永远不含 group_ratio，不能用。
// token 非空时带 sub_sites 凭据，可见上游给我们开的专属分组。
func FetchNewAPIGroupRatios(ctx context.Context, client *http.Client, panelURL, token string, upstreamUserId int) ([]GroupRatioSample, map[string]ModelRatio, error) {
	headers := map[string]string{}
	if strings.TrimSpace(token) != "" {
		headers["Authorization"] = "Bearer " + strings.TrimSpace(token)
		if upstreamUserId > 0 {
			// newapi access_token 鉴权强制要求该头，缺了必 401。
			headers["New-Api-User"] = strconv.Itoa(upstreamUserId)
		}
	}
	body, err := ratioMonitorGet(ctx, client, panelURL+"/api/pricing", headers)
	if err != nil {
		return nil, nil, err
	}
	var parsed struct {
		Success    bool               `json:"success"`
		GroupRatio map[string]float64 `json:"group_ratio"`
		Data       []struct {
			ModelName       string   `json:"model_name"`
			EnableGroups    []string `json:"enable_groups"`
			ModelRatio      float64  `json:"model_ratio"`
			CompletionRatio float64  `json:"completion_ratio"`
			QuotaType       int      `json:"quota_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, nil, err
	}
	if len(parsed.GroupRatio) == 0 {
		return nil, nil, fmt.Errorf("empty group_ratio")
	}
	// 反查 group -> models，供 inferUpstreamGroup 按模型集合定位我方 key 所属分组
	groupModels := map[string]map[string]struct{}{}
	modelRatios := make(map[string]ModelRatio, len(parsed.Data))
	for _, item := range parsed.Data {
		for _, g := range item.EnableGroups {
			if groupModels[g] == nil {
				groupModels[g] = map[string]struct{}{}
			}
			groupModels[g][item.ModelName] = struct{}{}
		}
		modelRatios[item.ModelName] = ModelRatio{
			Ratio:           item.ModelRatio,
			CompletionRatio: item.CompletionRatio,
			PerRequest:      item.QuotaType == 1,
		}
	}

	samples := make([]GroupRatioSample, 0, len(parsed.GroupRatio))
	for name, ratio := range parsed.GroupRatio {
		samples = append(samples, GroupRatioSample{
			GroupName: name,
			RatioKind: RatioKindGroup,
			Value:     ratio,
			Models:    groupModels[name],
		})
	}
	return samples, modelRatios, nil
}

// fetchTokenModels 用渠道自己的 key 取 /v1/models。
// new-api 的该端点按 token 所属分组返回模型（controller/model.go:ListModels），
// 是从外部定位"我方 key 属于哪个分组"的唯一线索。
func fetchTokenModels(ctx context.Context, client *http.Client, panelURL, apiKey string) map[string]struct{} {
	if strings.TrimSpace(apiKey) == "" {
		return nil
	}
	body, err := ratioMonitorGet(ctx, client, panelURL+"/v1/models", map[string]string{
		"Authorization": "Bearer " + strings.TrimSpace(apiKey),
	})
	if err != nil {
		return nil
	}
	var parsed struct {
		Data []struct {
			Id string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || len(parsed.Data) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(parsed.Data))
	for _, m := range parsed.Data {
		out[m.Id] = struct{}{}
	}
	return out
}

// inferUpstreamGroup 按模型集合反推我方 key 所属分组。
// 先试精确相等，再退化到"我方是某组子集"（token 可能配了模型白名单使集合更窄）。
// 只有唯一命中才采纳——多组候选时倍率可能相差数倍，猜错比不显示更糟。
func inferUpstreamGroup(samples []GroupRatioSample, mine map[string]struct{}) string {
	if len(mine) == 0 {
		return ""
	}
	equal := make([]string, 0, 2)
	subset := make([]string, 0, 4)
	for _, s := range samples {
		if s.RatioKind != RatioKindGroup || len(s.Models) == 0 {
			continue
		}
		if len(s.Models) == len(mine) && isSubset(mine, s.Models) {
			equal = append(equal, s.GroupName)
		}
		if isSubset(mine, s.Models) {
			subset = append(subset, s.GroupName)
		}
	}
	if len(equal) == 1 {
		return equal[0]
	}
	if len(equal) == 0 && len(subset) == 1 {
		return subset[0]
	}
	return ""
}

// describeGroupInference 说明反推为何没能唯一定位，供管理员判断该填哪个分组名。
// 返回空表示已成功定位，无需说明。
func describeGroupInference(samples []GroupRatioSample, mine map[string]struct{}) string {
	if len(mine) == 0 {
		return "无法读取本 key 可用模型列表"
	}
	type cand struct {
		name  string
		ratio float64
	}
	matched := make([]cand, 0, 4)
	for _, s := range samples {
		if s.RatioKind != RatioKindGroup || len(s.Models) == 0 {
			continue
		}
		if isSubset(mine, s.Models) {
			matched = append(matched, cand{s.GroupName, s.Value})
		}
	}
	if len(matched) == 0 {
		return fmt.Sprintf("本 key 的 %d 个模型不属于任何上游分组", len(mine))
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ratio < matched[j].ratio })
	parts := make([]string, 0, len(matched))
	for _, m := range matched {
		if len(parts) == 4 {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, fmt.Sprintf("%s(%g)", m.name, m.ratio))
	}
	return fmt.Sprintf("本 key 的模型同时属于 %d 个分组：%s", len(matched), strings.Join(parts, "、"))
}

func isSubset(sub, super map[string]struct{}) bool {
	for k := range sub {
		if _, ok := super[k]; !ok {
			return false
		}
	}
	return true
}

// PanelCache 同一面板的抓取结果缓存，一轮内复用。
// pomoai 一家对应 18 个渠道，每小时一轮若不去重就是每天 432 次相同请求，易触发上游限频。
// 仅缓存"面板级"结果（donehub/newapi 的分组倍率表全站一致）；
// sub2api 的 billing 按 key 返回，各渠道不同，不进缓存。
type PanelCache struct {
	mu   sync.Mutex
	data map[string]UpstreamRatioResult
}

func NewPanelCache() *PanelCache {
	return &PanelCache{data: map[string]UpstreamRatioResult{}}
}

func (c *PanelCache) get(key string) (UpstreamRatioResult, bool) {
	if c == nil {
		return UpstreamRatioResult{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data[key]
	return v, ok
}

func (c *PanelCache) put(key string, v UpstreamRatioResult) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = v
}

// FetchChannelGroupRatios 按已知类型抓取；kind 为空时依次探测 donehub -> sub2api -> newapi。
func FetchChannelGroupRatios(ctx context.Context, client *http.Client, channel *model.Channel, kind string, creds map[string]SubSiteCredential, panelCache *PanelCache) UpstreamRatioResult {
	panelURL := RatioPanelURL(channel)
	if !strings.HasPrefix(panelURL, "http") {
		return UpstreamRatioResult{Kind: UpstreamKindUnsupported, Err: fmt.Errorf("invalid panel url")}
	}

	apiKey := firstEnabledKey(channel)
	cred := creds[panelURL]

	try := func(k string) UpstreamRatioResult {
		// donehub / newapi 的分组倍率是面板级数据，同面板多渠道共享一次抓取。
		// newapi 还需按 key 反推分组，故缓存里只复用 samples，Group 仍逐渠道算。
		cacheKey := k + "|" + panelURL
		cached, hit := panelCache.get(cacheKey)

		var samples []GroupRatioSample
		var modelRatios map[string]ModelRatio
		var err error
		if hit && k != UpstreamKindSub2API {
			samples, modelRatios, err = cached.Samples, cached.ModelRatios, cached.Err
		} else {
			switch k {
			case UpstreamKindDoneHub:
				samples, err = FetchDoneHubGroupRatios(ctx, client, panelURL)
			case UpstreamKindSub2API:
				samples, err = FetchSub2APIGroupRatios(ctx, client, panelURL, apiKey)
			case UpstreamKindNewAPI:
				samples, modelRatios, err = FetchNewAPIGroupRatios(ctx, client, panelURL, cred.Token, cred.UpstreamUserId)
			}
			if k != UpstreamKindSub2API {
				panelCache.put(cacheKey, UpstreamRatioResult{Kind: k, Samples: samples, ModelRatios: modelRatios, Err: err})
			}
		}
		result := UpstreamRatioResult{Kind: k, Samples: samples, ModelRatios: modelRatios, Err: err}
		if err == nil && k == UpstreamKindNewAPI {
			mine := fetchTokenModels(ctx, client, panelURL, apiKey)
			result.Group = inferUpstreamGroup(samples, mine)
			if result.Group == "" {
				result.GroupHint = describeGroupInference(samples, mine)
			}
		}
		if k == UpstreamKindSub2API {
			// sub2api 的 billing 端点本身就按 key 返回，天然精确
			result.Group = sub2apiKeyGroupName
		}
		return result
	}

	if kind != "" && kind != UpstreamKindUnsupported {
		return try(kind)
	}

	var lastErr error
	for _, k := range []string{UpstreamKindDoneHub, UpstreamKindSub2API, UpstreamKindNewAPI} {
		result := try(k)
		if result.Err == nil && len(result.Samples) > 0 {
			return result
		}
		lastErr = result.Err
	}
	return UpstreamRatioResult{Kind: UpstreamKindUnsupported, Err: lastErr}
}

// SubSiteCredential 分站凭据，按面板域名索引。
type SubSiteCredential struct {
	Token          string
	UpstreamUserId int
}

// LoadSubSiteCredentials 一次性加载全部分站凭据，按面板域名建索引。
// 调用方在一轮抓取开始时加载一次并复用；不要在每个渠道里查，否则是 N 次全表扫。
func LoadSubSiteCredentials() map[string]SubSiteCredential {
	out := map[string]SubSiteCredential{}
	sites, err := model.GetEnabledSubSites()
	if err != nil {
		return out
	}
	for _, site := range sites {
		token, err := model.GetSubSiteTokenDecrypted(site.Id)
		if err != nil {
			continue
		}
		key := strings.TrimRight(strings.TrimSpace(site.BaseURL), "/")
		out[key] = SubSiteCredential{Token: token, UpstreamUserId: site.UpstreamUserId}
	}
	return out
}

// firstEnabledKey 只读取首个可用 key。
// 不用 GetNextEnabledKey：它会推进多 key 渠道的轮询指针并写库，
// 监控是只读功能，不该扰动线上 key 轮转。
func firstEnabledKey(channel *model.Channel) string {
	if !channel.ChannelInfo.IsMultiKey {
		return channel.Key
	}
	keys := channel.GetKeys()
	statusList := channel.ChannelInfo.MultiKeyStatusList
	for i, key := range keys {
		if statusList != nil {
			if status, ok := statusList[i]; ok && status != common.ChannelStatusEnabled {
				continue
			}
		}
		if strings.TrimSpace(key) != "" {
			return key
		}
	}
	return ""
}
