package model

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"

	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

type Log struct {
	Id        int    `json:"id" gorm:"index:idx_created_at_id,priority:2;index:idx_user_id_id,priority:2;index:idx_logs_channel_type_id,priority:3"`
	UserId    int    `json:"user_id" gorm:"index;index:idx_user_id_id,priority:1"`
	CreatedAt int64  `json:"created_at" gorm:"bigint;index:idx_created_at_id,priority:1;index:idx_created_at_type;index:idx_logs_group_created,priority:2;index:idx_logs_model_created,priority:2"`
	Type      int    `json:"type" gorm:"index:idx_created_at_type;index:idx_logs_channel_type_id,priority:2"`
	Content   string `json:"content"`
	Username  string `json:"username" gorm:"index;index:index_username_model_name,priority:2;default:''"`
	TokenName string `json:"token_name" gorm:"index;default:''"`
	// idx_logs_model_created (model_name, created_at)：按模型搜索日志时分页 Count 走覆盖索引，免回表扫百万行
	ModelName        string `json:"model_name" gorm:"index;index:index_username_model_name,priority:1;index:idx_logs_model_created,priority:1;default:''"`
	Quota            int    `json:"quota" gorm:"default:0"`
	PromptTokens     int    `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens int    `json:"completion_tokens" gorm:"default:0"`
	UseTime          int    `json:"use_time" gorm:"default:0"`
	IsStream         bool   `json:"is_stream"`
	// idx_logs_channel_type_id 复合索引：service.RecomputeChannelMetricsOnce 每 5min 跑 N×(channel×type)
	// 的 ORDER BY id DESC LIMIT 500 子查询，必须走 covering index reverse scan，否则 ROW_NUMBER 物化全表。
	ChannelId   int    `json:"channel" gorm:"index;index:idx_logs_channel_type_id,priority:1"`
	ChannelName string `json:"channel_name" gorm:"->"`
	TokenId     int    `json:"token_id" gorm:"default:0;index"`
	Group       string `json:"group" gorm:"index;index:idx_logs_group_created,priority:1"`
	Ip          string `json:"ip" gorm:"index;default:''"`
	RequestId   string `json:"request_id,omitempty" gorm:"type:varchar(64);index:idx_logs_request_id;default:''"`
	EventId     string `json:"-" gorm:"->;-:migration"`
	// 大日志表不在启动期自动建新索引；如后续增加上游 ID 搜索，应单独安排在线 DDL。
	UpstreamRequestId string `json:"upstream_request_id,omitempty" gorm:"type:varchar(128);default:''"`
	Other             string `json:"other"`
}

func ensureLogRequestId(log *Log) {
	if log != nil && log.RequestId == "" {
		log.RequestId = common.NewRequestId()
	}
}

func (log *Log) BeforeCreate(_ *gorm.DB) error {
	ensureLogRequestId(log)
	return nil
}

func clickHouseLogOrder(prefix string) string {
	return prefix + "created_at desc, " + prefix + "event_id desc"
}

func recentLogOrder(prefix string) string {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return clickHouseLogOrder(prefix)
	}
	return prefix + "created_at desc, " + prefix + "id desc"
}

func assignDisplayLogIds(logs []*Log, startIdx int) {
	for index := range logs {
		logs[index].Id = startIdx + index + 1
	}
}

func buildLogLikeCondition(column string, value string) (string, string, error) {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		pattern := strings.ReplaceAll(value, `\`, `\\`)
		pattern = strings.ReplaceAll(pattern, `_`, `\_`)
		if err := validateLikePattern(pattern); err != nil {
			return "", "", err
		}
		return column + " LIKE ?", pattern, nil
	}
	pattern, err := sanitizeLikePattern(value)
	if err != nil {
		return "", "", err
	}
	return column + " LIKE ? ESCAPE '!'", pattern, nil
}

func applyExplicitLogTextFilter(tx *gorm.DB, column string, value string) (*gorm.DB, error) {
	if value == "" {
		return tx, nil
	}
	if !strings.Contains(value, "%") {
		return tx.Where(column+" = ?", value), nil
	}
	condition, pattern, err := buildLogLikeCondition(column, value)
	if err != nil {
		return nil, err
	}
	return tx.Where(condition, pattern), nil
}

const logCreatedAtTypeIndexName = "idx_created_at_type"
const logChannelWindowIndexName = "idx_logs_channel_type_created_at_quota"
const channelQuotaSelect = "COALESCE(SUM(quota), 0) AS quota"

func userLogsTable(db *gorm.DB, logType int, startTimestamp, endTimestamp int64, hasAdditionalFilters bool) string {
	if db.Dialector.Name() != "mysql" || startTimestamp == 0 || endTimestamp == 0 || hasAdditionalFilters {
		return "logs"
	}

	if logType == LogTypeRefund {
		// Refund rows are sparse. MySQL otherwise prefers idx_user_id_id for
		// ORDER BY id and may fetch every historical row for a large user before
		// discovering that the bounded time window contains no matching rows.
		return "logs FORCE INDEX (`" + logCreatedAtTypeIndexName + "`)"
	}
	return "logs"
}

func channelQuotaTable(db *gorm.DB) string {
	if db.Dialector.Name() == "mysql" {
		return "logs FORCE INDEX (`" + logChannelWindowIndexName + "`)"
	}
	return "logs"
}

// logChannelWindowIndex describes the index required by channel-scoped time-window
// quota aggregates such as /api/channel/reconcile?summary_only=true and
// /api/log/stat?quota_only=true.
//
// It is deliberately kept separate from Log's tags: adding it there would make
// AutoMigrate build the index while an existing, high-volume logs table is starting.
// migrateLogSchema creates it only for a new or still-empty table; non-empty
// installations must add it as an independently scheduled online DDL.
type logChannelWindowIndex struct {
	ChannelId int   `gorm:"index:idx_logs_channel_type_created_at_quota,priority:1"`
	Type      int   `gorm:"index:idx_logs_channel_type_created_at_quota,priority:2"`
	CreatedAt int64 `gorm:"index:idx_logs_channel_type_created_at_quota,priority:3"`
	Quota     int   `gorm:"index:idx_logs_channel_type_created_at_quota,priority:4"`
}

func (logChannelWindowIndex) TableName() string {
	return "logs"
}

// don't use iota, avoid change log type value
const (
	LogTypeUnknown = 0
	LogTypeTopup   = 1
	LogTypeConsume = 2
	LogTypeManage  = 3
	LogTypeSystem  = 4
	LogTypeError   = 5
	LogTypeRefund  = 6
)

func formatUserLogs(logs []*Log, startIdx int) {
	for i := range logs {
		logs[i].ChannelName = ""
		var otherMap map[string]interface{}
		otherMap, _ = common.StrToMap(logs[i].Other)
		if otherMap != nil {
			// Remove admin-only debug fields.
			delete(otherMap, "admin_info")
			// delete(otherMap, "reject_reason")
			delete(otherMap, "stream_status")
			// 拒退分类（upstream_refusal/client_gone_quick/shutdown）是内部反滥用口径，
			// 用户侧解释走 Content 文案，字段仅管理端可见
			delete(otherMap, "refund_denied_reason")
		}
		logs[i].Other = common.MapToJsonStr(otherMap)
	}
	assignDisplayLogIds(logs, startIdx)
}

func GetLogByTokenId(tokenId int) (logs []*Log, err error) {
	err = LOG_DB.Model(&Log{}).Where("token_id = ?", tokenId).
		Order(recentLogOrder("")).Limit(common.MaxRecentItems).Find(&logs).Error
	formatUserLogs(logs, 0)
	return logs, err
}

func RecordLog(userId int, logType int, content string) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

// RecordLogWithAdminInfo 记录操作日志，并将管理员相关信息存入 Other.admin_info，
func RecordLogWithAdminInfo(userId int, logType int, content string, adminInfo map[string]interface{}) {
	if err := RecordLogWithAdminInfoAndRequestID(userId, logType, content, adminInfo, ""); err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

func RecordLogWithAdminInfoAndRequestID(
	userId int,
	logType int,
	content string,
	adminInfo map[string]interface{},
	requestId string,
) error {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return nil
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
		RequestId: requestId,
	}
	if len(adminInfo) > 0 {
		other := map[string]interface{}{
			"admin_info": adminInfo,
		}
		log.Other = common.MapToJsonStr(other)
	}
	return LOG_DB.Create(log).Error
}

func RecordTopupLog(userId int, content string, callerIp string, paymentMethod string, callbackPaymentMethod string) {
	username, _ := GetUsernameById(userId, false)
	adminInfo := map[string]interface{}{
		"server_ip":               common.GetIp(),
		"node_name":               common.NodeName,
		"caller_ip":               callerIp,
		"payment_method":          paymentMethod,
		"callback_payment_method": callbackPaymentMethod,
		"version":                 common.Version,
	}
	other := map[string]interface{}{
		"admin_info": adminInfo,
	}
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeTopup,
		Content:   content,
		Ip:        callerIp,
		Other:     common.MapToJsonStr(other),
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		common.SysLog("failed to record topup log: " + err.Error())
	}
}

func RecordErrorLog(c *gin.Context, userId int, channelId int, modelName string, tokenName string, content string, tokenId int, useTimeSeconds int,
	isStream bool, group string, other map[string]interface{}) {
	// RPM 只统计成功请求（type=2，见 RecordConsumeLog）。错误请求不计入渠道/用户 RPM，
	// 否则故障渠道（429/503 风暴）或跨渠道重试会把 RPM 刷高，与"实际有效吞吐"背离。
	logger.LogInfo(c, fmt.Sprintf("record error log: userId=%d, channelId=%d, modelName=%s, tokenName=%s, content=%s", userId, channelId, modelName, tokenName, common.LocalLogPreview(common.MaskSensitiveInfo(content))))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	otherStr := common.MapToJsonStr(other)
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeError,
		Content:          content,
		PromptTokens:     0,
		CompletionTokens: 0,
		TokenName:        tokenName,
		ModelName:        modelName,
		Quota:            0,
		ChannelId:        channelId,
		TokenId:          tokenId,
		UseTime:          useTimeSeconds,
		IsStream:         isStream,
		Group:            group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	}
}

type RecordConsumeLogParams struct {
	ChannelId        int                    `json:"channel_id"`
	PromptTokens     int                    `json:"prompt_tokens"`
	CompletionTokens int                    `json:"completion_tokens"`
	ModelName        string                 `json:"model_name"`
	TokenName        string                 `json:"token_name"`
	Quota            int                    `json:"quota"`
	Content          string                 `json:"content"`
	TokenId          int                    `json:"token_id"`
	UseTimeSeconds   int                    `json:"use_time_seconds"`
	IsStream         bool                   `json:"is_stream"`
	Group            string                 `json:"group"`
	Other            map[string]interface{} `json:"other"`
}

func RecordConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams) {
	common.IncrUserRPM(userId)
	common.IncrChannelRPM(params.ChannelId)
	if !common.LogConsumeEnabled {
		return
	}
	logger.LogInfo(c, fmt.Sprintf("record consume log: userId=%d, params=%s", userId, common.GetJsonString(params)))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	otherStr := common.MapToJsonStr(params.Other)
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeConsume,
		Content:          params.Content,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		TokenName:        params.TokenName,
		ModelName:        params.ModelName,
		Quota:            params.Quota,
		ChannelId:        params.ChannelId,
		TokenId:          params.TokenId,
		UseTime:          params.UseTimeSeconds,
		IsStream:         params.IsStream,
		Group:            params.Group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	}
	if common.DataExportEnabled {
		billingSource := ""
		if bs, ok := params.Other["billing_source"].(string); ok {
			billingSource = bs
		}
		LogQuotaData(userId, username, params.ModelName, params.Group, billingSource, params.Quota, log.CreatedAt, params.PromptTokens+params.CompletionTokens)
	}
}

type RecordTaskBillingLogParams struct {
	UserId    int
	LogType   int
	Content   string
	ChannelId int
	ModelName string
	Quota     int
	TokenId   int
	Group     string
	Other     map[string]interface{}
}

func RecordTaskBillingLog(params RecordTaskBillingLogParams) {
	if params.LogType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(params.UserId, false)
	tokenName := ""
	if params.TokenId > 0 {
		if token, err := GetTokenById(params.TokenId); err == nil {
			tokenName = token.Name
		}
	}
	log := &Log{
		UserId:    params.UserId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      params.LogType,
		Content:   params.Content,
		TokenName: tokenName,
		ModelName: params.ModelName,
		Quota:     params.Quota,
		ChannelId: params.ChannelId,
		TokenId:   params.TokenId,
		Group:     params.Group,
		Other:     common.MapToJsonStr(params.Other),
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		common.SysLog("failed to record task billing log: " + err.Error())
	}
}

// BuildAllLogsQuery 构造管理员日志筛选查询（列表与导出共用）
func BuildAllLogsQuery(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int, group string, requestId string) (*gorm.DB, error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB
	} else {
		tx = LOG_DB.Where("logs.type = ?", logType)
	}

	var err error
	if tx, err = applyExplicitLogTextFilter(tx, "logs.model_name", modelName); err != nil {
		return nil, err
	}
	if username != "" {
		tx = tx.Where("logs.username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if requestId != "" {
		tx = tx.Where("logs.request_id = ?", requestId)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if channel != 0 {
		tx = tx.Where("logs.channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	return tx, nil
}

func GetAllLogs(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, startIdx int, num int, channel int, group string, requestId string) (logs []*Log, total int64, err error) {
	tx, err := BuildAllLogsQuery(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group, requestId)
	if err != nil {
		return nil, 0, err
	}
	err = tx.Model(&Log{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	err = tx.Order(recentLogOrder("logs.")).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		assignDisplayLogIds(logs, startIdx)
	}

	channelIds := types.NewSet[int]()
	for _, log := range logs {
		if log.ChannelId != 0 {
			channelIds.Add(log.ChannelId)
		}
	}

	if channelIds.Len() > 0 {
		var channels []struct {
			Id   int    `gorm:"column:id"`
			Name string `gorm:"column:name"`
		}
		if common.MemoryCacheEnabled {
			// Cache get channel
			for _, channelId := range channelIds.Items() {
				if cacheChannel, err := CacheGetChannel(channelId); err == nil {
					channels = append(channels, struct {
						Id   int    `gorm:"column:id"`
						Name string `gorm:"column:name"`
					}{
						Id:   channelId,
						Name: cacheChannel.Name,
					})
				}
			}
		} else {
			// Bulk query channels from DB
			if err = DB.Table("channels").Select("id, name").Where("id IN ?", channelIds.Items()).Find(&channels).Error; err != nil {
				return logs, total, err
			}
		}
		channelMap := make(map[int]string, len(channels))
		for _, channel := range channels {
			channelMap[channel.Id] = channel.Name
		}
		for i := range logs {
			logs[i].ChannelName = channelMap[logs[i].ChannelId]
		}
	}

	return logs, total, err
}

const logSearchCountLimit = 10000

// BuildUserLogsQuery 构造用户自查日志筛选查询（列表与导出共用）
func BuildUserLogsQuery(userId int, logType int, startTimestamp int64, endTimestamp int64, modelName string, tokenName string, group string, requestId string) (*gorm.DB, error) {
	hasAdditionalFilters := modelName != "" || tokenName != "" || group != "" || requestId != ""
	baseQuery := LOG_DB.Table(userLogsTable(LOG_DB, logType, startTimestamp, endTimestamp, hasAdditionalFilters))
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = baseQuery.Where("logs.user_id = ?", userId)
	} else {
		tx = baseQuery.Where("logs.user_id = ? and logs.type = ?", userId, logType)
	}

	var err error
	if tx, err = applyExplicitLogTextFilter(tx, "logs.model_name", modelName); err != nil {
		return nil, err
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if requestId != "" {
		tx = tx.Where("logs.request_id = ?", requestId)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	return tx, nil
}

func buildBoundedLogCountQuery(query *gorm.DB, limit int) *gorm.DB {
	cursorColumn := "logs.id"
	order := "logs.id DESC"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		cursorColumn = "logs.event_id"
		order = recentLogOrder("logs.")
	}
	limitedQuery := query.Session(&gorm.Session{}).
		Model(&Log{}).
		Select(cursorColumn).
		Order(order).
		Limit(limit)

	return query.Session(&gorm.Session{NewDB: true}).
		Table("(?) AS bounded_logs", limitedQuery)
}

func countLogsUpTo(query *gorm.DB, limit int) (int64, error) {
	var total int64
	err := buildBoundedLogCountQuery(query, limit).Count(&total).Error
	return total, err
}

func GetUserLogs(userId int, logType int, startTimestamp int64, endTimestamp int64, modelName string, tokenName string, startIdx int, num int, group string, requestId string) (logs []*Log, total int64, totalIsCapped bool, err error) {
	tx, err := BuildUserLogsQuery(userId, logType, startTimestamp, endTimestamp, modelName, tokenName, group, requestId)
	if err != nil {
		return nil, 0, false, err
	}

	err = tx.Order(recentLogOrder("logs.")).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		common.SysError("failed to search user logs: " + err.Error())
		return nil, 0, false, errors.New("查询日志失败")
	}

	formatUserLogs(logs, startIdx)
	if startIdx == 0 && len(logs) < num {
		return logs, int64(len(logs)), false, nil
	}

	total, err = countLogsUpTo(tx, logSearchCountLimit+1)
	if err != nil {
		common.SysError("failed to count user logs: " + err.Error())
		return nil, 0, false, errors.New("查询日志失败")
	}
	if total > logSearchCountLimit {
		total = logSearchCountLimit
		totalIsCapped = true
	}

	return logs, total, totalIsCapped, nil
}

const (
	// LogExportMaxRowsUser 普通用户单次导出行数上限
	LogExportMaxRowsUser = 100000
	// LogExportMaxRowsAdmin 管理员单次导出行数上限
	LogExportMaxRowsAdmin = 2000000
	logExportBatchSize    = 5000
)

var logTypeLabels = map[int]string{
	LogTypeTopup:   "充值",
	LogTypeConsume: "消费",
	LogTypeManage:  "管理",
	LogTypeSystem:  "系统",
	LogTypeError:   "错误",
	LogTypeRefund:  "退款",
}

func logTypeLabel(t int) string {
	if s, ok := logTypeLabels[t]; ok {
		return s
	}
	return strconv.Itoa(t)
}

// csvSafe 防 CSV 公式注入：令牌名/模型名等为用户可控字段，
// 以 = + - @ 或 tab/回车开头时 Excel 会当公式执行，前置单引号中和
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// CountLogsForExport 导出前预检行数，超过上限直接报错，避免大结果集拖垮实例。
// 计数成本与日志列表页的 Count 相当（GORM Count 会忽略 Limit，故不加）。
// maxRows 由调用方按角色传入（管理员/普通用户上限不同）。
func CountLogsForExport(query *gorm.DB, maxRows int) (int64, error) {
	var total int64
	err := query.Session(&gorm.Session{}).Model(&Log{}).Count(&total).Error
	if err != nil {
		return 0, err
	}
	if total > int64(maxRows) {
		return total, fmt.Errorf("导出结果超过 %d 行上限，请缩小筛选范围", maxRows)
	}
	return total, nil
}

// StreamLogsCSV 脱敏流式导出：仅导出白名单列，不含渠道/分组/IP/other 等敏感信息；
// 错误日志的详情可能携带上游信息，导出时置空
func StreamLogsCSV(w io.Writer, query *gorm.DB, includeUsername bool, maxRows int) error {
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil { // BOM：Excel 打开中文不乱码
		return err
	}
	cw := csv.NewWriter(w)
	header := make([]string, 0, 12)
	header = append(header, "时间(UTC+8)")
	if includeUsername {
		header = append(header, "用户名")
	}
	header = append(header, "类型", "令牌名", "模型", "提示Tokens", "补全Tokens", "金额(USD)", "耗时(秒)", "流式", "请求ID", "详情")
	if err := cw.Write(header); err != nil {
		return err
	}

	cst := time.FixedZone("UTC+8", 8*3600)
	written := 0
	// 关系库按 (created_at, id)；ClickHouse 按唯一行键 (created_at, event_id)。
	var lastCreatedAt int64
	var lastId int
	var lastEventId string
	first := true
	for {
		var batch []*Log
		tx := query.Session(&gorm.Session{})
		if !first {
			if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
				tx = tx.Where(
					"logs.created_at < ? OR (logs.created_at = ? AND logs.event_id < ?)",
					lastCreatedAt, lastCreatedAt, lastEventId,
				)
			} else {
				tx = tx.Where(
					"logs.created_at < ? OR (logs.created_at = ? AND logs.id < ?)",
					lastCreatedAt, lastCreatedAt, lastId,
				)
			}
		}
		err := tx.Order(recentLogOrder("logs.")).Limit(logExportBatchSize).Find(&batch).Error
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, l := range batch {
			content := l.Content
			if l.Type == LogTypeError {
				content = ""
			}
			stream := "否"
			if l.IsStream {
				stream = "是"
			}
			row := make([]string, 0, 12)
			row = append(row, time.Unix(l.CreatedAt, 0).In(cst).Format("2006-01-02 15:04:05"))
			if includeUsername {
				row = append(row, csvSafe(l.Username))
			}
			row = append(row,
				logTypeLabel(l.Type),
				csvSafe(l.TokenName),
				csvSafe(l.ModelName),
				strconv.Itoa(l.PromptTokens),
				strconv.Itoa(l.CompletionTokens),
				strconv.FormatFloat(float64(l.Quota)/common.QuotaPerUnit, 'f', 6, 64),
				strconv.Itoa(l.UseTime),
				stream,
				l.RequestId,
				csvSafe(content),
			)
			if err := cw.Write(row); err != nil {
				return err
			}
			written++
			if written >= maxRows {
				break
			}
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return err
		}
		if written >= maxRows || len(batch) < logExportBatchSize {
			break
		}
		last := batch[len(batch)-1]
		lastCreatedAt = last.CreatedAt
		lastId = last.Id
		lastEventId = last.EventId
		first = false
	}
	cw.Flush()
	return cw.Error()
}

type Stat struct {
	Quota int `json:"quota"`
	Rpm   int `json:"rpm"`
	Tpm   int `json:"tpm"`
}

// SumChannelQuota returns the exact consume quota for one channel and bounded
// time window. The query only reads columns covered by
// idx_logs_channel_type_created_at_quota.
func SumChannelQuota(ctx context.Context, channelId int, startTimestamp, endTimestamp int64) (int64, error) {
	var row struct {
		Quota int64 `gorm:"column:quota"`
	}
	db := LOG_DB.WithContext(ctx)
	err := db.Table(channelQuotaTable(db)).
		Select(channelQuotaSelect).
		Where("channel_id = ? AND type = ? AND created_at >= ? AND created_at <= ?",
			channelId, LogTypeConsume, startTimestamp, endTimestamp).
		Scan(&row).Error
	if err != nil {
		common.SysError("failed to query channel quota: " + err.Error())
		return 0, errors.New("查询统计数据失败")
	}
	return row.Quota, nil
}

// SumUsedQuota's quota query is channel-scoped and time-bounded when the admin
// supplies channel/start/end filters. Existing large installations must provide
// idx_logs_channel_type_created_at_quota; see migrateLogSchema for the migration boundary.
func SumUsedQuota(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int, group string) (stat Stat, err error) {
	tx := LOG_DB.Table("logs").Select("COALESCE(sum(quota), 0) quota")

	// 为rpm和tpm创建单独的查询
	rpmTpmQuery := LOG_DB.Table("logs").Select("count(*) rpm, COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0) tpm")

	if tx, err = applyExplicitLogTextFilter(tx, "username", username); err != nil {
		return stat, err
	}
	if rpmTpmQuery, err = applyExplicitLogTextFilter(rpmTpmQuery, "username", username); err != nil {
		return stat, err
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
		rpmTpmQuery = rpmTpmQuery.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if tx, err = applyExplicitLogTextFilter(tx, "model_name", modelName); err != nil {
		return stat, err
	}
	if rpmTpmQuery, err = applyExplicitLogTextFilter(rpmTpmQuery, "model_name", modelName); err != nil {
		return stat, err
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
		rpmTpmQuery = rpmTpmQuery.Where("channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where(logGroupCol+" = ?", group)
		rpmTpmQuery = rpmTpmQuery.Where(logGroupCol+" = ?", group)
	}

	tx = tx.Where("type = ?", LogTypeConsume)
	rpmTpmQuery = rpmTpmQuery.Where("type = ?", LogTypeConsume)

	// 只统计最近60秒的rpm和tpm
	rpmTpmQuery = rpmTpmQuery.Where("created_at >= ?", time.Now().Add(-60*time.Second).Unix())

	// 执行查询
	if err := tx.Scan(&stat).Error; err != nil {
		common.SysError("failed to query log stat: " + err.Error())
		return stat, errors.New("查询统计数据失败")
	}
	if err := rpmTpmQuery.Scan(&stat).Error; err != nil {
		common.SysError("failed to query rpm/tpm stat: " + err.Error())
		return stat, errors.New("查询统计数据失败")
	}

	return stat, nil
}

func SumUsedToken(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string) (token int) {
	tx := LOG_DB.Table("logs").Select("COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0)")
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&token)
	return token
}

func CountOldLog(ctx context.Context, targetTimestamp int64) (int64, error) {
	var total int64
	if err := LOG_DB.WithContext(ctx).Model(&Log{}).
		Where("created_at < ?", targetTimestamp).Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func DeleteOldLogBatch(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		total, err := CountOldLog(ctx, targetTimestamp)
		if err != nil || total == 0 {
			return total, err
		}
		if err := LOG_DB.WithContext(ctx).Exec(
			"ALTER TABLE logs DELETE WHERE created_at < ? SETTINGS mutations_sync = 1",
			targetTimestamp,
		).Error; err != nil {
			return 0, err
		}
		return total, nil
	}
	result := LOG_DB.WithContext(ctx).Where("created_at < ?", targetTimestamp).
		Limit(limit).Delete(&Log{})
	return result.RowsAffected, result.Error
}

func DeleteOldLog(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	var total int64

	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		rowsAffected, err := DeleteOldLogBatch(ctx, targetTimestamp, limit)
		if err != nil {
			return total, err
		}
		total += rowsAffected
		if rowsAffected < int64(limit) {
			break
		}
	}

	return total, nil
}

type ChannelStatRow struct {
	ChannelId int    `gorm:"column:channel_id"`
	ModelName string `gorm:"column:model_name"`
	Quota     int64  `gorm:"column:quota"`
	CallCount int64  `gorm:"column:call_count"`
}

// channelStatsConcurrency 限制并发 LOG_DB 查询数。LOG_DB 连接池上限 ~1000；16 并发
// 既能把 ~140 渠道的 wall time 砍到原来的 1/16，又留足余量给主链路 relay 查询。
const channelStatsConcurrency = 16

// GetChannelStatsRows 并发拉每个渠道在 [startTs, endTs] 窗口的 (model, quota, count) 聚合。
//
// 历史：原本 for 循环串行 139 次 GROUP BY，prod 上 9M 行 logs / 30d 窗口 ~120-180s 超时。
// 现改 errgroup + SetLimit(16) 并发；每条 SQL 都按 channel/type 等值前缀和
// created_at 范围过滤，但未指定 optimizer hint，实际索引由数据库优化器选择；
// 并发只负责压缩各渠道查询的 wall time。
func GetChannelStatsRows(ctx context.Context, startTs, endTs int64) ([]ChannelStatRow, error) {
	var channelIds []int
	if err := DB.WithContext(ctx).Table("channels").Select("id").Scan(&channelIds).Error; err != nil {
		return nil, err
	}
	if len(channelIds) == 0 {
		return nil, nil
	}

	eg, gctx := errgroup.WithContext(ctx)
	eg.SetLimit(channelStatsConcurrency)

	var mu sync.Mutex
	rows := make([]ChannelStatRow, 0, len(channelIds)*4)

	for _, cid := range channelIds {
		cid := cid
		eg.Go(func() error {
			var part []ChannelStatRow
			if err := LOG_DB.WithContext(gctx).Raw(`
				SELECT ? AS channel_id,
				       model_name,
				       COALESCE(SUM(quota), 0) AS quota,
				       COUNT(*) AS call_count
				FROM logs
				WHERE channel_id = ? AND type = ? AND created_at >= ? AND created_at <= ?
				GROUP BY model_name`,
				cid, cid, LogTypeConsume, startTs, endTs).Scan(&part).Error; err != nil {
				return err
			}
			mu.Lock()
			rows = append(rows, part...)
			mu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return rows, nil
}

type ChannelTrendBucket struct {
	ChannelId   int   `gorm:"column:channel_id"`
	BucketStart int64 `gorm:"column:bucket_start"`
	Quota       int64 `gorm:"column:quota"`
	CallCount   int64 `gorm:"column:call_count"`
}

// GetChannelTrend buckets logs by (channel_id, time-bucket).
//
// tzOffsetSec：调用方本地时区相对 UTC 的偏移秒数（如东八区=28800，UTC=0）。
// 桶表达式 `((created_at + tz) - ((created_at + tz) % bucketSec)) - tz` 把对齐点
// 从 UTC 历元偏移到调用方本地时间，避免日 / 整点桶与本地午夜错位（非 UTC 用户的"今天"
// 跨 UTC 日界时，原本 (ca - ca%86400) 会把一个本地日切成两个桶）。
// 跨库安全：% 在 SQLite/MySQL/PostgreSQL 都是整数取模。
func GetChannelTrend(ctx context.Context, channelIds []int, startTs, endTs int64, bucketSec int, tzOffsetSec int64) ([]ChannelTrendBucket, error) {
	if len(channelIds) == 0 || bucketSec < 60 {
		return nil, nil
	}

	eg, gctx := errgroup.WithContext(ctx)
	eg.SetLimit(channelStatsConcurrency)

	var mu sync.Mutex
	rows := make([]ChannelTrendBucket, 0, len(channelIds)*32)

	for _, cid := range channelIds {
		cid := cid
		eg.Go(func() error {
			var part []ChannelTrendBucket
			if err := LOG_DB.WithContext(gctx).Raw(`
				SELECT ? AS channel_id,
				       ((created_at + ?) - ((created_at + ?) % ?)) - ? AS bucket_start,
				       COALESCE(SUM(quota), 0) AS quota,
				       COUNT(*) AS call_count
				FROM logs
				WHERE channel_id = ? AND type = ? AND created_at >= ? AND created_at <= ?
				GROUP BY bucket_start
				ORDER BY bucket_start ASC`,
				cid, tzOffsetSec, tzOffsetSec, bucketSec, tzOffsetSec,
				cid, LogTypeConsume, startTs, endTs).Scan(&part).Error; err != nil {
				return err
			}
			mu.Lock()
			rows = append(rows, part...)
			mu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return rows, nil
}

type ChannelReconcileRow struct {
	ChannelId    int    `gorm:"column:channel_id"`
	ModelName    string `gorm:"column:model_name"`
	SuccessCount int64  `gorm:"column:success_count"`
	Quota        int64  `gorm:"column:quota"`
	ErrorCount   int64  `gorm:"column:error_count"`
	TimeoutCount int64  `gorm:"column:timeout_count"`
}

type ChannelQuotaSummaryRow struct {
	ChannelId   int    `gorm:"column:channel_id"`
	ChannelName string `gorm:"column:channel_name"`
	Status      int    `gorm:"column:status"`
	Quota       int64  `gorm:"column:quota"`
}

// GetChannelQuotaSummaryRows returns only per-channel consume quota for callers
// that do not need model/error/timeout details. Each logs query is index-only on
// idx_logs_channel_type_created_at_quota.
func GetChannelQuotaSummaryRows(ctx context.Context, startTs, endTs int64) ([]ChannelQuotaSummaryRow, error) {
	var channels []ChannelQuotaSummaryRow
	if err := DB.WithContext(ctx).Table("channels").
		Select("id AS channel_id, name AS channel_name, status").
		Scan(&channels).Error; err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return nil, nil
	}

	eg, gctx := errgroup.WithContext(ctx)
	eg.SetLimit(channelStatsConcurrency)

	var mu sync.Mutex
	rows := make([]ChannelQuotaSummaryRow, 0, len(channels))
	for _, channel := range channels {
		channel := channel
		eg.Go(func() error {
			var quotaRows []struct {
				Quota int64 `gorm:"column:quota"`
			}
			db := LOG_DB.WithContext(gctx)
			if err := db.Table(channelQuotaTable(db)).
				Select(channelQuotaSelect).
				Where("channel_id = ? AND type = ? AND created_at >= ? AND created_at <= ?",
					channel.ChannelId, LogTypeConsume, startTs, endTs).
				Group("channel_id").
				Scan(&quotaRows).Error; err != nil {
				return err
			}
			if len(quotaRows) == 0 {
				return nil
			}
			channel.Quota = quotaRows[0].Quota
			mu.Lock()
			rows = append(rows, channel)
			mu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return rows, nil
}

// GetChannelReconcileRows 对账聚合：窗口内每个 (channel, model) 的成功数/费用/失败数/超时数。
// 查询形状与 GetChannelStatsRows 一致（per-channel 并发）；未指定 optimizer hint，
// 实际索引由数据库优化器选择。
//
// 超时判定与 service/channel_metrics.go 的 Go 端分类同口径（status_code=504，或无 status_code
// 但含 timeout 关键字），额外纳入 524（Cloudflare 超时，上游照常计费，对账必须可见）；
// 区别是这里在 SQL 端对全量行精确计数，而非 500 行取样。
// LOWER + LIKE 在 SQLite/MySQL/PG 三库行为一致。
func GetChannelReconcileRows(ctx context.Context, startTs, endTs int64) ([]ChannelReconcileRow, error) {
	var channelIds []int
	if err := DB.WithContext(ctx).Table("channels").Select("id").Scan(&channelIds).Error; err != nil {
		return nil, err
	}
	if len(channelIds) == 0 {
		return nil, nil
	}

	eg, gctx := errgroup.WithContext(ctx)
	eg.SetLimit(channelStatsConcurrency)

	var mu sync.Mutex
	rows := make([]ChannelReconcileRow, 0, len(channelIds)*4)

	for _, cid := range channelIds {
		cid := cid
		eg.Go(func() error {
			var part []ChannelReconcileRow
			if err := LOG_DB.WithContext(gctx).Raw(`
				SELECT ? AS channel_id,
				       model_name,
				       SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS success_count,
				       COALESCE(SUM(CASE WHEN type = ? THEN quota ELSE 0 END), 0) AS quota,
				       SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS error_count,
				       SUM(CASE WHEN type = ? AND (
				             content LIKE '%status_code=504%'
				             OR content LIKE '%status_code=524%'
				             OR (content NOT LIKE '%status_code=%'
				                 AND (LOWER(content) LIKE '%timeout%'
				                      OR LOWER(content) LIKE '%deadline exceeded%'))
				           ) THEN 1 ELSE 0 END) AS timeout_count
				FROM logs
				WHERE channel_id = ? AND type IN (?, ?) AND created_at >= ? AND created_at <= ?
				GROUP BY model_name`,
				cid, LogTypeConsume, LogTypeConsume, LogTypeError, LogTypeError,
				cid, LogTypeConsume, LogTypeError, startTs, endTs).Scan(&part).Error; err != nil {
				return err
			}
			mu.Lock()
			rows = append(rows, part...)
			mu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return rows, nil
}

type ChannelTopUserRow struct {
	UserId    int    `gorm:"column:user_id"`
	Username  string `gorm:"column:username"`
	Quota     int64  `gorm:"column:total_quota"`
	CallCount int64  `gorm:"column:call_count"`
	Tokens    int64  `gorm:"column:tokens"`
	LastSeen  int64  `gorm:"column:last_seen"`
}

// GetChannelTopUsers 聚合单渠道窗口内按用户的消费/调用排行（渠道大盘卡片展开用）。
// 查询形状与 GetChannelStatsRows 一致，按 channel/type + created_at 过滤；
// 未指定 optimizer hint，实际索引由数据库优化器选择。
//
// 跨库注意：MAX(username) 代替 ANY_VALUE（PG/SQLite 无）；GROUP BY 只留 user_id，
// 用户改名不拆行；排序别名 total_quota 避免裸 quota 与列名歧义时三库行为不一。
func GetChannelTopUsers(ctx context.Context, channelId int, startTs, endTs int64, sortBy string, limit int) ([]ChannelTopUserRow, error) {
	// ORDER BY 由白名单二选一拼接，不接受任意列名。窗口固定时 rpm 排序
	// 等价于 call_count 排序，rpm 数值由 controller 侧计算。
	orderBy := "total_quota DESC, user_id ASC"
	if sortBy == "rpm" {
		orderBy = "call_count DESC, user_id ASC"
	}
	var rows []ChannelTopUserRow
	if err := LOG_DB.WithContext(ctx).Raw(`
		SELECT user_id,
		       MAX(username)                           AS username,
		       COALESCE(SUM(quota), 0)                 AS total_quota,
		       COUNT(*)                                AS call_count,
		       COALESCE(SUM(prompt_tokens), 0)
		         + COALESCE(SUM(completion_tokens), 0) AS tokens,
		       MAX(created_at)                         AS last_seen
		FROM logs
		WHERE channel_id = ? AND type = ? AND created_at >= ? AND created_at <= ?
		GROUP BY user_id
		ORDER BY `+orderBy+`
		LIMIT ?`,
		channelId, LogTypeConsume, startTs, endTs, limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
