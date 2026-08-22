package service

import (
	"net/http"
	"sync/atomic"
	"testing"
)

type closeTrackingRoundTripper struct {
	closes atomic.Int32
}

func (t *closeTrackingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	panic("unexpected RoundTrip")
}

func (t *closeTrackingRoundTripper) CloseIdleConnections() {
	t.closes.Add(1)
}

func TestCloseHTTPTransportsClosesSharedAndProxyClientsOnce(t *testing.T) {
	originalHTTPClient := httpClient
	originalStreamingClient := streamingHttpClient
	originalProtectedClient := ssrfProtectedHTTPClient
	proxyClientLock.Lock()
	originalProxyClients := proxyClients
	proxyClientLock.Unlock()
	t.Cleanup(func() {
		httpClient = originalHTTPClient
		streamingHttpClient = originalStreamingClient
		ssrfProtectedHTTPClient = originalProtectedClient
		proxyClientLock.Lock()
		proxyClients = originalProxyClients
		proxyClientLock.Unlock()
	})

	sharedTransport := &closeTrackingRoundTripper{}
	proxyTransport := &closeTrackingRoundTripper{}
	sharedClient := &http.Client{Transport: sharedTransport}
	proxyClient := &http.Client{Transport: proxyTransport}
	httpClient = sharedClient
	streamingHttpClient = sharedClient
	ssrfProtectedHTTPClient = nil
	proxyClientLock.Lock()
	proxyClients = map[string]*http.Client{
		"http://proxy-a": proxyClient,
		"http://proxy-b": proxyClient,
	}
	proxyClientLock.Unlock()

	CloseHTTPTransports()

	if got := sharedTransport.closes.Load(); got != 1 {
		t.Fatalf("shared transport closes = %d, want 1", got)
	}
	if got := proxyTransport.closes.Load(); got != 1 {
		t.Fatalf("proxy transport closes = %d, want 1", got)
	}
	proxyClientLock.Lock()
	defer proxyClientLock.Unlock()
	if len(proxyClients) != 0 {
		t.Fatalf("proxy client cache size = %d, want 0", len(proxyClients))
	}
}
