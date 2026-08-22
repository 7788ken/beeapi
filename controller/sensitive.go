package controller

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

// GetAllSensitiveWords 关键词分页列表
func GetAllSensitiveWords(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	keyword := strings.TrimSpace(c.Query("keyword"))
	words, total, err := model.ListSensitiveWords(keyword, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(words)
	common.ApiSuccess(c, pageInfo)
}

// GetSensitiveWord 关键词详情
func GetSensitiveWord(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的关键词 ID")
		return
	}
	w, err := model.GetSensitiveWordById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, w)
}

type sensitiveWordPayload struct {
	Pattern     string `json:"pattern"`
	IsRegex     bool   `json:"is_regex"`
	Enabled     *bool  `json:"enabled"`
	Action      *int   `json:"action"`
	Description string `json:"description"`
}

// normalizeAction 默认 SensitiveActionMonitor（仅记录、不冻结 Token）。
// 仅当显式传入 SensitiveActionBlock 时才走"命中并冻结 token"路径。
func normalizeAction(p *int) int {
	if p == nil {
		return model.SensitiveActionMonitor
	}
	if *p == model.SensitiveActionBlock {
		return model.SensitiveActionBlock
	}
	return model.SensitiveActionMonitor
}

// AddSensitiveWord 新增
func AddSensitiveWord(c *gin.Context) {
	var p sensitiveWordPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		common.ApiError(c, err)
		return
	}
	p.Pattern = strings.TrimSpace(p.Pattern)
	if p.Pattern == "" {
		common.ApiErrorMsg(c, "关键词不能为空")
		return
	}
	if err := validatePattern(p.Pattern, p.IsRegex); err != nil {
		common.ApiError(c, err)
		return
	}
	w := &model.SensitiveWord{
		Pattern:     p.Pattern,
		IsRegex:     p.IsRegex,
		Enabled:     true,
		Action:      normalizeAction(p.Action),
		Description: p.Description,
	}
	if p.Enabled != nil {
		w.Enabled = *p.Enabled
	}
	if err := w.Insert(); err != nil {
		common.ApiError(c, err)
		return
	}
	service.LoadSensitiveWords()
	common.ApiSuccess(c, w)
}

// UpdateSensitiveWord 更新
func UpdateSensitiveWord(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的关键词 ID")
		return
	}
	var p sensitiveWordPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		common.ApiError(c, err)
		return
	}
	p.Pattern = strings.TrimSpace(p.Pattern)
	if p.Pattern == "" {
		common.ApiErrorMsg(c, "关键词不能为空")
		return
	}
	if err := validatePattern(p.Pattern, p.IsRegex); err != nil {
		common.ApiError(c, err)
		return
	}
	w, err := model.GetSensitiveWordById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	w.Pattern = p.Pattern
	w.IsRegex = p.IsRegex
	w.Description = p.Description
	w.Action = normalizeAction(p.Action)
	if p.Enabled != nil {
		w.Enabled = *p.Enabled
	}
	if err := w.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	service.LoadSensitiveWords()
	common.ApiSuccess(c, w)
}

// ToggleSensitiveWord 切换 enabled
func ToggleSensitiveWord(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的关键词 ID")
		return
	}
	w, err := model.GetSensitiveWordById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	w.Enabled = !w.Enabled
	if err := w.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	service.LoadSensitiveWords()
	common.ApiSuccess(c, w)
}

// DeleteSensitiveWord 删除
func DeleteSensitiveWord(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的关键词 ID")
		return
	}
	if err := model.DeleteSensitiveWordById(id); err != nil {
		common.ApiError(c, err)
		return
	}
	service.LoadSensitiveWords()
	common.ApiSuccess(c, nil)
}

// GetAllSensitiveBlockLogs 拦截记录分页列表
func GetAllSensitiveBlockLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	filter := model.SensitiveBlockLogFilter{
		Username:  strings.TrimSpace(c.Query("username")),
		ModelName: strings.TrimSpace(c.Query("model_name")),
		Ip:        strings.TrimSpace(c.Query("ip")),
		Pattern:   strings.TrimSpace(c.Query("pattern")),
		RequestId: strings.TrimSpace(c.Query("request_id")),
	}
	if v, err := strconv.Atoi(c.Query("user_id")); err == nil {
		filter.UserId = v
	}
	if v, err := strconv.Atoi(c.Query("token_id")); err == nil {
		filter.TokenId = v
	}
	if v, err := strconv.Atoi(c.Query("channel_id")); err == nil {
		filter.ChannelId = v
	}
	if v, err := strconv.ParseInt(c.Query("start_timestamp"), 10, 64); err == nil {
		filter.StartTimestamp = v
	}
	if v, err := strconv.ParseInt(c.Query("end_timestamp"), 10, 64); err == nil {
		filter.EndTimestamp = v
	}
	logs, total, err := model.ListSensitiveBlockLogs(filter, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
}

