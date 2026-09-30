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
	if err != nil {
		if !hold.Released() {
			hold.Discard()
		}
		return usage, err
	}
	if hold.Released() {
		return usage, nil
	}
	if qerr := hold.BlockedError(); qerr != nil {
		hold.Discard()
		service.ChargeOrDeferQualityFilter(c, info, usage, qerr)
		return usage, qerr
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
