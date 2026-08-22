// Package service - runtime_metrics.go
// 运行时大盘快照：每 60s 算一次过去 5 分钟的 Top10 数据，存 Redis + 内存兜底。
//
// 设计（docs/2026-05-12-runtime-dashboard.md）：
//   - 4 个面板：top users / top groups / top channels / top group×channel
//   - 每个面板预先按 RPM 和 Cost 两个维度排好；users / channels 再加 balance 排序
//   - SQL 跨库通用（纯标量聚合），单次重算 4-6 条 SQL 串行，预期 < 1s
//   - Redis key: dashboard:runtime:v1 (JSON)；TTL 70s（>tick interval，避免短暂空窗）
//   - 非重入 + 4min 超时；启动时立刻跑一次
package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
)

const (
	runtimeMetricsTickInterval = 60 * time.Second
	runtimeMetricsRunTimeout   = 4 * time.Minute
	// runtimeMetricsWindow：dashboard 聚合的时间窗。
	// 历史上是 300s（5min 平均）— 大窗口稳定但严重滞后实时：burst 已经停 4 分钟，dashboard 仍把该用户
	// 排在第一。改成 60s 后语义对齐"近 1 分钟实时 RPM"，跟用户/渠道列表的 Redis 滑动窗口 RPM 一致。
	// 副作用：Cost 字段也变成"近 1 分钟花费"，不是 5 分钟累计。前端 label 已同步改"1min window"。
	runtimeMetricsWindow   = int64(60)
	runtimeMetricsTopN     = 10
	runtimeMetricsRedisKey = "dashboard:runtime:v2" // bump key 避免与旧 5min 结构混用
	runtimeMetricsRedisTTL = 70 * time.Second
)

var (
	runtimeMetricsOnce    sync.Once
	runtimeMetricsRunning atomic.Bool

	// 内存兜底（当 Redis 不可用时仍能服务读请求）
	runtimeMetricsMu    sync.RWMutex
	runtimeMetricsCache *RuntimeSnapshot
)

// ──────────────────────────────────────────────────────────────────────────
// 数据结构
// ──────────────────────────────────────────────────────────────────────────

// TopUserItem 一行 top user 数据。预先包含 RPM/Cost/Balance 三个排序所需字段。
type TopUserItem struct {
	UserId   int     `json:"user_id"`
	Username string  `json:"username"`
	Rpm      float64 `json:"rpm"`     // 60s 成功请求数 / 1 = 近 1 分钟实时 RPM（只算成功，口径同渠道/用户列表的 Redis 滑动窗口）
	Cost     int64   `json:"cost"`    // 5min 消耗 quota
	Balance  int     `json:"balance"` // users.quota（剩余余额）
}

type TopGroupItem struct {
	Group       string  `json:"group"`
	Rpm         float64 `json:"rpm"`
	Cost        int64   `json:"cost"`
	UserCount   int     `json:"user_count"`   // distinct user_id
	ChannelUsed int     `json:"channel_used"` // distinct channel_id
}

type TopChannelItem struct {
	ChannelId    int     `json:"channel_id"`
	ChannelName  string  `json:"channel_name"`
	Group        string  `json:"group"` // channels.group（当前配置 group）
	Rpm          float64 `json:"rpm"`
	Cost         int64   `json:"cost"`
	SuccessCount int64   `json:"success_count"`
	ErrorCount   int64   `json:"error_count"`
	Balance      float64 `json:"balance"` // channels.balance
}

type TopGroupChannelItem struct {
	Group       string  `json:"group"`
	ChannelId   int     `json:"channel_id"`
	ChannelName string  `json:"channel_name"`
	Rpm         float64 `json:"rpm"`
	Cost        int64   `json:"cost"`
}

