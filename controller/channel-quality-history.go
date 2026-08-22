package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// GetChannelQualityHistory GET /api/channel/:id/quality/history
//
//	?start_timestamp=&end_timestamp=&tz_offset_sec=
//
// 渠道列表质量分 hover"查看更多"弹窗的数据源：按桶趋势 + 错误码/错误模型分布 + 首包均值。
// 默认窗口为最近 24h；范围上限 92 天（service 层校验）。
func GetChannelQualityHistory(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}

	now := common.GetTimestamp()
	startTs, _ := strconv.ParseInt(c.DefaultQuery("start_timestamp", "0"), 10, 64)
	endTs, _ := strconv.ParseInt(c.DefaultQuery("end_timestamp", "0"), 10, 64)
	if endTs <= 0 {
		endTs = now
	}
	if startTs <= 0 {
		startTs = endTs - 86400
	}
	// 与 channel_statistics.go 同约定：本地时区偏移秒，越界回退 0
	tzOffsetSec, _ := strconv.ParseInt(strings.TrimSpace(c.DefaultQuery("tz_offset_sec", "0")), 10, 64)
	if tzOffsetSec < -14*3600 || tzOffsetSec > 14*3600 {
		tzOffsetSec = 0
	}

	result, err := service.GetChannelQualityHistory(c.Request.Context(), channelId, startTs, endTs, tzOffsetSec)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}
