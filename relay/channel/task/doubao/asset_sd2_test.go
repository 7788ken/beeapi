package doubao

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsSd2AssetModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"dreamina-seedance-2-0-260128", true},
		{"dreamina-seedance-2-0-ep", true},
		{"dreamina-seedance-2-0-fast-ep", true},
		{"dreamina-seedance-2-0-mini-260615", true},
		{"dreamina-seedance-2-0-hc", false},
		{"DREAMINA-SEEDANCE-2-0-HC", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsSd2AssetModel(tc.model); got != tc.want {
			t.Fatalf("IsSd2AssetModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestMapSd2Status(t *testing.T) {
	cases := map[string]string{
		"processing": "Processing",
		"pending":    "Processing",
		"completed":  "Active",
		"failed":     "Failed",
		"":           "",
		"weird":      "weird",
	}
	for in, want := range cases {
		if got := mapSd2Status(in); got != want {
			t.Fatalf("mapSd2Status(%q) = %q, want %q", in, got, want)
		}
	}
}

// sd2Server 模拟 sd2 素材组上游：组创建/查询 + 素材上传/查询
func sd2Server(t *testing.T, groupExists bool) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/asset-groups":
			_, _ = w.Write([]byte(`{"id":"group-new-99w5l","name":"beeapi-ch7"}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/asset-groups/"):
			if groupExists {
				_, _ = w.Write([]byte(`{"id":"group-cached","group_type":"AIGC"}`))
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/v1/assets":
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			calls = append(calls, "body:"+string(buf))
			_, _ = w.Write([]byte(`{"id":"asset-sd2-pvcn4","task_id":"tsk-123","status":"processing"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/assets/get":
			_, _ = w.Write([]byte(`{"id":"asset-sd2-pvcn4","name":"child","url":"https://cdn/a.png","asset_type":"Image","group_id":"group-new-99w5l","status":"completed","error":null,"created_at":"2026-05-26T05:24:58Z","updated_at":"2026-05-26T05:25:03Z"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return server, &calls
}

func TestSd2CreateAndGetAsset(t *testing.T) {
	server, calls := sd2Server(t, false)
	defer server.Close()

	// 无缓存 → 创建组
	groupId, upErr, err := EnsureSd2Group(context.Background(), 7, server.URL, "k", "", "")
	if err != nil || upErr != nil || groupId != "group-new-99w5l" {
		t.Fatalf("ensure group failed: %q err=%v upErr=%v", groupId, err, upErr)
	}

	created, upErr, err := CreateAssetSd2(context.Background(), server.URL, "k", "", groupId, AssetCreateParams{
		URL: "https://example.com/a.png", Name: "child", AssetType: "Image",
	})
	if err != nil || upErr != nil {
		t.Fatalf("create failed: err=%v upErr=%v", err, upErr)
	}
	if created.AssetID != "asset-sd2-pvcn4" || created.TaskID != "tsk-123" || created.Status != "Processing" {
		t.Fatalf("unexpected create result: %+v", created)
	}

	// 上传 body 用 sd2 小写字段
	joined := strings.Join(*calls, "\n")
	for _, want := range []string{`"group_id":"group-new-99w5l"`, `"url":"https://example.com/a.png"`, `"asset_type":"Image"`, `"name":"child"`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("create body missing %s in calls: %s", want, joined)
		}
	}

	result, upErr, err := GetAssetSd2(context.Background(), server.URL, "k", "", created.AssetID, created.TaskID)
	if err != nil || upErr != nil {
		t.Fatalf("get failed: err=%v upErr=%v", err, upErr)
	}
	if result.Status != "Active" || result.Id != "asset-sd2-pvcn4" || result.GroupId == nil {
		t.Fatalf("unexpected get result: %+v", result)
	}
}

func TestEnsureSd2GroupReusesCachedWhenExists(t *testing.T) {
	server, calls := sd2Server(t, true)
	defer server.Close()

	sd2GroupCacheMu.Lock()
	sd2GroupCache[42] = "group-cached"
	sd2GroupCacheMu.Unlock()
	defer func() {
		sd2GroupCacheMu.Lock()
		delete(sd2GroupCache, 42)
		sd2GroupCacheMu.Unlock()
	}()

	groupId, upErr, err := EnsureSd2Group(context.Background(), 42, server.URL, "k", "", "")
	if err != nil || upErr != nil || groupId != "group-cached" {
		t.Fatalf("expected cached group, got %q err=%v upErr=%v", groupId, err, upErr)
	}
	joined := strings.Join(*calls, "\n")
	if strings.Contains(joined, "POST /v1/asset-groups") {
		t.Fatalf("should not recreate group when cached exists: %s", joined)
	}
}

func TestEnsureSd2GroupRebuildsWhenRotatedAway(t *testing.T) {
	server, _ := sd2Server(t, false) // 组查询 404 = 已被上游轮转回收
	defer server.Close()

	sd2GroupCacheMu.Lock()
	sd2GroupCache[43] = "group-rotated-away"
	sd2GroupCacheMu.Unlock()
	defer func() {
		sd2GroupCacheMu.Lock()
		delete(sd2GroupCache, 43)
		sd2GroupCacheMu.Unlock()
	}()

	groupId, upErr, err := EnsureSd2Group(context.Background(), 43, server.URL, "k", "", "")
	if err != nil || upErr != nil || groupId != "group-new-99w5l" {
		t.Fatalf("expected rebuilt group, got %q err=%v upErr=%v", groupId, err, upErr)
	}
}

func TestEnsureSd2GroupExplicitWins(t *testing.T) {
	groupId, upErr, err := EnsureSd2Group(context.Background(), 44, "http://unused", "k", "", "group-explicit")
	if err != nil || upErr != nil || groupId != "group-explicit" {
		t.Fatalf("explicit group should be used directly, got %q err=%v upErr=%v", groupId, err, upErr)
	}
}

func TestSd2UpstreamErrorSanitized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`<html>internal-secret stacktrace</html>`))
	}))
	defer server.Close()

	_, upErr, err := CreateAssetSd2(context.Background(), server.URL, "k", "", "g", AssetCreateParams{URL: "https://e.com/a.png", Name: "n", AssetType: "Image"})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if upErr == nil || strings.Contains(upErr.Message, "internal-secret") {
		t.Fatalf("raw body leaked: %+v", upErr)
	}

	// 结构化错误应透出 code/message
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"InvalidParameter","message":"bad url"}}`))
	}))
	defer server2.Close()
	_, upErr2, _ := CreateAssetSd2(context.Background(), server2.URL, "k", "", "g", AssetCreateParams{URL: "https://e.com/a.png", Name: "n", AssetType: "Image"})
	if upErr2 == nil || upErr2.Code != "InvalidParameter" || upErr2.Message != "bad url" {
		t.Fatalf("structured error not parsed: %+v", upErr2)
	}
}
