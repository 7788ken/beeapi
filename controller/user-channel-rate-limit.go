package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// 软 RPM 限流管理接口（admin-only，挂在 /api/channel/:id 子组下）。
//
// 行为约定：
//   - POST 创建：body 必须给 user_id + base_rpm，其余可留空走默认（jitter_pct=0.2, delays=50~500, enabled=true）
//   - PUT 更新：PATCH 风格，零值/未给的字段不写入，避免误清掉默认值
//   - DELETE/GET 都以 channel_id 做 scope 校验，防止跨渠道操作

type userChannelRateLimitReq struct {
	UserId     *int64   `json:"user_id"`
	BaseRpm    *int     `json:"base_rpm"`
	JitterPct  *float32 `json:"jitter_pct"`
	MinDelayMs *int     `json:"min_delay_ms"`
	MaxDelayMs *int     `json:"max_delay_ms"`
	Enabled    *bool    `json:"enabled"`
}

// ListChannelUserRpmRules GET /api/channel/:id/user_rpm
func ListChannelUserRpmRules(c *gin.Context) {
	channelId, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items, err := model.ListUserChannelRateLimitsByChannel(channelId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items, "total": len(items)})
}

// CreateChannelUserRpmRule POST /api/channel/:id/user_rpm
func CreateChannelUserRpmRule(c *gin.Context) {
	channelId, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req userChannelRateLimitReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid body: " + err.Error()})
		return
	}
	if req.UserId == nil || req.BaseRpm == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "user_id and base_rpm are required"})
		return
	}
	rule := &model.UserChannelRateLimit{
		UserId:     *req.UserId,
		ChannelId:  channelId,
		BaseRpm:    *req.BaseRpm,
		JitterPct:  0.2,
		MinDelayMs: 50,
		MaxDelayMs: 500,
		Enabled:    true,
	}
	if req.JitterPct != nil {
		rule.JitterPct = *req.JitterPct
	}
	if req.MinDelayMs != nil {
		rule.MinDelayMs = *req.MinDelayMs
	}
	if req.MaxDelayMs != nil {
		rule.MaxDelayMs = *req.MaxDelayMs
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	if err := model.CreateUserChannelRateLimit(rule); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rule)
}

// UpdateChannelUserRpmRule PUT /api/channel/:id/user_rpm/:rule_id
// PATCH 语义：仅更新非 nil 字段，避免覆盖默认值。
func UpdateChannelUserRpmRule(c *gin.Context) {
	channelId, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ruleId, err := strconv.ParseInt(c.Param("rule_id"), 10, 64)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req userChannelRateLimitReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid body: " + err.Error()})
		return
	}
	updates := map[string]any{}
	if req.BaseRpm != nil {
		updates["base_rpm"] = *req.BaseRpm
	}
	if req.JitterPct != nil {
		updates["jitter_pct"] = *req.JitterPct
	}
	if req.MinDelayMs != nil {
		updates["min_delay_ms"] = *req.MinDelayMs
	}
	if req.MaxDelayMs != nil {
		updates["max_delay_ms"] = *req.MaxDelayMs
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if err := model.UpdateUserChannelRateLimit(ruleId, channelId, updates); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"id": ruleId})
}

// DeleteChannelUserRpmRule DELETE /api/channel/:id/user_rpm/:rule_id
func DeleteChannelUserRpmRule(c *gin.Context) {
	channelId, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ruleId, err := strconv.ParseInt(c.Param("rule_id"), 10, 64)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DeleteUserChannelRateLimit(ruleId, channelId); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"id": ruleId})
}
