package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// GetChannelUptime GET /api/channel/uptime?hours=24&tz_offset_sec=
//
// 渠道列表「可用性」列的批量数据源：一次返回全部渠道近 N 小时的每小时成功/失败计数。
// hours 取值 1..168（默认 24）；结果在 service 层按 (hours, tz) 进程内缓存 5 分钟。
func GetChannelUptime(c *gin.Context) {
	hours, _ := strconv.Atoi(strings.TrimSpace(c.DefaultQuery("hours", "24")))
	if hours <= 0 {
		hours = 24
	}
	if hours > service.ChannelUptimeMaxHours {
		hours = service.ChannelUptimeMaxHours
	}
	// 与 channel-quality-history.go 同约定：本地时区偏移秒，越界回退 0
	tzOffsetSec, _ := strconv.ParseInt(strings.TrimSpace(c.DefaultQuery("tz_offset_sec", "0")), 10, 64)
	if tzOffsetSec < -14*3600 || tzOffsetSec > 14*3600 {
		tzOffsetSec = 0
	}

	data, err := service.GetChannelUptime(c.Request.Context(), hours, tzOffsetSec)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}
