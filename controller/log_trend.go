package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type logTrendRequest struct {
	UserID      *int
	Type        int
	StartTs     int64
	EndTs       int64
	ModelName   string
	Username    string
	TokenName   string
	Channel     int
	Group       string
	RequestID   string
	Bucket      int64
	TZOffsetSec int64
}

func parseLogTrendRequest(c *gin.Context, userID *int) (logTrendRequest, error) {
	parseInt64 := func(name string, fallback int64) (int64, error) {
		raw := strings.TrimSpace(c.Query(name))
		if raw == "" {
			return fallback, nil
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid %s", name)
		}
		return value, nil
	}
	parseInt := func(name string, fallback int) (int, error) {
		value, err := parseInt64(name, int64(fallback))
		if err != nil || value < int64(-1<<31) || value > int64(1<<31-1) {
			return 0, fmt.Errorf("invalid %s", name)
		}
		return int(value), nil
	}

	startTs, err := parseInt64("start_timestamp", 0)
	if err != nil {
		return logTrendRequest{}, err
	}
	endTs, err := parseInt64("end_timestamp", 0)
	if err != nil {
		return logTrendRequest{}, err
	}
	bucket, err := parseInt64("bucket_seconds", 0)
	if err != nil {
		return logTrendRequest{}, err
	}
	tzOffset, err := parseInt64("tz_offset_sec", 0)
	if err != nil {
		return logTrendRequest{}, err
	}
	channel, err := parseInt("channel", 0)
	if err != nil {
		return logTrendRequest{}, err
	}
	if channel < 0 {
		return logTrendRequest{}, fmt.Errorf("invalid channel")
	}

	return logTrendRequest{
		UserID:      userID,
		Type:        model.LogTypeConsume,
		StartTs:     startTs,
		EndTs:       endTs,
		ModelName:   strings.TrimSpace(c.Query("model_name")),
		Username:    strings.TrimSpace(c.Query("username")),
		TokenName:   strings.TrimSpace(c.Query("token_name")),
		Channel:     channel,
		Group:       strings.TrimSpace(c.Query("group")),
		RequestID:   strings.TrimSpace(c.Query("request_id")),
		Bucket:      bucket,
		TZOffsetSec: tzOffset,
	}, nil
}

func logTrendCacheKey(request logTrendRequest) string {
	payload, err := common.Marshal(request)
	if err != nil {
		return "usage-log-trend:v2:invalid"
	}
	scope := "admin"
	userID := 0
	if request.UserID != nil {
		scope = "self"
		userID = *request.UserID
	}
	return model.LogTrendCacheKey(scope, userID, string(payload))
}

func getLogTrend(c *gin.Context, userID *int) {
	// Log aggregates are permission-scoped; do not let a shared proxy cache them.
	c.Header("Cache-Control", "private, no-store")
	request, err := parseLogTrendRequest(c, userID)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	key := logTrendCacheKey(request)
	data, err := model.GetLogTrendCached(
		c.Request.Context(),
		key,
		request.UserID,
		request.Type,
		request.StartTs,
		request.EndTs,
		request.ModelName,
		request.Username,
		request.TokenName,
		request.Channel,
		request.Group,
		request.RequestID,
		request.Bucket,
		request.TZOffsetSec,
	)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
}

func GetLogTrend(c *gin.Context) {
	getLogTrend(c, nil)
}

func GetUserLogTrend(c *gin.Context) {
	userID := c.GetInt("id")
	if userID <= 0 {
		common.ApiErrorMsg(c, "invalid user")
		return
	}
	getLogTrend(c, &userID)
}
