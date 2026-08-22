package common

import (
	"os"
	"testing"
)

func TestInitRelayIdleConnTimeout(t *testing.T) {
	tests := []struct {
		name      string
		raw       *string
		want      int
		wantError bool
	}{
		{name: "default", want: DefaultRelayIdleConnTimeout},
		{name: "explicit empty uses default", raw: stringPointer(""), want: DefaultRelayIdleConnTimeout},
		{name: "explicit value", raw: stringPointer("37"), want: 37},
		{name: "zero disables limit", raw: stringPointer("0"), want: 0},
		{name: "reject negative", raw: stringPointer("-1"), wantError: true},
		{name: "reject non integer", raw: stringPointer("90s"), wantError: true},
		{name: "reject duration overflow", raw: stringPointer("9223372037"), wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			restoreRelayIdleConnTimeoutEnv(t)
			if test.raw == nil {
				if err := os.Unsetenv("RELAY_IDLE_CONN_TIMEOUT"); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("RELAY_IDLE_CONN_TIMEOUT", *test.raw)
			}

			const sentinel = 731
			RelayIdleConnTimeout = sentinel
			err := InitRelayIdleConnTimeout()
			if test.wantError {
				if err == nil {
					t.Fatal("InitRelayIdleConnTimeout returned no error")
				}
				if RelayIdleConnTimeout != sentinel {
					t.Fatalf("invalid setting changed RelayIdleConnTimeout to %d", RelayIdleConnTimeout)
				}
				return
			}
			if err != nil {
				t.Fatalf("InitRelayIdleConnTimeout returned error: %v", err)
			}
			if RelayIdleConnTimeout != test.want {
				t.Fatalf("RelayIdleConnTimeout = %d, want %d", RelayIdleConnTimeout, test.want)
			}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}

func restoreRelayIdleConnTimeoutEnv(t *testing.T) {
	t.Helper()
	originalValue := RelayIdleConnTimeout
	originalRaw, existed := os.LookupEnv("RELAY_IDLE_CONN_TIMEOUT")
	t.Cleanup(func() {
		RelayIdleConnTimeout = originalValue
		if existed {
			_ = os.Setenv("RELAY_IDLE_CONN_TIMEOUT", originalRaw)
		} else {
			_ = os.Unsetenv("RELAY_IDLE_CONN_TIMEOUT")
		}
	})
}
