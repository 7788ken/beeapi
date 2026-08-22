package relay

import (
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/types"
)

// 复现 relay 主链路：doRequest 返回 NewAPIError(UpstreamNoResponse)，
// DoApiRequest 用 fmt.Errorf("%w") 再包一层。wrapDoRequestError 必须穿透取回分类码。
func TestWrapDoRequestErrorPreservesNoResponse(t *testing.T) {
	inner := types.NewError(errors.New("dial tcp: connect: connection refused"), types.ErrorCodeUpstreamNoResponse)
	wrapped := fmt.Errorf("do request failed: %w", inner)
	got := wrapDoRequestError(wrapped)
	if got.GetErrorCode() != types.ErrorCodeUpstreamNoResponse {
		t.Fatalf("want UpstreamNoResponse preserved through fmt.Errorf wrap, got %s", got.GetErrorCode())
	}
}

func TestWrapDoRequestErrorPlainFallback(t *testing.T) {
	got := wrapDoRequestError(errors.New("some other error"))
	if got.GetErrorCode() != types.ErrorCodeDoRequestFailed {
		t.Fatalf("plain error should fall back to do_request_failed, got %s", got.GetErrorCode())
	}
}
