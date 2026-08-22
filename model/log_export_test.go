package model

import (
	"bytes"
	"encoding/csv"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// 验证脱敏导出的三条安全属性：用户越权隔离、敏感列不落盘、CSV 公式注入中和
func TestStreamLogsCSVSecurity(t *testing.T) {
	truncateTables(t)

	// user 1 的日志：含全部敏感字段 + 令牌名以 = 开头（公式注入探针）
	insertLog(t, &Log{
		UserId: 1, Type: LogTypeConsume, Username: "alice",
		TokenName: "=cmd|' /C calc'!A1", ModelName: "gpt-4",
		PromptTokens: 10, CompletionTokens: 20, Quota: 500000, UseTime: 3,
		ChannelId: 42, ChannelName: "Claude AWS B", Group: "aws-secret-group",
		Ip: "1.2.3.4", Other: `{"key_fp":"deadbeef","model_ratio":3.5}`,
		RequestId: "req-alice-1",
	})
	// user 2 的日志：不应出现在 user 1 的导出里
	insertLog(t, &Log{
		UserId: 2, Type: LogTypeConsume, Username: "bob",
		TokenName: "bob-token", ModelName: "claude-3",
		ChannelId: 99, ChannelName: "secret", Group: "g2", Ip: "9.9.9.9",
		Other: `{"key_fp":"cafe"}`, RequestId: "req-bob-1",
	})
	// user 1 的错误日志：content 应被置空
	insertLog(t, &Log{
		UserId: 1, Type: LogTypeError, Username: "alice",
		Content: "upstream bedrock arn:aws:secret leaked", RequestId: "req-alice-err",
	})

	// 用户侧查询：强制 user_id=1
	query, err := BuildUserLogsQuery(1, LogTypeUnknown, 0, 0, "", "", "", "")
	require.NoError(t, err)

	// 镜像 controller 路径：先 count 预检（复用同一 query），再流式导出
	cnt, err := CountLogsForExport(query, LogExportMaxRowsUser)
	require.NoError(t, err)
	require.Equal(t, int64(2), cnt, "user 1 只有 2 条，count 必须已按 user_id 隔离")

	var buf bytes.Buffer
	require.NoError(t, StreamLogsCSV(&buf, query, false, LogExportMaxRowsUser))
	out := buf.String()

	// 1) 越权隔离：user 2 的任何痕迹都不得出现
	require.NotContains(t, out, "bob")
	require.NotContains(t, out, "req-bob-1")
	require.NotContains(t, out, "claude-3")

	// 2) 敏感列不落盘：渠道/分组/IP/other/指纹全部不得出现
	require.NotContains(t, out, "Claude AWS B")
	require.NotContains(t, out, "aws-secret-group")
	require.NotContains(t, out, "1.2.3.4")
	require.NotContains(t, out, "key_fp")
	require.NotContains(t, out, "deadbeef")
	require.NotContains(t, out, "model_ratio")

	// 3) 错误日志 content 置空：不得泄露上游报错
	require.NotContains(t, out, "arn:aws:secret")

	// 4) CSV 公式注入中和：= 开头被前置单引号
	require.NotContains(t, out, ",=cmd")
	require.Contains(t, out, "'=cmd")

	// 5) 应正常导出 user 1 的两条日志（表头 + 2 行 + BOM）
	require.Contains(t, out, "req-alice-1")
	require.Contains(t, out, "gpt-4")
	require.True(t, strings.HasPrefix(out, "\xEF\xBB\xBF"), "应带 UTF-8 BOM")
	// 金额换算：500000/500000 = 1.000000 USD
	require.Contains(t, out, "1.000000")
}

// 强制跨批(>5000/批)验证游标分页：不重不漏、行数精确
func TestStreamLogsCSVMultiBatch(t *testing.T) {
	truncateTables(t)

	const n = logExportBatchSize*2 + 37 // 10037，跨 3 批且末批非整批
	rows := make([]*Log, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, &Log{
			UserId: 7, Type: LogTypeConsume, Username: "u7",
			ModelName: "gpt-4", RequestId: "rq-" + strconv.Itoa(i),
			CreatedAt: common.GetTimestamp(),
		})
	}
	require.NoError(t, LOG_DB.CreateInBatches(rows, 1000).Error)

	query, err := BuildUserLogsQuery(7, LogTypeUnknown, 0, 0, "", "", "", "")
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, StreamLogsCSV(&buf, query, false, LogExportMaxRowsUser))

	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(buf.String(), "\xEF\xBB\xBF")))
	records, err := r.ReadAll()
	require.NoError(t, err)

	// 表头 1 行 + n 条数据；request_id 去重后必须恰好 n 个（不重不漏）
	require.Len(t, records, n+1)
	seen := make(map[string]struct{}, n)
	for _, rec := range records[1:] {
		reqId := rec[len(rec)-2] // 倒数第二列是请求ID（最后一列是详情）
		_, dup := seen[reqId]
		require.False(t, dup, "请求ID 重复=游标分页有重叠: %s", reqId)
		seen[reqId] = struct{}{}
	}
	require.Len(t, seen, n)
}

// 验证管理员查询不做 user 隔离，但同样脱敏
func TestStreamLogsCSVAdminScopeNoUserFilter(t *testing.T) {
	truncateTables(t)
	insertLog(t, &Log{UserId: 1, Type: LogTypeConsume, Username: "alice", ModelName: "gpt-4", ChannelName: "up1", RequestId: "a1"})
	insertLog(t, &Log{UserId: 2, Type: LogTypeConsume, Username: "bob", ModelName: "gpt-4", ChannelName: "up2", RequestId: "b1"})

	query, err := BuildAllLogsQuery(LogTypeUnknown, 0, 0, "", "", "", 0, "", "")
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, StreamLogsCSV(&buf, query, true, LogExportMaxRowsAdmin))
	out := buf.String()

	// 管理员导出含两个用户，但渠道名脱敏
	require.Contains(t, out, "a1")
	require.Contains(t, out, "b1")
	require.Contains(t, out, "alice")
	require.Contains(t, out, "bob")
	require.NotContains(t, out, "up1")
	require.NotContains(t, out, "up2")
}

// 验证导出上限按角色参数化：同一结果集，传入的上限决定放行还是拒绝。
// 上限判定是纯粹的 total>maxRows 比较，用小上限即可等价验证边界，无需造出真实的十万/两百万行。
func TestCountLogsForExportRoleLimit(t *testing.T) {
	truncateTables(t)

	// 管理员上限必须大于普通用户上限，否则角色区分无意义
	require.Greater(t, LogExportMaxRowsAdmin, LogExportMaxRowsUser)

	for i := 0; i < 4; i++ {
		insertLog(t, &Log{
			UserId: 7, Type: LogTypeConsume, Username: "u7",
			ModelName: "gpt-4", RequestId: "rq-" + strconv.Itoa(i),
		})
	}

	query, err := BuildUserLogsQuery(7, LogTypeUnknown, 0, 0, "", "", "", "")
	require.NoError(t, err)

	// 上限低于结果集 -> 报错拒绝（模拟普通用户超限路径）
	_, err = CountLogsForExport(query, 3)
	require.Error(t, err, "结果集超过上限应报错")

	// 上限高于结果集 -> 放行（模拟管理员大上限路径）
	cnt, err := CountLogsForExport(query, LogExportMaxRowsAdmin)
	require.NoError(t, err, "上限内应放行")
	require.Equal(t, int64(4), cnt)
}
