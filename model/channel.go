package model

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"

	"github.com/samber/lo"
	"gorm.io/gorm"
)

type Channel struct {
	Id                 int     `json:"id"`
	Type               int     `json:"type" gorm:"default:0"`
	Key                string  `json:"key" gorm:"not null"`
	OpenAIOrganization *string `json:"openai_organization"`
	TestModel          *string `json:"test_model"`
	Status             int     `json:"status" gorm:"default:1"`
	Name               string  `json:"name" gorm:"index"`
	Weight             *uint   `json:"weight" gorm:"default:0"`
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	TestTime           int64   `json:"test_time" gorm:"bigint"`
	ResponseTime       int     `json:"response_time"` // in milliseconds
	BaseURL            *string `json:"base_url" gorm:"column:base_url;default:''"`
	Other              string  `json:"other"`
	Balance            float64 `json:"balance"` // in USD
	BalanceUpdatedTime int64   `json:"balance_updated_time" gorm:"bigint"`
	Models             string  `json:"models"`
	Group              string  `json:"group" gorm:"type:varchar(255);default:'default'"`
	UsedQuota          int64   `json:"used_quota" gorm:"bigint;default:0"`
	ModelMapping       *string `json:"model_mapping" gorm:"type:text"`
	//MaxInputTokens     *int    `json:"max_input_tokens" gorm:"default:0"`
	StatusCodeMapping *string `json:"status_code_mapping" gorm:"type:varchar(1024);default:''"`
	Priority          *int64  `json:"priority" gorm:"bigint;default:0"`
	AutoBan           *int    `json:"auto_ban" gorm:"default:1"`
	OtherInfo         string  `json:"other_info"`
	Tag               *string `json:"tag" gorm:"index"`
	Setting           *string `json:"setting" gorm:"type:text"` // 渠道额外设置
	ParamOverride     *string `json:"param_override" gorm:"type:text"`
	HeaderOverride    *string `json:"header_override" gorm:"type:text"`
	Remark            *string `json:"remark" gorm:"type:varchar(255)" validate:"max=255"`
	// add after v0.8.5
	ChannelInfo ChannelInfo `json:"channel_info" gorm:"type:json"`

	OtherSettings string `json:"settings" gorm:"column:settings"` // 其他设置，存储azure版本等不需要检索的信息，详见dto.ChannelOtherSettings

	// 渠道运行质量快照（被动机制，docs/2026-05-12-channel-quality-rpm-list-plan.md）
	// 由 service.ChannelMetricsTask 每 5min 重算一次，基于 logs 表 24h 滚动窗口。
	// GORM column 显式指定，避免 Go 字段名 snake_case 转换与 service UPDATE 字段名不一致。
	RpmLast24h       float64 `json:"rpm_24h" gorm:"column:rpm_24h;type:double precision;not null;default:0"`
	PeakRpm          float64 `json:"peak_rpm" gorm:"column:peak_rpm;type:double precision;not null;default:0"` // 历史峰值 RPM（lifetime since channel created）；由 service.ChannelPeakRpmTask 每 60s 比较实时窗口 RPM 并更新。
	PeakRpmAt        int64   `json:"peak_rpm_at" gorm:"column:peak_rpm_at;type:bigint;not null;default:0"`
	QualityScore     *int    `json:"quality_score" gorm:"column:quality_score;default:null"` // 0-100；NULL=无流量
	QualityUpdatedAt int64   `json:"quality_updated_at" gorm:"column:quality_updated_at;type:bigint;not null;default:0"`
	QualityDetail    string  `json:"quality_detail" gorm:"column:quality_detail;type:varchar(512);not null;default:''"` // 评分原始指标 JSON 快照（service.buildQualityDetail），列表 hover 展示用；空=未算过

	// 外部测评分数快照（由管理员主动触发 /api/channel/:id/verify，调用测评网关 /api/verify/claude）
	// 与 QualityScore 区分：这是第三方测评，不是基于自身流量的统计。
	// 详细历史报告见 channel_verify_reports 表；这里只冗余"最近一次"用于列表展示。
	VerifyScore    *int   `json:"verify_score" gorm:"column:verify_score;default:null;index"` // 0-100；NULL=未测过
	VerifyGrade    string `json:"verify_grade" gorm:"column:verify_grade;type:varchar(8);default:''"`
	VerifyTestedAt int64  `json:"verify_tested_at" gorm:"column:verify_tested_at;type:bigint;not null;default:0"`
	VerifyReportId int64  `json:"verify_report_id" gorm:"column:verify_report_id;type:bigint;not null;default:0"`

	// 自动定时测评 + 阈值启停 + 隔离标记（docs/2026-06-18-channel-verify-auto-schedule-plan.md）
	VerifyPrevScore        *int `json:"verify_prev_score" gorm:"column:verify_prev_score;default:null"`    // 上次成功分数：着色 + 告警对比
	VerifyIntervalMinutes  *int `json:"verify_interval_minutes" gorm:"column:verify_interval_minutes"`     // NULL=继承全局 / 0=关闭 / >0=分钟
	VerifyAutoDisableBelow *int `json:"verify_auto_disable_below" gorm:"column:verify_auto_disable_below"` // 评分<此值自动禁用；NULL=不启用
	VerifyAutoEnableAbove  *int `json:"verify_auto_enable_above" gorm:"column:verify_auto_enable_above"`   // 评分>=此值自动启用；NULL=不启用
	VerifyDisabled         *int `json:"verify_disabled" gorm:"column:verify_disabled;default:0"`           // 1=当前被 verify 低分禁用（与健康度禁用隔离）

	// 上游分组倍率监控快照（docs/2026-08-05-upstream-group-ratio-monitor.md）
	// 明细见 channel_group_ratio_baselines / channel_group_ratio_changes 两表；这里只冗余"最近一批次"用于列表角标。
	// GORM column 显式指定，避免 Go 字段名 snake_case 转换与 service UPDATE 字段名不一致。
	RatioPanelUrl     *string `json:"ratio_panel_url" gorm:"column:ratio_panel_url;type:varchar(255);default:null"`               // 面板域名，空则回落 base_url（ai-wave 的 relay 域名 /api/* 全 404）
	RatioUpstreamKind string  `json:"ratio_upstream_kind" gorm:"column:ratio_upstream_kind;type:varchar(16);not null;default:''"` // newapi|sub2api|donehub|unsupported；缓存探测结果避免每轮试三次
	RatioFetchedAt    int64   `json:"ratio_fetched_at" gorm:"column:ratio_fetched_at;type:bigint;not null;default:0"`
	RatioFetchStatus  string  `json:"ratio_fetch_status" gorm:"column:ratio_fetch_status;type:varchar(16);not null;default:''"` // ok | unsupported | error；区分"上游没涨"与"压根抓不到"
	RatioFetchMsg     string  `json:"ratio_fetch_msg" gorm:"column:ratio_fetch_msg;type:varchar(255);not null;default:''"`
	RatioUpCount      int     `json:"ratio_up_count" gorm:"column:ratio_up_count;not null;default:0"` // 最近一批次涨的分组数
	RatioDownCount    int     `json:"ratio_down_count" gorm:"column:ratio_down_count;not null;default:0"`
	RatioChangedAt    int64   `json:"ratio_changed_at" gorm:"column:ratio_changed_at;type:bigint;not null;default:0"`
	// 当前分组倍率摘要 JSON {"n":分组数,"min":最低,"max":最高,"g":分组名}，供列表直接展示"此刻的倍率"，免去逐行查基线表。
	RatioDetail string `json:"ratio_detail" gorm:"column:ratio_detail;type:varchar(512);not null;default:''"`
	// 本渠道 key 在上游所属的分组名。标准 new-api 不把 token 所属分组回显给 key 持有者，
	// 故：管理员显式指定 > 按模型集合自动反推（唯一命中才采纳）> 都没有则退回展示全表区间。
	RatioUpstreamGroup *string `json:"ratio_upstream_group" gorm:"column:ratio_upstream_group;type:varchar(191);default:null"` // 人工指定，权威
	RatioResolvedGroup string  `json:"ratio_resolved_group" gorm:"column:ratio_resolved_group;type:varchar(191);not null;default:''"` // 实际采用的分组名（人工或自动反推）

	// 实付倍率反推（docs 第十一节）：/api/pricing 的 group_ratio 只反映"抓取者身份"的价格，
	// 匿名拿到的是挂牌价而非我们谈下的价。用 /dashboard/billing/usage（认 sk- key）的增量
	// 除以同窗口日志的基准 quota，反推出真实实付倍率。
	RatioUsageSnapshot float64  `json:"ratio_usage_snapshot" gorm:"column:ratio_usage_snapshot;type:double precision;not null;default:0"` // 上轮 total_usage（USD 分）
	RatioUsageAt       int64    `json:"ratio_usage_at" gorm:"column:ratio_usage_at;type:bigint;not null;default:0"`
	RatioEffective     *float64 `json:"ratio_effective" gorm:"column:ratio_effective;type:double precision;default:null"` // 反推实付倍率；NULL=窗口内无流量或无法计算
	RatioEffectiveAt   int64    `json:"ratio_effective_at" gorm:"column:ratio_effective_at;type:bigint;not null;default:0"`
	// 人工填写的采购倍率基准；与抓取值/反推值偏差超阈值即告警，用于抓"上游偷偷取消专属折扣"
	RatioExpected *float64 `json:"ratio_expected" gorm:"column:ratio_expected;type:double precision;default:null"`

	// 健康度自动降级 / 升级（被动机制，详见 docs/2026-05-04-channel-health-auto-degrade-plan.md）
	DegradeLevel      *int    `json:"degrade_level" gorm:"default:0"`            // 0=Healthy, 1=L1, 2=L2
	OriginalPriority  *int64  `json:"original_priority" gorm:"bigint;default:0"` // 首次降级前 priority 快照
	OriginalWeight    *uint   `json:"original_weight" gorm:"default:0"`          // 首次降级前 weight 快照
	LastDemoteAt      *int64  `json:"last_demote_at" gorm:"bigint;default:0"`    // 上次降级时间，unix sec
	LastUpgradeAt     *int64  `json:"last_upgrade_at" gorm:"bigint;default:0"`   // 上次升级时间，unix sec
	LastDemoteReason  *string `json:"last_demote_reason" gorm:"type:text"`       // 上次降级原因
	LastDisabledAt    *int64  `json:"last_disabled_at" gorm:"bigint;default:0"`  // 上次自动禁用时间（反弹检测用）
	PermanentDisabled *int    `json:"permanent_disabled" gorm:"default:0"`       // 1=反弹达阈值锁死，恢复探活跳过
	RebounceCount     *int    `json:"rebounce_count" gorm:"default:0"`           // 24h 滚动窗口内 disable→enable→disable 次数

	// 路由模式可切换（docs/2026-05-26-channel-routing-mode-switchable.md）
	// 0=inherit（默认，跟随全局/分组），1=probabilistic（强制概率），2=capacity（强制容量）
	RoutingMode       *int `json:"routing_mode" gorm:"default:0"`
	CapacityLimit     *int `json:"capacity_limit" gorm:"default:null"`      // NULL=复用 weight
	CapacityWindowSec *int `json:"capacity_window_sec" gorm:"default:null"` // NULL=继承全局
	// 渠道级全满策略（下放全局 full_strategy）：空/NULL=继承全局，
	// 软档 fallback/degraded（满了不拒，挤/降级），硬档 reject/queue（满了拒/排队）。
	CapacityFullStrategy *string `json:"capacity_full_strategy" gorm:"type:varchar(16);default:null"`

	// 渠道级重试策略（docs/2026-06-07-claude-retry-cache-loss.md）
	// 0=inherit（默认，跟随全局/当前跨渠道行为），1=cost_guard（失败不跨渠道，交客户端重试），
	// 2=same_domain（仅在同 cache_domain 内换渠道重试），3=cross_channel（显式允许任意渠道）
	RetryStrategy *int    `json:"retry_strategy" gorm:"default:0"`
	CacheDomain   *string `json:"cache_domain" gorm:"type:varchar(64);default:''"` // 缓存域=上游账号/组织标识；空=按渠道自身 id

	// cache info
	Keys []string `json:"-" gorm:"-"`
}

