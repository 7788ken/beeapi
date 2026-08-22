package doubao

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
)

func TestSdFlavorBuildAndFetchURLs(t *testing.T) {
	a := &TaskAdaptor{UpstreamFlavor: UpstreamFlavorSd}
	a.baseURL = "https://model.service-inference.ai"

	u, err := a.BuildRequestURL(nil)
	if err != nil || u != "https://model.service-inference.ai/v1/video/generate" {
		t.Fatalf("unexpected sd submit url: %q err=%v", u, err)
	}

	arm := &TaskAdaptor{}
	arm.baseURL = "https://ark.cn-beijing.volces.com"
	u2, _ := arm.BuildRequestURL(nil)
	if u2 != "https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks" {
		t.Fatalf("ark submit url regressed: %q", u2)
	}
}

func TestSdFlavorFetchTaskURL(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"task":{"id":"mvt-1","status":"processing"}}`))
	}))
	defer server.Close()

	a := &TaskAdaptor{UpstreamFlavor: UpstreamFlavorSd}
	resp, err := a.FetchTask(server.URL, "k", map[string]any{"task_id": "mvt-1"}, "")
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	resp.Body.Close()
	if gotPath != "/v1/video/tasks/mvt-1" {
		t.Fatalf("unexpected sd fetch path: %q", gotPath)
	}
}

func TestParseSdTaskEnvelope(t *testing.T) {
	task, err := parseSdTaskEnvelope([]byte(`{"task":{"id":"mvt-2","status":"pending"}}`))
	if err != nil || task.ID != "mvt-2" {
		t.Fatalf("envelope parse failed: %+v err=%v", task, err)
	}
	// 容忍裸任务对象
	bare, err := parseSdTaskEnvelope([]byte(`{"id":"mvt-3","status":"completed"}`))
	if err != nil || bare.ID != "mvt-3" {
		t.Fatalf("bare parse failed: %+v err=%v", bare, err)
	}
	if _, err := parseSdTaskEnvelope([]byte(`{"foo":1}`)); err == nil {
		t.Fatalf("expected error on unrecognized body")
	}
}

func TestParseSdTaskResultCompleted(t *testing.T) {
	body := `{"task":{"id":"mvt-179","status":"completed","model":"dreamina-seedance-2-0-260128",
		"duration_seconds":4,"outputs":["https://cdn/video.mp4"],"error":null,
		"created_at":"2026-05-26T05:26:52.505Z","completed_at":"2026-05-26T05:35:22.566Z",
		"usage":{"completion_tokens":40594,"total_tokens":40594},
		"last_frame_url":"https://base/v1/video/files/mvt-179/last-frame"}}`
	ti, err := parseSdTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if ti.Status != model.TaskStatusSuccess || ti.Url != "https://cdn/video.mp4" {
		t.Fatalf("unexpected result: %+v", ti)
	}
	if ti.CompletionTokens != 40594 || ti.TotalTokens != 40594 {
		t.Fatalf("usage not parsed: %+v", ti)
	}
}

func TestParseSdTaskResultStatuses(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{"pending", string(model.TaskStatusQueued)},
		{"processing", string(model.TaskStatusInProgress)},
		{"failed", string(model.TaskStatusFailure)},
		{"weird-unknown", string(model.TaskStatusInProgress)},
	}
	for _, tc := range cases {
		ti, err := parseSdTaskResult([]byte(`{"task":{"id":"x","status":"` + tc.status + `"}}`))
		if err != nil || string(ti.Status) != tc.want {
			t.Fatalf("status %q → %v (want %v), err=%v", tc.status, ti.Status, tc.want, err)
		}
	}
}

func TestSdFailReasonShapes(t *testing.T) {
	if got := sdFailReason(nil); !strings.Contains(got, "without details") {
		t.Fatalf("nil error: %q", got)
	}
	if got := sdFailReason([]byte(`null`)); !strings.Contains(got, "without details") {
		t.Fatalf("null error: %q", got)
	}
	if got := sdFailReason([]byte(`"content policy violation"`)); got != "content policy violation" {
		t.Fatalf("string error: %q", got)
	}
	if got := sdFailReason([]byte(`{"code":"E100","message":"bad input"}`)); got != "code=E100: bad input" {
		t.Fatalf("object error: %q", got)
	}
}

func TestCreateAssetSdSuccess(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		_, _ = w.Write([]byte(`{"success":true,"data":{"Id":"asset-20260705003737-njxmg",
			"base_resp":{"status_code":0,"status_msg":"success"}}}`))
	}))
	defer server.Close()

	result, upErr, err := createAssetSd(context.Background(), server.URL, "sd-key", "", AssetCreateParams{
		URL: "https://example.com/a.jpg", Name: "avatar_front", AssetType: "Image",
		Model: "dreamina-seedance-2-0-hc",
	})
	if err != nil || upErr != nil {
		t.Fatalf("expected success, got err=%v upErr=%v", err, upErr)
	}
	if result.Id != "asset-20260705003737-njxmg" {
		t.Fatalf("unexpected id: %q", result.Id)
	}
	if gotPath != "POST /v1/sd/assets" {
		t.Fatalf("unexpected upstream path: %q", gotPath)
	}
	if gotAuth != "Bearer sd-key" {
		t.Fatalf("unexpected auth: %q", gotAuth)
	}
	for _, want := range []string{`"URL":"https://example.com/a.jpg"`, `"Name":"avatar_front"`, `"AssetType":"Image"`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body missing %s: %s", want, gotBody)
		}
	}
	// 旧体系不发 model 字段（上游忽略该字段，2026-07-17 实测；非 hc 模型走 sd2 素材组体系）
	if strings.Contains(gotBody, "model") {
		t.Fatalf("sd1 protocol must not send model, got: %s", gotBody)
	}
}

// 未显式指定 model 时不发该字段（素材归上游默认空间，防把分发默认值注册进错误空间）
func TestCreateAssetSdOmitsEmptyModel(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		_, _ = w.Write([]byte(`{"success":true,"data":{"Id":"asset-1","base_resp":{"status_code":0,"status_msg":"success"}}}`))
	}))
	defer server.Close()

	_, upErr, err := createAssetSd(context.Background(), server.URL, "k", "", AssetCreateParams{
		URL: "https://example.com/a.jpg", Name: "n", AssetType: "Image",
	})
	if err != nil || upErr != nil {
		t.Fatalf("expected success, got err=%v upErr=%v", err, upErr)
	}
	if strings.Contains(gotBody, "model") {
		t.Fatalf("empty model should be omitted, got: %s", gotBody)
	}
}

func TestGetAssetSdSuccessAndDispatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/sd/assets/asset-1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"Id":"asset-1","Status":"Active","AssetType":"Image",
			"Name":"avatar_front","URL":"https://cdn/a.jpg","GroupId":null,
			"CreateTime":"2026-07-04T12:15:34Z","UpdateTime":"2026-07-04T12:15:36Z",
			"base_resp":{"status_code":0,"status_msg":"success"}}}`))
	}))
	defer server.Close()

	// 经渠道类型分发入口（58 = sd 网关）
	result, upErr, err := GetAssetForChannel(context.Background(), 58, server.URL, "k", "", "asset-1")
	if err != nil || upErr != nil {
		t.Fatalf("expected success, got err=%v upErr=%v", err, upErr)
	}
	if result.Status != "Active" || result.URL != "https://cdn/a.jpg" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCreateAssetSdBusinessError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"success":false,"data":{"base_resp":{"status_code":1001,"status_msg":"invalid asset url"}}}`))
	}))
	defer server.Close()

	_, upErr, err := createAssetSd(context.Background(), server.URL, "k", "", AssetCreateParams{URL: "https://e.com/a.jpg", Name: "n", AssetType: "Image"})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if upErr == nil || upErr.Message != "invalid asset url" || upErr.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("unexpected upstream error: %+v", upErr)
	}
}

func TestCreateAssetSdNonJSONErrorDoesNotLeak(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`<html>secret-internal-host stacktrace</html>`))
	}))
	defer server.Close()

	_, upErr, err := createAssetSd(context.Background(), server.URL, "k", "", AssetCreateParams{URL: "https://e.com/a.jpg", Name: "n", AssetType: "Image"})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if upErr == nil || strings.Contains(upErr.Message, "secret-internal-host") {
		t.Fatalf("raw body leaked: %+v", upErr)
	}
}