// RuntimeSnapshot 完整快照；前端按 sort 选择对应维度的 top10 数组。
// 每个面板的多个排序数组事先算好，前端零计算选择。
type RuntimeSnapshot struct {
	GeneratedAt   int64 `json:"generated_at"`   // unix sec
	WindowSeconds int64 `json:"window_seconds"` // = 60
	ElapsedMs     int64 `json:"elapsed_ms"`     // 本次重算耗时（监控用）

	// 用户：3 套排序（rpm / cost / balance）
	TopUsersByRpm     []TopUserItem `json:"top_users_by_rpm"`
	TopUsersByCost    []TopUserItem `json:"top_users_by_cost"`
	TopUsersByBalance []TopUserItem `json:"top_users_by_balance"`

	// 分组：2 套（rpm / cost）；余额维度不适用 → 前端切到 balance 时仍显示 rpm 版
	TopGroupsByRpm  []TopGroupItem `json:"top_groups_by_rpm"`
	TopGroupsByCost []TopGroupItem `json:"top_groups_by_cost"`

	// 渠道：3 套（rpm / cost / balance）
	TopChannelsByRpm     []TopChannelItem `json:"top_channels_by_rpm"`
	TopChannelsByCost    []TopChannelItem `json:"top_channels_by_cost"`
	TopChannelsByBalance []TopChannelItem `json:"top_channels_by_balance"`

	// 分组×渠道：2 套（rpm / cost）
	TopGroupChannelsByRpm  []TopGroupChannelItem `json:"top_group_channels_by_rpm"`
	TopGroupChannelsByCost []TopGroupChannelItem `json:"top_group_channels_by_cost"`
}

// ──────────────────────────────────────────────────────────────────────────
// 启动 + 调度
// ──────────────────────────────────────────────────────────────────────────

func StartRuntimeMetricsTask() error {
	var startErr error
	runtimeMetricsOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		startErr = backgroundtask.Start("runtime-metrics", func(ctx context.Context) {
			logger.LogInfo(context.Background(),
				fmt.Sprintf("runtime metrics task started: tick=%s window=%ds",
					runtimeMetricsTickInterval, runtimeMetricsWindow))
			backgroundtask.RunPeriodic(ctx, runtimeMetricsTickInterval, true, func() {
				runRuntimeMetricsOnce()
			})
		})
	})
	return startErr
}

func runRuntimeMetricsOnce() {
	if !runtimeMetricsRunning.CompareAndSwap(false, true) {
		logger.LogWarn(context.Background(), "runtime metrics: previous run still inflight, skip")
		return
	}
	defer runtimeMetricsRunning.Store(false)

	ctx, cancel := context.WithTimeout(context.Background(), runtimeMetricsRunTimeout)
	defer cancel()

	if err := RecomputeRuntimeMetricsOnce(ctx); err != nil {
		logger.LogError(ctx, fmt.Sprintf("runtime metrics: failed: %v", err))
	}
}

// ──────────────────────────────────────────────────────────────────────────
// 重算入口
// ──────────────────────────────────────────────────────────────────────────

