package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"golang.org/x/net/proxy"
)

var (
	httpClient              *http.Client
	streamingHttpClient     *http.Client
	ssrfProtectedHTTPClient *http.Client
	proxyClientLock         sync.Mutex
	proxyClients            = make(map[string]*http.Client)
)

func checkRedirect(req *http.Request, via []*http.Request) error {
	urlStr := req.URL.String()
	if err := validateURLWithCurrentFetchSetting(urlStr, true); err != nil {
		return fmt.Errorf("redirect to %s blocked: %v", urlStr, err)
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return nil
}

func checkProtectedFetchRedirect(req *http.Request, via []*http.Request) error {
	urlStr := req.URL.String()
	if err := ValidateSSRFProtectedFetchURL(urlStr); err != nil {
		return fmt.Errorf("redirect to %s blocked: %v", urlStr, err)
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return nil
}

func validateURLWithCurrentFetchSetting(urlStr string, applyDomainIPFilter bool) error {
	fetchSetting := system_setting.GetFetchSetting()
	return common.ValidateURLWithFetchSetting(urlStr, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, applyDomainIPFilter && fetchSetting.ApplyIPFilterForDomain)
}

func ValidateSSRFProtectedFetchURL(urlStr string) error {
	return validateURLWithCurrentFetchSetting(urlStr, true)
}

func InitHttpClient() {
	dialer := &net.Dialer{
		Timeout:   time.Duration(common.RelayDialTimeout) * time.Second,
		KeepAlive: 30 * time.Second,
	}
	// 非流式 transport：连接/TLS 短超时快速暴露"节点无响应"；
	// 不设 ResponseHeaderTimeout —— 非流式上游可能正常生成很久才回 header，靠 per-model timeout 兜底，避免误杀长生成
	nonStreamTransport := &http.Transport{
		MaxIdleConns:        common.RelayMaxIdleConns,
		MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
		IdleConnTimeout:     time.Duration(common.RelayIdleConnTimeout) * time.Second,
		ForceAttemptHTTP2:   true,
		Proxy:               http.ProxyFromEnvironment, // Support HTTP_PROXY, HTTPS_PROXY, NO_PROXY env vars
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: time.Duration(common.RelayTLSHandshakeTimeout) * time.Second,
	}
	// 流式 transport：同样的连接/TLS 短超时，并额外限制响应头等待
	// （SSE 正常应很快回 200 header；长时间不回 header 视为节点无响应，触发 base_url 切换）
	streamTransport := &http.Transport{
		MaxIdleConns:          common.RelayMaxIdleConns,
		MaxIdleConnsPerHost:   common.RelayMaxIdleConnsPerHost,
		IdleConnTimeout:       time.Duration(common.RelayIdleConnTimeout) * time.Second,
		ForceAttemptHTTP2:     true,
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   time.Duration(common.RelayTLSHandshakeTimeout) * time.Second,
		ResponseHeaderTimeout: time.Duration(common.RelayStreamRespHeaderTimeout) * time.Second,
	}
	if common.TLSInsecureSkipVerify {
		nonStreamTransport.TLSClientConfig = common.InsecureTLSConfig
		streamTransport.TLSClientConfig = common.InsecureTLSConfig
	}

	if common.RelayTimeout == 0 {
		httpClient = &http.Client{
			Transport:     nonStreamTransport,
			CheckRedirect: checkRedirect,
		}
	} else {
		httpClient = &http.Client{
			Transport:     nonStreamTransport,
			Timeout:       time.Duration(common.RelayTimeout) * time.Second,
			CheckRedirect: checkRedirect,
		}
	}

	// 流式请求专用客户端：不设置 Client.Timeout，避免长流被强制断开
	// 流式空闲超时由 StreamScannerHandler 的 ticker 单独控制
	streamingHttpClient = &http.Client{
		Transport:     streamTransport,
		CheckRedirect: checkRedirect,
	}

	ssrfProtectedHTTPClient = newProtectedFetchHTTPClient()
}

// GetHttpClient 返回 relay/provider 集成使用的通用出站客户端。
// 不要在此处挂 SSRF 拨号防护：provider base_url 是 root/运维掌控的部署目标，
// 而非用户可控输入，可能合法指向私有网络、私有链路端点、自建服务或本地代理。
// 拉取任意用户可控 URL 的代码路径必须改用 GetSSRFProtectedHTTPClient 或
// ValidateSSRFProtectedFetchURL。
func GetHttpClient() *http.Client {
	return httpClient
}

// GetSSRFProtectedHTTPClient 返回带拨号时 SSRF 校验的客户端。
// ssrfProtectedHTTPClient 由 InitHttpClient 在启动时初始化，运行期只读。
func GetSSRFProtectedHTTPClient() *http.Client {
	if fetchSetting := system_setting.GetFetchSetting(); fetchSetting != nil && !fetchSetting.EnableSSRFProtection {
		return GetHttpClient()
	}
	return ssrfProtectedHTTPClient
}

func GetStreamingHttpClient() *http.Client {
	return streamingHttpClient
}

// GetHttpClientWithProxy returns the default client or a proxy-enabled one when proxyURL is provided.
func GetHttpClientWithProxy(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		return GetHttpClient(), nil
	}
	return NewProxyHttpClient(proxyURL)
}

// ResetProxyClientCache 清空代理客户端缓存，确保下次使用时重新初始化
func ResetProxyClientCache() {
	proxyClientLock.Lock()
	defer proxyClientLock.Unlock()
	for _, client := range proxyClients {
		if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
			transport.CloseIdleConnections()
		}
	}
	proxyClients = make(map[string]*http.Client)
}

