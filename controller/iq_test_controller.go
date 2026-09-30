package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func iqAPIError(c *gin.Context, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, gorm.ErrRecordNotFound) {
		status = http.StatusNotFound
	}
	if errors.Is(err, service.ErrIQVersionConflict) || errors.Is(err, service.ErrIQDisabled) {
		status = http.StatusConflict
	}
	c.JSON(status, gin.H{"success": false, "message": err.Error()})
}

func iqPositiveID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("id must be a positive integer")
	}
	return id, nil
}

func iqPage(c *gin.Context) (int, int, error) {
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		return 0, 0, fmt.Errorf("invalid page")
	}
	size, err := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil || size < 1 || size > 100 {
		return 0, 0, fmt.Errorf("page_size must be between 1 and 100")
	}
	if page > int(^uint(0)>>1)/size {
		return 0, 0, fmt.Errorf("page is too large")
	}
	return page, size, nil
}

func GetIQTestSetting(c *gin.Context) {
	setting, err := service.GetIQTestSetting()
	if err != nil {
		iqAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": setting})
}

func UpdateIQTestSetting(c *gin.Context) {
	var req struct {
		service.IQTestSetting
		ExpectedVersion *int64 `json:"expected_version"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		iqAPIError(c, err)
		return
	}
	expected := req.Version
	if req.ExpectedVersion != nil {
		expected = *req.ExpectedVersion
	}
	updated, err := service.SaveIQTestSetting(req.IQTestSetting, expected)
	if err != nil {
		iqAPIError(c, err)
		return
	}
	if err := service.ReconcileIQTestState(context.Background()); err != nil {
		iqAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": updated})
}

func GetIQTestModels(c *gin.Context) {
	page, size, err := iqPage(c)
	if err != nil {
		iqAPIError(c, err)
		return
	}
	rows, err := service.ListIQTestModels(c.Query("enabled_only") == "true")
	if err != nil {
		iqAPIError(c, err)
		return
	}
	total := len(rows)
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": rows[start:end], "total": total, "page": page, "page_size": size}})
}

func CreateIQTestModel(c *gin.Context) {
	var req service.IQTestModelInput
	if err := c.ShouldBindJSON(&req); err != nil {
		iqAPIError(c, err)
		return
	}
	row, err := service.CreateIQTestModelInput(req)
	if err != nil {
		iqAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": row})
}

func UpdateIQTestModel(c *gin.Context) {
	id, err := iqPositiveID(c.Param("id"))
	if err != nil {
		iqAPIError(c, err)
		return
	}
	var req service.IQTestModelInput
	if err := c.ShouldBindJSON(&req); err != nil {
		iqAPIError(c, err)
		return
	}
	if err := service.UpdateIQTestModel(id, req); err != nil {
		iqAPIError(c, err)
		return
	}
	if err := service.ReconcileIQTestState(context.Background()); err != nil {
		iqAPIError(c, err)
		return
	}
	row, err := model.GetIQTestModel(id)
	if err != nil {
		iqAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": row})
}

func DeleteIQTestModel(c *gin.Context) {
	id, err := iqPositiveID(c.Param("id"))
	if err != nil {
		iqAPIError(c, err)
		return
	}
	version, err := iqPositiveID(c.Query("version"))
	if err != nil {
		iqAPIError(c, err)
		return
	}
	if err := service.DeleteIQTestModel(id, version); err != nil {
		iqAPIError(c, err)
		return
	}
	if err := service.ReconcileIQTestState(context.Background()); err != nil {
		iqAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func RunIQTestNow(c *gin.Context) {
	var req struct {
		ChannelID      json.RawMessage `json:"channel_id"`
		ModelNames     []string        `json:"model_names"`
		IdempotencyKey string          `json:"idempotency_key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		iqAPIError(c, err)
		return
	}
	channelID := 0
	if len(req.ChannelID) > 0 {
		if err := common.Unmarshal(req.ChannelID, &channelID); err != nil {
			iqAPIError(c, err)
			return
		}
		if channelID <= 0 {
			iqAPIError(c, fmt.Errorf("channel_id must be positive when supplied"))
			return
		}
	}
	run, created, err := service.PrepareIQTestRun(model.IQTestRunTriggerRunNow, c.GetInt("id"), channelID, req.IdempotencyKey, req.ModelNames)
	if errors.Is(err, service.ErrIQBusy) {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error(), "data": run})
		return
	}
	if err != nil {
		iqAPIError(c, err)
		return
	}
	if created {
		if err := service.SubmitIQTestRun(run); err != nil {
			iqAPIError(c, err)
			return
		}
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	c.JSON(status, gin.H{"success": true, "data": gin.H{"run_id": run.RunID, "status": run.Status}})
}

func GetIQTestCoverage(c *gin.Context) {
	channelID := 0
	if raw := c.Query("channel_id"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			iqAPIError(c, fmt.Errorf("channel_id must be positive when supplied"))
			return
		}
		channelID = id
	}
	var modelNames []string
	if raw := c.Query("model_names"); raw != "" {
		modelNames = strings.Split(raw, ",")
	}
	preview, err := service.PreviewIQTestCoverage(channelID, modelNames)
	if err != nil {
		iqAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": preview})
}

func GetIQTestRun(c *gin.Context) {
	run, err := service.GetIQTestRun(c.Param("run_id"))
	if err != nil {
		iqAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": run})
}

func GetIQTestRuns(c *gin.Context) {
	page, size, err := iqPage(c)
	if err != nil {
		iqAPIError(c, err)
		return
	}
	rows, total, err := service.ListIQTestRuns(page, size)
	if err != nil {
		iqAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": rows, "total": total, "page": page, "page_size": size}})
}

func GetIQTestResults(c *gin.Context) {
	page, size, err := iqPage(c)
	if err != nil {
		iqAPIError(c, err)
		return
	}
	channelID := 0
	if raw := c.Query("channel_id"); raw != "" {
		id, err := iqPositiveID(raw)
		if err != nil {
			iqAPIError(c, err)
			return
		}
		channelID = int(id)
	}
	rows, total, err := service.ListIQTestResultsFiltered(c.Query("run_id"), channelID, c.Query("model"), c.Query("status"), page, size)
	if err != nil {
		iqAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": rows, "total": total, "page": page, "page_size": size}})
}

func GetIQTestResult(c *gin.Context) {
	id, err := iqPositiveID(c.Param("id"))
	if err != nil {
		iqAPIError(c, err)
		return
	}
	row, err := service.GetIQTestResult(id)
	if err != nil {
		iqAPIError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": row})
}
