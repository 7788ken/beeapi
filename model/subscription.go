package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/QuantumNous/new-api/setting"
	"github.com/samber/hot"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Subscription duration units
const (
	SubscriptionDurationYear   = "year"
	SubscriptionDurationMonth  = "month"
	SubscriptionDurationDay    = "day"
	SubscriptionDurationHour   = "hour"
	SubscriptionDurationCustom = "custom"
)

// Subscription quota reset period
const (
	SubscriptionResetNever   = "never"
	SubscriptionResetDaily   = "daily"
	SubscriptionResetWeekly  = "weekly"
	SubscriptionResetMonthly = "monthly"
	SubscriptionResetCustom  = "custom"
)

// subscriptionResetLocation 统一订阅重置边界为北京时间 (Asia/Shanghai)，
// 避免容器 TZ 差异导致 daily/weekly/monthly 对齐到非预期时刻。
var subscriptionResetLocation = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	return time.FixedZone("CST", 8*3600)
}()

var (
	ErrSubscriptionOrderNotFound      = errors.New("subscription order not found")
	ErrSubscriptionOrderStatusInvalid = errors.New("subscription order status invalid")
	ErrSubscriptionPlanSoldOut        = errors.New("套餐已售罄")
	ErrSubscriptionPlanInactive       = errors.New("套餐未启用")
	ErrSubscriptionPlanPriceInvalid   = errors.New("套餐金额异常")
	ErrSubscriptionInsufficientQuota  = errors.New("账户余额不足")
	// ErrNoEligibleSubscription 当前请求的 group 没有匹配的活跃订阅；
	// 上层据此决定走钱包回退（subscription_first）或直接报错（subscription_only）。
	ErrNoEligibleSubscription = errors.New("no eligible subscription for current group")
)