type ChannelInfo struct {
	IsMultiKey             bool                  `json:"is_multi_key"`                        // 是否多Key模式
	MultiKeySize           int                   `json:"multi_key_size"`                      // 多Key模式下的Key数量
	MultiKeyStatusList     map[int]int           `json:"multi_key_status_list"`               // key状态列表，key index -> status
	MultiKeyDisabledReason map[int]string        `json:"multi_key_disabled_reason,omitempty"` // key禁用原因列表，key index -> reason
	MultiKeyDisabledTime   map[int]int64         `json:"multi_key_disabled_time,omitempty"`   // key禁用时间列表，key index -> time
	MultiKeyPollingIndex   int                   `json:"multi_key_polling_index"`             // 多Key模式下轮询的key索引
	MultiKeyMode           constant.MultiKeyMode `json:"multi_key_mode"`
}

// Value implements driver.Valuer interface
func (c ChannelInfo) Value() (driver.Value, error) {
	return common.Marshal(&c)
}

// Scan implements sql.Scanner interface
func (c *ChannelInfo) Scan(value interface{}) error {
	bytesValue, _ := value.([]byte)
	return common.Unmarshal(bytesValue, c)
}

func (channel *Channel) GetKeys() []string {
	if channel.Key == "" {
		return []string{}
	}
	if len(channel.Keys) > 0 {
		return channel.Keys
	}
	trimmed := strings.TrimSpace(channel.Key)
	// If the key starts with '[', try to parse it as a JSON array (e.g., for Vertex AI scenarios)
	if strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
			res := make([]string, len(arr))
			for i, v := range arr {
				res[i] = string(v)
			}
			return res
		}
	}
	// Otherwise, fall back to splitting by newline
	keys := strings.Split(strings.Trim(channel.Key, "\n"), "\n")
	return keys
}

