package channel

import (
	"net"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/types"
)

// 验证真实的 connection refused 被归为 UpstreamNoResponse（可切 base_url），而非 DoRequestFailed。
func TestClassifyRefusedIsNoResponse(t *testing.T) {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:1", 2*time.Second)
	if err == nil {
		conn.Close()
		t.Skip("127.0.0.1:1 unexpectedly open")
	}
	if !isUpstreamNoResponse(err) {
		t.Fatalf("refused should be no-response, got: %v", err)
	}
	apiErr := classifyDoRequestError(err)
	if apiErr.GetErrorCode() != types.ErrorCodeUpstreamNoResponse {
		t.Fatalf("want UpstreamNoResponse, got %v (err=%v)", apiErr.GetErrorCode(), err)
	}
}
