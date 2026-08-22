package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
)

// 三个 fetcher 的解析口径按真实上游响应体固化（08-05 实测样本）。
// 上游改结构会先在这里断，而不是在生产静默返回空。

func TestFetchDoneHubGroupRatios(t *testing.T) {
	// www.ai-wave.org/api/user_group_map 实测响应裁剪
	body := `{"success":true,"message":"","data":{
		"CODEX":{"id":1,"symbol":"CODEX","name":"Codex","ratio":0.15,"api_rate":9999,"public":true},
		"CLAUDE_OFFICIAL":{"id":2,"symbol":"CLAUDE_OFFICIAL","name":"Claude | Official","ratio":2.5,"api_rate":20000,"public":true}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user_group_map" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	samples, err := FetchDoneHubGroupRatios(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 个分组各产出 group + api_rate 两条
	if len(samples) != 4 {
		t.Fatalf("want 4 samples, got %d: %+v", len(samples), samples)
	}
	got := map[string]float64{}
	for _, s := range samples {
		got[s.GroupName+"/"+s.RatioKind] = s.Value
	}
	if got["CODEX/"+RatioKindGroup] != 0.15 {
		t.Errorf("CODEX group ratio = %v, want 0.15", got["CODEX/"+RatioKindGroup])
	}
	if got["CLAUDE_OFFICIAL/"+RatioKindAPIRate] != 20000 {
		t.Errorf("api_rate = %v, want 20000", got["CLAUDE_OFFICIAL/"+RatioKindAPIRate])
	}
}

func TestFetchSub2APIGroupRatios(t *testing.T) {
	// allincoding.cc 实测形态：挂牌 3.46，我们的专属价 0.15
	body := `{"object":"sub2api.key_billing","schema_version":1,"billing_scope":"token",
		"group_rate_multiplier":3.46,"user_rate_multiplier":0.15,"resolved_rate_multiplier":0.15,
		"peak_rate_enabled":false,"effective_rate_multiplier":0.15}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sub2api/billing" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want Bearer sk-test", got)
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	samples, err := FetchSub2APIGroupRatios(context.Background(), srv.Client(), srv.URL, "sk-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := map[string]float64{}
	for _, s := range samples {
		got[s.RatioKind] = s.Value
	}
	if got[RatioKindGroup] != 3.46 {
		t.Errorf("group = %v, want 3.46", got[RatioKindGroup])
	}
	// resolved 是比对基准：上游取消专属折扣时这个值会跳到 3.46
	if got[RatioKindResolved] != 0.15 {
		t.Errorf("resolved = %v, want 0.15", got[RatioKindResolved])
	}
}

func TestFetchSub2APIRejectsWrongObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"object":"something.else"}`))
	}))
	defer srv.Close()
	if _, err := FetchSub2APIGroupRatios(context.Background(), srv.Client(), srv.URL, "sk-test"); err == nil {
		t.Fatal("want error on unexpected object, got nil")
	}
}

func TestFetchNewAPIGroupRatios(t *testing.T) {
	// 2api.powerapi.cc/api/pricing 实测形态裁剪
	body := `{"success":true,"data":[],"group_ratio":{"aws bedrock":2,"GLM":0.2},"usable_group":{"aws bedrock":"","GLM":""}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pricing" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		// 带 token 时必须同时带 New-Api-User，缺了上游必 401
		if r.Header.Get("Authorization") != "" && r.Header.Get("New-Api-User") == "" {
			t.Error("New-Api-User header missing while Authorization present")
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	samples, _, err := FetchNewAPIGroupRatios(context.Background(), srv.Client(), srv.URL, "tok", 2229)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("want 2 samples, got %d", len(samples))
	}
	for _, s := range samples {
		if s.RatioKind != RatioKindGroup {
			t.Errorf("kind = %s, want %s", s.RatioKind, RatioKindGroup)
		}
	}
}

// /api/ratio_config 恒不含 group_ratio（GetExposedData 写死 5 字段），
// 误用该端点时必须报错而不是静默返回空。
func TestFetchNewAPIRejectsEmptyGroupRatio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":{"model_ratio":{"gpt-4":15}}}`))
	}))
	defer srv.Close()
	if _, _, err := FetchNewAPIGroupRatios(context.Background(), srv.Client(), srv.URL, "", 0); err == nil {
		t.Fatal("want error on empty group_ratio, got nil")
	}
}

