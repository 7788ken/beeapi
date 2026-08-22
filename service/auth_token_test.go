package service

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func useAuthTestSessionSecret(t *testing.T) {
	t.Helper()
	previous := common.SessionSecret
	common.SessionSecret = "test-session-secret-with-sufficient-entropy"
	t.Cleanup(func() {
		common.SessionSecret = previous
	})
}

func TestAccessTokenRoundTripAndLifetime(t *testing.T) {
	useAuthTestSessionSecret(t)
	identity := AuthIdentity{
		UserID:          42,
		SessionID:       "session-1",
		UserAuthVersion: 3,
		SessionVersion:  2,
	}

	token, expiresAt, err := IssueAccessToken(identity)
	require.NoError(t, err)
	require.InDelta(t, time.Now().Add(AccessTokenTTL).Unix(), expiresAt, 2)

	parsed, err := ParseAccessToken(token)
	require.NoError(t, err)
	require.Equal(t, identity, parsed)
}

func TestAccessTokenRejectsTamperingWrongPurposeAndExpiry(t *testing.T) {
	useAuthTestSessionSecret(t)
	identity := AuthIdentity{
		UserID:          42,
		SessionID:       "session-1",
		UserAuthVersion: 1,
		SessionVersion:  1,
	}
	token, _, err := IssueAccessToken(identity)
	require.NoError(t, err)

	tamperAt := len(token) - 2
	replacement := byte('x')
	if token[tamperAt] == replacement {
		replacement = 'y'
	}
	tampered := token[:tamperAt] + string(replacement) + token[tamperAt+1:]
	_, err = ParseAccessToken(tampered)
	require.ErrorIs(t, err, ErrAuthTokenInvalid)
	_, internal, err := ParseDashboardAccessToken(tampered)
	require.True(t, internal)
	require.ErrorIs(t, err, ErrAuthTokenInvalid)

	now := time.Now()
	for _, test := range []struct {
		name     string
		tokenUse string
		expires  time.Time
		want     error
	}{
		{
			name:     "wrong purpose",
			tokenUse: "refresh",
			expires:  now.Add(time.Minute),
			want:     ErrAuthTokenInvalid,
		},
		{
			name:     "expired",
			tokenUse: accessTokenUse,
			expires:  now.Add(-time.Minute),
			want:     ErrAuthTokenExpired,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			claims := authClaims{
				TokenUse:        test.tokenUse,
				SessionID:       identity.SessionID,
				UserAuthVersion: identity.UserAuthVersion,
				SessionVersion:  identity.SessionVersion,
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer:    authTokenIssuer,
					Subject:   "42",
					Audience:  jwt.ClaimStrings{authTokenAudience},
					ExpiresAt: jwt.NewNumericDate(test.expires),
					NotBefore: jwt.NewNumericDate(now.Add(-2 * time.Minute)),
					IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Minute)),
					ID:        "test-token",
				},
			}
			raw, signErr := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).
				SignedString(authSigningKey(accessTokenUse))
			require.NoError(t, signErr)
			_, parseErr := ParseAccessToken(raw)
			require.True(t, errors.Is(parseErr, test.want))
			_, internal, classifiedErr := ParseDashboardAccessToken(raw)
			if test.tokenUse == accessTokenUse {
				require.True(t, internal)
				require.True(t, errors.Is(classifiedErr, test.want))
			} else {
				require.False(t, internal)
				require.NoError(t, classifiedErr)
			}
		})
	}
}

func TestDashboardAccessTokenLeavesExternalCredentialsUnclassified(t *testing.T) {
	useAuthTestSessionSecret(t)
	for _, raw := range []string{"", "opaque-personal-access-token", "opaque.key.with-dots"} {
		identity, internal, err := ParseDashboardAccessToken(raw)
		require.NoError(t, err)
		require.False(t, internal)
		require.Empty(t, identity)
	}

	external := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":       "external-issuer",
		"aud":       authTokenAudience,
		"token_use": accessTokenUse,
		"exp":       time.Now().Add(time.Minute).Unix(),
	})
	externalRaw, err := external.SignedString([]byte("external-secret"))
	require.NoError(t, err)
	_, internal, err := ParseDashboardAccessToken(externalRaw)
	require.NoError(t, err)
	require.False(t, internal)
}

