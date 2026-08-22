package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func setAllowIntranetForTest(t *testing.T) {
	t.Helper()
	prev := common.OptionMap["SubSiteAllowIntranet"]
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMap["SubSiteAllowIntranet"] = "true"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap["SubSiteAllowIntranet"] = prev
		common.OptionMapRWMutex.Unlock()
	})
}

// OK 路径：普通用户 token（role=1）即可通过 verify，Role 字段写入真实值。
// 历史版本要求 root（硬编码 res.Role=100），现已降级到 UserAuth。
func TestDoSubSiteVerify_OK(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"version":"v0.9.9-test"}}`))
	})
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":21,"role":1,"username":"u21"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := doSubSiteVerify(context.Background(), srv.URL, "good", 21)
	if res.Status != "ok" {
		t.Fatalf("want ok, got %+v", res)
	}
	if res.Version != "v0.9.9-test" {
		t.Fatalf("want version, got %q", res.Version)
	}
	if res.Role != 1 {
		t.Fatalf("want role 1, got %d", res.Role)
	}
}

// Admin 用户（role=10）verify 通过，Role 字段记录真实值。
func TestDoSubSiteVerify_AdminUser(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"version":"vA"}}`))
	})
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":2,"role":10,"username":"admin"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := doSubSiteVerify(context.Background(), srv.URL, "admin-token", 2)
	if res.Status != "ok" {
		t.Fatalf("want ok, got %+v", res)
	}
	if res.Role != 10 {
		t.Fatalf("want role 10, got %d", res.Role)
	}
}

// 向后兼容：root token（role=100）仍然 verify 通过，行为不变。
func TestDoSubSiteVerify_RootUser(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"version":"vR"}}`))
	})
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"role":100,"username":"root"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := doSubSiteVerify(context.Background(), srv.URL, "root-token", 1)
	if res.Status != "ok" {
		t.Fatalf("want ok, got %+v", res)
	}
	if res.Role != 100 {
		t.Fatalf("want role 100, got %d", res.Role)
	}
}

func TestDoSubSiteVerify_AuthFailed(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"version":"vX"}}`))
	})
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := doSubSiteVerify(context.Background(), srv.URL, "bad", 21)
	if res.Status != "auth_failed" {
		t.Fatalf("want auth_failed, got %+v", res)
	}
}

// 回归：401 路径透传上游 message，便于排查 id mismatch / 未登录等具体原因。
// 历史实现写死 "401 unauthorized"，丢失了上游 i18n 文案。
func TestDoSubSiteVerify_AuthFailed_PassMessage(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"version":"vX"}}`))
	})
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"message":"用户 id 与 token 不匹配"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := doSubSiteVerify(context.Background(), srv.URL, "tok", 999)
	if res.Status != "auth_failed" {
		t.Fatalf("want auth_failed, got %+v", res)
	}
	if res.Message != "用户 id 与 token 不匹配" {
		t.Fatalf("want passed-through message, got %q", res.Message)
	}
}

// access token 路径下，HTTP 403 表示账号过期；保留 role_insufficient 分类兼容旧 UI 文案。
func TestDoSubSiteVerify_RoleInsufficient(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"version":"vY"}}`))
	})
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := doSubSiteVerify(context.Background(), srv.URL, "expired-token", 0)
	if res.Status != "role_insufficient" {
		t.Fatalf("want role_insufficient, got %+v", res)
	}
}

