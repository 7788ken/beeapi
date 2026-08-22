package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetAllQuotaDates(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	dates, err := model.GetAllQuotaDates(startTimestamp, endTimestamp, username)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func GetQuotaDatesByUser(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	dates, err := model.GetQuotaDataGroupByUser(startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
}

func GetQuotaDatesByGroup(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	billingSource := c.Query("billing_source")
	dates, err := model.GetQuotaDataGroupByGroup(startTimestamp, endTimestamp, username, billingSource)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
}

// GetQuotaDatesByGroupMembers 返回某个分组下消费 top N 的用户
// query: group, start_timestamp, end_timestamp, billing_source, limit
func GetQuotaDatesByGroupMembers(c *gin.Context) {
	group := c.Query("group")
	if group == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "group is required",
		})
		return
	}
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	billingSource := c.Query("billing_source")
	limit, _ := strconv.Atoi(c.Query("limit"))
	dates, err := model.GetQuotaDataByGroupSumByUser(group, startTimestamp, endTimestamp, billingSource, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
}

func GetQuotaDatesByUserGroups(c *gin.Context) {
	username := c.Query("username")
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "username is required",
		})
		return
	}
	startTimestampRaw := c.Query("start_timestamp")
	endTimestampRaw := c.Query("end_timestamp")
	if startTimestampRaw == "" || endTimestampRaw == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "start_timestamp and end_timestamp are required",
		})
		return
	}
	startTimestamp, startErr := strconv.ParseInt(startTimestampRaw, 10, 64)
	endTimestamp, endErr := strconv.ParseInt(endTimestampRaw, 10, 64)
	if startErr != nil || endErr != nil || startTimestamp >= endTimestamp {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "invalid time range",
		})
		return
	}

	userId, err := model.GetUserIdByUsername(username)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiError(c, err)
			return
		}
		// 用户不存在/已删除：userId=0，model 层回退用 username 过滤 quota_data
		userId = 0
	}

	dates, err := model.GetQuotaDataByUserSumByGroup(userId, username, startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if dates == nil {
		// 显式空切片，避免 items 序列化成 JSON null
		dates = make([]*model.QuotaData, 0)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"username": username,
			"user_id":  userId,
			"items":    dates,
		},
	})
}

func GetUserQuotaDates(c *gin.Context) {
	userId := c.GetInt("id")
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	// 判断时间跨度是否超过 1 个月
	if endTimestamp-startTimestamp > 2592000 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "时间跨度不能超过 1 个月",
		})
		return
	}
	dates, err := model.GetQuotaDataByUserId(userId, startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}
