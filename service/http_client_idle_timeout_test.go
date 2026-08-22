package service

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestRelayTransportsUseConfiguredIdleConnTimeout(t *testing.T) {
	tests := []struct {
		name           string
		timeoutSeconds int
	}{
		{name: "configured timeout", timeoutSeconds: 37},
		{name: "zero disables timeout", timeoutSeconds: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			restoreRelayHTTPClientGlobals(t)
			common.RelayIdleConnTimeout = test.timeoutSeconds
			common.RelayMaxIdleConns = 50
			common.RelayMaxIdleConnsPerHost = 10
			common.RelayDialTimeout = 5
			common.RelayTLSHandshakeTimeout = 8
			common.RelayStreamRespHeaderTimeout = 30
			common.RelayTimeout = 0
			common.TLSInsecureSkipVerify = false

			InitHttpClient()
			assertIdleConnTimeout(t, httpClient.Transport, test.timeoutSeconds)
			assertIdleConnTimeout(t, streamingHttpClient.Transport, test.timeoutSeconds)

			httpProxyClient, err := NewProxyHttpClient("http://127.0.0.1:3128")
			require.NoError(t, err)
			assertIdleConnTimeout(t, httpProxyClient.Transport, test.timeoutSeconds)

			socksProxyClient, err := NewProxyHttpClient("socks5://127.0.0.1:1080")
			require.NoError(t, err)
			assertIdleConnTimeout(t, socksProxyClient.Transport, test.timeoutSeconds)

			protectedClient := newProtectedFetchHTTPClientWithProxy(nil, nil, nil, http.ProxyFromEnvironment)
			protectedRoundTripper, ok := protectedClient.Transport.(*ssrfProtectedRoundTripper)
			require.True(t, ok)
			assertIdleConnTimeout(t, protectedRoundTripper.transportFor(nil), test.timeoutSeconds)

			proxyURL, err := url.Parse("http://127.0.0.1:8080")
			require.NoError(t, err)
			assertIdleConnTimeout(t, protectedRoundTripper.transportFor(proxyURL), test.timeoutSeconds)
		})
	}
}

func assertIdleConnTimeout(t *testing.T, roundTripper http.RoundTripper, seconds int) {
	t.Helper()
	transport, ok := roundTripper.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, time.Duration(seconds)*time.Second, transport.IdleConnTimeout)
}

func restoreRelayHTTPClientGlobals(t *testing.T) {
	t.Helper()
	originalHTTPClient := httpClient
	originalStreamingHTTPClient := streamingHttpClient
	originalSSRFProtectedHTTPClient := ssrfProtectedHTTPClient
	originalRelayIdleConnTimeout := common.RelayIdleConnTimeout
	originalRelayMaxIdleConns := common.RelayMaxIdleConns
	originalRelayMaxIdleConnsPerHost := common.RelayMaxIdleConnsPerHost
	originalRelayDialTimeout := common.RelayDialTimeout
	originalRelayTLSHandshakeTimeout := common.RelayTLSHandshakeTimeout
	originalRelayStreamRespHeaderTimeout := common.RelayStreamRespHeaderTimeout
	originalRelayTimeout := common.RelayTimeout
	originalTLSInsecureSkipVerify := common.TLSInsecureSkipVerify

	ResetProxyClientCache()
	t.Cleanup(func() {
		ResetProxyClientCache()
		if httpClient != nil {
			httpClient.CloseIdleConnections()
		}
		if streamingHttpClient != nil {
			streamingHttpClient.CloseIdleConnections()
		}
		if ssrfProtectedHTTPClient != nil {
			ssrfProtectedHTTPClient.CloseIdleConnections()
		}
		httpClient = originalHTTPClient
		streamingHttpClient = originalStreamingHTTPClient
		ssrfProtectedHTTPClient = originalSSRFProtectedHTTPClient
		common.RelayIdleConnTimeout = originalRelayIdleConnTimeout
		common.RelayMaxIdleConns = originalRelayMaxIdleConns
		common.RelayMaxIdleConnsPerHost = originalRelayMaxIdleConnsPerHost
		common.RelayDialTimeout = originalRelayDialTimeout
		common.RelayTLSHandshakeTimeout = originalRelayTLSHandshakeTimeout
		common.RelayStreamRespHeaderTimeout = originalRelayStreamRespHeaderTimeout
		common.RelayTimeout = originalRelayTimeout
		common.TLSInsecureSkipVerify = originalTLSInsecureSkipVerify
	})
}