func (channel *Channel) GetNextEnabledKey() (string, int, *types.NewAPIError) {
	// If not in multi-key mode, return the original key string directly.
	if !channel.ChannelInfo.IsMultiKey {
		return channel.Key, 0, nil
	}

	// Obtain all keys (split by \n)
	keys := channel.GetKeys()
	if len(keys) == 0 {
		// No keys available, return error, should disable the channel
		return "", 0, types.NewError(errors.New("no keys available"), types.ErrorCodeChannelNoAvailableKey)
	}

	lock := GetChannelPollingLock(channel.Id)
	lock.Lock()
	defer lock.Unlock()

	statusList := channel.ChannelInfo.MultiKeyStatusList
	// helper to get key status, default to enabled when missing
	getStatus := func(idx int) int {
		if statusList == nil {
			return common.ChannelStatusEnabled
		}
		if status, ok := statusList[idx]; ok {
			return status
		}
		return common.ChannelStatusEnabled
	}

	// Collect indexes of enabled keys
	enabledIdx := make([]int, 0, len(keys))
	for i := range keys {
		if getStatus(i) == common.ChannelStatusEnabled {
			enabledIdx = append(enabledIdx, i)
		}
	}
	// If no specific status list or none enabled, return an explicit error so caller can
	// properly handle a channel with no available keys (e.g. mark channel disabled).
	// Returning the first key here caused requests to keep using an already-disabled key.
	if len(enabledIdx) == 0 {
		return "", 0, types.NewError(errors.New("no enabled keys"), types.ErrorCodeChannelNoAvailableKey)
	}

	switch channel.ChannelInfo.MultiKeyMode {
	case constant.MultiKeyModeRandom:
		// Randomly pick one enabled key
		selectedIdx := enabledIdx[rand.Intn(len(enabledIdx))]
		return keys[selectedIdx], selectedIdx, nil
	case constant.MultiKeyModePolling:
		// Use channel-specific lock to ensure thread-safe polling

		channelInfo, err := CacheGetChannelInfo(channel.Id)
		if err != nil {
			return "", 0, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
		}
		//println("before polling index:", channel.ChannelInfo.MultiKeyPollingIndex)
		defer func() {
			if common.DebugEnabled {
				println(fmt.Sprintf("channel %d polling index: %d", channel.Id, channel.ChannelInfo.MultiKeyPollingIndex))
			}
			if !common.MemoryCacheEnabled {
				_ = channel.SaveChannelInfo()
			} else {
				// CacheUpdateChannel(channel)
			}
		}()
		// Start from the saved polling index and look for the next enabled key
		start := channelInfo.MultiKeyPollingIndex
		if start < 0 || start >= len(keys) {
			start = 0
		}
		for i := 0; i < len(keys); i++ {
			idx := (start + i) % len(keys)
			if getStatus(idx) == common.ChannelStatusEnabled {
				// update polling index for next call (point to the next position)
				channel.ChannelInfo.MultiKeyPollingIndex = (idx + 1) % len(keys)
				return keys[idx], idx, nil
			}
		}
		// Fallback – should not happen, but return first enabled key
		return keys[enabledIdx[0]], enabledIdx[0], nil
	default:
		// Unknown mode, default to first enabled key (or original key string)
		return keys[enabledIdx[0]], enabledIdx[0], nil
	}
}

func (channel *Channel) SaveChannelInfo() error {
	return DB.Model(channel).Update("channel_info", channel.ChannelInfo).Error
}

func (channel *Channel) GetModels() []string {
	if channel.Models == "" {
		return []string{}
	}
	return strings.Split(strings.Trim(channel.Models, ","), ",")
}

func (channel *Channel) GetGroups() []string {
	if channel.Group == "" {
		return []string{}
	}
	groups := strings.Split(strings.Trim(channel.Group, ","), ",")
	for i, group := range groups {
		groups[i] = strings.TrimSpace(group)
	}
	return groups
}

func (channel *Channel) GetOtherInfo() map[string]interface{} {
	otherInfo := make(map[string]interface{})
	if channel.OtherInfo != "" {
		err := common.Unmarshal([]byte(channel.OtherInfo), &otherInfo)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal other info: channel_id=%d, tag=%s, name=%s, error=%v", channel.Id, channel.GetTag(), channel.Name, err))
		}
	}
	return otherInfo
}

func (channel *Channel) SetOtherInfo(otherInfo map[string]interface{}) {
	otherInfoBytes, err := json.Marshal(otherInfo)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal other info: channel_id=%d, tag=%s, name=%s, error=%v", channel.Id, channel.GetTag(), channel.Name, err))
		return
	}
	channel.OtherInfo = string(otherInfoBytes)
}

func (channel *Channel) GetTag() string {
	if channel.Tag == nil {
		return ""
	}
	return *channel.Tag
}

func (channel *Channel) SetTag(tag string) {
	channel.Tag = &tag
}

func (channel *Channel) GetAutoBan() bool {
	if channel.AutoBan == nil {
		return false
	}
	return *channel.AutoBan == 1
}

func (channel *Channel) Save() error {
	return DB.Save(channel).Error
}

func (channel *Channel) SaveWithoutKey() error {
	if channel.Id == 0 {
		return errors.New("channel ID is 0")
	}
	return DB.Omit("key").Save(channel).Error
}

