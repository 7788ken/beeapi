package common

import (
	"strings"
	"testing"
)

func TestMaskSensitiveInfo_NoLeak(t *testing.T) {
	type tc struct {
		in     string
		banned []string
	}
	cases := []tc{
		{"dial tcp https://api.internal-vendor.com/v1/chat failed", []string{"internal-vendor", "/v1/chat"}},
		{"upstream error from http://10.0.0.5:8080/v1/messages timeout", []string{"10.0.0.5", "8080"}},
		{"connect gw.internal.local:8443 refused", []string{"gw.internal", ":8443"}},
		{"request failed url=https://my-secret-gw.example.org:9000/relay", []string{"my-secret-gw", ":9000"}},
		{"Authorization: Bearer sk-abc123def456ghi789", []string{"sk-abc123def456ghi789"}},
		{"channel-billing ...?api_key=sk-livexxxxxxxxxxxx&model=gpt", []string{"sk-livexxxxxxxxxxxx"}},
		{"x-api-key: sk-ant-aaaaaaaaaaaaaaaa", []string{"sk-ant-aaaaaaaaaaaaaaaa"}},
		// base64 形态密钥（含 + / =）必须整段打掉，不能只去掉前半截
		{"channel-billing ...?api_key=YWJjZGVm+Z2hp/jkl0123==&model=gpt", []string{"YWJjZGVm", "Z2hp", "jkl0123"}},
		// Basic 认证头里的 base64 凭据（user:pass）必须打掉
		{"Authorization: Basic dXNlcjpwYXNzd29yZA== invalid", []string{"dXNlcjpwYXNzd29yZA"}},
	}
	for _, c := range cases {
		out := MaskSensitiveInfo(c.in)
		for _, b := range c.banned {
			if strings.Contains(out, b) {
				t.Errorf("LEAK %q in %q", b, out)
			}
		}
	}
	// 不影响业务：渠道 ID（数字）保留；普通错误文案中的关键名词不被吞
	if !strings.Contains(MaskSensitiveInfo("channel #42 unavailable"), "42") {
		t.Errorf("channel id should be preserved")
	}
	if got := MaskSensitiveInfo("model gpt-4o-mini not found in group default"); !strings.Contains(got, "gpt-4o-mini") || !strings.Contains(got, "default") {
		t.Errorf("normal error text should be preserved, got %q", got)
	}
}