// RecomputeRuntimeMetricsOnce 跑一次完整重算，写入 Redis 和内存。
// 串行执行 4 个面板的聚合 + 2 个余额维度查询，避免突发占用 DB 连接。
func RecomputeRuntimeMetricsOnce(ctx context.Context) error {
	if model.DB == nil || model.LOG_DB == nil {
		return errors.New("database not initialized")
	}
	start := time.Now()
	now := common.GetTimestamp()
	from := now - runtimeMetricsWindow
	windowMin := float64(runtimeMetricsWindow) / 60.0 // 1（60s 窗口 / 60s 每分钟）

	snap := &RuntimeSnapshot{
		GeneratedAt:   now,
		WindowSeconds: runtimeMetricsWindow,
	}

	// 1. Users（按 RPM / Cost 排）
	users, err := loadTopUsers(ctx, from, windowMin)
	if err != nil {
		return fmt.Errorf("loadTopUsers: %w", err)
	}
	snap.TopUsersByRpm = sortTopUsersByRpm(users)
	snap.TopUsersByCost = sortTopUsersByCost(users)

	// 2. Top users by balance（按 users.quota desc，不依赖 logs）
	snap.TopUsersByBalance, err = loadTopUsersByBalance(ctx)
	if err != nil {
		return fmt.Errorf("loadTopUsersByBalance: %w", err)
	}

	// 3. Groups
	groups, err := loadTopGroups(ctx, from, windowMin)
	if err != nil {
		return fmt.Errorf("loadTopGroups: %w", err)
	}
	snap.TopGroupsByRpm = sortTopGroupsByRpm(groups)
	snap.TopGroupsByCost = sortTopGroupsByCost(groups)

	// 4. Channels
	channels, err := loadTopChannels(ctx, from, windowMin)
	if err != nil {
		return fmt.Errorf("loadTopChannels: %w", err)
	}
	snap.TopChannelsByRpm = sortTopChannelsByRpm(channels)
	snap.TopChannelsByCost = sortTopChannelsByCost(channels)

	// 5. Top channels by balance
	snap.TopChannelsByBalance, err = loadTopChannelsByBalance(ctx)
	if err != nil {
		return fmt.Errorf("loadTopChannelsByBalance: %w", err)
	}

	// 6. Group × Channel
	groupCh, err := loadTopGroupChannels(ctx, from, windowMin)
	if err != nil {
		return fmt.Errorf("loadTopGroupChannels: %w", err)
	}
	snap.TopGroupChannelsByRpm = sortTopGroupChannelsByRpm(groupCh)
	snap.TopGroupChannelsByCost = sortTopGroupChannelsByCost(groupCh)

	snap.ElapsedMs = time.Since(start).Milliseconds()

	// 归一化空切片：避免 nil slice 序列化为 JSON null 让前端 .length 报错
	normalizeSnapshot(snap)

	// 持久化：内存兜底 + Redis（Redis 不可用时静默降级）
	storeRuntimeSnapshot(snap)
	logger.LogInfo(ctx, fmt.Sprintf("runtime metrics: done elapsed=%dms", snap.ElapsedMs))
	return nil
}

// EmptyRuntimeSnapshot 返回一个所有 top 数组都为空的快照，供首启冷启动时返回，
// 让前端正常渲染 "No data" 占位而不需特殊处理。
func EmptyRuntimeSnapshot() *RuntimeSnapshot {
	snap := &RuntimeSnapshot{
		GeneratedAt:   common.GetTimestamp(),
		WindowSeconds: runtimeMetricsWindow,
	}
	normalizeSnapshot(snap)
	return snap
}

// normalizeSnapshot 把所有 nil slice 替换为空 slice，
// 确保 JSON 输出是 [] 而非 null（前端 .length / .map 才能正常使用）。
func normalizeSnapshot(snap *RuntimeSnapshot) {
	if snap.TopUsersByRpm == nil {
		snap.TopUsersByRpm = []TopUserItem{}
	}
	if snap.TopUsersByCost == nil {
		snap.TopUsersByCost = []TopUserItem{}
	}
	if snap.TopUsersByBalance == nil {
		snap.TopUsersByBalance = []TopUserItem{}
	}
	if snap.TopGroupsByRpm == nil {
		snap.TopGroupsByRpm = []TopGroupItem{}
	}
	if snap.TopGroupsByCost == nil {
		snap.TopGroupsByCost = []TopGroupItem{}
	}
	if snap.TopChannelsByRpm == nil {
		snap.TopChannelsByRpm = []TopChannelItem{}
	}
	if snap.TopChannelsByCost == nil {
		snap.TopChannelsByCost = []TopChannelItem{}
	}
	if snap.TopChannelsByBalance == nil {
		snap.TopChannelsByBalance = []TopChannelItem{}
	}
	if snap.TopGroupChannelsByRpm == nil {
		snap.TopGroupChannelsByRpm = []TopGroupChannelItem{}
	}
	if snap.TopGroupChannelsByCost == nil {
		snap.TopGroupChannelsByCost = []TopGroupChannelItem{}
	}
}

