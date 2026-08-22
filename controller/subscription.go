package controller

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---- Shared types ----

type SubscriptionPlanPayload struct {
	Id int `json:"id"`

	Title         string `json:"title"`
	Subtitle      string `json:"subtitle"`
	Description   string `json:"description"`
	CoverImageUrl string `json:"cover_image_url"`

	PriceAmount float64 `json:"price_amount"`
	Currency    string  `json:"currency"`

	DurationUnit  string `json:"duration_unit"`
	DurationValue int    `json:"duration_value"`
	CustomSeconds int64  `json:"custom_seconds"`

	Enabled   bool `json:"enabled"`
	SortOrder int  `json:"sort_order"`

	StripePriceId  string `json:"stripe_price_id"`
	CreemProductId string `json:"creem_product_id"`

	MaxPurchasePerUser int `json:"max_purchase_per_user"`

	// 库存：-1 = 不限量；>=0 = 实际数量
	StockTotal int `json:"stock_total"`
	// 已售出（只读，前端不修改；admin 可参考已售数据）
	StockSold int `json:"stock_sold"`

	UpgradeGroup string  `json:"upgrade_group"`
	BoundGroup   *string `json:"bound_group,omitempty"`

	TotalAmount int64 `json:"total_amount"`

	QuotaResetPeriod        string `json:"quota_reset_period"`
	QuotaResetCustomSeconds int64  `json:"quota_reset_custom_seconds"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

type SubscriptionPlanDTO struct {
	Plan SubscriptionPlanPayload `json:"plan"`
}

type BillingPreferenceRequest struct {
	BillingPreference string `json:"billing_preference"`
}

func optionalStringPtr(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func subscriptionPlanPayloadFromModel(plan model.SubscriptionPlan) SubscriptionPlanPayload {
	return SubscriptionPlanPayload{
		Id:                      plan.Id,
		Title:                   plan.Title,
		Subtitle:                plan.Subtitle,
		Description:             plan.Description,
		CoverImageUrl:           plan.CoverImageUrl,
		PriceAmount:             plan.PriceAmount,
		Currency:                plan.Currency,
		DurationUnit:            plan.DurationUnit,
		DurationValue:           plan.DurationValue,
		CustomSeconds:           plan.CustomSeconds,
		Enabled:                 plan.Enabled,
		SortOrder:               plan.SortOrder,
		StripePriceId:           plan.StripePriceId,
		CreemProductId:          plan.CreemProductId,
		MaxPurchasePerUser:      plan.MaxPurchasePerUser,
		StockTotal:              plan.StockTotal,
		StockSold:               plan.StockSold,
		UpgradeGroup:            plan.UpgradeGroup,
		BoundGroup:              optionalStringPtr(plan.BoundGroup),
		TotalAmount:             plan.TotalAmount,
		QuotaResetPeriod:        plan.QuotaResetPeriod,
		QuotaResetCustomSeconds: plan.QuotaResetCustomSeconds,
		CreatedAt:               plan.CreatedAt,
		UpdatedAt:               plan.UpdatedAt,
	}
}

func newSubscriptionPlanDTO(plan model.SubscriptionPlan) SubscriptionPlanDTO {
	return SubscriptionPlanDTO{
		Plan: subscriptionPlanPayloadFromModel(plan),
	}
}

func (p SubscriptionPlanPayload) ToModel() model.SubscriptionPlan {
	plan := model.SubscriptionPlan{
		Id:                      p.Id,
		Title:                   p.Title,
		Subtitle:                p.Subtitle,
		Description:             p.Description,
		CoverImageUrl:           strings.TrimSpace(p.CoverImageUrl),
		PriceAmount:             p.PriceAmount,
		Currency:                p.Currency,
		DurationUnit:            p.DurationUnit,
		DurationValue:           p.DurationValue,
		CustomSeconds:           p.CustomSeconds,
		Enabled:                 p.Enabled,
		SortOrder:               p.SortOrder,
		StripePriceId:           p.StripePriceId,
		CreemProductId:          p.CreemProductId,
		MaxPurchasePerUser:      p.MaxPurchasePerUser,
		StockTotal:              p.StockTotal,
		StockSold:               p.StockSold,
		UpgradeGroup:            p.UpgradeGroup,
		TotalAmount:             p.TotalAmount,
		QuotaResetPeriod:        p.QuotaResetPeriod,
		QuotaResetCustomSeconds: p.QuotaResetCustomSeconds,
		CreatedAt:               p.CreatedAt,
		UpdatedAt:               p.UpdatedAt,
	}
	if p.BoundGroup != nil {
		plan.BoundGroup = strings.TrimSpace(*p.BoundGroup)
	}
	return plan
}

// ---- User APIs ----

func GetSubscriptionPlans(c *gin.Context) {
	var plans []model.SubscriptionPlan
	if err := model.DB.Where("enabled = ?", true).Order("sort_order desc, id desc").Find(&plans).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]SubscriptionPlanDTO, 0, len(plans))
	for _, p := range plans {
		result = append(result, newSubscriptionPlanDTO(p))
	}
	common.ApiSuccess(c, result)
}

func GetSubscriptionSelf(c *gin.Context) {
	userId := c.GetInt("id")
	settingMap, _ := model.GetUserSetting(userId, false)
	pref := common.NormalizeBillingPreference(settingMap.BillingPreference)

	// Get all subscriptions (including expired)
	allSubscriptions, err := model.GetAllUserSubscriptions(userId)
	if err != nil {
		allSubscriptions = []model.SubscriptionSummary{}
	}

	// Get active subscriptions for backward compatibility
	activeSubscriptions, err := model.GetAllActiveUserSubscriptions(userId)
	if err != nil {
		activeSubscriptions = []model.SubscriptionSummary{}
	}

	common.ApiSuccess(c, gin.H{
		"billing_preference": pref,
		"subscriptions":      activeSubscriptions, // all active subscriptions
		"all_subscriptions":  allSubscriptions,    // all subscriptions including expired
	})
}

// HideSelfSubscription 用户软删除自己已过期/已取消的订阅（is_hidden=true）。
// 拒绝隐藏 active 订阅。隐藏后不计入限购名额、从我的订阅列表移除。
func HideSelfSubscription(c *gin.Context) {
	userId := c.GetInt("id")
	subIdStr := c.Param("id")
	subId, err := strconv.Atoi(subIdStr)
	if err != nil || subId <= 0 {
		common.ApiErrorMsg(c, "invalid subscription id")
		return
	}
	if err := model.HideUserSubscription(userId, subId); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"id": subId, "is_hidden": true})
}

func UpdateSubscriptionPreference(c *gin.Context) {
	userId := c.GetInt("id")
	var req BillingPreferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	pref := common.NormalizeBillingPreference(req.BillingPreference)

	user, err := model.GetUserById(userId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	current := user.GetSetting()
	current.BillingPreference = pref
	user.SetSetting(current)
	if err := user.Update(false); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"billing_preference": pref})
}

// ---- Admin APIs ----

func AdminListSubscriptionPlans(c *gin.Context) {
	var plans []model.SubscriptionPlan
	if err := model.DB.Order("sort_order desc, id desc").Find(&plans).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]SubscriptionPlanDTO, 0, len(plans))
	for _, p := range plans {
		result = append(result, newSubscriptionPlanDTO(p))
	}
	common.ApiSuccess(c, result)
}

type AdminUpsertSubscriptionPlanRequest struct {
	Plan SubscriptionPlanPayload `json:"plan"`
}

func AdminCreateSubscriptionPlan(c *gin.Context) {
	var req AdminUpsertSubscriptionPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	plan := req.Plan.ToModel()
	plan.Id = 0
	if strings.TrimSpace(plan.Title) == "" {
		common.ApiErrorMsg(c, "套餐标题不能为空")
		return
	}
	if plan.PriceAmount < 0 {
		common.ApiErrorMsg(c, "价格不能为负数")
		return
	}
	if plan.PriceAmount > 9999 {
		common.ApiErrorMsg(c, "价格不能超过9999")
		return
	}
	if plan.Currency == "" {
		plan.Currency = "USD"
	}
	plan.Currency = "USD"
	if plan.DurationUnit == "" {
		plan.DurationUnit = model.SubscriptionDurationMonth
	}
	if plan.DurationValue <= 0 && plan.DurationUnit != model.SubscriptionDurationCustom {
		plan.DurationValue = 1
	}
	if plan.MaxPurchasePerUser < 0 {
		common.ApiErrorMsg(c, "购买上限不能为负数")
		return
	}
	if plan.TotalAmount < 0 {
		common.ApiErrorMsg(c, "总额度不能为负数")
		return
	}
	if plan.StockTotal < -1 {
		common.ApiErrorMsg(c, "库存数量不合法（-1=不限量，>=0=固定库存）")
		return
	}
	plan.UpgradeGroup = strings.TrimSpace(plan.UpgradeGroup)
	plan.BoundGroup = strings.TrimSpace(plan.BoundGroup)
	if plan.UpgradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[plan.UpgradeGroup]; !ok {
			common.ApiErrorMsg(c, "升级分组不存在")
			return
		}
	}
	plan.QuotaResetPeriod = model.NormalizeResetPeriod(plan.QuotaResetPeriod)
	if plan.QuotaResetPeriod == model.SubscriptionResetCustom && plan.QuotaResetCustomSeconds <= 0 {
		common.ApiErrorMsg(c, "自定义重置周期需大于0秒")
		return
	}
	err := model.DB.Create(&plan).Error
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(plan.Id)
	common.ApiSuccess(c, newSubscriptionPlanDTO(plan))
}

func AdminUpdateSubscriptionPlan(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	var req AdminUpsertSubscriptionPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	plan := req.Plan.ToModel()
	if strings.TrimSpace(plan.Title) == "" {
		common.ApiErrorMsg(c, "套餐标题不能为空")
		return
	}
	if plan.PriceAmount < 0 {
		common.ApiErrorMsg(c, "价格不能为负数")
		return
	}
	if plan.PriceAmount > 9999 {
		common.ApiErrorMsg(c, "价格不能超过9999")
		return
	}
	plan.Id = id
	if plan.Currency == "" {
		plan.Currency = "USD"
	}
	plan.Currency = "USD"
	if plan.DurationUnit == "" {
		plan.DurationUnit = model.SubscriptionDurationMonth
	}
	if plan.DurationValue <= 0 && plan.DurationUnit != model.SubscriptionDurationCustom {
		plan.DurationValue = 1
	}
	if plan.MaxPurchasePerUser < 0 {
		common.ApiErrorMsg(c, "购买上限不能为负数")
		return
	}
	if plan.TotalAmount < 0 {
		common.ApiErrorMsg(c, "总额度不能为负数")
		return
	}
	if plan.StockTotal < -1 {
		common.ApiErrorMsg(c, "库存数量不合法（-1=不限量，>=0=固定库存）")
		return
	}
	plan.UpgradeGroup = strings.TrimSpace(plan.UpgradeGroup)
	plan.BoundGroup = strings.TrimSpace(plan.BoundGroup)
	if plan.UpgradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[plan.UpgradeGroup]; !ok {
			common.ApiErrorMsg(c, "升级分组不存在")
			return
		}
	}
	plan.QuotaResetPeriod = model.NormalizeResetPeriod(plan.QuotaResetPeriod)
	if plan.QuotaResetPeriod == model.SubscriptionResetCustom && plan.QuotaResetCustomSeconds <= 0 {
		common.ApiErrorMsg(c, "自定义重置周期需大于0秒")
		return
	}

	err := model.DB.Transaction(func(tx *gorm.DB) error {
		// update plan (allow zero values updates with map)
		updateMap := map[string]interface{}{
			"title":                      plan.Title,
			"subtitle":                   plan.Subtitle,
			"description":                plan.Description,
			"cover_image_url":            plan.CoverImageUrl,
			"price_amount":               plan.PriceAmount,
			"currency":                   plan.Currency,
			"duration_unit":              plan.DurationUnit,
			"duration_value":             plan.DurationValue,
			"custom_seconds":             plan.CustomSeconds,
			"enabled":                    plan.Enabled,
			"sort_order":                 plan.SortOrder,
			"stripe_price_id":            plan.StripePriceId,
			"creem_product_id":           plan.CreemProductId,
			"max_purchase_per_user":      plan.MaxPurchasePerUser,
			"stock_total":                plan.StockTotal,
			"total_amount":               plan.TotalAmount,
			"upgrade_group":              plan.UpgradeGroup,
			"bound_group":                plan.BoundGroup,
			"quota_reset_period":         plan.QuotaResetPeriod,
			"quota_reset_custom_seconds": plan.QuotaResetCustomSeconds,
			"updated_at":                 common.GetTimestamp(),
		}
		if err := tx.Model(&model.SubscriptionPlan{}).Where("id = ?", id).Updates(updateMap).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(id)
	common.ApiSuccess(c, nil)
}

type AdminUpdateSubscriptionPlanStatusRequest struct {
	Enabled *bool `json:"enabled"`
}

func AdminUpdateSubscriptionPlanStatus(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	var req AdminUpdateSubscriptionPlanStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	if err := model.DB.Model(&model.SubscriptionPlan{}).Where("id = ?", id).Update("enabled", *req.Enabled).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(id)
	common.ApiSuccess(c, nil)
}

type AdminBindSubscriptionRequest struct {
	UserId int `json:"user_id"`
	PlanId int `json:"plan_id"`
}

func AdminBindSubscription(c *gin.Context) {
	var req AdminBindSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.UserId <= 0 || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	msg, err := model.AdminBindSubscription(req.UserId, req.PlanId, "")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}

// ---- Admin: user subscription management ----

func AdminListUserSubscriptions(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorMsg(c, "无效的用户ID")
		return
	}
	subs, err := model.GetAllUserSubscriptions(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, subs)
}

// AdminListAllUserSubscriptions 全局分页列表
// query: status (active/expired/cancelled/空), username, bound_group, page, page_size
func AdminListAllUserSubscriptions(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	rows, total, err := model.ListAdminUserSubscriptions(model.ListAdminUserSubscriptionsFilter{
		Status:     c.Query("status"),
		Username:   c.Query("username"),
		BoundGroup: c.Query("bound_group"),
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"items":     rows,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// AdminListBoundGroups 返回所有 plan.bound_group 去重列表（用于前端筛选下拉）
func AdminListBoundGroups(c *gin.Context) {
	groups, err := model.ListBoundGroups()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, groups)
}

// AdminGetSubscriptionGroupBudget 按 BoundGroup 聚合 active 订阅每日预算
func AdminGetSubscriptionGroupBudget(c *gin.Context) {
	rows, err := model.EstimateGroupDailyBudget()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

type AdminCreateUserSubscriptionRequest struct {
	PlanId int `json:"plan_id"`
}

type AdminResetSubscriptionRequest struct {
	PlanId           int   `json:"plan_id"`
	AdvanceResetTime *bool `json:"advance_reset_time"`
}

func (r AdminResetSubscriptionRequest) shouldAdvanceResetTime() bool {
	return r.AdvanceResetTime == nil || *r.AdvanceResetTime
}

func subscriptionResetItemsForUser(
	items []model.SubscriptionResetItem,
	userId int,
) []model.SubscriptionResetItem {
	filtered := make([]model.SubscriptionResetItem, 0)
	for _, item := range items {
		if item.UserId == userId {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func recordSubscriptionResetAudit(
	c *gin.Context,
	scope string,
	targetUserId int,
	planId int,
	advanceResetTime bool,
	result *model.SubscriptionResetResult,
	resetErr error,
) {
	adminId := c.GetInt("id")
	requestId := c.GetString(common.RequestIdKey)
	if targetUserId <= 0 {
		targetUserId = adminId
	}
	adminInfo := map[string]interface{}{
		"action":             "subscription_usage_reset",
		"scope":              scope,
		"admin_id":           adminId,
		"admin_username":     c.GetString("username"),
		"request_id":         requestId,
		"target_user_id":     targetUserId,
		"plan_id":            planId,
		"plan_title":         "",
		"matched_count":      0,
		"reset_count":        0,
		"user_count":         0,
		"advance_reset_time": advanceResetTime,
		"before_after":       []model.SubscriptionResetItem{},
		"failure_reason":     "",
	}
	content := "管理员重置订阅额度成功"
	if result != nil {
		adminInfo["plan_id"] = result.PlanId
		adminInfo["plan_title"] = result.PlanTitle
		adminInfo["matched_count"] = result.MatchedCount
		adminInfo["reset_count"] = result.ResetCount
		adminInfo["user_count"] = result.UserCount
		adminInfo["before_after"] = subscriptionResetItemsForUser(result.Items, targetUserId)
	}
	if resetErr != nil {
		content = "管理员重置订阅额度失败"
		adminInfo["failure_reason"] = resetErr.Error()
	}
	if err := model.RecordLogWithAdminInfoAndRequestID(
		targetUserId,
		model.LogTypeManage,
		content,
		adminInfo,
		requestId,
	); err != nil {
		common.SysLog(fmt.Sprintf(
			"failed to record subscription reset audit: request_id=%s target_user_id=%d plan_id=%d error=%v",
			requestId,
			targetUserId,
			planId,
			err,
		))
	}
}

// AdminCreateUserSubscription creates a new user subscription from a plan (no payment).
func AdminCreateUserSubscription(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorMsg(c, "无效的用户ID")
		return
	}
	var req AdminCreateUserSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	msg, err := model.AdminBindSubscription(userId, req.PlanId, "")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}

func AdminResetUserSubscriptionsByPlan(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil || userId <= 0 {
		common.ApiErrorMsg(c, "无效的用户ID")
		return
	}
	var req AdminResetSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	advanceResetTime := req.shouldAdvanceResetTime()
	result, err := model.AdminResetUserSubscriptionsByPlan(userId, req.PlanId, advanceResetTime)
	if err != nil {
		recordSubscriptionResetAudit(c, "user_plan", userId, req.PlanId, advanceResetTime, nil, err)
		common.ApiError(c, err)
		return
	}
	recordSubscriptionResetAudit(c, "user_plan", userId, req.PlanId, advanceResetTime, result, nil)
	common.ApiSuccess(c, result)
}

func AdminResetPlanSubscriptions(c *gin.Context) {
	planId, err := strconv.Atoi(c.Param("id"))
	if err != nil || planId <= 0 {
		common.ApiErrorMsg(c, "无效的套餐ID")
		return
	}
	var req AdminResetSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	advanceResetTime := req.shouldAdvanceResetTime()
	result, err := model.AdminResetPlanSubscriptions(planId, advanceResetTime)
	if err != nil {
		recordSubscriptionResetAudit(c, "plan", 0, planId, advanceResetTime, nil, err)
		common.ApiError(c, err)
		return
	}
	if len(result.AffectedUserIds) == 0 {
		recordSubscriptionResetAudit(c, "plan", 0, planId, advanceResetTime, result, nil)
	} else {
		for _, userId := range result.AffectedUserIds {
			recordSubscriptionResetAudit(c, "plan", userId, planId, advanceResetTime, result, nil)
		}
	}
	common.ApiSuccess(c, result)
}

// AdminInvalidateUserSubscription cancels a user subscription immediately.
func AdminInvalidateUserSubscription(c *gin.Context) {
	subId, _ := strconv.Atoi(c.Param("id"))
	if subId <= 0 {
		common.ApiErrorMsg(c, "无效的订阅ID")
		return
	}
	msg, err := model.AdminInvalidateUserSubscription(subId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}

type AdminUpdateUserSubscriptionExpiryRequest struct {
	EndTime int64 `json:"end_time"`
}

// AdminUpdateUserSubscriptionExpiry updates the end_time of a user subscription.
func AdminUpdateUserSubscriptionExpiry(c *gin.Context) {
	subId, _ := strconv.Atoi(c.Param("id"))
	if subId <= 0 {
		common.ApiErrorMsg(c, "无效的订阅ID")
		return
	}
	var req AdminUpdateUserSubscriptionExpiryRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.EndTime <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	msg, err := model.AdminUpdateUserSubscriptionExpiry(subId, req.EndTime)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminDeleteUserSubscription hard-deletes a user subscription.
func AdminDeleteUserSubscription(c *gin.Context) {
	subId, _ := strconv.Atoi(c.Param("id"))
	if subId <= 0 {
		common.ApiErrorMsg(c, "无效的订阅ID")
		return
	}
	msg, err := model.AdminDeleteUserSubscription(subId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}
