package ali

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
)

func TestUpdateTaskRejectsInvalidChannelProxyBeforeRequest(t *testing.T) {
	var targetRequested atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequested.Store(true)
	}))
	t.Cleanup(target.Close)

	info := newAliImageTaskRelayInfo(target.URL, "unsupported://proxy")
	response, err, responseBody := updateTask(info, "task-1")

	if err == nil {
		t.Fatal("updateTask returned no error")
	}
	if !strings.Contains(err.Error(), "create Ali async image task status client") {
		t.Fatalf("updateTask error = %q, want proxy client context", err)
	}
	if !strings.Contains(err.Error(), "unsupported proxy scheme: unsupported") {
		t.Fatalf("updateTask error = %q, want unsupported ChannelSetting.Proxy error", err)
	}
	if targetRequested.Load() {
		t.Fatal("updateTask sent a request before returning the proxy client error")
	}
	if response == nil {
		t.Fatal("updateTask returned a nil response")
	}
	if responseBody != nil {
		t.Fatalf("updateTask response body = %q, want nil", responseBody)
	}
}

func TestUpdateTaskUsesChannelHTTPProxy(t *testing.T) {
	service.ResetProxyClientCache()
	t.Cleanup(service.ResetProxyClientCache)

	var targetRequested atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequested.Store(true)
	}))
	t.Cleanup(target.Close)

	var proxyRequested atomic.Bool
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		proxyRequested.Store(true)
		wantURL := target.URL + "/api/v1/tasks/task-2"
		if req.URL.String() != wantURL {
			t.Errorf("request URL = %q, want %q", req.URL.String(), wantURL)
		}
		if req.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", req.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"task_status":"SUCCEEDED"}}`))
	}))
	t.Cleanup(proxyServer.Close)

	info := newAliImageTaskRelayInfo(target.URL, proxyServer.URL)
	response, err, responseBody := updateTask(info, "task-2")

	if err != nil {
		t.Fatalf("updateTask returned error: %v", err)
	}
	if !proxyRequested.Load() {
		t.Fatal("updateTask did not use ChannelSetting.Proxy")
	}
	if targetRequested.Load() {
		t.Fatal("updateTask bypassed ChannelSetting.Proxy")
	}
	if response.Output.TaskStatus != "SUCCEEDED" {
		t.Fatalf("task status = %q, want SUCCEEDED", response.Output.TaskStatus)
	}
	if string(responseBody) != `{"output":{"task_status":"SUCCEEDED"}}` {
		t.Fatalf("response body = %q", responseBody)
	}
}

func newAliImageTaskRelayInfo(baseURL, proxyURL string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: baseURL,
			ApiKey:         "test-key",
			ChannelSetting: dto.ChannelSettings{Proxy: proxyURL},
		},
	}
}
