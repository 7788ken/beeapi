package baidu

import (
	"net"
	"testing"

	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

// 取令牌请求把 client_secret 放在网址查询串里，请求失败时的错误不能带出来
func TestGetBaiduAccessTokenErrorOmitsClientSecret(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	deadProxy := "http://" + ln.Addr().String()
	require.NoError(t, ln.Close())
	// 走一个已关闭的本地代理，请求必然在网络层失败，不会真的连到百度
	t.Setenv("HTTPS_PROXY", deadProxy)
	t.Setenv("https_proxy", deadProxy)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	service.InitHttpClient()

	_, err = getBaiduAccessTokenHelper("baiduclientid|baiduclientsecret")
	require.Error(t, err)
	require.Contains(t, err.Error(), "proxyconnect")
	require.Contains(t, err.Error(), `"https://aip.baidubce.com/oauth/2.0/token": `)
	require.NotContains(t, err.Error(), "baiduclientsecret")
}

func TestGetBaiduAccessTokenInvalidKeyErrorOmitsClientSecret(t *testing.T) {
	// 密钥里的控制字符让 http.NewRequest 解析失败，错误里会带原始网址
	_, err := getBaiduAccessTokenHelper("baiduclientid|baiduclientsecret\x7f")
	require.Error(t, err)
	require.Contains(t, err.Error(), `parse "https://aip.baidubce.com/oauth/2.0/token": `)
	require.NotContains(t, err.Error(), "baiduclientsecret")
}
