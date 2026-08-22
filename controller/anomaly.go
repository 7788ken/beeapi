package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetAnomalyLogs(c *gin.Context) {
	params := parseAnomalyParams(c)

	logs, total, err := model.GetAnomalyLogs(params)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"data":           logs,
		"total":          total,
		"page_quota_sum": pageQuotaSum(logs),
	})
}

func GetAnomalySummary(c *gin.Context) {
	params := parseAnomalyParams(c)
	summary, err := model.GetAnomalySummary(params)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    summary,
	})
}

func GetAnomalyExport(c *gin.Context) {
	params := parseAnomalyParams(c)
	params.Page = 1
	params.PageSize = 10000

	logs, _, err := model.GetAnomalyLogs(params)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=anomaly_%s.csv", params.Type))
	c.Writer.WriteString("\xEF\xBB\xBF")
	c.Writer.WriteString("ID,Time,Type,User,Model,Quota($),Request ID,Detail\n")
	for _, l := range logs {
		c.Writer.WriteString(fmt.Sprintf("%d,%s,%s,%s,%s,%.4f,%s,\"%s\"\n",
			l.Id,
			formatUnixTime(l.CreatedAt),
			l.AnomalyType,
			csvEscape(l.Username),
			csvEscape(l.ModelName),
			float64(l.Quota)/500000,
			l.RequestId,
			csvEscape(l.AnomalyDetail),
		))
	}
}

func GetAnomalyStats(c *gin.Context) {
	startTimestamp := c.DefaultQuery("start_timestamp", "")
	endTimestamp := c.DefaultQuery("end_timestamp", "")

	var startTime, endTime int64
	if startTimestamp != "" {
		startTime, _ = strconv.ParseInt(startTimestamp, 10, 64)
	}
	if endTimestamp != "" {
		endTime, _ = strconv.ParseInt(endTimestamp, 10, 64)
	}

	stats, err := model.GetAnomalyStats(startTime, endTime)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}

func parseAnomalyParams(c *gin.Context) model.AnomalyQueryParams {
	pageStr := c.DefaultQuery("p", "1")
	pageSizeStr := c.DefaultQuery("page_size", "20")
	startTimestamp := c.DefaultQuery("start_timestamp", "")
	endTimestamp := c.DefaultQuery("end_timestamp", "")

	page, _ := strconv.Atoi(pageStr)
	pageSize, _ := strconv.Atoi(pageSizeStr)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var startTime, endTime int64
	if startTimestamp != "" {
		startTime, _ = strconv.ParseInt(startTimestamp, 10, 64)
	}
	if endTimestamp != "" {
		endTime, _ = strconv.ParseInt(endTimestamp, 10, 64)
	}

	channelId, _ := strconv.Atoi(c.DefaultQuery("channel_id", "0"))

	return model.AnomalyQueryParams{
		Type:      c.DefaultQuery("type", "all"),
		Page:      page,
		PageSize:  pageSize,
		StartTime: startTime,
		EndTime:   endTime,
		Username:  c.DefaultQuery("username", ""),
		ModelName:  c.DefaultQuery("model_name", ""),
		ChannelId: channelId,
	}
}

func pageQuotaSum(logs []model.AnomalyLog) int64 {
	var sum int64
	for _, l := range logs {
		sum += int64(l.Quota)
	}
	return sum
}

func formatUnixTime(ts int64) string {
	if ts == 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
	}
	return s
}
