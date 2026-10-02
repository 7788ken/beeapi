package relay

import (
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func doResponseWithQualityFilter(c *gin.Context, info *relaycommon.RelayInfo, do func() (any, *types.NewAPIError)) (any, *types.NewAPIError) {
	hold := service.BeginQualityHold(c, info)
	usage, err := do()
	// 放行前已拦截时上游是网关主动停掉的，截断引起的收尾错误不能盖过拦截结果。
	if qerr := hold.BlockedError(); qerr != nil {
		hold.Discard()
		service.ChargeOrDeferQualityFilter(c, info, usage, qerr)
		return usage, qerr
	}
	if err != nil {
		if !hold.Released() {
			hold.Discard()
		}
		return usage, err
	}
	if hold.Released() {
		return usage, nil
	}
	if qerr := service.ApplyResponseQualityFilter(c, info, usage); qerr != nil {
		hold.Discard()
		service.ChargeOrDeferQualityFilter(c, info, usage, qerr)
		return usage, qerr
	}
	if ferr := hold.Flush(); ferr != nil {
		return usage, types.NewError(ferr, types.ErrorCodeBadResponse)
	}
	return usage, nil
}
