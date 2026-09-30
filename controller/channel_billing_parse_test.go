package controller

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 分站 key 余额解析：quota→USD 折算、无限额度报错、异常响应报错。
func TestParseNewAPIKeyUsage(t *testing.T) {
	// 正常：剩余 1.5 * QuotaPerUnit → 1.5 USD
	body := []byte(`{"code":true,"message":"ok","data":{"object":"token_usage","name":"k1","total_granted":2000000,"total_used":500000,"total_available":` +
		strconv.FormatFloat(1.5*common.QuotaPerUnit, 'f', -1, 64) +
		`,"unlimited_quota":false}}`)
	balance, err := parseNewAPIKeyUsage(body)
	require.NoError(t, err)
	assert.InDelta(t, 1.5, balance, 1e-9)

	// 无限额度：无法按 key 折算
	_, err = parseNewAPIKeyUsage([]byte(`{"code":true,"data":{"total_available":0,"unlimited_quota":true}}`))
	require.Error(t, err)

	// 上游错误响应（code=false / 缺 data）
	_, err = parseNewAPIKeyUsage([]byte(`{"code":false,"message":"token not found"}`))
	require.Error(t, err)

	// 非 new-api 系上游的 404 HTML
	_, err = parseNewAPIKeyUsage([]byte(`<html>404 Not Found</html>`))
	require.Error(t, err)
}

func TestPickDeepSeekBalance(t *testing.T) {
	got, err := pickDeepSeekBalance([]deepSeekBalanceInfo{{Currency: "CNY", TotalBalance: "12.5"}})
	require.NoError(t, err)
	assert.Equal(t, "12.5", got)

	got, err = pickDeepSeekBalance([]deepSeekBalanceInfo{{Currency: "USD", TotalBalance: "3.2"}})
	require.NoError(t, err)
	assert.Equal(t, "3.2", got)

	got, err = pickDeepSeekBalance([]deepSeekBalanceInfo{
		{Currency: "USD", TotalBalance: "3.2"},
		{Currency: "CNY", TotalBalance: "12.5"},
	})
	require.NoError(t, err)
	assert.Equal(t, "12.5", got)

	_, err = pickDeepSeekBalance(nil)
	require.Error(t, err)
}