func GetAllChannels(startIdx int, num int, selectAll bool, idSort bool) ([]*Channel, error) {
	var channels []*Channel
	var err error
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	if selectAll {
		err = DB.Order(order).Find(&channels).Error
	} else {
		err = DB.Order(order).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	}
	return channels, err
}

// GetAutoDisabledChannelsForRecovery 拉取所有处于"自动禁用 + 未永久锁死"状态的渠道，
// 供恢复探活路径使用（被动模式下，§3.4.4）。
//
// 包含 key 字段（探活时需要拿 key 发请求）。
func GetAutoDisabledChannelsForRecovery() ([]*Channel, error) {
	var channels []*Channel
	err := DB.
		Where("status = ?", common.ChannelStatusAutoDisabled).
		Where("permanent_disabled = ? OR permanent_disabled IS NULL", 0).
		Where("verify_disabled = ? OR verify_disabled IS NULL", 0). // 隔离：健康度恢复探活不碰 verify 禁用的渠道
		Order("priority desc").
		Find(&channels).Error
	return channels, err
}

// GetDegradedChannelsForProbe 拉取降级等级 >= minLevel 且仍 enabled 的渠道，
// 供降级探测恢复路径使用（解决高降级渠道无流量→无法升级的死锁）。
func GetDegradedChannelsForProbe(minLevel int) ([]*Channel, error) {
	var channels []*Channel
	err := DB.
		Where("status = ?", common.ChannelStatusEnabled).
		Where("degrade_level >= ?", minLevel).
		Order("degrade_level desc").
		Find(&channels).Error
	return channels, err
}

func GetChannelsByTag(tag string, idSort bool, selectAll bool) ([]*Channel, error) {
	var channels []*Channel
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	query := DB.Where("tag = ?", tag).Order(order)
	if !selectAll {
		query = query.Omit("key")
	}
	err := query.Find(&channels).Error
	return channels, err
}

func SearchChannels(keyword string, group string, model string, idSort bool) ([]*Channel, error) {
	var channels []*Channel
	modelsCol := "`models`"

	// 如果是 PostgreSQL，使用双引号
	if common.UsingPostgreSQL {
		modelsCol = `"models"`
	}

	baseURLCol := "`base_url`"
	// 如果是 PostgreSQL，使用双引号
	if common.UsingPostgreSQL {
		baseURLCol = `"base_url"`
	}

	order := "priority desc"
	if idSort {
		order = "id desc"
	}

	// 构造基础查询
	baseQuery := DB.Model(&Channel{}).Omit("key")

	// 构造WHERE子句
	var whereClause string
	var args []interface{}
	if group != "" && group != "null" {
		var groupCondition string
		if common.UsingMySQL {
			groupCondition = `CONCAT(',', ` + commonGroupCol + `, ',') LIKE ?`
		} else {
			// sqlite, PostgreSQL
			groupCondition = `(',' || ` + commonGroupCol + ` || ',') LIKE ?`
		}
		whereClause = "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + ` LIKE ? AND ` + groupCondition
		args = append(args, common.String2Int(keyword), "%"+keyword+"%", keyword, "%"+keyword+"%", "%"+model+"%", "%,"+group+",%")
	} else {
		whereClause = "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
		args = append(args, common.String2Int(keyword), "%"+keyword+"%", keyword, "%"+keyword+"%", "%"+model+"%")
	}

	// 执行查询
	err := baseQuery.Where(whereClause, args...).Order(order).Find(&channels).Error
	if err != nil {
		return nil, err
	}
	return channels, nil
}

func GetChannelById(id int, selectAll bool) (*Channel, error) {
	channel := &Channel{Id: id}
	var err error = nil
	if selectAll {
		err = DB.First(channel, "id = ?", id).Error
	} else {
		err = DB.Omit("key").First(channel, "id = ?", id).Error
	}
	if err != nil {
		return nil, err
	}
	return channel, nil
}

