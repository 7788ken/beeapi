package doubao

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsSdMaxAssetModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"doubao-seedance-2-0-260128-max", true},
		{"doubao-seedance-2-0-fast-260128-max", true},
		{"doubao-seedance-2-0-mini-260615-max", true},
		{"doubao-seedance-2-5-260628-max", true},
		{"dreamina-seedance-2-0-260128-max", true}, // 海外品牌 max 同走 db-sd-max
		{"DOUBAO-SEEDANCE-2-0-260128-MAX", true},   // 大小写不敏感
		{"  doubao-seedance-2-0-260128-max  ", true},
		{"doubao-seedance-2-0-260128", false}, // 非 max
		{"dreamina-seedance-2-0-hc", false},   // hc 旧体系
		{"dreamina-seedance-2-0-mini-260615", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsSdMaxAssetModel(tc.model); got != tc.want {
			t.Fatalf("IsSdMaxAssetModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

// TestSdMaxTakesPrecedenceOverSd2 固化判定顺序：-max 模型同时满足 IsSd2AssetModel
// （不含 "-hc"），故 controller 必须先判 IsSdMaxAssetModel，否则会被误路由进 sd2 素材组。
func TestSdMaxTakesPrecedenceOverSd2(t *testing.T) {
	const m = "doubao-seedance-2-0-260128-max"
	if !IsSdMaxAssetModel(m) {
		t.Fatalf("expected IsSdMaxAssetModel(%q)=true", m)
	}
	if !IsSd2AssetModel(m) {
		t.Fatalf("sanity: %q 也应满足 IsSd2AssetModel（说明判定顺序敏感）", m)
	}
}

// sdMaxServer 模拟 db-sd-max 素材上游：POST 上传（异步 Processing）+ GET 查询（Active）。
func sdMaxServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/db-sd-max/assets":
			b, _ := io.ReadAll(r.Body)
			calls = append(calls, "body:"+string(b))
			_, _ = w.Write([]byte(`{"success":true,"data":{"Id":"mva-e144dfc364a647f1","Ref":"asset://mva-e144dfc364a647f1","Status":"Processing","AssetType":"Image","Name":"strawberry-ref","Model":"doubao-seedance-2-0-260128-max","URL":"https://cdn/a.png","Lines":{"total":2,"active":0,"failed":0},"Error":null,"CreateTime":"2026-09-03T03:51:53.442Z","UpdateTime":"2026-09-03T03:51:53.442Z"}}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v2/db-sd-max/assets/"):
			_, _ = w.Write([]byte(`{"success":true,"data":{"Id":"mva-e144dfc364a647f1","Ref":"asset://mva-e144dfc364a647f1","Status":"Active","AssetType":"Image","Name":"strawberry-ref","Model":"doubao-seedance-2-0-260128-max","URL":"https://cdn/a.png","Lines":{"total":2,"active":1,"failed":0},"Error":null,"CreateTime":"2026-09-03T03:51:53.442Z","UpdateTime":"2026-09-03T03:51:55.295Z"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return server, &calls
}

func TestSdMaxCreateAndGetAsset(t *testing.T) {
	server, calls := sdMaxServer(t)
	defer server.Close()

	created, upErr, err := CreateAssetSdMax(context.Background(), server.URL, "k", "", AssetCreateParams{
		URL: "https://cdn/a.png", Name: "strawberry-ref", AssetType: "Image", Model: "doubao-seedance-2-0-260128-max",
	})
	if err != nil || upErr != nil {
		t.Fatalf("create failed: err=%v upErr=%v", err, upErr)
	}
	if created.Id != "mva-e144dfc364a647f1" || created.Status != "Processing" || created.AssetType != "Image" {
		t.Fatalf("unexpected create result: %+v", created)
	}

	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "POST /v2/db-sd-max/assets") {
		t.Fatalf("create did not hit /v2/db-sd-max/assets: %s", joined)
	}
	// 上传 body 用 PascalCase 字段 + Model
	for _, want := range []string{`"URL":"https://cdn/a.png"`, `"Name":"strawberry-ref"`, `"AssetType":"Image"`, `"Model":"doubao-seedance-2-0-260128-max"`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("create body missing %s in calls: %s", want, joined)
		}
	}

	result, upErr, err := GetAssetSdMax(context.Background(), server.URL, "k", "", created.Id)
	if err != nil || upErr != nil {
		t.Fatalf("get failed: err=%v upErr=%v", err, upErr)
	}
	if result.Status != "Active" || result.Id != "mva-e144dfc364a647f1" {
		t.Fatalf("unexpected get result: %+v", result)
	}
	if !strings.Contains(strings.Join(*calls, "\n"), "GET /v2/db-sd-max/assets/mva-e144dfc364a647f1") {
		t.Fatalf("get did not hit expected path: %s", strings.Join(*calls, "\n"))
	}
}

// TestSdMaxCreateOmitsEmptyModel 上游 Model 可选：为空时不应出现在上传 body。
func TestSdMaxCreateOmitsEmptyModel(t *testing.T) {
	server, calls := sdMaxServer(t)
	defer server.Close()

	if _, upErr, err := CreateAssetSdMax(context.Background(), server.URL, "k", "", AssetCreateParams{
		URL: "https://cdn/a.png", Name: "n", AssetType: "Image", // Model 留空
	}); err != nil || upErr != nil {
		t.Fatalf("create failed: err=%v upErr=%v", err, upErr)
	}
	if joined := strings.Join(*calls, "\n"); strings.Contains(joined, `"Model"`) {
		t.Fatalf("empty Model should be omitted from body, got: %s", joined)
	}
}

func TestSdMaxUpstreamErrorSanitized(t *testing.T) {
	// 非 JSON 5xx：不泄露上游原文
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`<html>internal-secret stacktrace</html>`))
	}))
	defer server.Close()
	_, upErr, err := CreateAssetSdMax(context.Background(), server.URL, "k", "", AssetCreateParams{URL: "https://e.com/a.png", Name: "n", AssetType: "Image"})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if upErr == nil || strings.Contains(upErr.Message, "internal-secret") {
		t.Fatalf("raw body leaked: %+v", upErr)
	}

	// success:false + message：透出 message（HTTP 200 但业务失败）
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"message":"URL not reachable","request_id":"r-1"}`))
	}))
	defer server2.Close()
	_, upErr2, _ := CreateAssetSdMax(context.Background(), server2.URL, "k", "", AssetCreateParams{URL: "https://e.com/a.png", Name: "n", AssetType: "Image"})
	if upErr2 == nil || upErr2.Message != "URL not reachable" {
		t.Fatalf("endpoint error message not surfaced: %+v", upErr2)
	}
}