// IncrementSubscriptionPlanSoldTx 用乐观锁原子递增 stock_sold；
// 当 stock_total >= 0 且 stock_sold >= stock_total 时 RowsAffected=0，返回 ErrSubscriptionPlanSoldOut。
// stock_total < 0 视为不限量，永远成功。
func IncrementSubscriptionPlanSoldTx(tx *gorm.DB, planId int) error {
	if tx == nil || planId <= 0 {
		return errors.New("invalid plan id")
	}
	res := tx.Model(&SubscriptionPlan{}).
		Where("id = ? AND (stock_total < 0 OR stock_sold < stock_total)", planId).
		Updates(map[string]interface{}{
			"stock_sold": gorm.Expr("stock_sold + 1"),
			"updated_at": common.GetTimestamp(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrSubscriptionPlanSoldOut
	}
	return nil
}

const (
	subscriptionPlanCacheNamespace     = "new-api:subscription_plan:v1"
	subscriptionPlanInfoCacheNamespace = "new-api:subscription_plan_info:v1"
)

var (
	subscriptionPlanCacheOnce     sync.Once
	subscriptionPlanInfoCacheOnce sync.Once

	subscriptionPlanCache     *cachex.HybridCache[SubscriptionPlan]
	subscriptionPlanInfoCache *cachex.HybridCache[SubscriptionPlanInfo]
)

func subscriptionPlanCacheTTL() time.Duration {
	ttlSeconds := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_CACHE_TTL", 300)
	if ttlSeconds <= 0 {
		ttlSeconds = 300
	}
	return time.Duration(ttlSeconds) * time.Second
}

func subscriptionPlanInfoCacheTTL() time.Duration {
	ttlSeconds := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_INFO_CACHE_TTL", 120)
	if ttlSeconds <= 0 {
		ttlSeconds = 120
	}
	return time.Duration(ttlSeconds) * time.Second
}

func subscriptionPlanCacheCapacity() int {
	capacity := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_CACHE_CAP", 5000)
	if capacity <= 0 {
		capacity = 5000
	}
	return capacity
}

func subscriptionPlanInfoCacheCapacity() int {
	capacity := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_INFO_CACHE_CAP", 10000)
	if capacity <= 0 {
		capacity = 10000
	}
	return capacity
}

func getSubscriptionPlanCache() *cachex.HybridCache[SubscriptionPlan] {
	subscriptionPlanCacheOnce.Do(func() {
		ttl := subscriptionPlanCacheTTL()
		subscriptionPlanCache = cachex.NewHybridCache[SubscriptionPlan](cachex.HybridCacheConfig[SubscriptionPlan]{
			Namespace: cachex.Namespace(subscriptionPlanCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[SubscriptionPlan]{},
			Memory: func() *hot.HotCache[string, SubscriptionPlan] {
				return hot.NewHotCache[string, SubscriptionPlan](hot.LRU, subscriptionPlanCacheCapacity()).
					WithTTL(ttl).
					WithJanitor().
					Build()
			},
		})
	})
	return subscriptionPlanCache
}

func getSubscriptionPlanInfoCache() *cachex.HybridCache[SubscriptionPlanInfo] {
	subscriptionPlanInfoCacheOnce.Do(func() {
		ttl := subscriptionPlanInfoCacheTTL()
		subscriptionPlanInfoCache = cachex.NewHybridCache[SubscriptionPlanInfo](cachex.HybridCacheConfig[SubscriptionPlanInfo]{
			Namespace: cachex.Namespace(subscriptionPlanInfoCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[SubscriptionPlanInfo]{},
			Memory: func() *hot.HotCache[string, SubscriptionPlanInfo] {
				return hot.NewHotCache[string, SubscriptionPlanInfo](hot.LRU, subscriptionPlanInfoCacheCapacity()).
					WithTTL(ttl).
					WithJanitor().
					Build()
			},
		})
	})
	return subscriptionPlanInfoCache
}

func subscriptionPlanCacheKey(id int) string {
	if id <= 0 {
		return ""
	}
	return strconv.Itoa(id)
}

func InvalidateSubscriptionPlanCache(planId int) {
	if planId <= 0 {
		return
	}
	cache := getSubscriptionPlanCache()
	_, _ = cache.DeleteMany([]string{subscriptionPlanCacheKey(planId)})
	infoCache := getSubscriptionPlanInfoCache()
	_ = infoCache.Purge()
}

// Subscription plan
type SubscriptionPlan struct {
	Id int `json:"id"`

	Title    string `json:"title" gorm:"type:varchar(128);not null"`
	Subtitle string `json:"subtitle" gorm:"type:varchar(255);default:''"`
	// 套餐介绍（比副标题更长的说明，支持多行；空 = 不展示）
	Description string `json:"description" gorm:"type:text"`

	// 套餐封面图 URL（外链，admin 配置；空 = 不展示）
	// 用 text 兜底带签名的长 URL（CF/OSS 鉴权链可能 >500 字符）
	CoverImageUrl string `json:"cover_image_url" gorm:"type:text"`

	// Display money amount (follow existing code style: float64 for money)
	PriceAmount float64 `json:"price_amount" gorm:"type:decimal(10,6);not null;default:0"`
	Currency    string  `json:"currency" gorm:"type:varchar(8);not null;default:'USD'"`

	DurationUnit  string `json:"duration_unit" gorm:"type:varchar(16);not null;default:'month'"`
	DurationValue int    `json:"duration_value" gorm:"type:int;not null;default:1"`
	CustomSeconds int64  `json:"custom_seconds" gorm:"type:bigint;not null;default:0"`

	Enabled   bool `json:"enabled" gorm:"default:true"`
	SortOrder int  `json:"sort_order" gorm:"type:int;default:0"`

	StripePriceId  string `json:"stripe_price_id" gorm:"type:varchar(128);default:''"`
	CreemProductId string `json:"creem_product_id" gorm:"type:varchar(128);default:''"`

	// Max purchases per user (0 = unlimited)
	MaxPurchasePerUser int `json:"max_purchase_per_user" gorm:"type:int;default:0"`

	// 套餐总库存：-1 = 不限量；>=0 = 实际数量。每售出一份 StockSold +1，受乐观锁保护。
	StockTotal int `json:"stock_total" gorm:"type:int;not null;default:-1"`
	StockSold  int `json:"stock_sold" gorm:"type:int;not null;default:0"`

	// Upgrade user group after purchase (empty = no change)
	UpgradeGroup string `json:"upgrade_group" gorm:"type:varchar(64);default:''"`

	// Bind subscription usage to a specific channel group (empty = no restriction)
	BoundGroup string `json:"bound_group" gorm:"column:bound_group;type:varchar(64);not null;default:''"`

	// Total quota (amount in quota units, 0 = unlimited)
	TotalAmount int64 `json:"total_amount" gorm:"type:bigint;not null;default:0"`

	// Quota reset period for plan
	QuotaResetPeriod        string `json:"quota_reset_period" gorm:"type:varchar(16);default:'never'"`
	QuotaResetCustomSeconds int64  `json:"quota_reset_custom_seconds" gorm:"type:bigint;default:0"`

	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

func (p *SubscriptionPlan) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	p.CreatedAt = now
	p.UpdatedAt = now
	return nil
}

func (p *SubscriptionPlan) BeforeUpdate(tx *gorm.DB) error {
	p.UpdatedAt = common.GetTimestamp()
	return nil
}

// Subscription order (payment -> webhook -> create UserSubscription)
type SubscriptionOrder struct {
	Id     int     `json:"id"`
	UserId int     `json:"user_id" gorm:"index"`
	PlanId int     `json:"plan_id" gorm:"index"`
	Money  float64 `json:"money"`

	TradeNo         string `json:"trade_no" gorm:"unique;type:varchar(255);index"`
	PaymentMethod   string `json:"payment_method" gorm:"type:varchar(50)"`
	PaymentProvider string `json:"payment_provider" gorm:"type:varchar(50);default:''"`
	Status          string `json:"status"`
	CreateTime      int64  `json:"create_time"`
	CompleteTime    int64  `json:"complete_time"`

	ProviderPayload string `json:"provider_payload" gorm:"type:text"`
}

func (o *SubscriptionOrder) Insert() error {
	if o.CreateTime == 0 {
		o.CreateTime = common.GetTimestamp()
	}
	return DB.Create(o).Error
}

func (o *SubscriptionOrder) Update() error {
	return DB.Save(o).Error
}

func GetSubscriptionOrderByTradeNo(tradeNo string) *SubscriptionOrder {
	if tradeNo == "" {
		return nil
	}
	var order SubscriptionOrder
	if err := DB.Where("trade_no = ?", tradeNo).First(&order).Error; err != nil {
		return nil
	}
	return &order
}

func transitionPendingSubscriptionOrderTx(tx *gorm.DB, order *SubscriptionOrder, updates map[string]interface{}) error {
	if tx == nil || order == nil || order.Id <= 0 {
		return ErrSubscriptionOrderNotFound
	}
	return requireSingleRow(
		tx.Model(&SubscriptionOrder{}).
			Where("id = ? AND status = ?", order.Id, common.TopUpStatusPending).
			Updates(updates),
		ErrSubscriptionOrderStatusInvalid,
	)
}

// User subscription instance
type UserSubscription struct {
	Id     int `json:"id"`
	UserId int `json:"user_id" gorm:"index;index:idx_user_sub_active,priority:1"`
	PlanId int `json:"plan_id" gorm:"index"`

	AmountTotal int64 `json:"amount_total" gorm:"type:bigint;not null;default:0"`
	AmountUsed  int64 `json:"amount_used" gorm:"type:bigint;not null;default:0"`

	StartTime int64  `json:"start_time" gorm:"bigint"`
	EndTime   int64  `json:"end_time" gorm:"bigint;index;index:idx_user_sub_active,priority:3"`
	Status    string `json:"status" gorm:"type:varchar(32);index;index:idx_user_sub_active,priority:2"` // active/exhausted/expired/cancelled

	Source string `json:"source" gorm:"type:varchar(32);default:'order'"` // order/admin

	LastResetTime int64 `json:"last_reset_time" gorm:"type:bigint;default:0"`
	NextResetTime int64 `json:"next_reset_time" gorm:"type:bigint;default:0;index"`

	// ExhaustNotifiedAt 额度耗尽处理时间戳（0=未处理）。ProcessExhaustedSubscriptions
	// 用它做扫描过滤与通知去重；周期重置（maybeResetUserSubscriptionWithPlanTx）时清零，
	// 使下一周期耗尽可再次触发处理。
	ExhaustNotifiedAt int64 `json:"exhaust_notified_at" gorm:"type:bigint;not null;default:0"`

	UpgradeGroup  string `json:"upgrade_group" gorm:"type:varchar(64);default:''"`
	PrevUserGroup string `json:"prev_user_group" gorm:"type:varchar(64);default:''"`

	// IsHidden 用户主动从"我的订阅"列表中软删除（仅 expired/cancelled 或 end_time 已过的订阅允许）。
	// 行不删除，仅置 true：从用户视图隐藏，且不再计入 max_purchase_per_user 限购名额。
	// 不加单列索引：基数仅 2，无独立查询用途；过滤都依附于 (user_id, status, end_time) 的复合索引 idx_user_sub_active。
	IsHidden bool `json:"is_hidden" gorm:"not null;default:false"`

	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

func (s *UserSubscription) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	s.CreatedAt = now
	s.UpdatedAt = now
	return nil
}

func (s *UserSubscription) BeforeUpdate(tx *gorm.DB) error {
	s.UpdatedAt = common.GetTimestamp()
	return nil
}

func casUserSubscriptionTx(tx *gorm.DB, snapshot *UserSubscription, updates map[string]interface{}) error {
	if tx == nil || snapshot == nil || snapshot.Id <= 0 {
		return errFinancialStateConflict
	}
	if _, ok := updates["updated_at"]; !ok {
		updates["updated_at"] = common.GetTimestamp()
	}
	return requireSingleRow(
		tx.Model(&UserSubscription{}).
			Where(
				"id = ? AND status = ? AND amount_used = ? AND end_time = ? AND last_reset_time = ? AND next_reset_time = ? AND exhaust_notified_at = ? AND is_hidden = ? AND updated_at = ?",
				snapshot.Id,
				snapshot.Status,
				snapshot.AmountUsed,
				snapshot.EndTime,
				snapshot.LastResetTime,
				snapshot.NextResetTime,
				snapshot.ExhaustNotifiedAt,
				snapshot.IsHidden,
				snapshot.UpdatedAt,
			).
			Updates(updates),
		errFinancialStateConflict,
	)
}

func casDeleteUserSubscriptionTx(tx *gorm.DB, snapshot *UserSubscription) error {
	if tx == nil || snapshot == nil || snapshot.Id <= 0 {
		return errFinancialStateConflict
	}
	return requireSingleRow(
		tx.Where(
			"id = ? AND status = ? AND amount_used = ? AND end_time = ? AND last_reset_time = ? AND next_reset_time = ? AND exhaust_notified_at = ? AND is_hidden = ? AND updated_at = ?",
			snapshot.Id,
			snapshot.Status,
			snapshot.AmountUsed,
			snapshot.EndTime,
			snapshot.LastResetTime,
			snapshot.NextResetTime,
			snapshot.ExhaustNotifiedAt,
			snapshot.IsHidden,
			snapshot.UpdatedAt,
		).Delete(&UserSubscription{}),
		errFinancialStateConflict,
	)
}

type SubscriptionSummary struct {
	Subscription *UserSubscription `json:"subscription"`
	Plan         *SubscriptionPlan `json:"plan,omitempty"`
}

type SubscriptionResetItem struct {
	SubscriptionId   int    `json:"subscription_id"`
	UserId           int    `json:"user_id"`
	PlanId           int    `json:"plan_id"`
	AmountUsedBefore int64  `json:"amount_used_before"`
	AmountUsedAfter  int64  `json:"amount_used_after"`
	StatusBefore     string `json:"status_before"`
	StatusAfter      string `json:"status_after"`
	GroupRestoredTo  string `json:"group_restored_to,omitempty"`
}

type SubscriptionResetResult struct {
	PlanId           int                     `json:"plan_id"`
	PlanTitle        string                  `json:"plan_title"`
	MatchedCount     int                     `json:"matched_count"`
	ResetCount       int                     `json:"reset_count"`
	UserCount        int                     `json:"user_count"`
	AdvanceResetTime bool                    `json:"advance_reset_time"`
	Items            []SubscriptionResetItem `json:"items"`
	AffectedUserIds  []int                   `json:"-"`
}

func calcPlanEndTime(start time.Time, plan *SubscriptionPlan) (int64, error) {
	if plan == nil {
		return 0, errors.New("plan is nil")
	}
	if plan.DurationValue <= 0 && plan.DurationUnit != SubscriptionDurationCustom {
		return 0, errors.New("duration_value must be > 0")
	}
	switch plan.DurationUnit {
	case SubscriptionDurationYear:
		return start.AddDate(plan.DurationValue, 0, 0).Unix(), nil
	case SubscriptionDurationMonth:
		return start.AddDate(0, plan.DurationValue, 0).Unix(), nil
	case SubscriptionDurationDay:
		return start.Add(time.Duration(plan.DurationValue) * 24 * time.Hour).Unix(), nil
	case SubscriptionDurationHour:
		return start.Add(time.Duration(plan.DurationValue) * time.Hour).Unix(), nil
	case SubscriptionDurationCustom:
		if plan.CustomSeconds <= 0 {
			return 0, errors.New("custom_seconds must be > 0")
		}
		return start.Add(time.Duration(plan.CustomSeconds) * time.Second).Unix(), nil
	default:
		return 0, fmt.Errorf("invalid duration_unit: %s", plan.DurationUnit)
	}
}

func NormalizeResetPeriod(period string) string {
	switch strings.TrimSpace(period) {
	case SubscriptionResetDaily, SubscriptionResetWeekly, SubscriptionResetMonthly, SubscriptionResetCustom:
		return strings.TrimSpace(period)
	default:
		return SubscriptionResetNever
	}
}

func calcNextResetTime(base time.Time, plan *SubscriptionPlan, endUnix int64) int64 {
	if plan == nil {
		return 0
	}
	period := NormalizeResetPeriod(plan.QuotaResetPeriod)
	if period == SubscriptionResetNever {
		return 0
	}
	// Daily/Weekly/Monthly 统一对齐到北京时间 00:00；Custom 仍按相对秒数滚动。
	baseCN := base.In(subscriptionResetLocation)
	var next time.Time
	switch period {
	case SubscriptionResetDaily:
		next = time.Date(baseCN.Year(), baseCN.Month(), baseCN.Day(), 0, 0, 0, 0, subscriptionResetLocation).
			AddDate(0, 0, 1)
	case SubscriptionResetWeekly:
		// Align to next Monday 00:00 (Beijing time)
		weekday := int(baseCN.Weekday()) // Sunday=0
		// Convert to Monday=1..Sunday=7
		if weekday == 0 {
			weekday = 7
		}
		daysUntil := 8 - weekday
		next = time.Date(baseCN.Year(), baseCN.Month(), baseCN.Day(), 0, 0, 0, 0, subscriptionResetLocation).
			AddDate(0, 0, daysUntil)
	case SubscriptionResetMonthly:
		// Align to first day of next month 00:00 (Beijing time)
		next = time.Date(baseCN.Year(), baseCN.Month(), 1, 0, 0, 0, 0, subscriptionResetLocation).
			AddDate(0, 1, 0)
	case SubscriptionResetCustom:
		if plan.QuotaResetCustomSeconds <= 0 {
			return 0
		}
		next = base.Add(time.Duration(plan.QuotaResetCustomSeconds) * time.Second)
	default:
		return 0
	}
	if endUnix > 0 && next.Unix() > endUnix {
		return 0
	}
	return next.Unix()
}

func GetSubscriptionPlanById(id int) (*SubscriptionPlan, error) {
	return getSubscriptionPlanByIdTx(nil, id)
}

func getSubscriptionPlanByIdTx(tx *gorm.DB, id int) (*SubscriptionPlan, error) {
	if id <= 0 {
		return nil, errors.New("invalid plan id")
	}
	key := subscriptionPlanCacheKey(id)
	if key != "" {
		if cached, found, err := getSubscriptionPlanCache().Get(key); err == nil && found {
			return &cached, nil
		}
	}
	var plan SubscriptionPlan
	query := DB
	if tx != nil {
		query = tx
	}
	if err := query.Where("id = ?", id).First(&plan).Error; err != nil {
		return nil, err
	}
	_ = getSubscriptionPlanCache().SetWithTTL(key, plan, subscriptionPlanCacheTTL())
	return &plan, nil
}

func CountUserSubscriptionsByPlan(userId int, planId int) (int64, error) {
	if userId <= 0 || planId <= 0 {
		return 0, errors.New("invalid userId or planId")
	}
	var count int64
	// 已被用户软删除（is_hidden=true）的订阅不占用 max_purchase_per_user 限购名额。
	if err := DB.Model(&UserSubscription{}).
		Where("user_id = ? AND plan_id = ? AND is_hidden = ?", userId, planId, false).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func getUserGroupByIdTx(tx *gorm.DB, userId int) (string, error) {
	if userId <= 0 {
		return "", errors.New("invalid userId")
	}
	if tx == nil {
		tx = DB
	}
	var group string
	if err := tx.Model(&User{}).Where("id = ?", userId).Select(commonGroupCol).Find(&group).Error; err != nil {
		return "", err
	}
	return group, nil
}

func downgradeUserGroupForSubscriptionTx(tx *gorm.DB, sub *UserSubscription, now int64) (string, error) {
	if tx == nil || sub == nil {
		return "", errors.New("invalid downgrade args")
	}
	upgradeGroup := strings.TrimSpace(sub.UpgradeGroup)
	if upgradeGroup == "" {
		return "", nil
	}
	currentGroup, err := getUserGroupByIdTx(tx, sub.UserId)
	if err != nil {
		return "", err
	}
	if currentGroup != upgradeGroup {
		return "", nil
	}
	var activeSub UserSubscription
	activeQuery := tx.Where("user_id = ? AND status = ? AND end_time > ? AND id <> ? AND upgrade_group <> ''",
		sub.UserId, "active", now, sub.Id).
		Order("end_time desc, id desc").
		Limit(1).
		Find(&activeSub)
	if activeQuery.Error == nil && activeQuery.RowsAffected > 0 {
		return "", nil
	}
	prevGroup := strings.TrimSpace(sub.PrevUserGroup)
	if prevGroup == "" || prevGroup == currentGroup {
		return "", nil
	}
	if err := tx.Model(&User{}).Where("id = ?", sub.UserId).
		Update("group", prevGroup).Error; err != nil {
		return "", err
	}
	return prevGroup, nil
}

func CreateUserSubscriptionFromPlanTx(tx *gorm.DB, userId int, plan *SubscriptionPlan, source string) (*UserSubscription, error) {
	if tx == nil {
		return nil, errors.New("tx is nil")
	}
	if plan == nil || plan.Id == 0 {
		return nil, errors.New("invalid plan")
	}
	if userId <= 0 {
		return nil, errors.New("invalid user id")
	}
	if plan.MaxPurchasePerUser > 0 {
		var count int64
		// 与 CountUserSubscriptionsByPlan 保持一致：软删除(is_hidden=true)的订阅不占限购名额，事务内二次校验同样排除。
		if err := tx.Model(&UserSubscription{}).
			Where("user_id = ? AND plan_id = ? AND is_hidden = ?", userId, plan.Id, false).
			Count(&count).Error; err != nil {
			return nil, err
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			return nil, errors.New("已达到该套餐购买上限")
		}
	}
	nowUnix := getDBTimestampTx(tx)
	now := time.Unix(nowUnix, 0)
	endUnix, err := calcPlanEndTime(now, plan)
	if err != nil {
		return nil, err
	}
	resetBase := now
	nextReset := calcNextResetTime(resetBase, plan, endUnix)
	lastReset := int64(0)
	if nextReset > 0 {
		lastReset = now.Unix()
	}
	upgradeGroup := strings.TrimSpace(plan.UpgradeGroup)
	prevGroup := ""
	if upgradeGroup != "" {
		currentGroup, err := getUserGroupByIdTx(tx, userId)
		if err != nil {
			return nil, err
		}
		if currentGroup != upgradeGroup {
			prevGroup = currentGroup
			if err := tx.Model(&User{}).Where("id = ?", userId).
				Update("group", upgradeGroup).Error; err != nil {
				return nil, err
			}
		}
	}
	sub := &UserSubscription{
		UserId:        userId,
		PlanId:        plan.Id,
		AmountTotal:   plan.TotalAmount,
		AmountUsed:    0,
		StartTime:     now.Unix(),
		EndTime:       endUnix,
		Status:        "active",
		Source:        source,
		LastResetTime: lastReset,
		NextResetTime: nextReset,
		UpgradeGroup:  upgradeGroup,
		PrevUserGroup: prevGroup,
		CreatedAt:     common.GetTimestamp(),
		UpdatedAt:     common.GetTimestamp(),
	}
	if err := tx.Create(sub).Error; err != nil {
		return nil, err
	}
	return sub, nil
}

// Complete a subscription order (idempotent). Creates a UserSubscription snapshot from the plan.
// expectedPaymentProvider guards against cross-gateway callback attacks (empty skips the check).
// actualPaymentMethod updates the order's PaymentMethod to reflect the real payment type used (empty skips update).
func CompleteSubscriptionOrder(tradeNo string, providerPayload string, expectedPaymentProvider string, actualPaymentMethod string) error {
	if tradeNo == "" {
		return errors.New("tradeNo is empty")
	}
	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}
	var logUserId int
	var logPlanTitle string
	var logMoney float64
	var logPaymentMethod string
	var upgradeGroup string
	err := DB.Transaction(func(tx *gorm.DB) error {
		var order SubscriptionOrder
		if err := withForUpdate(tx).Where(refCol+" = ?", tradeNo).First(&order).Error; err != nil {
			return ErrSubscriptionOrderNotFound
		}
		if expectedPaymentProvider != "" && order.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		if order.Status == common.TopUpStatusSuccess {
			return nil
		}
		if order.Status != common.TopUpStatusPending {
			return ErrSubscriptionOrderStatusInvalid
		}
		// Lock/write the shared topup row before plan, subscription, and user rows.
		// Topup callbacks use TopUp -> User, so acquiring TopUp after changing the
		// user here would create a User <-> TopUp deadlock cycle.
		if err := upsertSubscriptionTopUpTx(tx, &order); err != nil {
			return err
		}
		plan, err := getSubscriptionPlanByIdTx(tx, order.PlanId)
		if err != nil {
			return err
		}
		if !plan.Enabled {
			// still allow completion for already purchased orders
		}
		upgradeGroup = strings.TrimSpace(plan.UpgradeGroup)
		// 库存乐观锁：售罄时拒绝；不限量套餐 (stock_total<0) 始终通过
		if err := IncrementSubscriptionPlanSoldTx(tx, plan.Id); err != nil {
			return err
		}
		_, err = CreateUserSubscriptionFromPlanTx(tx, order.UserId, plan, "order")
		if err != nil {
			return err
		}
		completeTime := common.GetTimestamp()
		orderUpdates := map[string]interface{}{
			"status":        common.TopUpStatusSuccess,
			"complete_time": completeTime,
		}
		if providerPayload != "" {
			order.ProviderPayload = providerPayload
			orderUpdates["provider_payload"] = providerPayload
		}
		if actualPaymentMethod != "" && order.PaymentMethod != actualPaymentMethod {
			order.PaymentMethod = actualPaymentMethod
			orderUpdates["payment_method"] = actualPaymentMethod
		}
		if err := transitionPendingSubscriptionOrderTx(tx, &order, orderUpdates); err != nil {
			return err
		}
		logUserId = order.UserId
		logPlanTitle = plan.Title
		logMoney = order.Money
		logPaymentMethod = order.PaymentMethod
		return nil
	})
	if err != nil {
		return err
	}
	if upgradeGroup != "" && logUserId > 0 {
		_ = UpdateUserGroupCache(logUserId, upgradeGroup)
	}
	if logUserId > 0 {
		msg := fmt.Sprintf("订阅购买成功，套餐: %s，支付金额: %.2f，支付方式: %s", logPlanTitle, logMoney, logPaymentMethod)
		RecordLog(logUserId, LogTypeTopup, msg)
	}
	return nil
}

// CompleteSubscriptionOrderByBalance 用账户余额支付订阅。
// 在事务内原子扣减用户 quota 并创建 UserSubscription，
// 在 logs 中写一条 LogTypeConsume（出现在"使用日志"页，记录此次余额扣费）。
// 余额支付不在账单（topups）中写镜像，与"在线支付双记录"区分。
func CompleteSubscriptionOrderByBalance(tradeNo string) error {
	if tradeNo == "" {
		return errors.New("tradeNo is empty")
	}
	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}
	var (
		logUserId      int
		logPlanTitle   string
		logQuotaCost   int
		logPriceAmount float64
		upgradeGroup   string
	)
	err := DB.Transaction(func(tx *gorm.DB) error {
		var order SubscriptionOrder
		if err := withForUpdate(tx).Where(refCol+" = ?", tradeNo).First(&order).Error; err != nil {
			return ErrSubscriptionOrderNotFound
		}
		if order.PaymentProvider != PaymentProviderBalance {
			return ErrPaymentMethodMismatch
		}
		if order.Status == common.TopUpStatusSuccess {
			return nil
		}
		if order.Status != common.TopUpStatusPending {
			return ErrSubscriptionOrderStatusInvalid
		}
		plan, err := getSubscriptionPlanByIdTx(tx, order.PlanId)
		if err != nil {
			return err
		}
		if !plan.Enabled {
			return ErrSubscriptionPlanInactive
		}
		// 库存乐观锁：先扣库存，售罄则整事务回滚（包括 user.quota 不会被扣）
		if err := IncrementSubscriptionPlanSoldTx(tx, plan.Id); err != nil {
			return err
		}
		quotaCost := int(plan.PriceAmount * common.QuotaPerUnit)
		if quotaCost <= 0 {
			return ErrSubscriptionPlanPriceInvalid
		}
		// 原子扣减：当且仅当 quota >= cost 才更新成功
		res := tx.Model(&User{}).Where("id = ? AND quota >= ?", order.UserId, quotaCost).
			Update("quota", gorm.Expr("quota - ?", quotaCost))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrSubscriptionInsufficientQuota
		}

		upgradeGroup = strings.TrimSpace(plan.UpgradeGroup)
		if _, err := CreateUserSubscriptionFromPlanTx(tx, order.UserId, plan, "order"); err != nil {
			return err
		}
		if err := transitionPendingSubscriptionOrderTx(tx, &order, map[string]interface{}{
			"status":        common.TopUpStatusSuccess,
			"complete_time": common.GetTimestamp(),
		}); err != nil {
			return err
		}

		logUserId = order.UserId
		logPlanTitle = plan.Title
		logQuotaCost = quotaCost
		logPriceAmount = plan.PriceAmount
		return nil
	})
	if err != nil {
		return err
	}
	if upgradeGroup != "" && logUserId > 0 {
		_ = UpdateUserGroupCache(logUserId, upgradeGroup)
	}
	if logUserId > 0 {
		msg := fmt.Sprintf("余额支付购买订阅成功，套餐: %s，扣费: %.2f USD（%d quota）", logPlanTitle, logPriceAmount, logQuotaCost)
		RecordLog(logUserId, LogTypeConsume, msg)
	}
	return nil
}

func upsertSubscriptionTopUpTx(tx *gorm.DB, order *SubscriptionOrder) error {
	if tx == nil || order == nil {
		return errors.New("invalid subscription order")
	}
	now := common.GetTimestamp()
	var topup TopUp
	if err := withForUpdate(tx).Where("trade_no = ?", order.TradeNo).First(&topup).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			topup = TopUp{
				UserId:        order.UserId,
				Amount:        0,
				Money:         order.Money,
				TradeNo:       order.TradeNo,
				PaymentMethod: order.PaymentMethod,
				CreateTime:    order.CreateTime,
				CompleteTime:  now,
				Status:        common.TopUpStatusSuccess,
			}
			return tx.Create(&topup).Error
		}
		return err
	}
	topup.Money = order.Money
	if topup.PaymentMethod == "" {
		topup.PaymentMethod = order.PaymentMethod
	} else if topup.PaymentMethod != order.PaymentMethod {
		return ErrPaymentMethodMismatch
	}
	if topup.CreateTime == 0 {
		topup.CreateTime = order.CreateTime
	}
	topup.CompleteTime = now
	topup.Status = common.TopUpStatusSuccess
	return tx.Save(&topup).Error
}

