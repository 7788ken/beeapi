package common

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	DefaultRelayIdleConnTimeout    = 90
	maxRelayIdleConnTimeoutSeconds = uint64(^uint64(0)>>1) / uint64(time.Second)
)

// InitRelayIdleConnTimeout parses RELAY_IDLE_CONN_TIMEOUT atomically.
// Invalid values leave RelayIdleConnTimeout unchanged.
func InitRelayIdleConnTimeout() error {
	timeout := DefaultRelayIdleConnTimeout
	if raw := os.Getenv("RELAY_IDLE_CONN_TIMEOUT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("RELAY_IDLE_CONN_TIMEOUT must be a non-negative integer: %w", err)
		}
		timeout = parsed
	}
	if timeout < 0 {
		return fmt.Errorf("RELAY_IDLE_CONN_TIMEOUT must be non-negative")
	}
	if uint64(timeout) > maxRelayIdleConnTimeoutSeconds {
		return fmt.Errorf(
			"RELAY_IDLE_CONN_TIMEOUT must not exceed %d seconds",
			maxRelayIdleConnTimeoutSeconds,
		)
	}

	RelayIdleConnTimeout = timeout
	return nil
}