func BatchInsertChannels(channels []Channel) error {
	if len(channels) == 0 {
		return nil
	}
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	for _, chunk := range lo.Chunk(channels, 50) {
		if err := tx.Create(&chunk).Error; err != nil {
			tx.Rollback()
			return err
		}
		for _, channel_ := range chunk {
			if err := channel_.AddAbilities(tx); err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	return tx.Commit().Error
}

func BatchDeleteChannels(ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	scopes := make([]channelDeleteScope, 0)
	for _, chunk := range batchDeleteIDChunks(uniqueBatchDeleteIDs(ids)) {
		chunkIds := chunk
		scopes = append(scopes, func(tx *gorm.DB) *gorm.DB {
			return tx.Where("id IN (?)", chunkIds)
		})
	}
	_, err := deleteChannelsWithLedger(scopes, false)
	return err
}

func (channel *Channel) GetPriority() int64 {
	if channel.Priority == nil {
		return 0
	}
	return *channel.Priority
}

func (channel *Channel) GetWeight() int {
	if channel.Weight == nil {
		return 0
	}
	return int(*channel.Weight)
}

// GetRoutingMode 返回路由模式（0=inherit, 1=probabilistic, 2=capacity）
func (channel *Channel) GetRoutingMode() int {
	if channel.RoutingMode == nil {
		return 0
	}
	return *channel.RoutingMode
}

// 渠道级重试策略常量
const (
	RetryStrategyInherit      = 0 // 跟随全局：当前跨渠道行为
	RetryStrategyCostGuard    = 1 // 成本保护：失败不跨渠道
	RetryStrategySameDomain   = 2 // 同缓存域：仅在同 cache_domain 内换渠道
	RetryStrategyCrossChannel = 3 // 跨渠道：显式允许任意渠道
)

// GetRetryStrategy 返回渠道重试策略（0=inherit/1=cost_guard/2=same_domain/3=cross_channel）
func (channel *Channel) GetRetryStrategy() int {
	if channel == nil || channel.RetryStrategy == nil {
		return RetryStrategyInherit
	}
	return *channel.RetryStrategy
}

// GetCacheDomain 返回缓存域；空时回退到渠道自身 id（最保守：同缓存域退化为仅同渠道）
func (channel *Channel) GetCacheDomain() string {
	if channel == nil {
		return ""
	}
	if channel.CacheDomain != nil {
		if domain := strings.TrimSpace(*channel.CacheDomain); domain != "" {
			return domain
		}
	}
	return "ch:" + strconv.Itoa(channel.Id)
}

// GetCapacityLimit 返回容量上限；NULL 时复用 weight
func (channel *Channel) GetCapacityLimit() int {
	if channel.CapacityLimit == nil {
		return channel.GetWeight()
	}
	return *channel.CapacityLimit
}

// GetCapacityWindowSec 返回滑动窗口秒数；NULL 时返回 0 表示继承全局
func (channel *Channel) GetCapacityWindowSec() int {
	if channel.CapacityWindowSec == nil {
		return 0
	}
	return *channel.CapacityWindowSec
}

// GetCapacityFullStrategy 返回渠道级全满策略；空/NULL 表示继承全局
func (channel *Channel) GetCapacityFullStrategy() string {
	if channel.CapacityFullStrategy == nil {
		return ""
	}
	return *channel.CapacityFullStrategy
}

func (channel *Channel) GetBaseURL() string {
	if channel.BaseURL == nil {
		return ""
	}
	url := *channel.BaseURL
	if url == "" {
		url = constant.ChannelBaseURLs[channel.Type]
	}
	return url
}

func (channel *Channel) GetModelMapping() string {
	if channel.ModelMapping == nil {
		return ""
	}
	return *channel.ModelMapping
}

func (channel *Channel) GetStatusCodeMapping() string {
	if channel.StatusCodeMapping == nil {
		return ""
	}
	return *channel.StatusCodeMapping
}

func (channel *Channel) Insert() error {
	var err error
	err = DB.Create(channel).Error
	if err != nil {
		return err
	}
	err = channel.AddAbilities(nil)
	return err
}

func (channel *Channel) Update() error {
	// If this is a multi-key channel, recalculate MultiKeySize based on the current key list to avoid inconsistency after editing keys
	if channel.ChannelInfo.IsMultiKey {
		var keyStr string
		if channel.Key != "" {
			keyStr = channel.Key
		} else {
			// If key is not provided, read the existing key from the database
			if existing, err := GetChannelById(channel.Id, true); err == nil {
				keyStr = existing.Key
			}
		}
		// Parse the key list (supports newline separation or JSON array)
		keys := []string{}
		if keyStr != "" {
			trimmed := strings.TrimSpace(keyStr)
			if strings.HasPrefix(trimmed, "[") {
				var arr []json.RawMessage
				if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
					keys = make([]string, len(arr))
					for i, v := range arr {
						keys[i] = string(v)
					}
				}
			}
			if len(keys) == 0 { // fallback to newline split
				keys = strings.Split(strings.Trim(keyStr, "\n"), "\n")
			}
		}
		channel.ChannelInfo.MultiKeySize = len(keys)
		// Clean up status data that exceeds the new key count to prevent index out of range
		if channel.ChannelInfo.MultiKeyStatusList != nil {
			for idx := range channel.ChannelInfo.MultiKeyStatusList {
				if idx >= channel.ChannelInfo.MultiKeySize {
					delete(channel.ChannelInfo.MultiKeyStatusList, idx)
				}
			}
		}
	}
	var err error
	err = DB.Model(channel).Updates(channel).Error
	if err != nil {
		return err
	}
	DB.Model(channel).First(channel, "id = ?", channel.Id)
	err = channel.UpdateAbilities(nil)
	return err
}

func (channel *Channel) UpdateResponseTime(responseTime int64) {
	err := DB.Model(channel).Select("response_time", "test_time").Updates(Channel{
		TestTime:     common.GetTimestamp(),
		ResponseTime: int(responseTime),
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update response time: channel_id=%d, error=%v", channel.Id, err))
	}
}

func (channel *Channel) UpdateBalance(balance float64) {
	err := DB.Model(channel).Select("balance_updated_time", "balance").Updates(Channel{
		BalanceUpdatedTime: common.GetTimestamp(),
		Balance:            balance,
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update balance: channel_id=%d, error=%v", channel.Id, err))
	}
}

// UpdateChannelHealthFields 用 map 形式精确更新 channel 健康度相关列。
// fields 的 key 必须是 GORM column 名（snake_case），由调用方负责拼装。
// 走 map 而非 struct 是因为需要"显式写入零值"（例如把 original_priority 重置回 0、清 reason），
// 而 Updates(struct) 会对零值自动 skip（Rule 6 / Channel 自身字段全是指针就是为了规避这点）。
//
// 仅由 service/channel_health.go 在状态机里调用；这里不做业务判断，只做 SQL 透传。
func UpdateChannelHealthFields(channelId int, fields map[string]interface{}) error {
	if channelId <= 0 || len(fields) == 0 {
		return nil
	}
	return DB.Model(&Channel{}).Where("id = ?", channelId).Updates(fields).Error
}

func (channel *Channel) Delete() error {
	if channel.Id == 0 {
		return errors.New("id 为空！")
	}
	_, err := deleteChannelsWithLedger(
		[]channelDeleteScope{func(tx *gorm.DB) *gorm.DB {
			return tx.Where("id = ?", channel.Id)
		}},
		true,
	)
	return err
}

type channelDeleteScope func(*gorm.DB) *gorm.DB

func deleteChannelsWithLedger(scopes []channelDeleteScope, requireOne bool) ([]int, error) {
	var channelIds []int
	err := DB.Transaction(func(tx *gorm.DB) error {
		for _, scope := range scopes {
			var channels []Channel
			if err := scope(batchDeleteRowLock(tx).Model(&Channel{})).
				Select("id").
				Order("id").
				Find(&channels).Error; err != nil {
				return err
			}
			for _, selected := range channels {
				channelIds = append(channelIds, selected.Id)
			}
		}
		if requireOne && len(channelIds) == 0 {
			return gorm.ErrRecordNotFound
		}
		if requireOne && len(channelIds) != 1 {
			return fmt.Errorf("delete channel selected %d rows, want 1", len(channelIds))
		}
		if len(channelIds) == 0 {
			return nil
		}

		for _, channelId := range channelIds {
			if err := createBatchUpdateDeleteLedgers(
				tx,
				channelId,
				BatchUpdateTypeChannelUsedQuota,
			); err != nil {
				return err
			}
		}
		if err := deleteChannelAbilities(tx, channelIds); err != nil {
			return err
		}
		if err := deleteChannelGroupRatios(tx, channelIds); err != nil {
			return err
		}
		for _, chunk := range batchDeleteIDChunks(channelIds) {
			if err := requireSelectedDeleteRows(
				"channels",
				len(chunk),
				tx.Where("id IN (?)", chunk).Delete(&Channel{}),
			); err != nil {
				return err
			}
		}
		return nil
	})
	return channelIds, err
}

var channelStatusLock sync.Mutex

// channelPollingLocks stores locks for each channel.id to ensure thread-safe polling
var channelPollingLocks sync.Map

// GetChannelPollingLock returns or creates a mutex for the given channel ID
func GetChannelPollingLock(channelId int) *sync.Mutex {
	if lock, exists := channelPollingLocks.Load(channelId); exists {
		return lock.(*sync.Mutex)
	}
	// Create new lock for this channel
	newLock := &sync.Mutex{}
	actual, _ := channelPollingLocks.LoadOrStore(channelId, newLock)
	return actual.(*sync.Mutex)
}

// CleanupChannelPollingLocks removes locks for channels that no longer exist
// This is optional and can be called periodically to prevent memory leaks
func CleanupChannelPollingLocks() {
	var activeChannelIds []int
	DB.Model(&Channel{}).Pluck("id", &activeChannelIds)

	activeChannelSet := make(map[int]bool)
	for _, id := range activeChannelIds {
		activeChannelSet[id] = true
	}

	channelPollingLocks.Range(func(key, value interface{}) bool {
		channelId := key.(int)
		if !activeChannelSet[channelId] {
			channelPollingLocks.Delete(channelId)
		}
		return true
	})
}

func hasEnabledMultiKey(keys []string, statusList map[int]int) bool {
	for idx := range keys {
		if statusList == nil || statusList[idx] == common.ChannelStatusEnabled || statusList[idx] == 0 {
			return true
		}
	}
	return false
}

func handlerMultiKeyUpdate(channel *Channel, usingKey string, status int, reason string) {
	keys := channel.GetKeys()
	if len(keys) == 0 {
		channel.Status = status
	} else {
		keyIndex := -1
		for i, key := range keys {
			if key == usingKey {
				keyIndex = i
				break
			}
		}
		if usingKey != "" && keyIndex < 0 {
			common.SysLog(fmt.Sprintf("failed to update multi-key channel status: channel_id=%d, key not found", channel.Id))
			return
		}
		if usingKey == "" {
			channel.Status = status
			return
		}
		if channel.ChannelInfo.MultiKeyStatusList == nil {
			channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
		}
		if status == common.ChannelStatusEnabled {
			delete(channel.ChannelInfo.MultiKeyStatusList, keyIndex)
			if channel.ChannelInfo.MultiKeyDisabledReason != nil {
				delete(channel.ChannelInfo.MultiKeyDisabledReason, keyIndex)
			}
			if channel.ChannelInfo.MultiKeyDisabledTime != nil {
				delete(channel.ChannelInfo.MultiKeyDisabledTime, keyIndex)
			}
		} else {
			channel.ChannelInfo.MultiKeyStatusList[keyIndex] = status
			if channel.ChannelInfo.MultiKeyDisabledReason == nil {
				channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
			}
			if channel.ChannelInfo.MultiKeyDisabledTime == nil {
				channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
			}
			channel.ChannelInfo.MultiKeyDisabledReason[keyIndex] = reason
			channel.ChannelInfo.MultiKeyDisabledTime[keyIndex] = common.GetTimestamp()
		}
		if !hasEnabledMultiKey(keys, channel.ChannelInfo.MultiKeyStatusList) {
			channel.Status = common.ChannelStatusAutoDisabled
			info := channel.GetOtherInfo()
			info["status_reason"] = "All keys are disabled"
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
		} else if status == common.ChannelStatusEnabled && channel.Status != common.ChannelStatusManuallyDisabled {
			channel.Status = common.ChannelStatusEnabled
		}
	}
}

func UpdateChannelStatus(channelId int, usingKey string, status int, reason string) bool {
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()

		channelCache, _ := CacheGetChannel(channelId)
		if channelCache == nil {
			return false
		}
		if channelCache.ChannelInfo.IsMultiKey {
			beforeStatus := channelCache.Status
			// Use per-channel lock to prevent concurrent map read/write with GetNextEnabledKey
			pollingLock := GetChannelPollingLock(channelId)
			pollingLock.Lock()
			// 如果是多Key模式，更新缓存中的状态
			handlerMultiKeyUpdate(channelCache, usingKey, status, reason)
			pollingLock.Unlock()
			if beforeStatus != channelCache.Status {
				CacheUpdateChannelStatus(channelId, channelCache.Status)
			}
			//CacheUpdateChannel(channelCache)
			//return true
		} else {
			// 如果缓存渠道存在，且状态已是目标状态，直接返回
			if channelCache.Status == status {
				return false
			}
			CacheUpdateChannelStatus(channelId, status)
		}
	}

	shouldUpdateAbilities := false
	defer func() {
		if shouldUpdateAbilities {
			err := UpdateAbilityStatus(channelId, status == common.ChannelStatusEnabled)
			if err != nil {
				common.SysLog(fmt.Sprintf("failed to update ability status: channel_id=%d, error=%v", channelId, err))
			}
		}
	}()
	channel, err := GetChannelById(channelId, true)
	if err != nil {
		return false
	} else {
		if channel.Status == status {
			return false
		}

		if channel.ChannelInfo.IsMultiKey {
			beforeStatus := channel.Status
			// Protect map writes with the same per-channel lock used by readers
			pollingLock := GetChannelPollingLock(channelId)
			pollingLock.Lock()
			handlerMultiKeyUpdate(channel, usingKey, status, reason)
			pollingLock.Unlock()
			if beforeStatus != channel.Status {
				shouldUpdateAbilities = true
			}
		} else {
			info := channel.GetOtherInfo()
			info["status_reason"] = reason
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
			channel.Status = status
			shouldUpdateAbilities = true
		}
		// 任意启用（单/多 key 两条分支均经此）→ 清 verify 隔离标记，覆盖手动 UI / 健康度恢复 / verify 自启
		if channel.Status == common.ChannelStatusEnabled {
			zero := 0
			channel.VerifyDisabled = &zero
		}
		err = channel.SaveWithoutKey()
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to update channel status: channel_id=%d, status=%d, error=%v", channel.Id, status, err))
			return false
		}
	}
	return true
}

func EnableChannelByTag(tag string) error {
	err := DB.Model(&Channel{}).Where("tag = ?", tag).Update("status", common.ChannelStatusEnabled).Error
	if err != nil {
		return err
	}
	err = UpdateAbilityStatusByTag(tag, true)
	return err
}

func DisableChannelByTag(tag string) error {
	err := DB.Model(&Channel{}).Where("tag = ?", tag).Update("status", common.ChannelStatusManuallyDisabled).Error
	if err != nil {
		return err
	}
	err = UpdateAbilityStatusByTag(tag, false)
	return err
}

func EditChannelByTag(tag string, newTag *string, modelMapping *string, models *string, group *string, priority *int64, weight *uint, paramOverride *string, headerOverride *string) error {
	updateData := Channel{}
	shouldReCreateAbilities := false
	updatedTag := tag
	// 如果 newTag 不为空且不等于 tag，则更新 tag
	if newTag != nil && *newTag != tag {
		updateData.Tag = newTag
		updatedTag = *newTag
	}
	if modelMapping != nil {
		updateData.ModelMapping = modelMapping
	}
	if models != nil && *models != "" {
		shouldReCreateAbilities = true
		updateData.Models = *models
	}
	if group != nil && *group != "" {
		shouldReCreateAbilities = true
		updateData.Group = *group
	}
	if priority != nil {
		updateData.Priority = priority
	}
	if weight != nil {
		updateData.Weight = weight
	}
	if paramOverride != nil {
		updateData.ParamOverride = paramOverride
	}
	if headerOverride != nil {
		updateData.HeaderOverride = headerOverride
	}

	err := DB.Model(&Channel{}).Where("tag = ?", tag).Updates(updateData).Error
	if err != nil {
		return err
	}
	if shouldReCreateAbilities {
		channels, err := GetChannelsByTag(updatedTag, false, false)
		if err == nil {
			for _, channel := range channels {
				err = channel.UpdateAbilities(nil)
				if err != nil {
					common.SysLog(fmt.Sprintf("failed to update abilities: channel_id=%d, tag=%s, error=%v", channel.Id, channel.GetTag(), err))
				}
			}
		}
	} else {
		err := UpdateAbilityByTag(tag, newTag, priority, weight)
		if err != nil {
			return err
		}
	}
	return nil
}

func UpdateChannelUsedQuota(id int, quota int) error {
	if common.BatchUpdateEnabled {
		if err := addNewRecord(BatchUpdateTypeChannelUsedQuota, id, quota); err != nil {
			return recordBatchAdmissionError("update channel used quota", err)
		}
		return nil
	}
	return updateChannelUsedQuota(id, quota)
}

func updateChannelUsedQuota(id int, quota int) error {
	err := DB.Model(&Channel{}).Where("id = ?", id).Update("used_quota", gorm.Expr("used_quota + ?", quota)).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update channel used quota: channel_id=%d, delta_quota=%d, error=%v", id, quota, err))
	}
	return err
}