// 防御性兜底回归（EN）：即使新探针几乎不会返回 200+success:false+这种文案，
// 一旦未来上游某条边角路径返回 "Unauthorized, insufficient privileges"，
// 分类器仍应正确归到 role_insufficient（而非被 "unauthorized" 关键字吞成 auth_failed）。
func TestDoSubSiteVerify_RoleInsufficient_BodyMessage_EN(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"version":"vY"}}`))
	})
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":false,"message":"Unauthorized, insufficient privileges"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := doSubSiteVerify(context.Background(), srv.URL, "non-root-token", 0)
	if res.Status != "role_insufficient" {
		t.Fatalf("want role_insufficient, got %+v", res)
	}
}

// 防御性兜底回归（ZH）：中文 i18n 文案 "无权进行此操作，权限不足"，关键字 无权/权限 命中 role_insufficient。
func TestDoSubSiteVerify_RoleInsufficient_BodyMessage_ZH(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"version":"vY"}}`))
	})
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":false,"message":"无权进行此操作，权限不足"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := doSubSiteVerify(context.Background(), srv.URL, "non-root-token", 0)
	if res.Status != "role_insufficient" {
		t.Fatalf("want role_insufficient, got %+v", res)
	}
}

func TestFetchSubSiteFromGroups_StringList(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/group/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":["default","vip","svip"]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := newSubSiteHTTPClient(subSiteFetchTimeout)
	g, err := fetchSubSiteFromGroups(context.Background(), client, srv.URL, "t", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 3 {
		t.Fatalf("want 3, got %d", len(g))
	}
}

func TestFetchSubSiteFromPricing_GroupModelsReverseMap(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pricing", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"success": true,
			"usable_group": {"default":"普通","vip":"VIP"},
			"group_ratio": {"default":1, "vip":0.8},
			"data": [
				{"model_name":"gpt-4o","enable_groups":["default","vip"]},
				{"model_name":"claude-3","enable_groups":["vip"]}
			]
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := newSubSiteHTTPClient(subSiteFetchTimeout)
	g, err := fetchSubSiteFromPricing(context.Background(), client, srv.URL, "t", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 2 {
		t.Fatalf("want 2 groups, got %d", len(g))
	}
	for _, gp := range g {
		switch gp.Group {
		case "default":
			if len(gp.Models) != 1 || gp.Models[0] != "gpt-4o" {
				t.Fatalf("default models wrong: %v", gp.Models)
			}
		case "vip":
			if len(gp.Models) != 2 {
				t.Fatalf("vip models wrong: %v", gp.Models)
			}
			if gp.Ratio != 0.8 {
				t.Fatalf("vip ratio wrong: %v", gp.Ratio)
			}
		}
	}
}

func TestFetchSubSiteFromRatioConfig(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ratio_config", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"group_ratio": {"default":1, "vip":0.7},
				"group_group_ratio": {"vip": {"premium-tier": 0.5}}
			}
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := newSubSiteHTTPClient(subSiteFetchTimeout)
	g, err := fetchSubSiteFromRatioConfig(context.Background(), client, srv.URL, "t", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 2 {
		t.Fatalf("want 2, got %d", len(g))
	}
	for _, gp := range g {
		if gp.Group == "vip" && gp.TierOverrides["premium-tier"] != 0.5 {
			t.Fatalf("tier override mismatch: %+v", gp.TierOverrides)
		}
	}
}

// 回归：上游 /api/pricing 的 data[].enable_groups 字段未被上游按 usable_group 过滤，
// 仍含用户不可见的分组（如 svip/internal），不能用它撑大 keys，
// 否则会把用户没权限的分组泄漏到 sub_site 同步候选列表。
func TestFetchSubSiteFromPricing_EnableGroupsLeak(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pricing", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"success": true,
			"usable_group": {"default":"普通","vip":"VIP"},
			"group_ratio": {"default":1, "vip":0.8},
			"data": [
				{"model_name":"gpt-4o","enable_groups":["default","vip","svip","internal"]},
				{"model_name":"secret","enable_groups":["internal"]}
			]
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := newSubSiteHTTPClient(subSiteFetchTimeout)
	g, err := fetchSubSiteFromPricing(context.Background(), client, srv.URL, "t", 0)
	if err != nil {
		t.Fatal(err)
	}
	gotGroups := map[string]bool{}
	for _, gp := range g {
		gotGroups[gp.Group] = true
	}
	for _, leaked := range []string{"svip", "internal"} {
		if gotGroups[leaked] {
			t.Fatalf("leaked non-usable group %q via enable_groups: got %v", leaked, gotGroups)
		}
	}
	if !gotGroups["default"] || !gotGroups["vip"] {
		t.Fatalf("missing usable groups: %v", gotGroups)
	}
}

// 回归：usable_group 缺失（个别 fork 行为）时，应退化用 enable_groups 全集做 keys，
// 保持原 fallback 路径不被新逻辑误伤。
func TestFetchSubSiteFromPricing_UsableGroupMissing_Fallback(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pricing", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": [
				{"model_name":"m1","enable_groups":["a","b"]},
				{"model_name":"m2","enable_groups":["b","c"]}
			]
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := newSubSiteHTTPClient(subSiteFetchTimeout)
	g, err := fetchSubSiteFromPricing(context.Background(), client, srv.URL, "t", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 3 {
		t.Fatalf("want 3 (a/b/c fallback), got %d (%+v)", len(g), g)
	}
}

// 回归：fetchSubSiteGroups user 路径下（admin /api/group/ 失败 + pricing 是权威白名单），
// ratio_config 作为公开端点不能把白名单外的分组带入候选列表。
func TestFetchSubSiteGroups_UserPath_RatioConfigNoLeak(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	// admin 路径失败（普通用户无权限）
	mux.HandleFunc("/api/group/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	// user 路径：pricing 告知用户只可见 default + vip
	mux.HandleFunc("/api/pricing", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"success": true,
			"usable_group": {"default":"普通","vip":"VIP"},
			"group_ratio": {"default":1, "vip":0.8},
			"data": [{"model_name":"gpt-4o","enable_groups":["default","vip"]}]
		}`))
	})
	// ratio_config 公开返回全量（含用户看不到的 svip/internal）
	mux.HandleFunc("/api/ratio_config", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"group_ratio": {"default":1, "vip":0.8, "svip":0.5, "internal":0.1}
			}
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	groups, source, err := fetchSubSiteGroups(context.Background(), srv.URL, "user-token", 25)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, gp := range groups {
		got[gp.Group] = true
	}
	for _, leaked := range []string{"svip", "internal"} {
		if got[leaked] {
			t.Fatalf("leaked non-usable group %q via ratio_config: got %v (source=%s)", leaked, got, source)
		}
	}
	if !got["default"] || !got["vip"] {
		t.Fatalf("missing usable groups: %v", got)
	}
	if len(groups) != 2 {
		t.Fatalf("want exactly 2 groups, got %d (%v)", len(groups), got)
	}
}

// 回归：admin 路径下（/api/group/ 成功），ratio_config 仍可补全 admin 视角全量分组，
// 保持原 admin 行为不被新白名单逻辑误伤。
func TestFetchSubSiteGroups_AdminPath_RatioConfigStillEnriches(t *testing.T) {
	setAllowIntranetForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/group/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":["default","vip","svip","internal"]}`))
	})
	mux.HandleFunc("/api/pricing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"usable_group":{},"data":[]}`))
	})
	mux.HandleFunc("/api/ratio_config", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"group_ratio": {"default":1, "vip":0.8, "svip":0.5, "internal":0.1, "extra":2.0}
			}
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	groups, _, err := fetchSubSiteGroups(context.Background(), srv.URL, "admin-token", 1)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, gp := range groups {
		got[gp.Group] = true
	}
	for _, want := range []string{"default", "vip", "svip", "internal", "extra"} {
		if !got[want] {
			t.Fatalf("admin path lost group %q: %v", want, got)
		}
	}
}

func TestSubSiteTrimBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://x.com/":   "https://x.com",
		"  https://x.com/": "https://x.com",
		"https://x.com":    "https://x.com",
	}
	for in, want := range cases {
		if got := subSiteTrimBaseURL(in); got != want {
			t.Fatalf("trim %q want %q got %q", in, want, got)
		}
	}
}

func TestNewJSONReader(t *testing.T) {
	r, err := newJSONReader(map[string]any{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), `"a":1`) {
		t.Fatalf("unexpected: %s", string(buf[:n]))
	}
}