// storeRuntimeSnapshot 持久化快照。
// 顺序：先写 Redis，再写内存。GetRuntimeSnapshot 优先内存（更"新鲜"），
// Redis 作为跨实例广播/重启兜底用。这样 Redis 写失败也能保证读路径拿到最新数据。
func storeRuntimeSnapshot(snap *RuntimeSnapshot) {
	if common.RedisEnabled && common.RDB != nil {
		if data, err := common.Marshal(snap); err != nil {
			common.SysError(fmt.Sprintf("runtime metrics: marshal failed: %v", err))
		} else if err := common.RedisSet(runtimeMetricsRedisKey, string(data), runtimeMetricsRedisTTL); err != nil {
			common.SysError(fmt.Sprintf("runtime metrics: redis set failed: %v", err))
		}
	}
	runtimeMetricsMu.Lock()
	runtimeMetricsCache = snap
	runtimeMetricsMu.Unlock()
}

// GetRuntimeSnapshot 读快照：优先内存（同一实例最新）；内存空时回退 Redis（跨实例兜底）。
// 用于 controller 读路径（admin 列表请求）；纯内存读极快，不阻塞。
func GetRuntimeSnapshot() (*RuntimeSnapshot, error) {
	runtimeMetricsMu.RLock()
	cached := runtimeMetricsCache
	runtimeMetricsMu.RUnlock()
	if cached != nil {
		return cached, nil
	}
	// 内存没有，尝试 Redis（多 replica 或本实例重启场景）
	if common.RedisEnabled && common.RDB != nil {
		if raw, err := common.RedisGet(runtimeMetricsRedisKey); err == nil && raw != "" {
			var snap RuntimeSnapshot
			if err := common.UnmarshalJsonStr(raw, &snap); err == nil {
				// 同步回写内存，下次读不用走 Redis
				runtimeMetricsMu.Lock()
				runtimeMetricsCache = &snap
				runtimeMetricsMu.Unlock()
				return &snap, nil
			}
			// 反序列化失败 = 脏数据（schema 变更后残留），主动清掉避免重复尝试
			common.SysError(fmt.Sprintf("runtime metrics: redis deserialize failed, purging key %s", runtimeMetricsRedisKey))
			_ = common.RedisDel(runtimeMetricsRedisKey)
		}
	}
	return nil, errors.New("runtime metrics not yet computed")
}

// ──────────────────────────────────────────────────────────────────────────
// SQL 聚合
// ──────────────────────────────────────────────────────────────────────────

func loadTopUsers(ctx context.Context, from int64, windowMin float64) ([]TopUserItem, error) {
	type row struct {
		UserId int   `gorm:"column:user_id"`
		Succ   int64 `gorm:"column:succ"`
		Cost   int64 `gorm:"column:cost"`
	}
	var rows []row
	// 取超过 Top10 个候选（30 个），便于按 cost / rpm 两个维度都能取出真 Top10。
	// RPM 只算成功（type=2）：失败请求（429/503 风暴、跨渠道重试）计入会虚高，
	// 与渠道/用户列表的 Redis 实时 RPM（07-09 起只算成功）口径拉齐；候选排序同步用 succ。
	if err := model.LOG_DB.WithContext(ctx).Raw(`
		SELECT user_id,
		       SUM(CASE WHEN type = 2 THEN 1 ELSE 0 END) AS succ,
		       SUM(quota) AS cost
		FROM logs
		WHERE created_at >= ? AND user_id > 0 AND type IN (2, 5)
		GROUP BY user_id
		ORDER BY succ DESC
		LIMIT 30`, from).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []TopUserItem{}, nil
	}

	// 用一次 JOIN 拿 username + balance（quota）
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserId)
	}
	type userInfo struct {
		Id       int    `gorm:"column:id"`
		Username string `gorm:"column:username"`
		Quota    int    `gorm:"column:quota"`
	}
	var infos []userInfo
	if err := model.DB.WithContext(ctx).Raw(`
		SELECT id, username, quota FROM users WHERE id IN ?`, ids).Scan(&infos).Error; err != nil {
		return nil, err
	}
	infoMap := make(map[int]userInfo, len(infos))
	for _, u := range infos {
		infoMap[u.Id] = u
	}

	items := make([]TopUserItem, 0, len(rows))
	for _, r := range rows {
		info := infoMap[r.UserId]
		items = append(items, TopUserItem{
			UserId:   r.UserId,
			Username: info.Username,
			Rpm:      float64(r.Succ) / windowMin,
			Cost:     r.Cost,
			Balance:  info.Quota,
		})
	}
	return items, nil
}

