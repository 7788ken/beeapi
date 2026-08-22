package controller

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDashboardFrontendsUseHttpOnlyRefreshAndMemoryOnlyAccessTokens(t *testing.T) {
	paths := []string{
		"../web/default/src/lib/api.ts",
		"../web/default/src/features/auth/api.ts",
		"../web/default/src/features/auth/sign-in/components/user-auth-form.tsx",
		"../web/default/src/features/auth/secure-verification/api.ts",
		"../web/default/src/features/auth/secure-verification/hooks/use-secure-verification.ts",
		"../web/default/src/features/channels/api.ts",
		"../web/default/src/features/channels/components/dialogs/ollama-models-dialog.tsx",
		"../web/default/src/features/channels/components/drawers/channel-mutate-drawer.tsx",
		"../web/default/src/features/profile/api.ts",
		"../web/default/src/features/profile/components/passkey-card.tsx",
		"../web/default/src/features/playground/hooks/use-stream-request.ts",
		"../web/default/src/stores/auth-store.ts",
		"../web/default/src/routes/_authenticated/route.tsx",
		"../web/default/src/routes/oauth/$provider.tsx",
		"../web/classic/src/helpers/api.js",
		"../web/classic/src/helpers/auth.jsx",
		"../web/classic/src/helpers/data.js",
		"../web/classic/src/helpers/utils.jsx",
		"../web/classic/src/components/auth/LoginForm.jsx",
		"../web/classic/src/components/auth/RegisterForm.jsx",
		"../web/classic/src/components/auth/OAuth2Callback.jsx",
		"../web/classic/src/components/auth/TwoFAVerification.jsx",
		"../web/classic/src/components/settings/personal/cards/AccountManagement.jsx",
		"../web/classic/src/components/table/channels/modals/EditChannelModal.jsx",
		"../web/classic/src/components/table/channels/modals/OllamaModelModal.jsx",
		"../web/classic/src/services/secureVerification.js",
		"../web/classic/src/hooks/common/useSecureVerification.jsx",
		"../web/classic/src/hooks/playground/useApiRequest.jsx",
	}
	var source strings.Builder
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		source.Write(data)
	}
	contract := source.String()
	require.Contains(t, contract, "/api/user/refresh")
	require.Contains(t, contract, ".post('/api/user/logout")
	require.NotContains(t, contract, "localStorage.setItem('access_token'")
	require.NotContains(t, contract, "localStorage.setItem('refresh_token'")
	require.NotContains(t, contract, "sessionStorage.setItem('access_token'")
	require.NotContains(t, contract, "sessionStorage.setItem('refresh_token'")
	require.NotContains(t, contract, "localStorage.setItem('user', JSON.stringify(data))")
	require.Contains(t, contract, "delete clean.access_token")
	require.Contains(t, contract, "delete data.access_token")
	require.Contains(t, contract, "hadStoredToken")
	require.Contains(t, contract, "isDashboardAuthResponse(response.config.url)")
	require.Contains(t, contract, "delete user.access_token")
	require.Contains(t, contract, "flow_token: pendingTwoFAFlowToken")
	require.Contains(t, contract, "flow_token: flowToken")
	require.Contains(t, contract, "navigator.locks.request")
	require.Contains(t, contract, "new BroadcastChannel(refreshLockName)")
	require.Contains(t, contract, "new BroadcastChannel(dashboardRefreshLockName)")
	require.Contains(t, contract, "getDashboardAuthHeaders")
	require.Contains(t, contract, "await authHeader()")
	require.Contains(t, contract, "isTerminalDashboardAuthError")
	require.Contains(t, contract, "if (!isTerminalDashboardAuthError(error)) throw error")
	require.Contains(t, contract, "'X-Security-Proof': proof")
	require.Contains(t, contract, "'X-Auth-Flow': flowToken")
	require.Contains(t, contract, "'X-Auth-Flow': data.flow_token")
	require.NotContains(t, contract, "params: { flow_token")
	require.NotContains(t, contract, "flow_token=${encodeURIComponent")
	require.Contains(t, contract, "scope: 'channel.key.read'")
	require.Contains(t, contract, "API.get('/api/oauth/telegram/bind', { params })")
	require.Contains(t, contract, "api.post('/api/oauth/wechat/bind', { code })")
	require.Contains(t, contract, "res.data?.data?.action === 'bind'")
	require.NotContains(t, contract, "window.location.assign(`/api/oauth/telegram/bind")
	require.NotContains(t, contract, "method: 'passkey',\n    })")
}

func TestLegacyOAuthCookieControllersAreRemoved(t *testing.T) {
	for _, path := range []string{"github.go", "discord.go", "linuxdo.go", "oidc.go"} {
		_, err := os.Stat(path)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}