// GetSensitiveBlockLog 命中记录详情
func GetSensitiveBlockLog(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的记录 ID")
		return
	}
	logEntry, err := model.GetSensitiveBlockLogById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, logEntry)
}

type sensitiveBlockTokenTogglePayload struct {
	Disabled bool `json:"disabled"`
}

// GetSensitiveAuditStats 返回异步审计管道实时计数器 + 配置快照。
// GET /api/sensitive_block/stats
// 给前端 master 开关旁的统计面板调用，admin 可观察队列健康度（dropped 多 = queue 容量不够 / worker 慢）。
func GetSensitiveAuditStats(c *gin.Context) {
	enq, proc, drop, fail := service.SensitiveAuditStats()
	common.ApiSuccess(c, gin.H{
		"enqueued":         enq,
		"processed":        proc,
		"dropped":          drop,
		"failed":           fail,
		"queue_depth":      service.SensitiveAuditQueueDepth(),
		"queue_cap":        service.SensitiveAuditQueueCap(),
		"suspicious_users": service.SensitiveSuspiciousUserCount(),
		"async_enabled":    setting.GetSensitiveAsyncEnabled(),
		"sample_rate":      setting.GetSensitiveSampleRate(),
		"dump_to_file":     setting.GetSensitiveDumpToFile(),
		"retention_days":   setting.GetSensitiveDumpRetentionDays(),
		"disk_guard_pct":   setting.GetSensitiveDumpDiskGuardPercent(),
	})
}

// GetSensitiveBlockBody 按需读取命中记录关联的 dump 文件内容。
// GET /api/sensitive_block/:id/body
// 列表/详情接口默认不返回 body（避免大字段），点击"加载完整请求体"才请求本接口。
func GetSensitiveBlockBody(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的记录 ID")
		return
	}
	logEntry, err := model.GetSensitiveBlockLogById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 阶段 1 之前的存量记录：dump_path 为空但 request_body 还在 DB，直接吐回兼容
	if logEntry.DumpPath == "" {
		common.ApiSuccess(c, gin.H{
			"source":      "legacy_db",
			"body":        logEntry.RequestBody,
			"size":        len(logEntry.RequestBody),
			"sha256":      logEntry.BodySha256,
			"dump_exists": false,
		})
		return
	}
	if !logEntry.DumpExists {
		common.ApiErrorMsg(c, "dump 文件已过期被清理")
		return
	}
	dumpPath := logEntry.DumpPath
	if logEntry.AuditJobId != nil {
		job, err := model.GetSensitiveAuditJob(*logEntry.AuditJobId)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if job.DumpState == model.SensitiveAuditDumpDeleted {
			common.ApiErrorMsg(c, "dump 文件已过期被清理")
			return
		}
		if job.StorageNode != common.NodeName {
			common.ApiErrorMsg(c, "dump 文件位于节点 "+job.StorageNode+"，当前节点无法读取")
			return
		}
		dumpPath = job.DumpPath
	}
	payload, err := service.ReadSensitiveDump(dumpPath)
	if err != nil {
		common.SysError("[sensitive] 读 dump 失败 id=" + strconv.Itoa(id) + " path=" + dumpPath + " err=" + err.Error())
		common.ApiErrorMsg(c, "读取 dump 文件失败："+err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{
		"source":      "dump_file",
		"body":        payload.Body,
		"size":        logEntry.BodySize,
		"sha256":      logEntry.BodySha256,
		"dump_exists": true,
		"dump_path":   dumpPath,
		"timestamp":   payload.Timestamp,
	})
}

// ToggleSensitiveBlockToken 在命中记录上对其对应的用户 Token 启用/禁用一键切换。
// POST /api/sensitive_block/:id/toggle_token  body {"disabled": true|false}
// 同步把 sensitive_block_logs.token_disabled 也刷一下，方便列表显示当前状态。
func ToggleSensitiveBlockToken(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的记录 ID")
		return
	}
	var p sensitiveBlockTokenTogglePayload
	if err := c.ShouldBindJSON(&p); err != nil {
		common.ApiError(c, err)
		return
	}
	logEntry, err := model.GetSensitiveBlockLogById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if logEntry.TokenId <= 0 {
		common.ApiErrorMsg(c, "记录未关联 Token")
		return
	}
	disabled := service.SetSensitiveTokenStatus(logEntry.TokenId, p.Disabled)
	if err := model.UpdateSensitiveBlockLogTokenDisabled(id, disabled); err != nil {
		common.SysError("更新命中记录 token_disabled 失败：" + err.Error())
	}
	common.ApiSuccess(c, gin.H{"token_disabled": disabled})
}

// validatePattern 校验关键词格式（正则需要可编译）
func validatePattern(pattern string, isRegex bool) error {
	if isRegex {
		if _, err := regexp.Compile(pattern); err != nil {
			return errors.New("正则表达式无效：" + err.Error())
		}
	}
	return nil
}