func loadTopUsersByBalance(ctx context.Context) ([]TopUserItem, error) {
	type row struct {
		Id       int    `gorm:"column:id"`
		Username string `gorm:"column:username"`
		Quota    int    `gorm:"column:quota"`
	}
	var rows []row
	if err := model.DB.WithContext(ctx).Raw(`
		SELECT id, username, quota FROM users
		WHERE status = 1 AND quota > 0
		ORDER BY quota DESC LIMIT ?`, runtimeMetricsTopN).Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]TopUserItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, TopUserItem{
			UserId:   r.Id,
			Username: r.Username,
			Balance:  r.Quota,
		})
	}
	return items, nil
}

func loadTopGroups(ctx context.Context, from int64, windowMin float64) ([]TopGroupItem, error) {
	type row struct {
		GroupName   string `gorm:"column:group_name"`
		Succ        int64  `gorm:"column:succ"`
		Cost        int64  `gorm:"column:cost"`
		UserCount   int    `gorm:"column:user_count"`
		ChannelUsed int    `gorm:"column:channel_used"`
	}
	var rows []row
	// 用 LogGroupColumnRef 拿 logs.group 的跨库引用；alias 为 group_name 避免结果集再撞保留字。
	// RPM 只算成功（type=2）；user_count/channel_used 保留 2+5 全量（"活跃"语义，含纯报错流量）。
	gc := model.LogGroupColumnRef()
	if err := model.LOG_DB.WithContext(ctx).Raw(`
		SELECT `+gc+` AS group_name,
		       SUM(CASE WHEN type = 2 THEN 1 ELSE 0 END) AS succ,
		       SUM(quota) AS cost,
		       COUNT(DISTINCT user_id) AS user_count,
		       COUNT(DISTINCT channel_id) AS channel_used
		FROM logs
		WHERE created_at >= ? AND `+gc+` != '' AND type IN (2, 5)
		GROUP BY `+gc+`
		ORDER BY succ DESC
		LIMIT 30`, from).Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]TopGroupItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, TopGroupItem{
			Group:       r.GroupName,
			Rpm:         float64(r.Succ) / windowMin,
			Cost:        r.Cost,
			UserCount:   r.UserCount,
			ChannelUsed: r.ChannelUsed,
		})
	}
	return items, nil
}

