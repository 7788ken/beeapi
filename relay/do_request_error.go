package relay

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/types"
)

// wrapDoRequestError 包装 adaptor.DoRequest 返回的错误。
//
// 若 doRequest 已把它分类为"上游节点网络无响应"(ErrorCodeUpstreamNoResponse) 或
// "上游超时"(ErrorCodeUpstreamTimeout)（见 relay/channel.classifyDoRequestError），则保留该错误码——
// relay 主循环据此在同渠道内切换 base_url（仅 UpstreamNoResponse 触发切换）。
// 否则归为通用 do_request_failed。
//
// 注意：DoApiRequest 用 fmt.Errorf("%w") 包了一层，故用 errors.As 穿透取分类码。
func wrapDoRequestError(err error) *types.NewAPIError {
	var apiErr *types.NewAPIError
	if errors.As(err, &apiErr) {
		switch apiErr.GetErrorCode() {
		case types.ErrorCodeUpstreamNoResponse, types.ErrorCodeUpstreamTimeout:
			return apiErr
		}
	}
	return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
}