func ExpireSubscriptionOrder(tradeNo string, expectedPaymentProvider string) error {
	if tradeNo == "" {
		return errors.New("tradeNo is empty")
	}
	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var order SubscriptionOrder
		if err := withForUpdate(tx).Where(refCol+" = ?", tradeNo).First(&order).Error; err != nil {
			return ErrSubscriptionOrderNotFound
		}
		if expectedPaymentProvider != "" && order.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		if order.Status != common.TopUpStatusPending {
			return nil
		}
		return transitionPendingSubscriptionOrderTx(tx, &order, map[string]interface{}{
			"status":        common.TopUpStatusExpired,
			"complete_time": common.GetTimestamp(),
		})
	})
}

// Admin bind (no payment). Creates a UserSubscription from a plan.
func AdminBindSubscription(userId int, planId int, sourceNote string) (string, error) {
	if userId <= 0 || planId <= 0 {
		return "", errors.New("invalid userId or planId")
	}
	plan, err := GetSubscriptionPlanById(planId)
	if err != nil {
		return "", err
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		_, err := CreateUserSubscriptionFromPlanTx(tx, userId, plan, "admin")
		return err
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(plan.UpgradeGroup) != "" {
		_ = UpdateUserGroupCache(userId, plan.UpgradeGroup)
		return fmt.Sprintf("用户分组将升级到 %s", plan.UpgradeGroup), nil
	}
	return "", nil
}

// GetAllActiveUserSubscriptions returns all active subscriptions for a user.
// exhausted（额度耗尽、等待重置/到期）订阅仍属生效周期内，需在"我的订阅"展示。
// is_hidden=true 的订阅被排除（防御性：active 通常不会被隐藏，但软删除规则只允许 expired/cancelled）。
func GetAllActiveUserSubscriptions(userId int) ([]SubscriptionSummary, error) {
	if userId <= 0 {
		return nil, errors.New("invalid userId")
	}
	now := common.GetTimestamp()
	var subs []UserSubscription
	err := DB.Where("user_id = ? AND status IN ? AND end_time > ? AND is_hidden = ?", userId, []string{"active", "exhausted"}, now, false).
		Order("end_time desc, id desc").
		Find(&subs).Error
	if err != nil {
		return nil, err
	}
	return buildSubscriptionSummaries(subs), nil
}

// HasActiveUserSubscription returns whether the user has any active subscription.
// This is a lightweight existence check to avoid heavy pre-consume transactions.
func HasActiveUserSubscription(userId int) (bool, error) {
	if userId <= 0 {
		return false, errors.New("invalid userId")
	}
	now := common.GetTimestamp()
	var count int64
	if err := DB.Model(&UserSubscription{}).
		Where("user_id = ? AND status = ? AND end_time > ? AND is_hidden = ?", userId, "active", now, false).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetActiveUserSubscriptionWithPlan returns the active subscription and its plan for a user.
// MVP assumes at most one active subscription exists at the same time.
func GetActiveUserSubscriptionWithPlan(userId int) (*UserSubscription, *SubscriptionPlan, error) {
	if userId <= 0 {
		return nil, nil, errors.New("invalid userId")
	}
	now := common.GetTimestamp()
	var sub UserSubscription
	if err := DB.Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now).
		Order("end_time desc, id desc").
		First(&sub).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	plan, err := getSubscriptionPlanByIdTx(nil, sub.PlanId)
	if err != nil {
		return nil, nil, err
	}
	return &sub, plan, nil
}

// GetAllUserSubscriptions returns all subscriptions (active and expired) for a user.
// 软删除（is_hidden=true）的订阅从用户视图中隐藏。
func GetAllUserSubscriptions(userId int) ([]SubscriptionSummary, error) {
	if userId <= 0 {
		return nil, errors.New("invalid userId")
	}
	var subs []UserSubscription
	err := DB.Where("user_id = ? AND is_hidden = ?", userId, false).
		Order("end_time desc, id desc").
		Find(&subs).Error
	if err != nil {
		return nil, err
	}
	return buildSubscriptionSummaries(subs), nil
}

func buildSubscriptionSummaries(subs []UserSubscription) []SubscriptionSummary {
	if len(subs) == 0 {
		return []SubscriptionSummary{}
	}
	result := make([]SubscriptionSummary, 0, len(subs))
	for _, sub := range subs {
		subCopy := sub
		summary := SubscriptionSummary{
			Subscription: &subCopy,
		}
		if plan, err := getSubscriptionPlanByIdTx(nil, sub.PlanId); err == nil && plan != nil {
			planCopy := *plan
			summary.Plan = &planCopy
		}
		result = append(result, summary)
	}
	return result
}

// AdminUpdateUserSubscriptionExpiry updates a user subscription's end_time.
// If new end_time > now and current status is not active, reactivates the subscription
// (status -> active) and restores plan's upgrade group when applicable.
// If new end_time <= now, marks the subscription as expired and downgrades user group.
// Recomputes next_reset_time when the plan defines a reset period.
func AdminUpdateUserSubscriptionExpiry(userSubscriptionId int, newEndTime int64) (string, error) {
	if userSubscriptionId <= 0 {
		return "", errors.New("invalid userSubscriptionId")
	}
	if newEndTime <= 0 {
		return "", errors.New("invalid end_time")
	}
	now := common.GetTimestamp()
	cacheGroup := ""
	noticeGroup := ""
	noticeAction := "" // "downgrade" | "upgrade"
	var userId int
	var finalStatus string
	err := DB.Transaction(func(tx *gorm.DB) error {
		var sub UserSubscription
		if err := withForUpdate(tx).
			Where("id = ?", userSubscriptionId).First(&sub).Error; err != nil {
			return err
		}
		userId = sub.UserId
		if sub.StartTime > 0 && newEndTime <= sub.StartTime {
			return errors.New("end_time 不能早于 start_time")
		}

		updates := map[string]interface{}{
			"end_time":   newEndTime,
			"updated_at": now,
		}

		if newEndTime <= now {
			finalStatus = "expired"
			updates["status"] = finalStatus
			updates["next_reset_time"] = int64(0)
			if err := casUserSubscriptionTx(tx, &sub, updates); err != nil {
				return err
			}
			sub.EndTime = newEndTime
			sub.Status = finalStatus
			target, err := downgradeUserGroupForSubscriptionTx(tx, &sub, now)
			if err != nil {
				return err
			}
			if target != "" {
				cacheGroup = target
				noticeGroup = target
				noticeAction = "downgrade"
			}
			return nil
		}

		// newEndTime > now: keep or reactivate
		finalStatus = "active"
		updates["status"] = finalStatus

		// Recompute next_reset_time using plan settings
		plan, err := getSubscriptionPlanByIdTx(tx, sub.PlanId)
		if err == nil && plan != nil {
			base := time.Unix(now, 0)
			updates["next_reset_time"] = calcNextResetTime(base, plan, newEndTime)
		}

		// Reactivation: if previous status was not active, restore upgrade group when applicable
		if sub.Status != "active" {
			upgradeGroup := strings.TrimSpace(sub.UpgradeGroup)
			if upgradeGroup != "" {
				currentGroup, err := getUserGroupByIdTx(tx, sub.UserId)
				if err != nil {
					return err
				}
				if currentGroup != upgradeGroup {
					if err := tx.Model(&User{}).Where("id = ?", sub.UserId).
						Update("group", upgradeGroup).Error; err != nil {
						return err
					}
					updates["prev_user_group"] = currentGroup
					cacheGroup = upgradeGroup
					noticeGroup = upgradeGroup
					noticeAction = "upgrade"
				}
			}
		}

		if err := casUserSubscriptionTx(tx, &sub, updates); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if cacheGroup != "" && userId > 0 {
		_ = UpdateUserGroupCache(userId, cacheGroup)
	}
	endStr := time.Unix(newEndTime, 0).Format("2006-01-02 15:04:05")
	switch noticeAction {
	case "downgrade":
		return fmt.Sprintf("已将有效期改为 %s，订阅已过期，用户分组将回退到 %s", endStr, noticeGroup), nil
	case "upgrade":
		return fmt.Sprintf("已延长有效期至 %s，用户分组已恢复到 %s", endStr, noticeGroup), nil
	}
	if finalStatus == "expired" {
		return fmt.Sprintf("已将有效期改为 %s，订阅已过期", endStr), nil
	}
	return fmt.Sprintf("已更新有效期至 %s", endStr), nil
}

// AdminInvalidateUserSubscription marks a user subscription as cancelled and ends it immediately.
func AdminInvalidateUserSubscription(userSubscriptionId int) (string, error) {
	if userSubscriptionId <= 0 {
		return "", errors.New("invalid userSubscriptionId")
	}
	now := common.GetTimestamp()
	cacheGroup := ""
	downgradeGroup := ""
	var userId int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var sub UserSubscription
		if err := withForUpdate(tx).
			Where("id = ?", userSubscriptionId).First(&sub).Error; err != nil {
			return err
		}
		userId = sub.UserId
		if err := casUserSubscriptionTx(tx, &sub, map[string]interface{}{
			"status":     "cancelled",
			"end_time":   now,
			"updated_at": now,
		}); err != nil {
			return err
		}
		target, err := downgradeUserGroupForSubscriptionTx(tx, &sub, now)
		if err != nil {
			return err
		}
		if target != "" {
			cacheGroup = target
			downgradeGroup = target
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if cacheGroup != "" && userId > 0 {
		_ = UpdateUserGroupCache(userId, cacheGroup)
	}
	if downgradeGroup != "" {
		return fmt.Sprintf("用户分组将回退到 %s", downgradeGroup), nil
	}
	return "", nil
}

// AdminDeleteUserSubscription hard-deletes a user subscription.
// HideUserSubscription 用户软删除自己的非活跃订阅。
// 允许条件（满足任一）：
//   - status != "active"（已是 expired/cancelled）
//   - end_time 已过（即便 status 还是 active，前端按 end_time 也已显示为"已过期"；
//     避免 ExpireDueSubscriptions cron 滞后窗口里前端可点、后端拒绝的语义割裂）。
//
// 行不删除，仅置 is_hidden=true：从用户视图隐藏，且不再计入 max_purchase_per_user 限购名额。
// 拒绝隐藏仍在生效中的订阅（status=active AND end_time>now），避免误删导致无法计费。
// 强制 user_id 校验，防止跨用户越权。
func HideUserSubscription(userId int, userSubscriptionId int) error {
	if userId <= 0 || userSubscriptionId <= 0 {
		return errors.New("invalid userId or userSubscriptionId")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var sub UserSubscription
		if err := withForUpdate(tx).
			Where("id = ? AND user_id = ?", userSubscriptionId, userId).First(&sub).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("subscription not found")
			}
			return err
		}
		if sub.IsHidden {
			return nil // 幂等：已隐藏直接返回成功
		}
		now := common.GetTimestamp()
		// exhausted（额度耗尽）且未到期的订阅同样禁止隐藏：周期重置会将其复活并恢复
		// 用户分组，若已隐藏则复活后不进计费候选，用户会卡在订阅分组按钱包扣费。
		stillActive := (sub.Status == "active" || sub.Status == "exhausted") && sub.EndTime > now
		if stillActive {
			return errors.New("active subscription cannot be hidden")
		}
		return casUserSubscriptionTx(tx, &sub, map[string]interface{}{
			"is_hidden": true,
		})
	})
}

func AdminDeleteUserSubscription(userSubscriptionId int) (string, error) {
	if userSubscriptionId <= 0 {
		return "", errors.New("invalid userSubscriptionId")
	}
	now := common.GetTimestamp()
	cacheGroup := ""
	downgradeGroup := ""
	var userId int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var sub UserSubscription
		if err := withForUpdate(tx).
			Where("id = ?", userSubscriptionId).First(&sub).Error; err != nil {
			return err
		}
		userId = sub.UserId
		target, err := downgradeUserGroupForSubscriptionTx(tx, &sub, now)
		if err != nil {
			return err
		}
		if target != "" {
			cacheGroup = target
			downgradeGroup = target
		}
		if err := casDeleteUserSubscriptionTx(tx, &sub); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if cacheGroup != "" && userId > 0 {
		_ = UpdateUserGroupCache(userId, cacheGroup)
	}
	if downgradeGroup != "" {
		return fmt.Sprintf("用户分组将回退到 %s", downgradeGroup), nil
	}
	return "", nil
}

func resetUserSubscriptionTx(
	tx *gorm.DB,
	sub *UserSubscription,
	plan *SubscriptionPlan,
	now int64,
	advanceResetTime bool,
) (SubscriptionResetItem, error) {
	if tx == nil || sub == nil || plan == nil || sub.Id <= 0 {
		return SubscriptionResetItem{}, errors.New("invalid reset args")
	}
	item := SubscriptionResetItem{
		SubscriptionId:   sub.Id,
		UserId:           sub.UserId,
		PlanId:           sub.PlanId,
		AmountUsedBefore: sub.AmountUsed,
		StatusBefore:     sub.Status,
		AmountUsedAfter:  0,
		StatusAfter:      sub.Status,
	}
	updates := map[string]interface{}{
		"amount_used":         int64(0),
		"exhaust_notified_at": int64(0),
	}
	if sub.Status == "exhausted" {
		updates["status"] = "active"
		item.StatusAfter = "active"
	}
	if advanceResetTime {
		nextReset := calcNextResetTime(time.Unix(now, 0), plan, sub.EndTime)
		lastReset := int64(0)
		if nextReset > 0 {
			lastReset = now
		}
		updates["last_reset_time"] = lastReset
		updates["next_reset_time"] = nextReset
	}
	if err := casUserSubscriptionTx(tx, sub, updates); err != nil {
		return SubscriptionResetItem{}, err
	}

	if sub.Status == "exhausted" {
		upgradeGroup := strings.TrimSpace(sub.UpgradeGroup)
		if upgradeGroup != "" {
			result := tx.Model(&User{}).
				Where(map[string]interface{}{"id": sub.UserId, "group": "auto"}).
				Update("group", upgradeGroup)
			if result.Error != nil {
				return SubscriptionResetItem{}, result.Error
			}
			if result.RowsAffected == 1 {
				item.GroupRestoredTo = upgradeGroup
			}
		}
	}
	return item, nil
}

func buildSubscriptionResetResult(
	plan *SubscriptionPlan,
	items []SubscriptionResetItem,
	advanceResetTime bool,
) *SubscriptionResetResult {
	userIds := make([]int, 0, len(items))
	seenUsers := make(map[int]struct{}, len(items))
	for _, item := range items {
		if _, ok := seenUsers[item.UserId]; ok {
			continue
		}
		seenUsers[item.UserId] = struct{}{}
		userIds = append(userIds, item.UserId)
	}
	return &SubscriptionResetResult{
		PlanId:           plan.Id,
		PlanTitle:        plan.Title,
		MatchedCount:     len(items),
		ResetCount:       len(items),
		UserCount:        len(userIds),
		AdvanceResetTime: advanceResetTime,
		Items:            items,
		AffectedUserIds:  userIds,
	}
}

func loadSubscriptionResetPlanTx(tx *gorm.DB, planId int) (*SubscriptionPlan, error) {
	if tx == nil || planId <= 0 {
		return nil, errors.New("invalid reset plan")
	}
	var plan SubscriptionPlan
	if err := tx.Where("id = ?", planId).First(&plan).Error; err != nil {
		return nil, err
	}
	return &plan, nil
}

func resetSubscriptionsTx(
	tx *gorm.DB,
	plan *SubscriptionPlan,
	subs []UserSubscription,
	now int64,
	advanceResetTime bool,
) (*SubscriptionResetResult, error) {
	items := make([]SubscriptionResetItem, 0, len(subs))
	for i := range subs {
		item, err := resetUserSubscriptionTx(tx, &subs[i], plan, now, advanceResetTime)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return buildSubscriptionResetResult(plan, items, advanceResetTime), nil
}

func refreshSubscriptionResetUserCaches(result *SubscriptionResetResult) {
	if result == nil {
		return
	}
	for _, userId := range result.AffectedUserIds {
		if err := UpdateUserGroupCache(userId, ""); err != nil {
			common.SysLog(fmt.Sprintf("failed to refresh user auth cache after subscription reset: user_id=%d error=%v", userId, err))
		}
	}
}

func AdminResetUserSubscriptionsByPlan(
	userId int,
	planId int,
	advanceResetTime bool,
) (*SubscriptionResetResult, error) {
	if userId <= 0 || planId <= 0 {
		return nil, errors.New("invalid userId or planId")
	}
	now := GetDBTimestamp()
	var result *SubscriptionResetResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		plan, err := loadSubscriptionResetPlanTx(tx, planId)
		if err != nil {
			return err
		}
		var subs []UserSubscription
		if err := withForUpdate(tx).
			Where(
				"user_id = ? AND plan_id = ? AND status IN ? AND end_time > ? AND is_hidden = ?",
				userId,
				planId,
				[]string{"active", "exhausted"},
				now,
				false,
			).
			Order("end_time asc, id asc").
			Find(&subs).Error; err != nil {
			return err
		}
		if len(subs) == 0 {
			return errors.New("该用户没有有效的此套餐订阅")
		}
		result, err = resetSubscriptionsTx(tx, plan, subs, now, advanceResetTime)
		return err
	})
	if err != nil {
		return nil, err
	}
	refreshSubscriptionResetUserCaches(result)
	return result, nil
}