func DeleteChannelByStatus(status int64) (int64, error) {
	channelIds, err := deleteChannelsWithLedger(
		[]channelDeleteScope{func(tx *gorm.DB) *gorm.DB {
			return tx.Where("status = ?", status)
		}},
		false,
	)
	return int64(len(channelIds)), err
}

func DeleteDisabledChannel() (int64, error) {
	channelIds, err := deleteChannelsWithLedger(
		[]channelDeleteScope{func(tx *gorm.DB) *gorm.DB {
			return tx.Where(
				"status = ? OR status = ?",
				common.ChannelStatusAutoDisabled,
				common.ChannelStatusManuallyDisabled,
			)
		}},
		false,
	)
	return int64(len(channelIds)), err
}

func GetPaginatedTags(offset int, limit int) ([]*string, error) {
	var tags []*string
	err := DB.Model(&Channel{}).Select("DISTINCT tag").Where("tag != ''").Offset(offset).Limit(limit).Find(&tags).Error
	return tags, err
}

func SearchTags(keyword string, group string, model string, idSort bool) ([]*string, error) {
	var tags []*string
	modelsCol := "`models`"

	// 如果是 PostgreSQL，使用双引号
	if common.UsingPostgreSQL {
		modelsCol = `"models"`
	}

	baseURLCol := "`base_url`"
	// 如果是 PostgreSQL，使用双引号
	if common.UsingPostgreSQL {
		baseURLCol = `"base_url"`
	}

	order := "priority desc"
	if idSort {
		order = "id desc"
	}

	// 构造基础查询
	baseQuery := DB.Model(&Channel{}).Omit("key")

	// 构造WHERE子句
	var whereClause string
	var args []interface{}
	if group != "" && group != "null" {
		var groupCondition string
		if common.UsingMySQL {
			groupCondition = `CONCAT(',', ` + commonGroupCol + `, ',') LIKE ?`
		} else {
			// sqlite, PostgreSQL
			groupCondition = `(',' || ` + commonGroupCol + ` || ',') LIKE ?`
		}
		whereClause = "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + ` LIKE ? AND ` + groupCondition
		args = append(args, common.String2Int(keyword), "%"+keyword+"%", keyword, "%"+keyword+"%", "%"+model+"%", "%,"+group+",%")
	} else {
		whereClause = "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
		args = append(args, common.String2Int(keyword), "%"+keyword+"%", keyword, "%"+keyword+"%", "%"+model+"%")
	}

	subQuery := baseQuery.Where(whereClause, args...).
		Select("tag").
		Where("tag != ''").
		Order(order)

	err := DB.Table("(?) as sub", subQuery).
		Select("DISTINCT tag").
		Find(&tags).Error

	if err != nil {
		return nil, err
	}

	return tags, nil
}