// CloseHTTPTransports releases every idle outbound connection owned by the
// shared relay clients. Call it only after HTTP and background work have
// drained, otherwise a still-running request may immediately create a new
// connection.
func CloseHTTPTransports() {
	proxyClientLock.Lock()
	clients := make([]*http.Client, 0, len(proxyClients)+3)
	clients = append(clients, httpClient, streamingHttpClient, ssrfProtectedHTTPClient)
	for _, client := range proxyClients {
		clients = append(clients, client)
	}
	proxyClients = make(map[string]*http.Client)
	proxyClientLock.Unlock()

	seen := make(map[*http.Client]struct{}, len(clients))
	for _, client := range clients {
		if client == nil {
			continue
		}
		if _, exists := seen[client]; exists {
			continue
		}
		seen[client] = struct{}{}
		client.CloseIdleConnections()
	}
}

// NewProxyHttpClient 创建支持代理的 HTTP 客户端
func NewProxyHttpClient(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		if client := GetHttpClient(); client != nil {
			return client, nil
		}
		return http.DefaultClient, nil
	}

	proxyClientLock.Lock()
	if client, ok := proxyClients[proxyURL]; ok {
		proxyClientLock.Unlock()
		return client, nil
	}
	proxyClientLock.Unlock()

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}

	switch parsedURL.Scheme {
	case "http", "https":
		transport := &http.Transport{
			MaxIdleConns:        common.RelayMaxIdleConns,
			MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
			IdleConnTimeout:     time.Duration(common.RelayIdleConnTimeout) * time.Second,
			ForceAttemptHTTP2:   true,
			Proxy:               http.ProxyURL(parsedURL),
		}
		if common.TLSInsecureSkipVerify {
			transport.TLSClientConfig = common.InsecureTLSConfig
		}
		client := &http.Client{
			Transport:     transport,
			CheckRedirect: checkRedirect,
		}
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
		proxyClientLock.Lock()
		proxyClients[proxyURL] = client
		proxyClientLock.Unlock()
		return client, nil

	case "socks5", "socks5h":
		// 获取认证信息
		var auth *proxy.Auth
		if parsedURL.User != nil {
			auth = &proxy.Auth{
				User:     parsedURL.User.Username(),
				Password: "",
			}
			if password, ok := parsedURL.User.Password(); ok {
				auth.Password = password
			}
		}

		// 创建 SOCKS5 代理拨号器
		// proxy.SOCKS5 使用 tcp 参数，所有 TCP 连接包括 DNS 查询都将通过代理进行。行为与 socks5h 相同
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, proxy.Direct)
		if err != nil {
			return nil, err
		}

		transport := &http.Transport{
			MaxIdleConns:        common.RelayMaxIdleConns,
			MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
			IdleConnTimeout:     time.Duration(common.RelayIdleConnTimeout) * time.Second,
			ForceAttemptHTTP2:   true,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		}
		if common.TLSInsecureSkipVerify {
			transport.TLSClientConfig = common.InsecureTLSConfig
		}

		client := &http.Client{Transport: transport, CheckRedirect: checkRedirect}
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
		proxyClientLock.Lock()
		proxyClients[proxyURL] = client
		proxyClientLock.Unlock()
		return client, nil

	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s, must be http, https, socks5 or socks5h", parsedURL.Scheme)
	}
}