func AdminResetPlanSubscriptions(planId int, advanceResetTime bool) (*SubscriptionResetResult, error) {
	if planId <= 0 {
		return nil, errors.New("invalid planId")
	}
	now := GetDBTimestamp()
	var result *SubscriptionResetResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		plan, err := loadSubscriptionResetPlanTx(tx, planId)
		if err != nil {
			return err
		}
		var subs []UserSubscription
		if err := withForUpdate(tx).
			Where(
				"plan_id = ? AND status IN ? AND end_time > ? AND is_hidden = ?",
				planId,
				[]string{"active", "exhausted"},
				now,
				false,
			).
			Order("user_id asc, end_time asc, id asc").
			Find(&subs).Error; err != nil {
			return err
		}
		result, err = resetSubscriptionsTx(tx, plan, subs, now, advanceResetTime)
		return err
	})
	if err != nil {
		return nil, err
	}
	refreshSubscriptionResetUserCaches(result)
	return result, nil
}

type SubscriptionPreConsumeResult struct {
	UserSubscriptionId int
	PreConsumed        int64
	AmountTotal        int64
	AmountUsedBefore   int64
	AmountUsedAfter    int64
}

// ExpireDueSubscriptions marks expired subscriptions and handles group downgrade.
func ExpireDueSubscriptions(limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	now := GetDBTimestamp()
	var subs []UserSubscription
	// exhausted（额度耗尽）订阅到期时同样收尾为 expired；
	// 其用户分组已是 auto（≠ upgrade_group），下方降级逻辑天然 no-op。
	if err := DB.Where("status IN ? AND end_time > 0 AND end_time <= ?", []string{"active", "exhausted"}, now).
		Order("end_time asc, id asc").
		Limit(limit).
		Find(&subs).Error; err != nil {
		return 0, err
	}
	if len(subs) == 0 {
		return 0, nil
	}
	expiredCount := 0
	userIds := make(map[int]struct{}, len(subs))
	for _, sub := range subs {
		if sub.UserId > 0 {
			userIds[sub.UserId] = struct{}{}
		}
	}
	for userId := range userIds {
		cacheGroup := ""
		err := DB.Transaction(func(tx *gorm.DB) error {
			res := tx.Model(&UserSubscription{}).
				Where("user_id = ? AND status IN ? AND end_time > 0 AND end_time <= ?", userId, []string{"active", "exhausted"}, now).
				Updates(map[string]interface{}{
					"status":     "expired",
					"updated_at": common.GetTimestamp(),
				})
			if res.Error != nil {
				return res.Error
			}
			expiredCount += int(res.RowsAffected)

			// If there's an active upgraded subscription, keep current group.
			var activeSub UserSubscription
			activeQuery := tx.Where("user_id = ? AND status = ? AND end_time > ? AND upgrade_group <> ''",
				userId, "active", now).
				Order("end_time desc, id desc").
				Limit(1).
				Find(&activeSub)
			if activeQuery.Error == nil && activeQuery.RowsAffected > 0 {
				return nil
			}

			// No active upgraded subscription, downgrade to previous group if needed.
			var lastExpired UserSubscription
			expiredQuery := tx.Where("user_id = ? AND status = ? AND upgrade_group <> ''",
				userId, "expired").
				Order("end_time desc, id desc").
				Limit(1).
				Find(&lastExpired)
			if expiredQuery.Error != nil || expiredQuery.RowsAffected == 0 {
				return nil
			}
			upgradeGroup := strings.TrimSpace(lastExpired.UpgradeGroup)
			prevGroup := strings.TrimSpace(lastExpired.PrevUserGroup)
			if upgradeGroup == "" || prevGroup == "" {
				return nil
			}
			currentGroup, err := getUserGroupByIdTx(tx, userId)
			if err != nil {
				return err
			}
			if currentGroup != upgradeGroup || currentGroup == prevGroup {
				return nil
			}
			if err := tx.Model(&User{}).Where("id = ?", userId).
				Update("group", prevGroup).Error; err != nil {
				return err
			}
			cacheGroup = prevGroup
			return nil
		})
		if err != nil {
			return expiredCount, err
		}
		if cacheGroup != "" {
			_ = UpdateUserGroupCache(userId, cacheGroup)
		}
	}
	return expiredCount, nil
}

