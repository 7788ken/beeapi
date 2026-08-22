package doubao

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
)

// 默认出站 client 由 main 的 InitHttpClient 初始化，单测须自行初始化
func TestMain(m *testing.M) {
	service.InitHttpClient()
	os.Exit(m.Run())
}

func TestCreateAssetSuccess(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		gotAuth = r.Header.Get("Authorization")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ResponseMetadata":{},"Result":{"Id":"asset-20260716000000-abcde"}}`))
	}))
	defer server.Close()

	result, upErr, err := CreateAsset(context.Background(), server.URL, "test-key", "", AssetCreateParams{
		Model: "doubao-seedance-2-0", URL: "https://example.com/a.jpg", Name: "avatar", AssetType: "Image",
	})
	if err != nil || upErr != nil {
		t.Fatalf("expected success, got err=%v upErr=%v", err, upErr)
	}
	if result.Id != "asset-20260716000000-abcde" {
		t.Fatalf("unexpected asset id: %q", result.Id)
	}
	if !strings.Contains(gotPath, "Action=CreateAsset") || !strings.Contains(gotPath, "Version=2024-01-01") {
		t.Fatalf("unexpected upstream path: %q", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("unexpected auth header: %q", gotAuth)
	}
	for _, want := range []string{`"model":"doubao-seedance-2-0"`, `"URL":"https://example.com/a.jpg"`, `"AssetType":"Image"`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("request body missing %s, got: %s", want, gotBody)
		}
	}
	// GroupId 为空时应省略（omitempty）
	if strings.Contains(gotBody, "GroupId") {
		t.Fatalf("empty GroupId should be omitted, got: %s", gotBody)
	}
}

func TestCreateAssetStopsWhenContextIsCanceled(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseServer := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		select {
		case <-r.Context().Done():
		case <-releaseServer:
		}
	}))
	defer func() {
		close(releaseServer)
		server.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, _, err := CreateAsset(ctx, server.URL, "k", "", AssetCreateParams{
			Model: "m", URL: "https://e.com/a.jpg", Name: "n", AssetType: "Image",
		})
		result <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("asset request did not start")
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CreateAsset() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("asset request did not stop after context cancellation")
	}
}

func TestCreateAssetEmptyIdIsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ResponseMetadata":{},"Result":{}}`))
	}))
	defer server.Close()

	_, upErr, err := CreateAsset(context.Background(), server.URL, "k", "", AssetCreateParams{Model: "m", URL: "https://e.com/a.jpg", Name: "n", AssetType: "Image"})
	if err == nil || upErr != nil {
		t.Fatalf("expected local error on empty id, got err=%v upErr=%v", err, upErr)
	}
}

func TestGetAssetUpstreamBusinessError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"InvalidParameter","Message":"asset not found"}},"Result":{}}`))
	}))
	defer server.Close()

	_, upErr, err := GetAsset(context.Background(), server.URL, "k", "", "asset-x")
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if upErr == nil || upErr.Code != "InvalidParameter" || upErr.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("unexpected upstream error: %+v", upErr)
	}
}

func TestGetAssetNonJSONErrorDoesNotLeakBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`<html>internal-host-secret.volces.local stacktrace</html>`))
	}))
	defer server.Close()

	_, upErr, err := GetAsset(context.Background(), server.URL, "k", "", "asset-x")
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if upErr == nil || upErr.Code != "upstream_error" {
		t.Fatalf("expected sanitized upstream_error, got: %+v", upErr)
	}
	if strings.Contains(upErr.Message, "internal-host-secret") {
		t.Fatalf("upstream raw body leaked into error message: %q", upErr.Message)
	}
}

func TestGetAssetWhitelistDropsUnknownFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ResponseMetadata":{},"Result":{"Id":"asset-1","Status":"Active","AssetType":"Image",` +
			`"Name":"a","URL":"https://cdn/a.jpg","CreateTime":"2026-07-16T00:00:00Z","UpdateTime":"2026-07-16T00:00:01Z",` +
			`"InternalUploadPath":"/mnt/secret","PresignSecret":"xyz"}}`))
	}))
	defer server.Close()

	result, upErr, err := GetAsset(context.Background(), server.URL, "k", "", "asset-1")
	if err != nil || upErr != nil {
		t.Fatalf("expected success, got err=%v upErr=%v", err, upErr)
	}
	raw, err := common.Marshal(result)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	for _, forbidden := range []string{"InternalUploadPath", "PresignSecret"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("non-whitelist field %s leaked: %s", forbidden, raw)
		}
	}
	if result.Status != "Active" || result.URL != "https://cdn/a.jpg" {
		t.Fatalf("whitelist fields not parsed: %+v", result)
	}
}