// 已被判为 unsupported 的渠道必须在后续轮次重新探测三类端点，
// 否则上游临时 500 会把渠道永久钉死在 unsupported 再也不监控。
func TestUnsupportedKindStillReprobes(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/api/pricing" {
			w.Write([]byte(`{"success":true,"group_ratio":{"default":1}}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	panel := srv.URL
	ch := &model.Channel{Id: 1, RatioPanelUrl: &panel}
	res := FetchChannelGroupRatios(context.Background(), srv.Client(), ch,
		UpstreamKindUnsupported, map[string]SubSiteCredential{}, NewPanelCache())
	if res.Err != nil {
		t.Fatalf("reprobe should succeed, got %v", res.Err)
	}
	if res.Kind != UpstreamKindNewAPI {
		t.Fatalf("kind = %s, want %s", res.Kind, UpstreamKindNewAPI)
	}
	if hits < 2 {
		t.Errorf("expected multiple probe attempts, got %d", hits)
	}
}

// 已知类型的渠道不应再试其它两类端点，避免每轮多打两次无效请求。
func TestKnownKindSkipsProbing(t *testing.T) {
	paths := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths[r.URL.Path]++
		w.Write([]byte(`{"success":true,"group_ratio":{"default":1}}`))
	}))
	defer srv.Close()

	panel := srv.URL
	ch := &model.Channel{Id: 1, RatioPanelUrl: &panel}
	FetchChannelGroupRatios(context.Background(), srv.Client(), ch,
		UpstreamKindNewAPI, map[string]SubSiteCredential{}, NewPanelCache())
	if paths["/api/user_group_map"] != 0 || paths["/v1/sub2api/billing"] != 0 {
		t.Errorf("known kind must not probe other endpoints: %+v", paths)
	}
	if paths["/api/pricing"] != 1 {
		t.Errorf("want 1 pricing call, got %d", paths["/api/pricing"])
	}
}

func groupSample(name string, ratio float64, models ...string) GroupRatioSample {
	set := map[string]struct{}{}
	for _, m := range models {
		set[m] = struct{}{}
	}
	return GroupRatioSample{GroupName: name, RatioKind: RatioKindGroup, Value: ratio, Models: set}
}

func modelSet(models ...string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, m := range models {
		set[m] = struct{}{}
	}
	return set
}

// 模型集合精确相等且唯一时采纳（guicore 实测形态：我方 7 个模型只匹配 gemini 组）。
func TestInferUpstreamGroupExactUnique(t *testing.T) {
	samples := []GroupRatioSample{
		groupSample("gemini", 0.4, "gemini-3-pro", "gemini-3-flash"),
		groupSample("claude_max", 1.35, "claude-opus-5", "claude-sonnet-5"),
	}
	if got := inferUpstreamGroup(samples, modelSet("gemini-3-pro", "gemini-3-flash")); got != "gemini" {
		t.Fatalf("got %q, want gemini", got)
	}
}

// 我方 token 配了模型白名单导致集合更窄时，退化到唯一子集匹配。
func TestInferUpstreamGroupSubsetFallback(t *testing.T) {
	samples := []GroupRatioSample{
		groupSample("default", 1, "a", "b", "c"),
		groupSample("premium", 5, "x", "y"),
	}
	if got := inferUpstreamGroup(samples, modelSet("a", "b")); got != "default" {
		t.Fatalf("got %q, want default", got)
	}
}

// 多组候选且倍率不同时必须放弃猜测——猜错会显示错误的采购价。
func TestInferUpstreamGroupAmbiguousReturnsEmpty(t *testing.T) {
	samples := []GroupRatioSample{
		groupSample("KIRO", 0.1, "claude-opus-5"),
		groupSample("KIRO_100", 0.2, "claude-opus-5"),
	}
	if got := inferUpstreamGroup(samples, modelSet("claude-opus-5")); got != "" {
		t.Fatalf("ambiguous case must return empty, got %q", got)
	}
}

func TestInferUpstreamGroupNoModelsReturnsEmpty(t *testing.T) {
	samples := []GroupRatioSample{groupSample("g", 1, "a")}
	if got := inferUpstreamGroup(samples, nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// 定位到分组后只展示该组倍率，而不是全表区间。
func TestBuildRatioSummaryPrefersLocatedGroup(t *testing.T) {
	samples := []GroupRatioSample{
		groupSample("cheap", 0.3, "a"),
		groupSample("mine", 1.8, "b"),
		groupSample("pricey", 5.25, "c"),
	}
	got := buildRatioSummary(samples, "mine")
	if got != `{"n":1,"min":1.8,"max":1.8,"g":"mine"}` {
		t.Fatalf("summary = %s", got)
	}
	// 未定位到分组时退回区间，且不带分组名
	fallback := buildRatioSummary(samples, "")
	if fallback != `{"n":3,"min":0.3,"max":5.25}` {
		t.Fatalf("fallback = %s", fallback)
	}
}

// 同面板多渠道只抓一次：pomoai 一家 18 个渠道，每小时一轮若不去重
// 就是每天 432 次相同请求，足以触发上游限频。
func TestPanelCacheDedupesSamePanel(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/pricing" {
			hits++
		}
		w.Write([]byte(`{"success":true,"group_ratio":{"default":1}}`))
	}))
	defer srv.Close()

	panel := srv.URL
	cache := NewPanelCache()
	for i := 0; i < 5; i++ {
		ch := &model.Channel{Id: i + 1, RatioPanelUrl: &panel}
		res := FetchChannelGroupRatios(context.Background(), srv.Client(), ch,
			UpstreamKindNewAPI, map[string]SubSiteCredential{}, cache)
		if res.Err != nil {
			t.Fatalf("channel %d: %v", i+1, res.Err)
		}
		if len(res.Samples) != 1 {
			t.Fatalf("channel %d got %d samples", i+1, len(res.Samples))
		}
	}
	if hits != 1 {
		t.Errorf("pricing fetched %d times, want 1 (cached across channels)", hits)
	}
}

// sub2api 的 billing 按 key 返回，各渠道结果不同，绝不能走面板缓存。
func TestPanelCacheDoesNotCacheSub2API(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"object":"sub2api.key_billing","group_rate_multiplier":1,` +
			`"resolved_rate_multiplier":1,"effective_rate_multiplier":1}`))
	}))
	defer srv.Close()

	panel := srv.URL
	cache := NewPanelCache()
	for i := 0; i < 3; i++ {
		ch := &model.Channel{Id: i + 1, RatioPanelUrl: &panel, Key: "sk-test"}
		FetchChannelGroupRatios(context.Background(), srv.Client(), ch,
			UpstreamKindSub2API, map[string]SubSiteCredential{}, cache)
	}
	if hits != 3 {
		t.Errorf("sub2api fetched %d times, want 3 (per-key, never cached)", hits)
	}
}