// SubscriptionExhaustedEvent 描述一条已完成耗尽处理的订阅，供 service 层发送通知。
type SubscriptionExhaustedEvent struct {
	UserId           int
	UserEmail        string
	UserSetting      dto.UserSetting
	PlanTitle        string
	Pref             string // 归一化后的计费偏好
	DowngradedToAuto bool   // true=用户分组已降级为 auto
	// GroupExhausted true=该订阅绑定分组（BoundGroup）内已无任何可消费订阅，
	// 即本次耗尽真正中断了该分组的订阅抵扣。仅此时才发用户通知；
	// 分组内还有其他可用订阅时服务无变化，不打扰用户。
	GroupExhausted bool
}

// ProcessExhaustedSubscriptions 处理额度已耗尽但仍为 active 的订阅（cron 调用，Reset 之后执行）。
// 按用户计费偏好分派：
//   - subscription_then_auto：status 置为 exhausted，并在满足降级条件（分组确因本订阅
//     升级、且无其他 active 升级订阅）时把 user.group 降为 auto，转由钱包按量继续服务；
//   - 其他偏好：仅落 exhaust_notified_at 标记，status 保持 active（请求路径继续 403），
//     等待续费 / 周期重置 / 到期。
//
// exhaust_notified_at 兼做幂等标记：每次耗尽只处理并通知一次，周期重置时清零。
func ProcessExhaustedSubscriptions(limit int) (int, []SubscriptionExhaustedEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	now := GetDBTimestamp()
	var subs []UserSubscription
	// 已到重置点的订阅交给 ResetDueSubscriptions 复活，不在此处理。
	if err := DB.Where("status = ? AND amount_total > 0 AND amount_used >= amount_total AND exhaust_notified_at = 0 AND end_time > ? AND (next_reset_time = 0 OR next_reset_time > ?)",
		"active", now, now).
		Order("id asc").
		Limit(limit).
		Find(&subs).Error; err != nil {
		return 0, nil, err
	}
	if len(subs) == 0 {
		return 0, nil, nil
	}
	processed := 0
	events := make([]SubscriptionExhaustedEvent, 0, len(subs))
	for _, sub := range subs {
		subCopy := sub
		event := SubscriptionExhaustedEvent{}
		handled := false
		err := DB.Transaction(func(tx *gorm.DB) error {
			var locked UserSubscription
			if err := withForUpdate(tx).
				Where("id = ?", subCopy.Id).First(&locked).Error; err != nil {
				return err
			}
			// 锁内重验：结算退款可能使用量回落，重置/到期任务可能已改状态。
			if locked.Status != "active" || locked.ExhaustNotifiedAt != 0 ||
				locked.AmountTotal <= 0 || locked.AmountUsed < locked.AmountTotal {
				return nil
			}
			var user User
			userQuery := tx.Select("id", "email", "setting").
				Where("id = ?", locked.UserId).Limit(1).Find(&user)
			if userQuery.Error != nil {
				return userQuery.Error
			}
			if userQuery.RowsAffected == 0 {
				// 用户已不存在，仅落标记避免重复扫描。
				return casUserSubscriptionTx(tx, &locked, map[string]interface{}{
					"exhaust_notified_at": now,
					"updated_at":          common.GetTimestamp(),
				})
			}
			pref := common.NormalizeBillingPreference(user.GetSetting().BillingPreference)
			// plan 提供 BoundGroup（分组耗尽判定口径）与 Title（通知文案）。
			plan, planErr := getSubscriptionPlanByIdTx(tx, locked.PlanId)
			if planErr != nil {
				return planErr
			}
			planTitle := ""
			groupExhausted := true
			if plan != nil {
				planTitle = plan.Title
				// 同 BoundGroup 内是否还有"未处理"的 active 订阅（exhaust_notified_at=0）：
				// 有量的订阅（组仍可用）或满额待处理的订阅（发信责任接力给它）都算，
				// 保证分组耗尽通知恰好由组内最后一个被处理的订阅触发一次。
				var remainCount int64
				if err := tx.Table("user_subscriptions us").
					Joins("JOIN subscription_plans p ON p.id = us.plan_id").
					Where("us.user_id = ? AND us.status = ? AND us.exhaust_notified_at = 0 AND us.end_time > ? AND us.is_hidden = ? AND us.id <> ?",
						locked.UserId, "active", now, false, locked.Id).
					Where("p.bound_group = ?", plan.BoundGroup).
					Count(&remainCount).Error; err != nil {
					return err
				}
				groupExhausted = remainCount == 0
			}
			updates := map[string]interface{}{
				"exhaust_notified_at": now,
				"updated_at":          common.GetTimestamp(),
			}
			downgraded := false
			if pref == "subscription_then_auto" {
				updates["status"] = "exhausted"
				// 降级判定按同 UpgradeGroup 维度：其他分组的生效订阅不阻止本组降级
				//（用户分组只可能被同组订阅撑着；异组订阅依赖 token 分组，与 user.group 无关）。
				// 同口径用 exhaust_notified_at=0：同组还有未处理订阅时降级责任接力给它。
				upgradeGroup := strings.TrimSpace(locked.UpgradeGroup)
				if upgradeGroup != "" {
					currentGroup, err := getUserGroupByIdTx(tx, locked.UserId)
					if err != nil {
						return err
					}
					if currentGroup == upgradeGroup && currentGroup != "auto" {
						var activeSub UserSubscription
						activeQuery := tx.Where("user_id = ? AND status = ? AND end_time > ? AND id <> ? AND upgrade_group = ? AND exhaust_notified_at = 0",
							locked.UserId, "active", now, locked.Id, upgradeGroup).
							Limit(1).
							Find(&activeSub)
						if activeQuery.Error != nil {
							return activeQuery.Error
						}
						if activeQuery.RowsAffected == 0 {
							if err := tx.Model(&User{}).Where("id = ?", locked.UserId).
								Update("group", "auto").Error; err != nil {
								return err
							}
							downgraded = true
						}
					}
				}
			}
			if err := casUserSubscriptionTx(tx, &locked, updates); err != nil {
				return err
			}
			event = SubscriptionExhaustedEvent{
				UserId:           locked.UserId,
				UserEmail:        user.Email,
				UserSetting:      user.GetSetting(),
				PlanTitle:        planTitle,
				Pref:             pref,
				DowngradedToAuto: downgraded,
				GroupExhausted:   groupExhausted,
			}
			handled = true
			return nil
		})
		if err != nil {
			return processed, events, err
		}
		if handled {
			processed++
			if event.DowngradedToAuto {
				_ = UpdateUserGroupCache(event.UserId, "auto")
			}
			events = append(events, event)
		}
	}
	return processed, events, nil
}

