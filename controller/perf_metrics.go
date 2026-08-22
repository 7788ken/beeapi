package controller

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func GetPerfMetricsSummary(c *gin.Context) {
	hours := 24
	if rawHours := c.Query("hours"); rawHours != "" {
		if parsed, err := strconv.Atoi(rawHours); err == nil {
			hours = parsed
		}
	}

	result, err := perfmetrics.QuerySummaryAll(hours)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

func GetPerfMetrics(c *gin.Context) {
	modelName := c.Query("model")
	if modelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "model is required",
		})
		return
	}

	hours := 24
	if rawHours := c.Query("hours"); rawHours != "" {
		if parsed, err := strconv.Atoi(rawHours); err == nil {
			hours = parsed
		}
	}

	result, err := perfmetrics.Query(perfmetrics.QueryParams{
		Model: modelName,
		Group: c.Query("group"),
		Hours: hours,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	result.Groups = filterActiveGroups(result.Groups)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// GetPerfMetricsGroupUptime returns per-group hourly availability series,
// restricted to the groups visible to the current (possibly anonymous) user.
//
// 口径：同扫 logs 的 type=2/5。真实流量按发生时刻的归属如实计入（含现已禁用渠道的历史）；
// 启用态测活只计入当前 enabled 的分组；禁用期探活（token_name=模型测试-停用）永不计入——
// 与渠道列表「可用性」列（含已禁用渠道、不区分测活状态）不同；详见 service/group_uptime.go 顶部说明。
func GetPerfMetricsGroupUptime(c *gin.Context) {
	// 本接口匿名可访问（TryUserAuth），而每个未命中的 hours 都要扫一次 logs，
	// 因此收敛到白名单档位，避免遍历入参绕过缓存反复打库。
	hours := service.NormalizeGroupUptimeHours(strings.TrimSpace(c.DefaultQuery("hours", "24")))

	result, err := service.GetGroupUptime(c.Request.Context(), hours)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// Anonymous callers have no user row to look up; empty group falls back to
	// the default usable-group view, matching /api/price_changes.
	userGroup := ""
	if userId := c.GetInt("id"); userId > 0 {
		userGroup, _ = model.GetUserGroup(userId, false)
	}
	visibleGroups := service.GetUserVisibleGroups(userGroup)
	groupNames := make([]string, 0, len(visibleGroups))
	for group := range visibleGroups {
		groupNames = append(groupNames, group)
	}
	// 供给判定与零序列填充在 service.FilterGroupUptimeSeries：无供给整窗画红 0%，
	// 有供给无日志留空灰显；分支顺序承重，见其注释。
	filtered := service.FilterGroupUptimeSeries(result, groupNames, hours, time.Now())

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    filtered,
	})
}

func filterActiveGroups(groups []perfmetrics.GroupResult) []perfmetrics.GroupResult {
	activeGroups := ratio_setting.GetGroupRatioCopy()
	filtered := make([]perfmetrics.GroupResult, 0, len(groups))
	for _, g := range groups {
		if _, ok := activeGroups[g.Group]; ok || g.Group == "auto" {
			filtered = append(filtered, g)
		}
	}
	return filtered
}
