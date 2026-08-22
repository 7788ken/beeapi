package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestFormatUserLogsStripsQuotaSaturationAudit(t *testing.T) {
	logs := []*Log{{Other: common.MapToJsonStr(map[string]interface{}{
		"model_price": 0.04,
		"admin_info": map[string]interface{}{
			"quota_saturation": map[string]interface{}{
				"kind":    common.QuotaClampOverflow,
				"clamped": common.MaxQuota,
			},
		},
	})}}

	formatUserLogs(logs, 0)

	other, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	require.NotContains(t, other, "admin_info")
	require.Contains(t, other, "model_price")
}
