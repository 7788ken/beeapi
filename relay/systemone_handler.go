package relay

import (
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel/typesafe"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// SystemOneHelper 中继一次 TypeSafe System One 评估。
//
// 和这里其它 helper 不同，它不做任何转换：System One 请求是 state 加带类型的 questions，
// 答案是带校准概率的类型化值，两边都按客户和厂商写的样子转发——那个结构就是产品本身。
// 唯一会改写的是模型名，且仅在渠道配了映射时；也只有那种情况才为 body 付一次解码重编码。
func SystemOneHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	request, ok := info.Request.(*dto.SystemOneRequest)
	if !ok {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("invalid request type, expected dto.SystemOneRequest, got %T", info.Request),
			types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	requestedModel := request.Model

	if err := helper.ModelMappedHelper(c, info, request); err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}
	upstreamModel := info.UpstreamModelName
	if upstreamModel == "" {
		upstreamModel = request.Model
	}

	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	var requestBody io.Reader
	if upstreamModel == requestedModel {
		info.UpstreamRequestBodySize = storage.Size()
		requestBody = common.ReaderOnly(storage)
	} else {
		// 渠道改了模型名，body 里得写新名字。走一次 map 往返能保留客户发的其它所有字段，
		// 包括厂商在这段代码之后新增的字段。
		raw, err := storage.Bytes()
		if err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		fields := map[string]any{}
		if err := common.Unmarshal(raw, &fields); err != nil {
			return types.NewError(fmt.Errorf("could not apply the channel's model mapping: %w", err), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		fields["model"] = upstreamModel
		rewritten, err := common.Marshal(fields)
		if err != nil {
			return types.NewError(fmt.Errorf("could not apply the channel's model mapping: %w", err), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		body, size, closer, err := relaycommon.NewOutboundJSONBody(rewritten)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		defer closer.Close()
		info.UpstreamRequestBodySize = size
		requestBody = body
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)

	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return wrapDoRequestError(err)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	var httpResp *http.Response
	if resp != nil {
		httpResp = resp.(*http.Response)
		if httpResp.StatusCode != http.StatusOK {
			newAPIError = typesafe.RelayErrorHandler(c.Request.Context(), httpResp)
			service.ResetStatusCode(newAPIError, statusCodeMappingStr)
			return newAPIError
		}
	}

	usage, newAPIError := adaptor.DoResponse(c, httpResp, info)
	if newAPIError != nil {
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}
	service.PostTextConsumeQuota(c, info, usage.(*dto.Usage), nil)
	return nil
}