// SubscriptionPreConsumeRecord stores idempotent pre-consume operations per request.
type SubscriptionPreConsumeRecord struct {
	Id                 int    `json:"id"`
	RequestId          string `json:"request_id" gorm:"type:varchar(64);uniqueIndex"`
	UserId             int    `json:"user_id" gorm:"index"`
	TokenId            int    `json:"token_id" gorm:"index"`
	UserSubscriptionId int    `json:"user_subscription_id" gorm:"index"`
	PreConsumed        int64  `json:"pre_consumed" gorm:"type:bigint;not null;default:0"`
	Status             string `json:"status" gorm:"type:varchar(32);index"` // consumed/refunded
	CreatedAt          int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt          int64  `json:"updated_at" gorm:"bigint;index"`
}

func (r *SubscriptionPreConsumeRecord) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	r.CreatedAt = now
	r.UpdatedAt = now
	return nil
}

func (r *SubscriptionPreConsumeRecord) BeforeUpdate(tx *gorm.DB) error {
	r.UpdatedAt = common.GetTimestamp()
	return nil
}

// maybeResetUserSubscriptionWithPlanTx 按套餐周期滚动重置额度。
// 返回 advanced=true 表示本次确实发生了重置（AmountUsed 已清零）。
func maybeResetUserSubscriptionWithPlanTx(tx *gorm.DB, sub *UserSubscription, plan *SubscriptionPlan, now int64) (bool, error) {
	if tx == nil || sub == nil || plan == nil {
		return false, errors.New("invalid reset args")
	}
	if sub.NextResetTime > 0 && sub.NextResetTime > now {
		return false, nil
	}
	if NormalizeResetPeriod(plan.QuotaResetPeriod) == SubscriptionResetNever {
		return false, nil
	}
	baseUnix := sub.LastResetTime
	if baseUnix <= 0 {
		baseUnix = sub.StartTime
	}
	base := time.Unix(baseUnix, 0)
	next := calcNextResetTime(base, plan, sub.EndTime)
	advanced := false
	for next > 0 && next <= now {
		advanced = true
		base = time.Unix(next, 0)
		next = calcNextResetTime(base, plan, sub.EndTime)
	}
	if !advanced {
		if sub.NextResetTime == 0 && next > 0 {
			updates := map[string]interface{}{
				"next_reset_time": next,
				"last_reset_time": base.Unix(),
			}
			if err := casUserSubscriptionTx(tx, sub, updates); err != nil {
				return false, err
			}
			sub.NextResetTime = next
			sub.LastResetTime = base.Unix()
			sub.UpdatedAt = updates["updated_at"].(int64)
			return false, nil
		}
		return false, nil
	}
	updates := map[string]interface{}{
		"amount_used":         int64(0),
		"exhaust_notified_at": int64(0),
		"last_reset_time":     base.Unix(),
		"next_reset_time":     next,
	}
	if err := casUserSubscriptionTx(tx, sub, updates); err != nil {
		return false, err
	}
	sub.AmountUsed = 0
	sub.ExhaustNotifiedAt = 0
	sub.LastResetTime = base.Unix()
	sub.NextResetTime = next
	sub.UpdatedAt = updates["updated_at"].(int64)
	return true, nil
}

// matchBoundGroup 按当前模式判断 plan.BoundGroup 是否可被 usingGroup 消费。
// 严格模式（默认）：双方都必须非空且相等。
// 兼容模式：BoundGroup="" 视为通用订阅，可被任意非空 usingGroup 消费。
func matchBoundGroup(boundGroup, usingGroup string) bool {
	if usingGroup == "" {
		return false
	}
	if boundGroup == "" {
		return !setting.SubscriptionStrictGroupIsolation
	}
	return boundGroup == usingGroup
}

// findEligibleSubscriptionsTx 返回当前请求 group 可用、状态在 statuses 内的订阅，按 end_time asc, id asc 排序。
// usingGroup == "" 时直接返回空（严格隔离：无 group 上下文不允许消费订阅）。
// 同时返回 planId -> *SubscriptionPlan 的映射，避免外层 N+1 查询。
// 调用方需在事务中使用，调用前已锁定相关记录。
func findEligibleSubscriptionsTx(tx *gorm.DB, userId int, usingGroup string, now int64, statuses []string) (
	[]UserSubscription, map[int]*SubscriptionPlan, error,
) {
	if tx == nil {
		return nil, nil, errors.New("tx is nil")
	}
	if usingGroup == "" {
		return nil, nil, nil
	}
	var subs []UserSubscription
	if err := withForUpdate(tx).
		Where("user_id = ? AND status IN ? AND end_time > ? AND is_hidden = ?", userId, statuses, now, false).
		Order("end_time asc, id asc").
		Find(&subs).Error; err != nil {
		return nil, nil, err
	}
	if len(subs) == 0 {
		return nil, nil, nil
	}
	planMap := make(map[int]*SubscriptionPlan, len(subs))
	filtered := make([]UserSubscription, 0, len(subs))
	for _, s := range subs {
		plan, err := getSubscriptionPlanByIdTx(tx, s.PlanId)
		if err != nil {
			return nil, nil, err
		}
		if plan == nil {
			continue
		}
		planMap[plan.Id] = plan
		if !matchBoundGroup(plan.BoundGroup, usingGroup) {
			continue
		}
		filtered = append(filtered, s)
	}
	return filtered, planMap, nil
}

