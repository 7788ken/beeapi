package service

import (
	"os"
	"testing"
)

// 验证网关 URL 按 protocol 派生、未配置时禁用、旧 env 对 claude 的兼容回退。
func TestVerifyGatewayURL(t *testing.T) {
	os.Unsetenv("VERIFY_GATEWAY_URL")
	os.Unsetenv("VERIFY_GATEWAY_BASE")

	// 未配置 VERIFY_GATEWAY_BASE 时该功能视为未启用，返回空串。
	if got := VerifyGatewayURL("claude"); got != "" {
		t.Errorf("claude unconfigured = %q, want empty", got)
	}
	if got := VerifyGatewayURL("openai"); got != "" {
		t.Errorf("openai unconfigured = %q, want empty", got)
	}

	// 旧 env 仅对 claude 生效（线上兼容）；openai 不受其影响。
	os.Setenv("VERIFY_GATEWAY_URL", "https://legacy.example/api/verify/claude")
	defer os.Unsetenv("VERIFY_GATEWAY_URL")
	if got := VerifyGatewayURL("claude"); got != "https://legacy.example/api/verify/claude" {
		t.Errorf("claude legacy fallback = %q", got)
	}
	if got := VerifyGatewayURL("openai"); got != "" {
		t.Errorf("openai must ignore legacy claude env = %q, want empty", got)
	}

	// 自定义 base
	os.Unsetenv("VERIFY_GATEWAY_URL")
	os.Setenv("VERIFY_GATEWAY_BASE", "https://origin.verify.example.com")
	defer os.Unsetenv("VERIFY_GATEWAY_BASE")
	if got := VerifyGatewayURL("openai"); got != "https://origin.verify.example.com/api/verify/openai" {
		t.Errorf("custom base openai = %q", got)
	}
}