func (channel *Channel) ValidateSettings() error {
	channelParams := &dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		err := common.Unmarshal([]byte(*channel.Setting), channelParams)
		if err != nil {
			return err
		}
	}
	channelOtherSettings := &dto.ChannelOtherSettings{}
	if channel.OtherSettings != "" {
		if err := common.UnmarshalJsonStr(channel.OtherSettings, channelOtherSettings); err != nil {
			return err
		}
	}
	if channel.Type == constant.ChannelTypeAdvancedCustom && channelOtherSettings.AdvancedCustom == nil {
		return fmt.Errorf("advanced_custom is required")
	}
	if channelOtherSettings.AdvancedCustom != nil {
		if err := channelOtherSettings.AdvancedCustom.Validate(); err != nil {
			return err
		}
	}
	if channel.Type == constant.ChannelTypeAdvancedCustom &&
		channelOtherSettings.UpstreamModelUpdateCheckEnabled {
		if _, ok := channelOtherSettings.AdvancedCustom.ModelListRoute(); !ok {
			return fmt.Errorf("advanced custom channels require a %s route when upstream model update checks are enabled", dto.AdvancedCustomModelListPath)
		}
	}
	return nil
}

func (channel *Channel) GetSetting() dto.ChannelSettings {
	setting := dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		err := common.Unmarshal([]byte(*channel.Setting), &setting)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
			channel.Setting = nil // 清空设置以避免后续错误
			_ = channel.Save()    // 保存修改
		}
	}
	return setting
}

