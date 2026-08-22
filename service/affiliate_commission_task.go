package service

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billinglifecycle"

	"gorm.io/gorm"
)

const (
	// 每 30 分钟检查一次：是否有"前一天"的数据未结算
	affiliateCommissionTickInterval = 30 * time.Minute
	// 每天最早结算时机（北京时间），避开 00:00 边界
	affiliateCommissionEarliestHour = 0
	affiliateCommissionEarliestMin  = 30
	// log 扫描批量
	affiliateCommissionLogBatchSize = 1000
	// IN 子句分批，避开 PG 65535 / SQLite 32766 参数上限
	affiliateCommissionUserBatchSize = 500
	// 内存保护：单日 unique 消费用户上限。超过中止结算并报警，留作运维介入。
	affiliateCommissionMaxUniqueUsers = 200000
	// 事务里 batch insert daily 记录的批量
	affiliateCommissionInsertBatchSize = 200
)

var affiliateCommissionRunning atomic.Bool

// 北京时间时区
var beijingLoc = time.FixedZone("CST", 8*60*60)

// StartAffiliateCommissionTask 启动推荐分成日结 cron。
// 触发条件：开关 + 比例 > 0 + 当前北京时间 >= 00:30 + 前一天未结算。
func StartAffiliateCommissionTask() error {
	if !common.IsMasterNode {
		return nil
	}
	return billinglifecycle.StartProducer("affiliate-commission", func(ctx context.Context, _ *billinglifecycle.Ticket) {
		logger.LogInfo(context.Background(), fmt.Sprintf("affiliate commission task started: tick=%s", affiliateCommissionTickInterval))
		billinglifecycle.RunPeriodic(ctx, affiliateCommissionTickInterval, true, func() {
			runAffiliateCommissionOnce()
		})
	})
}

func runAffiliateCommissionOnce() {
	if !affiliateCommissionRunning.CompareAndSwap(false, true) {
		return
	}
	defer affiliateCommissionRunning.Store(false)

	ctx := context.Background()

	if !common.AffiliateCommissionEnabled || common.AffiliateCommissionRatio <= 0 {
		return
	}

	now := time.Now().In(beijingLoc)
	if now.Hour() < affiliateCommissionEarliestHour ||
		(now.Hour() == affiliateCommissionEarliestHour && now.Minute() < affiliateCommissionEarliestMin) {
		// 还没到 00:30，等下一个 tick
		return
	}

	yesterday := now.AddDate(0, 0, -1)
	dateStr := yesterday.Format("2006-01-02")

	settled, err := model.HasAffiliateCommissionForDate(dateStr)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("affiliate commission: check settled failed for %s: %v", dateStr, err))
		return
	}
	if settled {
		return
	}

	startSec := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, beijingLoc).Unix()
	endSec := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, beijingLoc).AddDate(0, 0, 1).Unix()

	settleAffiliateCommissionForDay(ctx, dateStr, startSec, endSec)
}

// settleAffiliateCommissionForDay 结算指定一天的推荐分成。
// 流程：扫 logs → 聚合 → 拉 inviter（分批 IN）→ 事务里 (daily insert + inviter quota update)。
func settleAffiliateCommissionForDay(ctx context.Context, dateStr string, startSec, endSec int64) {
	consumeByUser, ok := scanConsumeByUser(ctx, dateStr, startSec, endSec)
	if !ok {
		return
	}

	if len(consumeByUser) == 0 {
		// 当天无任何余额消费，写占位防重复扫描。yesterday 数据窗口已 closed，安全。
		_ = model.SaveAffiliateCommissionDaily([]model.AffiliateCommissionDaily{{
			Date:      dateStr,
			InviterId: 0,
			InviteeId: 0,
		}})
		logger.LogInfo(ctx, fmt.Sprintf("affiliate commission: %s no balance consume, marked settled", dateStr))
		return
	}

	inviterById, ok := loadInviterMapping(ctx, dateStr, consumeByUser)
	if !ok {
		return
	}

	ratio := common.AffiliateCommissionRatio
	records, commissionByInviter := buildCommissionRecords(dateStr, consumeByUser, inviterById, ratio)

	if len(records) == 0 {
		// 有消费但都无 inviter 或 commission=0，写占位标记已结算
		_ = model.SaveAffiliateCommissionDaily([]model.AffiliateCommissionDaily{{
			Date:      dateStr,
			InviterId: 0,
			InviteeId: 0,
		}})
		logger.LogInfo(ctx, fmt.Sprintf("affiliate commission: %s no eligible commission, marked settled", dateStr))
		return
	}

	// 事务化：daily insert + inviter quota update 任一失败回滚整体。
	// 失败后 HasAffiliateCommissionForDate 仍返回 false，下次 tick 重试，不丢钱。
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.CreateInBatches(records, affiliateCommissionInsertBatchSize).Error; err != nil {
			return fmt.Errorf("insert daily records: %w", err)
		}
		for inviterId, total := range commissionByInviter {
			if err := tx.Model(&model.User{}).Where("id = ?", inviterId).
				Updates(map[string]interface{}{
					"aff_quota":   gorm.Expr("aff_quota + ?", total),
					"aff_history": gorm.Expr("aff_history + ?", total),
				}).Error; err != nil {
				return fmt.Errorf("update inviter=%d quota: %w", inviterId, err)
			}
		}
		return nil
	})
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("affiliate commission settle tx failed for %s: %v", dateStr, err))
		return
	}

	logger.LogInfo(ctx, fmt.Sprintf(
		"affiliate commission settled %s: invitees=%d, inviters=%d, total=%d",
		dateStr, len(records), len(commissionByInviter), sumIntMap(commissionByInviter),
	))
}

