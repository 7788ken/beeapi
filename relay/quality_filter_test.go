package relay

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// 拦截后停流，handler 收尾时可能因截断报错（如客户端已断开时写收尾帧失败），结果仍必须是拦截错误。
func TestDoResponseWithQualityFilterBlockWinsOverTruncationError(t *testing.T) {
	prev := *operation_setting.GetResponseQualitySetting()
	t.Cleanup(func() { *operation_setting.GetResponseQualitySetting() = prev })
	cfg := operation_setting.GetResponseQualitySetting()
	cfg.ApplyAllChannels = true
	cfg.BlockApologyEnabled = true
	cfg.LowTokenThreshold = 300

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-test"}}

	_, err := doResponseWithQualityFilter(c, info, func() (any, *types.NewAPIError) {
		service.NoteQualityStreamDelta(c, service.QualityStreamDelta{Visible: "I'm sorry, I can't help with that."})
		return nil, types.NewError(errors.New("write final frame failed after cut"), types.ErrorCodeBadResponse)
	})
	if err == nil || err.GetErrorCode() != types.ErrorCodeResponseQualityApology {
		t.Fatalf("blocked attempt must end with the quality error, got %v", err)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("blocked attempt leaked to client: %q", rec.Body.String())
	}
}