func loadTopChannels(ctx context.Context, from int64, windowMin float64) ([]TopChannelItem, error) {
	type row struct {
		ChannelId int   `gorm:"column:channel_id"`
		Cost      int64 `gorm:"column:cost"`
		Succ      int64 `gorm:"column:succ"`
		Err       int64 `gorm:"column:err"`
	}
	var rows []row
	// RPM 只算成功；succ/err 两列本就分开统计，候选排序从 req 换 succ，
	// 避免 429/503 风暴渠道靠报错量霸榜（正是"RPM 不准"的用户体感来源）。
	if err := model.LOG_DB.WithContext(ctx).Raw(`
		SELECT channel_id,
		       SUM(quota) AS cost,
		       SUM(CASE WHEN type = 2 THEN 1 ELSE 0 END) AS succ,
		       SUM(CASE WHEN type = 5 THEN 1 ELSE 0 END) AS err
		FROM logs
		WHERE created_at >= ? AND channel_id IS NOT NULL AND type IN (2, 5)
		GROUP BY channel_id
		ORDER BY succ DESC
		LIMIT 30`, from).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []TopChannelItem{}, nil
	}
	// 拉 channel name + group + balance
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ChannelId)
	}
	type chInfo struct {
		Id        int     `gorm:"column:id"`
		Name      string  `gorm:"column:name"`
		GroupName string  `gorm:"column:group_name"`
		Balance   float64 `gorm:"column:balance"`
	}
	var infos []chInfo
	// channels 表的 group 用主库的 GroupColumnRef；alias 为 group_name 避免再次撞保留字。
	chGC := model.GroupColumnRef()
	if err := model.DB.WithContext(ctx).Raw(`
		SELECT id, name, `+chGC+` AS group_name, balance FROM channels WHERE id IN ?`,
		ids).Scan(&infos).Error; err != nil {
		return nil, err
	}
	infoMap := make(map[int]chInfo, len(infos))
	for _, c := range infos {
		infoMap[c.Id] = c
	}
	items := make([]TopChannelItem, 0, len(rows))
	for _, r := range rows {
		info := infoMap[r.ChannelId]
		items = append(items, TopChannelItem{
			ChannelId:    r.ChannelId,
			ChannelName:  info.Name,
			Group:        info.GroupName,
			Rpm:          float64(r.Succ) / windowMin,
			Cost:         r.Cost,
			SuccessCount: r.Succ,
			ErrorCount:   r.Err,
			Balance:      info.Balance,
		})
	}
	return items, nil
}

func loadTopChannelsByBalance(ctx context.Context) ([]TopChannelItem, error) {
	type row struct {
		Id        int     `gorm:"column:id"`
		Name      string  `gorm:"column:name"`
		GroupName string  `gorm:"column:group_name"`
		Balance   float64 `gorm:"column:balance"`
	}
	var rows []row
	chGC := model.GroupColumnRef()
	if err := model.DB.WithContext(ctx).Raw(`
		SELECT id, name, `+chGC+` AS group_name, balance FROM channels
		WHERE status = 1 AND balance > 0
		ORDER BY balance DESC LIMIT ?`, runtimeMetricsTopN).Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]TopChannelItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, TopChannelItem{
			ChannelId:   r.Id,
			ChannelName: r.Name,
			Group:       r.GroupName,
			Balance:     r.Balance,
		})
	}
	return items, nil
}