func (channel *Channel) SetSetting(setting dto.ChannelSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.Setting = common.GetPointer[string](string(settingBytes))
}

// GetEffectiveBaseURLs 返回该渠道生效的 base_url 列表：主 base_url 在首位，
// 其后是 setting.backup_base_urls 的备用地址；去重、剔空、去尾斜杠、保持顺序。
// 网络无响应时由 relay 在同渠道内按此列表切换。
func (channel *Channel) GetEffectiveBaseURLs() []string {
	urls := make([]string, 0, 4)
	seen := make(map[string]bool)
	add := func(u string) {
		u = strings.TrimRight(strings.TrimSpace(u), "/")
		if u == "" || seen[u] {
			return
		}
		seen[u] = true
		urls = append(urls, u)
	}
	add(channel.GetBaseURL())
	for _, u := range channel.GetSetting().BackupBaseURLs {
		add(u)
	}
	return urls
}

func (channel *Channel) GetOtherSettings() dto.ChannelOtherSettings {
	setting := dto.ChannelOtherSettings{}
	if channel.OtherSettings != "" {
		err := common.UnmarshalJsonStr(channel.OtherSettings, &setting)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
			channel.OtherSettings = "{}" // 清空设置以避免后续错误
			_ = channel.Save()           // 保存修改
		}
	}
	return setting
}

func (channel *Channel) SetOtherSettings(setting dto.ChannelOtherSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.OtherSettings = string(settingBytes)
}

func (channel *Channel) GetMaxTtftMs() int {
	s := channel.GetOtherSettings()
	if s.MaxTtftMs != nil && *s.MaxTtftMs > 0 {
		return *s.MaxTtftMs
	}
	return 0
}

func (channel *Channel) GetParamOverride() map[string]interface{} {
	paramOverride := make(map[string]interface{})
	if channel.ParamOverride != nil && *channel.ParamOverride != "" {
		err := common.Unmarshal([]byte(*channel.ParamOverride), &paramOverride)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal param override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return paramOverride
}

func (channel *Channel) GetHeaderOverride() map[string]interface{} {
	headerOverride := make(map[string]interface{})
	if channel.HeaderOverride != nil && *channel.HeaderOverride != "" {
		err := common.Unmarshal([]byte(*channel.HeaderOverride), &headerOverride)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal header override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return headerOverride
}

func GetChannelsByIds(ids []int) ([]*Channel, error) {
	var channels []*Channel
	err := DB.Where("id in (?)", ids).Find(&channels).Error
	return channels, err
}

func BatchSetChannelTag(ids []int, tag *string) error {
	// 开启事务
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}

	// 更新标签
	err := tx.Model(&Channel{}).Where("id in (?)", ids).Update("tag", tag).Error
	if err != nil {
		tx.Rollback()
		return err
	}

	// update ability status
	channels, err := GetChannelsByIds(ids)
	if err != nil {
		tx.Rollback()
		return err
	}

	for _, channel := range channels {
		err = channel.UpdateAbilities(tx)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	// 提交事务
	return tx.Commit().Error
}

// CountAllChannels returns total channels in DB
func CountAllChannels() (int64, error) {
	var total int64
	err := DB.Model(&Channel{}).Count(&total).Error
	return total, err
}

// CountAllTags returns number of non-empty distinct tags
func CountAllTags() (int64, error) {
	var total int64
	err := DB.Model(&Channel{}).Where("tag is not null AND tag != ''").Distinct("tag").Count(&total).Error
	return total, err
}

// Get channels of specified type with pagination
func GetChannelsByType(startIdx int, num int, idSort bool, channelType int) ([]*Channel, error) {
	var channels []*Channel
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	err := DB.Where("type = ?", channelType).Order(order).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	return channels, err
}

// Count channels of specific type
func CountChannelsByType(channelType int) (int64, error) {
	var count int64
	err := DB.Model(&Channel{}).Where("type = ?", channelType).Count(&count).Error
	return count, err
}

// Return map[type]count for all channels
func CountChannelsGroupByType() (map[int64]int64, error) {
	type result struct {
		Type  int64 `gorm:"column:type"`
		Count int64 `gorm:"column:count"`
	}
	var results []result
	err := DB.Model(&Channel{}).Select("type, count(*) as count").Group("type").Find(&results).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[int64]int64)
	for _, r := range results {
		counts[r.Type] = r.Count
	}
	return counts, nil
}

type ChannelLite struct {
	Id        int     `gorm:"column:id"`
	Name      string  `gorm:"column:name"`
	Group     string  `gorm:"column:group"`
	Status    int     `gorm:"column:status"`
	Type      int     `gorm:"column:type"`
	PeakRpm   float64 `gorm:"column:peak_rpm"`
	PeakRpmAt int64   `gorm:"column:peak_rpm_at"`
}

func GetAllChannelsLite(ctx context.Context) ([]ChannelLite, error) {
	out := make([]ChannelLite, 0)
	err := DB.WithContext(ctx).Table("channels").
		Select("id, name, " + commonGroupCol + ", status, type, peak_rpm, peak_rpm_at").
		Scan(&out).Error
	return out, err
}

// ChannelBillSource 对账上游账单用：渠道 id/名称/base_url/key
// （名称用于匹配上游令牌名，key 用于指纹精确绑定；多 key 渠道由调用方取首行）。
type ChannelBillSource struct {
	Id      int    `gorm:"column:id"`
	Name    string `gorm:"column:name"`
	BaseUrl string `gorm:"column:base_url"`
	Key     string `gorm:"column:key"`
}

// GetChannelBillSources 全部有 base_url 的渠道（空 base_url 无法归站点，跳过）。
func GetChannelBillSources(ctx context.Context) ([]ChannelBillSource, error) {
	var rows []ChannelBillSource
	err := DB.WithContext(ctx).Table("channels").
		Select("id, name, base_url, " + commonKeyCol).
		Where("base_url IS NOT NULL AND base_url != ''").
		Scan(&rows).Error
	return rows, err
}

func GetChannelNamesByIds(ctx context.Context, ids []int) (map[int]string, error) {
	out := make(map[int]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	type row struct {
		Id   int    `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	var rows []row
	if err := DB.WithContext(ctx).Table("channels").
		Select("id, name").
		Where("id IN ?", ids).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.Id] = r.Name
	}
	return out, nil
}