// scanConsumeByUser 流式扫一天的余额消费 logs，按 user_id 聚合。
// 第二个返回值 false 表示出错或 unique user 数超过保护阈值，调用方应中止。
func scanConsumeByUser(ctx context.Context, dateStr string, startSec, endSec int64) (map[int]int, bool) {
	consumeByUser := make(map[int]int)
	var lastCreatedAt int64
	var lastId int
	var lastEventId string
	first := true
	for {
		var logs []model.Log
		query := model.LOG_DB.WithContext(ctx).
			Where("type = ? AND created_at >= ? AND created_at < ?", model.LogTypeConsume, startSec, endSec).
			Limit(affiliateCommissionLogBatchSize)
		if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
			if !first {
				query = query.Where(
					"created_at > ? OR (created_at = ? AND event_id > ?)",
					lastCreatedAt, lastCreatedAt, lastEventId,
				)
			}
			query = query.Order("created_at ASC, event_id ASC")
		} else {
			if !first {
				query = query.Where(
					"created_at > ? OR (created_at = ? AND id > ?)",
					lastCreatedAt, lastCreatedAt, lastId,
				)
			}
			query = query.Order("created_at ASC, id ASC")
		}
		err := query.Find(&logs).Error
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("affiliate commission: scan logs failed for %s: %v", dateStr, err))
			return nil, false
		}
		if len(logs) == 0 {
			break
		}
		for _, log := range logs {
			if log.Quota <= 0 {
				continue
			}
			if isSubscriptionConsume(log.Other) {
				continue
			}
			if _, ok := consumeByUser[log.UserId]; !ok && len(consumeByUser) >= affiliateCommissionMaxUniqueUsers {
				logger.LogWarn(ctx, fmt.Sprintf(
					"affiliate commission: unique user count exceeded %d for %s, abort settlement",
					affiliateCommissionMaxUniqueUsers, dateStr,
				))
				return nil, false
			}
			consumeByUser[log.UserId] += log.Quota
		}
		if len(logs) < affiliateCommissionLogBatchSize {
			break
		}
		last := logs[len(logs)-1]
		lastCreatedAt = last.CreatedAt
		lastId = last.Id
		lastEventId = last.EventId
		first = false
	}
	return consumeByUser, true
}

// loadInviterMapping 分批查 user.inviter_id，避开 IN 子句参数上限。
func loadInviterMapping(ctx context.Context, dateStr string, consumeByUser map[int]int) (map[int]int, bool) {
	userIds := make([]int, 0, len(consumeByUser))
	for uid := range consumeByUser {
		userIds = append(userIds, uid)
	}
	type inviteeRow struct {
		Id        int
		InviterId int
	}
	inviterById := make(map[int]int, len(userIds))
	for i := 0; i < len(userIds); i += affiliateCommissionUserBatchSize {
		end := i + affiliateCommissionUserBatchSize
		if end > len(userIds) {
			end = len(userIds)
		}
		var rows []inviteeRow
		err := model.DB.Model(&model.User{}).
			Select("id, inviter_id").
			Where("id IN ? AND inviter_id <> 0", userIds[i:end]).
			Find(&rows).Error
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("affiliate commission: load inviter batch [%d:%d] failed for %s: %v", i, end, dateStr, err))
			return nil, false
		}
		for _, r := range rows {
			inviterById[r.Id] = r.InviterId
		}
	}
	return inviterById, true
}

// buildCommissionRecords 组装日结记录。
func buildCommissionRecords(dateStr string, consumeByUser map[int]int, inviterById map[int]int, ratio float64) ([]model.AffiliateCommissionDaily, map[int]int) {
	commissionByInviter := make(map[int]int)
	records := make([]model.AffiliateCommissionDaily, 0, len(inviterById))
	for inviteeId, consume := range consumeByUser {
		inviterId, ok := inviterById[inviteeId]
		if !ok {
			continue
		}
		commission := int(float64(consume) * ratio)
		if commission <= 0 {
			continue
		}
		records = append(records, model.AffiliateCommissionDaily{
			Date:         dateStr,
			InviterId:    inviterId,
			InviteeId:    inviteeId,
			InviteeQuota: consume,
			Commission:   commission,
		})
		commissionByInviter[inviterId] += commission
	}
	return records, commissionByInviter
}

// isSubscriptionConsume 判断 logs.Other 是否包含有效的 subscription_id（>0），用于排除订阅消费。
func isSubscriptionConsume(other string) bool {
	if other == "" {
		return false
	}
	otherMap, err := common.StrToMap(other)
	if err != nil || otherMap == nil {
		return false
	}
	v, ok := otherMap["subscription_id"]
	if !ok {
		return false
	}
	switch n := v.(type) {
	case float64:
		return n > 0
	case int:
		return n > 0
	case int64:
		return n > 0
	case string:
		return n != "" && n != "0"
	}
	return false
}

func sumIntMap(m map[int]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}