func TestSecurityProofRoundTripAndPurposeIsolation(t *testing.T) {
	useAuthTestSessionSecret(t)
	identity := AuthIdentity{
		UserID:          42,
		SessionID:       "session-1",
		UserAuthVersion: 3,
		SessionVersion:  2,
	}
	proof, expiresAt, err := IssueSecurityProof(
		identity,
		"2fa",
		[]string{"channel.key.read", "channel.key.read"},
	)
	require.NoError(t, err)
	require.InDelta(t, time.Now().Add(SecurityProofTTL).Unix(), expiresAt, 2)

	method, err := VerifySecurityProof(
		proof,
		identity,
		"channel.key.read",
		[]string{"2fa", "passkey"},
	)
	require.NoError(t, err)
	require.Equal(t, "2fa", method)

	// A proof is reusable only inside its short lifetime and exact binding.
	// Repeating the same check is valid; cross-purpose and cross-session replay
	// below must fail.
	method, err = VerifySecurityProof(
		proof,
		identity,
		"channel.key.read",
		[]string{"2fa"},
	)
	require.NoError(t, err)
	require.Equal(t, "2fa", method)

	_, err = ParseAccessToken(proof)
	require.ErrorIs(t, err, ErrAuthTokenInvalid)
	_, internal, err := ParseDashboardAccessToken(proof)
	require.True(t, internal)
	require.ErrorIs(t, err, ErrAuthTokenInvalid)

	access, _, err := IssueAccessToken(identity)
	require.NoError(t, err)
	_, err = VerifySecurityProof(
		access,
		identity,
		"channel.key.read",
		[]string{"2fa"},
	)
	require.ErrorIs(t, err, ErrAuthTokenInvalid)
}

func TestSecurityProofRejectsScopeMethodAndIdentityReplay(t *testing.T) {
	useAuthTestSessionSecret(t)
	identity := AuthIdentity{
		UserID:          42,
		SessionID:       "session-1",
		UserAuthVersion: 3,
		SessionVersion:  2,
	}
	proof, _, err := IssueSecurityProof(
		identity,
		"2fa",
		[]string{"channel.key.read"},
	)
	require.NoError(t, err)

	_, err = VerifySecurityProof(
		proof,
		identity,
		"passkey.delete",
		[]string{"2fa"},
	)
	require.ErrorIs(t, err, ErrProofScope)
	_, err = VerifySecurityProof(
		proof,
		identity,
		"channel.key.read",
		[]string{"passkey"},
	)
	require.ErrorIs(t, err, ErrProofMethod)

	for _, changed := range []AuthIdentity{
		{
			UserID:          identity.UserID + 1,
			SessionID:       identity.SessionID,
			UserAuthVersion: identity.UserAuthVersion,
			SessionVersion:  identity.SessionVersion,
		},
		{
			UserID:          identity.UserID,
			SessionID:       "session-2",
			UserAuthVersion: identity.UserAuthVersion,
			SessionVersion:  identity.SessionVersion,
		},
		{
			UserID:          identity.UserID,
			SessionID:       identity.SessionID,
			UserAuthVersion: identity.UserAuthVersion + 1,
			SessionVersion:  identity.SessionVersion,
		},
		{
			UserID:          identity.UserID,
			SessionID:       identity.SessionID,
			UserAuthVersion: identity.UserAuthVersion,
			SessionVersion:  identity.SessionVersion + 1,
		},
	} {
		_, err = VerifySecurityProof(
			proof,
			changed,
			"channel.key.read",
			[]string{"2fa"},
		)
		require.ErrorIs(t, err, ErrAuthTokenInvalid)
	}
}

func TestSecurityProofRejectsInvalidAndExpiredClaims(t *testing.T) {
	useAuthTestSessionSecret(t)
	identity := AuthIdentity{
		UserID:          42,
		SessionID:       "session-1",
		UserAuthVersion: 3,
		SessionVersion:  2,
	}
	for _, test := range []struct {
		name   string
		method string
		scopes []string
	}{
		{name: "missing method", scopes: []string{"channel.key.read"}},
		{name: "missing scopes", method: "2fa"},
		{name: "blank scope", method: "2fa", scopes: []string{" "}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := IssueSecurityProof(identity, test.method, test.scopes)
			require.ErrorIs(t, err, ErrAuthTokenInvalid)
		})
	}

	now := time.Now()
	claims := authClaims{
		TokenUse:        securityProofTokenUse,
		SessionID:       identity.SessionID,
		UserAuthVersion: identity.UserAuthVersion,
		SessionVersion:  identity.SessionVersion,
		Method:          "2fa",
		Scopes:          []string{"channel.key.read"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    authTokenIssuer,
			Subject:   "42",
			Audience:  jwt.ClaimStrings{authTokenAudience},
			ExpiresAt: jwt.NewNumericDate(now.Add(-time.Minute)),
			NotBefore: jwt.NewNumericDate(now.Add(-2 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Minute)),
			ID:        "expired-proof",
		},
	}
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).
		SignedString(authSigningKey(securityProofTokenUse))
	require.NoError(t, err)
	_, err = VerifySecurityProof(
		expired,
		identity,
		"channel.key.read",
		[]string{"2fa"},
	)
	require.ErrorIs(t, err, ErrAuthTokenExpired)
}