// GetEligibleActiveSubscription 返回当前请求 group 可用的首条活跃订阅（按 end_time asc, id asc）。
// 用于 BillingSession 入口预判：决定走订阅扣费还是钱包回退。
// 第三个返回值 hasExhausted 表示该 group 无活跃订阅、但存在 exhausted（额度耗尽、未到期）
// 订阅——此时调用方不得回退钱包（该分组套餐价只对订阅额度有效），应直接拒绝。
// 返回 (nil, nil, false, nil) 表示与该 group 无任何订阅关系。
func GetEligibleActiveSubscription(userId int, usingGroup string) (*UserSubscription, *SubscriptionPlan, bool, error) {
	if userId <= 0 {
		return nil, nil, false, errors.New("invalid userId")
	}
	if usingGroup == "" {
		return nil, nil, false, nil
	}
	var (
		retSub       *UserSubscription
		retPlan      *SubscriptionPlan
		hasExhausted bool
	)
	// GetDBTimestamp 是一次独立的 DB 查询，必须在事务外取，否则在
	// SetMaxOpenConns(1) 的环境（含测试 SQLite）会与事务自身争连接死锁。
	now := GetDBTimestamp()
	err := DB.Transaction(func(tx *gorm.DB) error {
		subs, planMap, err := findEligibleSubscriptionsTx(tx, userId, usingGroup, now, []string{"active", "exhausted"})
		if err != nil {
			return err
		}
		for i := range subs {
			if subs[i].Status == "exhausted" {
				hasExhausted = true
				continue
			}
			if retSub == nil {
				s := subs[i]
				retSub = &s
				retPlan = planMap[s.PlanId]
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, false, err
	}
	return retSub, retPlan, hasExhausted, nil
}

// PreConsumeUserSubscription pre-consumes from an active subscription whose plan.BoundGroup matches usingGroup.
// usingGroup 必填：严格模式下空 usingGroup 直接返回 ErrNoEligibleSubscription，不消费任何订阅。
// quotaType 参数保留以兼容旧签名（暂未使用）。
func PreConsumeUserSubscription(requestId string, userId int, modelName string, quotaType int, amount int64, usingGroup string) (*SubscriptionPreConsumeResult, error) {
	return preConsumeUserSubscription(requestId, userId, 0, modelName, quotaType, amount, usingGroup)
}

// PreConsumeUserSubscriptionToken reserves subscription and token quota in one
// main-database transaction. The durable request record binds retries and
// refunds to the same subscription/token pair.
func PreConsumeUserSubscriptionToken(requestId string, userId int, tokenId int, modelName string, quotaType int, amount int64, usingGroup string) (*SubscriptionPreConsumeResult, error) {
	if tokenId <= 0 {
		return nil, errors.New("invalid tokenId")
	}
	return preConsumeUserSubscription(requestId, userId, tokenId, modelName, quotaType, amount, usingGroup)
}

func preConsumeUserSubscription(requestId string, userId int, tokenId int, modelName string, quotaType int, amount int64, usingGroup string) (*SubscriptionPreConsumeResult, error) {
	if userId <= 0 {
		return nil, errors.New("invalid userId")
	}
	if strings.TrimSpace(requestId) == "" {
		return nil, errors.New("requestId is empty")
	}
	if amount <= 0 {
		return nil, errors.New("amount must be > 0")
	}
	now := GetDBTimestamp()

	returnValue := &SubscriptionPreConsumeResult{}

	err := DB.Transaction(func(tx *gorm.DB) error {
		// 幂等护栏：同 requestId 重放直接返回上次结果，不做 group 二次校验，
		// 避免破坏 settle/refund 链路（PostConsumeUserSubscriptionDelta/RefundSubscriptionPreConsume 都按 requestId）。
		var existing SubscriptionPreConsumeRecord
		query := tx.Where("request_id = ?", requestId).Limit(1).Find(&existing)
		if query.Error != nil {
			return query.Error
		}
		if query.RowsAffected > 0 {
			if existing.Status == "refunded" {
				return errors.New("subscription pre-consume already refunded")
			}
			if existing.UserId != userId || existing.TokenId != tokenId {
				return errors.New("subscription pre-consume identity mismatch")
			}
			var sub UserSubscription
			if err := tx.Where("id = ?", existing.UserSubscriptionId).First(&sub).Error; err != nil {
				return err
			}
			returnValue.UserSubscriptionId = sub.Id
			returnValue.PreConsumed = existing.PreConsumed
			returnValue.AmountTotal = sub.AmountTotal
			returnValue.AmountUsedBefore = sub.AmountUsed
			returnValue.AmountUsedAfter = sub.AmountUsed
			return nil
		}

		subs, planMap, err := findEligibleSubscriptionsTx(tx, userId, usingGroup, now, []string{"active"})
		if err != nil {
			return err
		}
		if len(subs) == 0 {
			return ErrNoEligibleSubscription
		}
		for _, candidate := range subs {
			sub := candidate
			plan := planMap[sub.PlanId]
			if _, err := maybeResetUserSubscriptionWithPlanTx(tx, &sub, plan, now); err != nil {
				return err
			}
			usedBefore := sub.AmountUsed
			if sub.AmountTotal > 0 {
				remain := sub.AmountTotal - usedBefore
				if remain < amount {
					continue
				}
			}
			record := &SubscriptionPreConsumeRecord{
				RequestId:          requestId,
				UserId:             userId,
				TokenId:            tokenId,
				UserSubscriptionId: sub.Id,
				PreConsumed:        amount,
				Status:             "consumed",
			}
			createResult := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "request_id"}},
				DoNothing: true,
			}).Create(record)
			if createResult.Error != nil {
				return createResult.Error
			}
			if createResult.RowsAffected == 0 {
				var dup SubscriptionPreConsumeRecord
				if err := tx.Where("request_id = ?", requestId).First(&dup).Error; err != nil {
					return err
				}
				if dup.Status == "refunded" {
					return errors.New("subscription pre-consume already refunded")
				}
				if dup.UserId != userId || dup.TokenId != tokenId {
					return errors.New("subscription pre-consume identity mismatch")
				}
				var dupSubscription UserSubscription
				if err := tx.Where("id = ?", dup.UserSubscriptionId).First(&dupSubscription).Error; err != nil {
					return err
				}
				returnValue.UserSubscriptionId = dupSubscription.Id
				returnValue.PreConsumed = dup.PreConsumed
				returnValue.AmountTotal = dupSubscription.AmountTotal
				returnValue.AmountUsedBefore = dupSubscription.AmountUsed
				returnValue.AmountUsedAfter = dupSubscription.AmountUsed
				return nil
			}
			newUsed := sub.AmountUsed + amount
			if err := casUserSubscriptionTx(tx, &sub, map[string]interface{}{
				"amount_used": newUsed,
			}); err != nil {
				return err
			}
			sub.AmountUsed = newUsed
			if tokenId > 0 {
				if amount > int64(^uint(0)>>1) {
					return errors.New("subscription quota exceeds int range")
				}
				if err := adjustTokenQuotaTx(tx, tokenId, userId, int(amount)); err != nil {
					return err
				}
			}
			returnValue.UserSubscriptionId = sub.Id
			returnValue.PreConsumed = amount
			returnValue.AmountTotal = sub.AmountTotal
			returnValue.AmountUsedBefore = usedBefore
			returnValue.AmountUsedAfter = sub.AmountUsed
			return nil
		}
		return fmt.Errorf("subscription quota insufficient, need=%d", amount)
	})
	if err != nil {
		return nil, err
	}
	return returnValue, nil
}

// RefundSubscriptionPreConsume is idempotent and refunds pre-consumed subscription quota by requestId.
func RefundSubscriptionPreConsume(requestId string) error {
	if strings.TrimSpace(requestId) == "" {
		return errors.New("requestId is empty")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var record SubscriptionPreConsumeRecord
		if err := withForUpdate(tx).
			Where("request_id = ?", requestId).First(&record).Error; err != nil {
			return err
		}
		if record.Status == "refunded" {
			return nil
		}
		recordResult := tx.Model(&SubscriptionPreConsumeRecord{}).
			Where("id = ? AND status = ?", record.Id, "consumed").
			Update("status", "refunded")
		if recordResult.Error != nil {
			return recordResult.Error
		}
		if recordResult.RowsAffected != 1 {
			var current SubscriptionPreConsumeRecord
			if err := tx.Where("id = ?", record.Id).First(&current).Error; err != nil {
				return err
			}
			if current.Status == "refunded" {
				return nil
			}
			return errFinancialStateConflict
		}
		if record.PreConsumed <= 0 {
			return nil
		}
		subscriptionResult := tx.Model(&UserSubscription{}).
			Where("id = ? AND user_id = ? AND amount_used >= ?", record.UserSubscriptionId, record.UserId, record.PreConsumed).
			Update("amount_used", gorm.Expr("amount_used - ?", record.PreConsumed))
		if subscriptionResult.Error != nil {
			return subscriptionResult.Error
		}
		if subscriptionResult.RowsAffected != 1 {
			return errors.New("subscription pre-consume refund rejected")
		}
		if record.TokenId > 0 {
			if record.PreConsumed > int64(^uint(0)>>1) {
				return errors.New("subscription quota exceeds int range")
			}
			if err := adjustTokenQuotaTx(tx, record.TokenId, record.UserId, -int(record.PreConsumed)); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReserveSubscriptionPreConsumeTokenQuota extends an in-flight subscription
// reservation and its durable record together with the token reservation.
func ReserveSubscriptionPreConsumeTokenQuota(requestId string, userId int, userSubscriptionId int, tokenId int, delta int) error {
	if strings.TrimSpace(requestId) == "" {
		return errors.New("requestId is empty")
	}
	if userId <= 0 || userSubscriptionId <= 0 || tokenId <= 0 {
		return errors.New("user, subscription and token ids must be positive")
	}
	if delta <= 0 {
		return errors.New("reservation delta must be positive")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		recordResult := tx.Model(&SubscriptionPreConsumeRecord{}).
			Where(
				"request_id = ? AND user_id = ? AND user_subscription_id = ? AND token_id = ? AND status = ?",
				requestId, userId, userSubscriptionId, tokenId, "consumed",
			).
			Update("pre_consumed", gorm.Expr("pre_consumed + ?", delta))
		if recordResult.Error != nil {
			return recordResult.Error
		}
		if recordResult.RowsAffected != 1 {
			return errors.New("subscription pre-consume reservation not found")
		}

		subscriptionResult := tx.Model(&UserSubscription{}).
			Where(
				"id = ? AND user_id = ? AND (amount_total <= 0 OR amount_used + ? <= amount_total)",
				userSubscriptionId, userId, delta,
			).
			Update("amount_used", gorm.Expr("amount_used + ?", delta))
		if subscriptionResult.Error != nil {
			return subscriptionResult.Error
		}
		if subscriptionResult.RowsAffected != 1 {
			return errors.New("subscription quota adjustment rejected")
		}
		return adjustTokenQuotaTx(tx, tokenId, userId, delta)
	})
}

// ResetDueSubscriptions resets subscriptions whose next_reset_time has passed.
// exhausted（额度耗尽）订阅重置后恢复为 active；若用户分组曾被耗尽处理降为
// auto，则恢复回订阅的 upgrade_group（用户已手动改组时不动）。
func ResetDueSubscriptions(limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	now := GetDBTimestamp()
	var subs []UserSubscription
	if err := DB.Where("next_reset_time > 0 AND next_reset_time <= ? AND status IN ?", now, []string{"active", "exhausted"}).
		Order("next_reset_time asc").
		Limit(limit).
		Find(&subs).Error; err != nil {
		return 0, err
	}
	if len(subs) == 0 {
		return 0, nil
	}
	resetCount := 0
	for _, sub := range subs {
		subCopy := sub
		plan, err := getSubscriptionPlanByIdTx(nil, sub.PlanId)
		if err != nil || plan == nil {
			continue
		}
		restoredUserId := 0
		restoredGroup := ""
		err = DB.Transaction(func(tx *gorm.DB) error {
			var locked UserSubscription
			if err := withForUpdate(tx).
				Where("id = ? AND next_reset_time > 0 AND next_reset_time <= ?", subCopy.Id, now).
				First(&locked).Error; err != nil {
				return nil
			}
			wasExhausted := locked.Status == "exhausted"
			advanced, err := maybeResetUserSubscriptionWithPlanTx(tx, &locked, plan, now)
			if err != nil {
				return err
			}
			resetCount++
			if !wasExhausted || !advanced {
				return nil
			}
			if err := requireSingleRow(
				tx.Model(&UserSubscription{}).
					Where("id = ? AND status = ?", locked.Id, "exhausted").
					Updates(map[string]interface{}{
						"status":     "active",
						"updated_at": common.GetTimestamp(),
					}),
				errFinancialStateConflict,
			); err != nil {
				return err
			}
			upgradeGroup := strings.TrimSpace(locked.UpgradeGroup)
			if upgradeGroup == "" {
				return nil
			}
			currentGroup, err := getUserGroupByIdTx(tx, locked.UserId)
			if err != nil {
				return err
			}
			if currentGroup != "auto" {
				return nil
			}
			if err := tx.Model(&User{}).Where("id = ?", locked.UserId).
				Update("group", upgradeGroup).Error; err != nil {
				return err
			}
			restoredUserId = locked.UserId
			restoredGroup = upgradeGroup
			return nil
		})
		if err != nil {
			return resetCount, err
		}
		if restoredGroup != "" && restoredUserId > 0 {
			_ = UpdateUserGroupCache(restoredUserId, restoredGroup)
		}
	}
	return resetCount, nil
}

// CleanupSubscriptionPreConsumeRecords removes old idempotency records to keep table small.
func CleanupSubscriptionPreConsumeRecords(olderThanSeconds int64) (int64, error) {
	if olderThanSeconds <= 0 {
		olderThanSeconds = 7 * 24 * 3600
	}
	cutoff := GetDBTimestamp() - olderThanSeconds
	res := DB.Where("updated_at < ?", cutoff).Delete(&SubscriptionPreConsumeRecord{})
	return res.RowsAffected, res.Error
}

type SubscriptionPlanInfo struct {
	PlanId    int
	PlanTitle string
}

func GetSubscriptionPlanInfoByUserSubscriptionId(userSubscriptionId int) (*SubscriptionPlanInfo, error) {
	if userSubscriptionId <= 0 {
		return nil, errors.New("invalid userSubscriptionId")
	}
	cacheKey := fmt.Sprintf("sub:%d", userSubscriptionId)
	if cached, found, err := getSubscriptionPlanInfoCache().Get(cacheKey); err == nil && found {
		return &cached, nil
	}
	var sub UserSubscription
	if err := DB.Where("id = ?", userSubscriptionId).First(&sub).Error; err != nil {
		return nil, err
	}
	plan, err := getSubscriptionPlanByIdTx(nil, sub.PlanId)
	if err != nil {
		return nil, err
	}
	info := &SubscriptionPlanInfo{
		PlanId:    sub.PlanId,
		PlanTitle: plan.Title,
	}
	_ = getSubscriptionPlanInfoCache().SetWithTTL(cacheKey, *info, subscriptionPlanInfoCacheTTL())
	return info, nil
}

// Update subscription used amount by delta (positive consume more, negative refund).
func PostConsumeUserSubscriptionDelta(userSubscriptionId int, delta int64) error {
	if userSubscriptionId <= 0 {
		return errors.New("invalid userSubscriptionId")
	}
	if delta == 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var sub UserSubscription
		if err := withForUpdate(tx).
			Where("id = ?", userSubscriptionId).
			First(&sub).Error; err != nil {
			return err
		}
		newUsed := sub.AmountUsed + delta
		if newUsed < 0 {
			newUsed = 0
		}
		if sub.AmountTotal > 0 && newUsed > sub.AmountTotal {
			return fmt.Errorf("subscription used exceeds total, used=%d total=%d", newUsed, sub.AmountTotal)
		}
		if newUsed == sub.AmountUsed {
			return nil
		}
		return casUserSubscriptionTx(tx, &sub, map[string]interface{}{
			"amount_used": newUsed,
		})
	})
}

