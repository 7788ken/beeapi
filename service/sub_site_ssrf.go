package service

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

var (
	ErrSubSiteBadScheme    = errors.New("sub_site: scheme must be http or https")
	ErrSubSiteEmptyHost    = errors.New("sub_site: empty host")
	ErrSubSiteDNS          = errors.New("sub_site: dns lookup failed")
	ErrSubSitePrivateAddr  = errors.New("sub_site: target resolves to a private/loopback/link-local address")
	ErrSubSiteMalformedURL = errors.New("sub_site: malformed base_url")
)

// allowIntranet 控制是否绕过私有地址黑名单（option key SubSiteAllowIntranet=true）。
func allowIntranet() bool {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return strings.EqualFold(common.OptionMap["SubSiteAllowIntranet"], "true")
}

// privateIPv4Nets 私有 / 回环 / 链路本地 IPv4 段。
var privateIPv4Nets = []*net.IPNet{
	mustCIDR("127.0.0.0/8"),
	mustCIDR("10.0.0.0/8"),
	mustCIDR("172.16.0.0/12"),
	mustCIDR("192.168.0.0/16"),
	mustCIDR("169.254.0.0/16"),
	mustCIDR("0.0.0.0/8"),
}

// privateIPv6Nets 私有 / 回环 / 链路本地 IPv6 段，含 v4-mapped。
var privateIPv6Nets = []*net.IPNet{
	mustCIDR("::1/128"),
	mustCIDR("fc00::/7"),
	mustCIDR("fe80::/10"),
	mustCIDR("::ffff:0:0/96"),
}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		for _, n := range privateIPv4Nets {
			if n.Contains(v4) {
				return true
			}
		}
		return false
	}
	for _, n := range privateIPv6Nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateSubSiteBaseURL 校验上游分站 base_url：
//   - scheme 必须是 http/https；
//   - host 解析所有 A/AAAA 记录均不在私有段；
//   - 可被 SubSiteAllowIntranet=true 整体放行（仅推荐内网测试）。
func ValidateSubSiteBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ErrSubSiteMalformedURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ErrSubSiteMalformedURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ErrSubSiteBadScheme
	}
	host := u.Hostname()
	if host == "" {
		return ErrSubSiteEmptyHost
	}
	if allowIntranet() {
		return nil
	}
	// 字面 IP 直接校验
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return ErrSubSitePrivateAddr
		}
		return nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return ErrSubSiteDNS
	}
	if len(addrs) == 0 {
		return ErrSubSiteDNS
	}
	for _, a := range addrs {
		if isPrivateIP(a) {
			return ErrSubSitePrivateAddr
		}
	}
	return nil
}

// NewSubSiteDialContext 返回一个 DialContext，二次校验同一目标 IP（缓解 DNS rebinding）。
// 已通过 ValidateSubSiteBaseURL 的请求，建立 TCP 前再次拒绝私有 IP。
func NewSubSiteDialContext(base net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if allowIntranet() {
			return base.DialContext(ctx, network, addr)
		}
		var ips []net.IP
		if ip := net.ParseIP(host); ip != nil {
			ips = []net.IP{ip}
		} else {
			ips, err = net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, ErrSubSiteDNS
			}
		}
		for _, ip := range ips {
			if isPrivateIP(ip) {
				return nil, ErrSubSitePrivateAddr
			}
		}
		// 用第一条 IP 拨号，避免再次 DNS 解析时 rebinding。
		return base.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
}
