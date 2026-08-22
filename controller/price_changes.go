package controller

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/price_notify_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 价格变动展示与通知 API。设计见 Docs/price-change-notify-design.md 第 7 节。
// 用户侧 GET /api/price_changes 走 TryUserAuth（匿名可访问，分组过滤与 pricing 页同源）；
// 其余 pending/publish/batches 均 AdminAuth。

const (
	priceChangesMaxDays     = 90
	priceChangesMaxPageSize = 100
	priceChangesDefaultPage = 10
)

// GetPriceChanges GET /api/price_changes?days=30
func GetPriceChanges(c *gin.Context) {
	setting := price_notify_setting.GetPriceNotifySetting()
	if !setting.Enabled {
		c.JSON(200, gin.H{
			"success": true,
			"data": gin.H{
				"enabled":    false,
				"badge_days": setting.GetBadgeDays(),
				"batches":    []service.PriceChangeBatchView{},
			},
		})
		return
	}
	days := setting.GetHistoryDays()
	if raw := c.Query("days"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed < days {
			days = parsed
		}
	}
	if days < 1 {
		days = 1
	}
	if days > priceChangesMaxDays {
		days = priceChangesMaxDays
	}
	viewerGroup := ""
	if userId, exists := c.Get("id"); exists {
		if user, err := model.GetUserCache(userId.(int)); err == nil {
			viewerGroup = user.Group
		}
	}
	batches, err := service.GetVisiblePriceChanges(viewerGroup, days)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"data": gin.H{
			"enabled":    true,
			"badge_days": setting.GetBadgeDays(),
			"batches":    batches,
		},
	})
}

// GetPendingPriceChanges GET /api/price_changes/pending （AdminAuth）
func GetPendingPriceChanges(c *gin.Context) {
	pending, err := service.GetPendingPriceChanges()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "data": pending})
}

type publishPriceChangesRequest struct {
	Note      string `json:"note"`
	SendEmail bool   `json:"send_email"`
}

// PublishPriceChanges POST /api/price_changes/publish （AdminAuth）
func PublishPriceChanges(c *gin.Context) {
	if !price_notify_setting.GetPriceNotifySetting().Enabled {
		c.JSON(200, gin.H{"success": false, "message": "价格变动通知功能未启用"})
		return
	}
	var req publishPriceChangesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	batchId, err := service.PublishPriceChanges(c.GetInt("id"), req.Note, req.SendEmail)
	if err != nil {
		if errors.Is(err, service.ErrNoPendingPriceChanges) {
			c.JSON(200, gin.H{"success": false, "message": "没有待发布的价格变动"})
			return
		}
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "data": gin.H{"batch_id": batchId}})
}

type pricePublishBatchView struct {
	Id             int                      `json:"id"`
	PublishedAt    int64                    `json:"published_at"`
	OperatorId     int                      `json:"operator_id"`
	Note           string                   `json:"note"`
	AffectedGroups []string                 `json:"affected_groups"`
	Summary        service.PriceDiffSummary `json:"summary"`
	IsBaseline     bool                     `json:"is_baseline"`
	EmailState     string                   `json:"email_state"`
	EmailTotal     int                      `json:"email_total"`
	EmailSent      int                      `json:"email_sent"`
	EmailFailed    int                      `json:"email_failed"`
}

type pricePublishBatchDetailView struct {
	pricePublishBatchView
	Items []service.PriceDiffItem `json:"items"`
}

func toPricePublishBatchView(batch *model.PricePublishBatch) pricePublishBatchView {
	view := pricePublishBatchView{
		Id:             batch.Id,
		PublishedAt:    batch.CreatedAt,
		OperatorId:     batch.OperatorId,
		Note:           batch.Note,
		AffectedGroups: []string{},
		IsBaseline:     batch.IsBaseline,
		EmailState:     batch.EmailState,
		EmailTotal:     batch.EmailTotal,
		EmailSent:      batch.EmailSent,
		EmailFailed:    batch.EmailFailed,
	}
	if view.EmailState == "" {
		view.EmailState = model.PriceEmailStateNone
	}
	if batch.AffectedGroups != "" {
		_ = common.UnmarshalJsonStr(batch.AffectedGroups, &view.AffectedGroups)
	}
	if batch.Summary != "" {
		_ = common.UnmarshalJsonStr(batch.Summary, &view.Summary)
	}
	return view
}

// GetPricePublishBatches GET /api/price_changes/batches?p=1&page_size=10 （AdminAuth，含 baseline，倒序）
func GetPricePublishBatches(c *gin.Context) {
	p, _ := strconv.Atoi(c.Query("p"))
	if p < 1 {
		p = 1
	}
	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	if pageSize <= 0 {
		pageSize = priceChangesDefaultPage
	}
	if pageSize > priceChangesMaxPageSize {
		pageSize = priceChangesMaxPageSize
	}
	batches, total, err := model.GetPricePublishBatches((p-1)*pageSize, pageSize)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	views := make([]pricePublishBatchView, 0, len(batches))
	for _, batch := range batches {
		views = append(views, toPricePublishBatchView(batch))
	}
	c.JSON(200, gin.H{
		"success": true,
		"data": gin.H{
			"items": views,
			"total": total,
		},
	})
}

// GetPricePublishBatchDetail GET /api/price_changes/batches/:id （AdminAuth，明细不过滤分组）
func GetPricePublishBatchDetail(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "无效的批次 ID")
		return
	}
	batch, items, err := service.GetPricePublishBatchDetail(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(200, gin.H{"success": false, "message": "批次不存在"})
			return
		}
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"data": pricePublishBatchDetailView{
			pricePublishBatchView: toPricePublishBatchView(batch),
			Items:                 items,
		},
	})
}
