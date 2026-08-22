package service

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAttachQuotaSaturationNestsUnderAdminInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{
		UserId:          7,
		OriginModelName: "gpt-image-1",
		QuotaClamp: &common.QuotaClamp{
			Op:       "QuotaFromDecimal",
			Kind:     common.QuotaClampOverflow,
			Original: 1.8e19,
			Clamped:  common.MaxQuota,
		},
	}
	other := map[string]interface{}{
		"admin_info": map[string]interface{}{"use_channel": []string{"1"}},
	}

	attachQuotaSaturation(ctx, info, other)

	adminInfo := other["admin_info"].(map[string]interface{})
	require.Contains(t, adminInfo, "use_channel")
	saturation := adminInfo["quota_saturation"].(map[string]interface{})
	require.Equal(t, common.QuotaClampOverflow, saturation["kind"])
	require.Equal(t, common.MaxQuota, saturation["clamped"])
}

func TestPreConsumeBillingRejectsInvalidQuotaBeforeDeduction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	clamp := &common.QuotaClamp{
		Op:       "QuotaFromFloat",
		Kind:     common.QuotaClampOverflow,
		Original: 1e30,
		Clamped:  common.MaxQuota,
	}

	apiErr := PreConsumeBilling(ctx, common.MaxQuota, &relaycommon.RelayInfo{QuotaClamp: clamp})
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	require.Equal(t, types.ErrorCodeModelPriceError, apiErr.GetErrorCode())

	apiErr = PreConsumeBilling(ctx, -1, &relaycommon.RelayInfo{})
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	require.Equal(t, types.ErrorCodeModelPriceError, apiErr.GetErrorCode())
}