func loadTopGroupChannels(ctx context.Context, from int64, windowMin float64) ([]TopGroupChannelItem, error) {
	type row struct {
		GroupName string `gorm:"column:group_name"`
		ChannelId int    `gorm:"column:channel_id"`
		Succ      int64  `gorm:"column:succ"`
		Cost      int64  `gorm:"column:cost"`
	}
	var rows []row
	gc := model.LogGroupColumnRef()
	if err := model.LOG_DB.WithContext(ctx).Raw(`
		SELECT `+gc+` AS group_name, channel_id,
		       SUM(CASE WHEN type = 2 THEN 1 ELSE 0 END) AS succ, SUM(quota) AS cost
		FROM logs
		WHERE created_at >= ? AND `+gc+` != '' AND channel_id IS NOT NULL AND type IN (2, 5)
		GROUP BY `+gc+`, channel_id
		ORDER BY succ DESC
		LIMIT 30`, from).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []TopGroupChannelItem{}, nil
	}
	// 拉 channel name
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ChannelId)
	}
	type chName struct {
		Id   int    `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	var names []chName
	if err := model.DB.WithContext(ctx).Raw(`
		SELECT id, name FROM channels WHERE id IN ?`, ids).Scan(&names).Error; err != nil {
		return nil, err
	}
	nameMap := make(map[int]string, len(names))
	for _, n := range names {
		nameMap[n.Id] = n.Name
	}
	items := make([]TopGroupChannelItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, TopGroupChannelItem{
			Group:       r.GroupName,
			ChannelId:   r.ChannelId,
			ChannelName: nameMap[r.ChannelId],
			Rpm:         float64(r.Succ) / windowMin,
			Cost:        r.Cost,
		})
	}
	return items, nil
}

// ──────────────────────────────────────────────────────────────────────────
// 排序辅助（限定 TopN）
// ──────────────────────────────────────────────────────────────────────────

func sortTopUsersByRpm(items []TopUserItem) []TopUserItem {
	dup := append([]TopUserItem(nil), items...)
	sort.SliceStable(dup, func(i, j int) bool { return dup[i].Rpm > dup[j].Rpm })
	return topN(dup, runtimeMetricsTopN)
}
func sortTopUsersByCost(items []TopUserItem) []TopUserItem {
	dup := append([]TopUserItem(nil), items...)
	sort.SliceStable(dup, func(i, j int) bool { return dup[i].Cost > dup[j].Cost })
	return topN(dup, runtimeMetricsTopN)
}

func sortTopGroupsByRpm(items []TopGroupItem) []TopGroupItem {
	dup := append([]TopGroupItem(nil), items...)
	sort.SliceStable(dup, func(i, j int) bool { return dup[i].Rpm > dup[j].Rpm })
	return topNGroup(dup, runtimeMetricsTopN)
}
func sortTopGroupsByCost(items []TopGroupItem) []TopGroupItem {
	dup := append([]TopGroupItem(nil), items...)
	sort.SliceStable(dup, func(i, j int) bool { return dup[i].Cost > dup[j].Cost })
	return topNGroup(dup, runtimeMetricsTopN)
}

func sortTopChannelsByRpm(items []TopChannelItem) []TopChannelItem {
	dup := append([]TopChannelItem(nil), items...)
	sort.SliceStable(dup, func(i, j int) bool { return dup[i].Rpm > dup[j].Rpm })
	return topNCh(dup, runtimeMetricsTopN)
}
func sortTopChannelsByCost(items []TopChannelItem) []TopChannelItem {
	dup := append([]TopChannelItem(nil), items...)
	sort.SliceStable(dup, func(i, j int) bool { return dup[i].Cost > dup[j].Cost })
	return topNCh(dup, runtimeMetricsTopN)
}

func sortTopGroupChannelsByRpm(items []TopGroupChannelItem) []TopGroupChannelItem {
	dup := append([]TopGroupChannelItem(nil), items...)
	sort.SliceStable(dup, func(i, j int) bool { return dup[i].Rpm > dup[j].Rpm })
	return topNGC(dup, runtimeMetricsTopN)
}
func sortTopGroupChannelsByCost(items []TopGroupChannelItem) []TopGroupChannelItem {
	dup := append([]TopGroupChannelItem(nil), items...)
	sort.SliceStable(dup, func(i, j int) bool { return dup[i].Cost > dup[j].Cost })
	return topNGC(dup, runtimeMetricsTopN)
}

// Go 没泛型 slice 截断 helper（避免引入 generics 复杂度），手写 4 个
func topN(items []TopUserItem, n int) []TopUserItem {
	if len(items) > n {
		return items[:n]
	}
	return items
}
func topNGroup(items []TopGroupItem, n int) []TopGroupItem {
	if len(items) > n {
		return items[:n]
	}
	return items
}
func topNCh(items []TopChannelItem, n int) []TopChannelItem {
	if len(items) > n {
		return items[:n]
	}
	return items
}
func topNGC(items []TopGroupChannelItem, n int) []TopGroupChannelItem {
	if len(items) > n {
		return items[:n]
	}
	return items
}
