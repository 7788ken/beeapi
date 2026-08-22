package common

import (
	"reflect"
	"testing"
)

func resetSessionCookieSettingsAfterTest(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		SessionCookieSecure = false
		SessionCookieTrustedURLs = nil
	})
}

func TestNormalizeOrigin(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "lowercase scheme host and default HTTPS port", raw: " HTTPS://Example.COM:443/ ", want: "https://example.com"},
		{name: "preserve non-default port", raw: "https://Example.COM:8443", want: "https://example.com:8443"},
		{name: "normalize IPv6 default port", raw: "https://[2001:db8::1]:443", want: "https://[2001:db8::1]"},
		{name: "normalize HTTP default port", raw: "http://Example.COM:80", want: "http://example.com"},
		{name: "reject empty", raw: "", wantErr: true},
		{name: "reject null", raw: "null", wantErr: true},
		{name: "reject path", raw: "https://example.com/login", wantErr: true},
		{name: "reject query", raw: "https://example.com?next=evil", wantErr: true},
		{name: "reject empty query", raw: "https://example.com?", wantErr: true},
		{name: "reject fragment", raw: "https://example.com#fragment", wantErr: true},
		{name: "reject empty fragment", raw: "https://example.com#", wantErr: true},
		{name: "reject userinfo", raw: "https://user@example.com", wantErr: true},
		{name: "reject wildcard", raw: "https://*.example.com", wantErr: true},
		{name: "reject unsupported scheme", raw: "ftp://example.com", wantErr: true},
		{name: "reject empty port", raw: "https://example.com:", wantErr: true},
		{name: "reject zero port", raw: "https://example.com:0", wantErr: true},
		{name: "reject out of range port", raw: "https://example.com:65536", wantErr: true},
		{name: "reject newline", raw: "https://example.com\nhttps://evil.example", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeOrigin(test.raw)
			if test.wantErr {
				if err == nil {
					t.Fatalf("NormalizeOrigin(%q) returned no error", test.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeOrigin(%q) returned error: %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("NormalizeOrigin(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestInitSessionCookieSettings(t *testing.T) {
	tests := []struct {
		name       string
		secure     string
		trusted    string
		wantSecure bool
		wantURLs   []string
		wantErr    bool
	}{
		{name: "defaults to insecure"},
		{name: "explicit false", secure: "false"},
		{name: "reject invalid boolean", secure: "yes", wantErr: true},
		{name: "reject trusted origin without secure", trusted: "https://example.com", wantErr: true},
		{name: "require trusted origin when secure", secure: "true", wantErr: true},
		{name: "reject HTTP trusted origin", secure: "true", trusted: "http://example.com", wantErr: true},
		{name: "reject empty origin in list", secure: "true", trusted: "https://example.com,", wantErr: true},
		{name: "reject origin path", secure: "true", trusted: "https://example.com/login", wantErr: true},
		{
			name:       "enable and canonicalize exact HTTPS origins",
			secure:     " TRUE ",
			trusted:    " https://Example.COM:443/, https://admin.example.com:8443 ",
			wantSecure: true,
			wantURLs:   []string{"https://example.com", "https://admin.example.com:8443"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetSessionCookieSettingsAfterTest(t)
			t.Setenv("SESSION_COOKIE_SECURE", test.secure)
			t.Setenv("SESSION_COOKIE_TRUSTED_URL", test.trusted)

			err := InitSessionCookieSettings()
			if test.wantErr {
				if err == nil {
					t.Fatal("InitSessionCookieSettings returned no error")
				}
				if SessionCookieSecure || SessionCookieTrustedURLs != nil {
					t.Fatalf(
						"invalid settings left partial state: secure=%v trusted=%v",
						SessionCookieSecure,
						SessionCookieTrustedURLs,
					)
				}
				return
			}
			if err != nil {
				t.Fatalf("InitSessionCookieSettings returned error: %v", err)
			}
			if SessionCookieSecure != test.wantSecure {
				t.Fatalf("SessionCookieSecure = %v, want %v", SessionCookieSecure, test.wantSecure)
			}
			if !reflect.DeepEqual(SessionCookieTrustedURLs, test.wantURLs) {
				t.Fatalf("SessionCookieTrustedURLs = %v, want %v", SessionCookieTrustedURLs, test.wantURLs)
			}
		})
	}
}
