package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 分组可用率按渠道聚合再经 abilities 映射到分组，归属按"样本发生时刻是否可被路由"：
// 真实流量按全量 (分组,渠道) 映射计入（含 enabled=0 的历史归属）；启用态测活（token_name=模型测试）
// 只计入当前 enabled 的分组；禁用期探活（token_name=模型测试-停用）永不计入。
// 测活的判别是三重门：user_id=1 且 token_id=0 且 token_name 为测活标记——重名的真实令牌
// （无论普通用户还是 root 自建）都必须按真实流量算。一个渠道服务多个分组时其样本各计一次。
func TestGetGroupUptimeMapsChannelLogsToEveryServedGroup(t *testing.T) {
	dsn := fmt.Sprintf("file:group_uptime_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	model.InitColumnRefs()

	require.NoError(t, db.Exec(`CREATE TABLE channels (id INTEGER PRIMARY KEY)`).Error)
	require.NoError(t, db.Exec("CREATE TABLE abilities (`group` TEXT, model TEXT, channel_id INTEGER, enabled INTEGER)").Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id INTEGER,
			user_id INTEGER,
			token_id INTEGER,
			type INTEGER,
			created_at INTEGER,
			token_name TEXT
		)`).Error)

	require.NoError(t, db.Exec(`INSERT INTO channels (id) VALUES (1), (2)`).Error)
	// 渠道 1 同时服务 alpha 与 beta（多模型行需去重）；渠道 2 服务 beta 与 gamma，
	// 其中 gamma 行 enabled=0（渠道在该分组已被禁用）；delta 的两行 enabled 混排（1/0 并存），
	// 必须按"任一行启用即启用"合并，且 DISTINCT 出的两行不得让样本重复计数。
	require.NoError(t, db.Exec("INSERT INTO abilities (`group`, model, channel_id, enabled) VALUES "+
		"('alpha','m1',1,1), ('alpha','m2',1,1), ('beta','m1',1,1), ('beta','m1',2,1), ('gamma','m1',2,0), "+
		"('delta','m1',1,1), ('delta','m2',1,0)").Error)

	now := common.GetTimestamp()
	// 渠道 1 @now：真实 1 成功 1 失败 + 启用态测活 1 成功（user_id=1、token_id=0）
	// + 两条"重名但不是测活"的真实成功：普通用户(user_id=9)令牌起名模型测试、
	// root 自建令牌(token_id=5)起名模型测试——user_id 与 token_id 两道门各钉一条。
	// 渠道 1 @now-7200：真实 1 成功（跨小时桶，序列须按 Ts 升序出两个点）。
	// 渠道 2 @now：真实 1 成功 + root 非测活令牌真实 1 成功 + 启用态测活 1 成功
	// + 禁用期探活 3 失败（必须整体不计入任何分组）。
	require.NoError(t, db.Exec(`
		INSERT INTO logs (channel_id, user_id, token_id, type, created_at, token_name) VALUES
			(1, 7, 2, 2, ?, 'real'),
			(1, 7, 2, 5, ?, 'real'),
			(1, 1, 0, 2, ?, '模型测试'),
			(1, 9, 3, 2, ?, '模型测试'),
			(1, 1, 5, 2, ?, '模型测试'),
			(1, 7, 2, 2, ?, 'real'),
			(2, 7, 2, 2, ?, 'real'),
			(2, 1, 4, 2, ?, 'root-usage'),
			(2, 1, 0, 2, ?, '模型测试'),
			(2, 1, 0, 5, ?, '模型测试-停用'),
			(2, 1, 0, 5, ?, '模型测试-停用'),
			(2, 1, 0, 5, ?, '模型测试-停用')`,
		now, now, now, now, now, now-7200, now, now, now, now, now, now).Error)

	previousDB, previousLogDB := model.DB, model.LOG_DB
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() { model.DB, model.LOG_DB = previousDB, previousLogDB })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := GetGroupUptime(ctx, 24)
	require.NoError(t, err)
	series := result.Series

	// alpha 只由渠道 1 供给，两个桶按 Ts 升序：早桶（now-7200）= 真实 1/1；
	// 晚桶（now）= 真实 3/4 + 启用态测活 1/1 = 4 成功 / 5 总数。
	// 两条重名真实成功（user_id=9 与 token_id=5）必须落在"真实"里而不是被当测活扣掉。
	require.Len(t, series["alpha"], 2)
	require.Less(t, series["alpha"][0].Ts, series["alpha"][1].Ts)
	require.Equal(t, int64(1), series["alpha"][0].RequestCount)
	require.Equal(t, int64(1), series["alpha"][0].SuccessCount)
	require.Equal(t, int64(5), series["alpha"][1].RequestCount)
	require.Equal(t, int64(4), series["alpha"][1].SuccessCount)
	require.InDelta(t, 80.0, series["alpha"][1].SuccessRate, 0.01)

	// delta 与 alpha 同由渠道 1 供给：enabled 混排的两行合并为"启用"，
	// 且 DISTINCT 返回的两行不得让样本翻倍——序列必须与 alpha 完全一致。
	require.Equal(t, series["alpha"], series["delta"])

	// beta 由渠道 1+2 汇总，晚桶 = 渠道1 (4/5) + 渠道2 真实 2/2 + 启用态测活 1/1 = 7 成功 / 8 总数；
	// 渠道 2 的 3 条禁用期探活失败一条都不能混进来。
	require.Len(t, series["beta"], 2)
	require.Equal(t, int64(8), series["beta"][1].RequestCount)
	require.Equal(t, int64(7), series["beta"][1].SuccessCount)
	require.InDelta(t, 87.5, series["beta"][1].SuccessRate, 0.01)

	// gamma 的唯一映射行 enabled=0：真实流量（含 root 用户的非测活请求）仍按历史归属计入
	// （2 成功 / 2 总数），但启用态测活不归它、禁用期探活也不计入——RequestCount=2 钉死这两点。
	require.Len(t, series["gamma"], 1)
	require.Equal(t, int64(2), series["gamma"][0].RequestCount)
	require.Equal(t, int64(2), series["gamma"][0].SuccessCount)
	require.InDelta(t, 100.0, series["gamma"][0].SuccessRate, 0.01)
	// gamma 没有任何启用渠道 → 不算「有供给」，调用方据此把它整窗画成 0%
	// （series 里的历史真实流量会被 FilterGroupUptimeSeries 覆盖，见下个测试）。
	require.NotContains(t, result.GroupsWithSupply, "gamma")
	require.Contains(t, result.GroupsWithSupply, "alpha")
	require.Contains(t, result.GroupsWithSupply, "beta")
	require.Contains(t, result.GroupsWithSupply, "delta")
}

// FilterGroupUptimeSeries 的分支顺序是承重的：无供给分组即使 Series 里带着已禁用渠道的
// 历史真实流量，也必须整窗画 0%——先取序列会把"现在就不可用"渲染成绿色高可用。
func TestFilterGroupUptimeSeriesZeroFillsUnsuppliedBeforeReadingSeries(t *testing.T) {
	now := time.Unix(1_800_000_123, 0)
	history := []GroupUptimePoint{{Ts: now.Unix() - now.Unix()%3600, RequestCount: 2, SuccessCount: 2, SuccessRate: 100}}
	result := GroupUptimeResult{
		Series: map[string][]GroupUptimePoint{
			"supplied":       history,
			"dead-with-past": history,
			"hidden":         history,
		},
		GroupsWithSupply: map[string]struct{}{"supplied": {}, "idle": {}},
	}

	out := FilterGroupUptimeSeries(result, []string{"supplied", "dead-with-past", "no-supply-no-data", "idle"}, 24, now)

	require.Equal(t, history, out["supplied"]) // 有供给有数据：透传
	// 无供给：整窗 0% 红，历史真实流量被覆盖（分支顺序换掉时这里会变成 100% 绿）
	require.Len(t, out["dead-with-past"], 24)
	for _, p := range out["dead-with-past"] {
		require.Zero(t, p.RequestCount)
		require.Zero(t, p.SuccessRate)
	}
	require.Len(t, out["no-supply-no-data"], 24) // 无供给且无数据：同样整窗 0%
	require.NotContains(t, out, "idle")          // 有供给但窗口内无日志：留空灰显
	require.NotContains(t, out, "hidden")        // 不在可见清单里：不返回
	require.Len(t, out, 3)
}

func TestGetGroupUptimeRejectsOutOfRangeHours(t *testing.T) {
	_, err := GetGroupUptime(context.Background(), 0)
	require.Error(t, err)
	_, err = GetGroupUptime(context.Background(), GroupUptimeMaxHours+1)
	require.Error(t, err)
}

// 无启用渠道的分组要画成整窗口 0%：槽位数、整点对齐与升序都必须和正常序列一致，
// 否则前端按 ts 补槽时对不上、又会退回灰色「无数据」。
func TestZeroUptimeSeriesCoversWholeWindowAtHourBoundaries(t *testing.T) {
	now := time.Unix(1_800_000_123, 0)
	points := ZeroUptimeSeries(24, now)

	require.Len(t, points, 24)
	for i, p := range points {
		require.Zero(t, p.Ts%3600, "bucket %d not aligned to the hour", i)
		require.Zero(t, p.RequestCount)
		require.Zero(t, p.SuccessCount)
		require.Zero(t, p.SuccessRate)
		if i > 0 {
			require.Equal(t, points[i-1].Ts+3600, p.Ts)
		}
	}
	require.Equal(t, now.Unix()-now.Unix()%3600, points[23].Ts)
}