// AdjustSubscriptionTokenQuota applies one task settlement to the subscription
// and token rows in the same main-database transaction.
func AdjustSubscriptionTokenQuota(userID int, userSubscriptionID int, tokenID int, delta int) error {
	if userID <= 0 || userSubscriptionID <= 0 || tokenID <= 0 {
		return errors.New("user, subscription and token ids must be positive")
	}
	if delta == 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&UserSubscription{}).
			Where("id = ? AND user_id = ?", userSubscriptionID, userID)
		if delta > 0 {
			query = query.Where("(amount_total <= 0 OR amount_used + ? <= amount_total)", delta)
		} else {
			query = query.Where("amount_used >= ?", -delta)
		}
		result := query.Update("amount_used", gorm.Expr("amount_used + ?", delta))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("subscription quota adjustment rejected")
		}
		return adjustTokenQuotaTx(tx, tokenID, userID, delta)
	})
}

// planDurationSeconds 返回 plan 单期总秒数；用于"never"/未知 reset 周期时的均摊
func planDurationSeconds(plan *SubscriptionPlan) int64 {
	if plan == nil {
		return 0
	}
	switch plan.DurationUnit {
	case SubscriptionDurationYear:
		return int64(plan.DurationValue) * 365 * 86400
	case SubscriptionDurationMonth:
		return int64(plan.DurationValue) * 30 * 86400
	case SubscriptionDurationDay:
		return int64(plan.DurationValue) * 86400
	case SubscriptionDurationHour:
		return int64(plan.DurationValue) * 3600
	case SubscriptionDurationCustom:
		return plan.CustomSeconds
	}
	return 0
}

// EstimateDailyQuota 估算一份订阅每天可用配额（quota 单位）
// 按 QuotaResetPeriod 优先，否则按 plan 总周期均摊
func EstimateDailyQuota(plan *SubscriptionPlan) float64 {
	if plan == nil || plan.TotalAmount <= 0 {
		return 0
	}
	total := float64(plan.TotalAmount)
	switch plan.QuotaResetPeriod {
	case SubscriptionResetDaily:
		return total
	case SubscriptionResetWeekly:
		return total / 7
	case SubscriptionResetMonthly:
		return total / 30
	case SubscriptionResetCustom:
		if plan.QuotaResetCustomSeconds > 0 {
			return total * 86400 / float64(plan.QuotaResetCustomSeconds)
		}
	}
	// never / fallthrough：按 plan 总周期均摊
	dur := planDurationSeconds(plan)
	if dur <= 0 {
		return 0
	}
	return total * 86400 / float64(dur)
}

// EstimateDailyPrice 估算每天单份订阅折算货币金额
func EstimateDailyPrice(plan *SubscriptionPlan) float64 {
	if plan == nil || plan.PriceAmount <= 0 {
		return 0
	}
	dur := planDurationSeconds(plan)
	if dur <= 0 {
		return 0
	}
	return plan.PriceAmount * 86400 / float64(dur)
}

// AdminUserSubscriptionRow admin 列表返回的扁平化行
type AdminUserSubscriptionRow struct {
	UserSubscription
	Username  string `json:"username"`
	PlanTitle string `json:"plan_title"`
	PlanGroup string `json:"plan_bound_group"`
}

// ListAdminUserSubscriptionsFilter 筛选参数
type ListAdminUserSubscriptionsFilter struct {
	Status     string
	Username   string
	BoundGroup string
	Page       int
	PageSize   int
}

// ListAdminUserSubscriptions 按 status / username / bound_group 分页查询
func ListAdminUserSubscriptions(filter ListAdminUserSubscriptionsFilter) ([]AdminUserSubscriptionRow, int64, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 || filter.PageSize > 200 {
		filter.PageSize = 20
	}
	offset := (filter.Page - 1) * filter.PageSize

	base := DB.Table("user_subscriptions us").
		Joins("LEFT JOIN users u ON u.id = us.user_id").
		Joins("LEFT JOIN subscription_plans p ON p.id = us.plan_id")

	if filter.Status != "" {
		base = base.Where("us.status = ?", filter.Status)
	}
	if filter.Username != "" {
		base = base.Where("u.username = ?", filter.Username)
	}
	if filter.BoundGroup != "" {
		base = base.Where("p.bound_group = ?", filter.BoundGroup)
	}

	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	type rawRow struct {
		Id            int    `gorm:"column:id"`
		UserId        int    `gorm:"column:user_id"`
		PlanId        int    `gorm:"column:plan_id"`
		AmountTotal   int64  `gorm:"column:amount_total"`
		AmountUsed    int64  `gorm:"column:amount_used"`
		StartTime     int64  `gorm:"column:start_time"`
		EndTime       int64  `gorm:"column:end_time"`
		Status        string `gorm:"column:status"`
		Source        string `gorm:"column:source"`
		LastResetTime int64  `gorm:"column:last_reset_time"`
		NextResetTime int64  `gorm:"column:next_reset_time"`
		UpgradeGroup  string `gorm:"column:upgrade_group"`
		PrevUserGroup string `gorm:"column:prev_user_group"`
		CreatedAt     int64  `gorm:"column:created_at"`
		UpdatedAt     int64  `gorm:"column:updated_at"`
		Username      string `gorm:"column:username"`
		PlanTitle     string `gorm:"column:plan_title"`
		PlanGroup     string `gorm:"column:plan_bound_group"`
	}

	var raws []rawRow
	err := base.Select(`
		us.id, us.user_id, us.plan_id, us.amount_total, us.amount_used,
		us.start_time, us.end_time, us.status, us.source,
		us.last_reset_time, us.next_reset_time,
		us.upgrade_group, us.prev_user_group, us.created_at, us.updated_at,
		u.username AS username,
		p.title AS plan_title,
		p.bound_group AS plan_bound_group
	`).
		Order("us.id DESC").
		Limit(filter.PageSize).
		Offset(offset).
		Find(&raws).Error
	if err != nil {
		return nil, 0, err
	}

	rows := make([]AdminUserSubscriptionRow, 0, len(raws))
	for _, r := range raws {
		rows = append(rows, AdminUserSubscriptionRow{
			UserSubscription: UserSubscription{
				Id:            r.Id,
				UserId:        r.UserId,
				PlanId:        r.PlanId,
				AmountTotal:   r.AmountTotal,
				AmountUsed:    r.AmountUsed,
				StartTime:     r.StartTime,
				EndTime:       r.EndTime,
				Status:        r.Status,
				Source:        r.Source,
				LastResetTime: r.LastResetTime,
				NextResetTime: r.NextResetTime,
				UpgradeGroup:  r.UpgradeGroup,
				PrevUserGroup: r.PrevUserGroup,
				CreatedAt:     r.CreatedAt,
				UpdatedAt:     r.UpdatedAt,
			},
			Username:  r.Username,
			PlanTitle: r.PlanTitle,
			PlanGroup: r.PlanGroup,
		})
	}
	return rows, total, nil
}

// ListBoundGroups 返回去重后的所有 plan.bound_group（非空）
func ListBoundGroups() ([]string, error) {
	var groups []string
	err := DB.Model(&SubscriptionPlan{}).
		Distinct("bound_group").
		Where("bound_group <> ''").
		Order("bound_group ASC").
		Pluck("bound_group", &groups).Error
	return groups, err
}

// GroupBudgetRow 按 BoundGroup 聚合 active 订阅的每日预算
type GroupBudgetRow struct {
	BoundGroup    string  `json:"bound_group"`
	ActiveCount   int64   `json:"active_count"`
	DailyQuota    float64 `json:"daily_quota"`
	DailyPriceUSD float64 `json:"daily_price_usd"`
	Currency      string  `json:"currency"`
}

// EstimateGroupDailyBudget 计算每个 BoundGroup 的每日预算（quota + 货币）
// 仅统计 status=active 且 plan.bound_group != ''
func EstimateGroupDailyBudget() ([]GroupBudgetRow, error) {
	now := common.GetTimestamp()
	type subPlan struct {
		BoundGroup              string
		PriceAmount             float64
		Currency                string
		TotalAmount             int64
		DurationUnit            string
		DurationValue           int
		CustomSeconds           int64
		QuotaResetPeriod        string
		QuotaResetCustomSeconds int64
	}
	var rows []subPlan
	err := DB.Table("user_subscriptions us").
		Select("p.bound_group, p.price_amount, p.currency, p.total_amount, p.duration_unit, p.duration_value, p.custom_seconds, p.quota_reset_period, p.quota_reset_custom_seconds").
		Joins("JOIN subscription_plans p ON p.id = us.plan_id").
		Where("us.status = ? AND p.bound_group <> ''", "active").
		Where("us.end_time = 0 OR us.end_time > ?", now).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}

	type agg struct {
		count    int64
		quota    float64
		price    float64
		currency string
	}
	bucket := make(map[string]*agg)
	for _, r := range rows {
		plan := &SubscriptionPlan{
			TotalAmount:             r.TotalAmount,
			PriceAmount:             r.PriceAmount,
			Currency:                r.Currency,
			DurationUnit:            r.DurationUnit,
			DurationValue:           r.DurationValue,
			CustomSeconds:           r.CustomSeconds,
			QuotaResetPeriod:        r.QuotaResetPeriod,
			QuotaResetCustomSeconds: r.QuotaResetCustomSeconds,
		}
		a, ok := bucket[r.BoundGroup]
		if !ok {
			a = &agg{currency: r.Currency}
			bucket[r.BoundGroup] = a
		}
		a.count++
		a.quota += EstimateDailyQuota(plan)
		a.price += EstimateDailyPrice(plan)
	}

	out := make([]GroupBudgetRow, 0, len(bucket))
	for g, a := range bucket {
		out = append(out, GroupBudgetRow{
			BoundGroup:    g,
			ActiveCount:   a.count,
			DailyQuota:    a.quota,
			DailyPriceUSD: a.price,
			Currency:      a.currency,
		})
	}
	return out, nil
}
