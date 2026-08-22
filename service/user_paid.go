package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 余额提醒只发给有商业关系的用户。未充值账号在余额跌破阈值后会每次请求都触发发信，
// 挤占 SMTP 服务商的发送配额，导致真实客户的提醒被 451 限速丢弃。

const paidUserCacheTTL = 10 * time.Minute

// paidUserUsedQuotaFallback 历史消费兜底线（$50）。
// 站内大量真实客户的额度由管理员直接充值，不在支付订单/兑换码/订阅订单里留痕，
// 只按充值表判定会把这批人（含余额已见底的老客户）静默屏蔽。
// 赠送与签到攒不到这个量级：无充值记录的用户里 89% 历史消费不足 $50。
const paidUserUsedQuotaFallback = 25000000

type paidUserEntry struct {
	paid bool
	at   time.Time
}

var paidUserCache sync.Map // userId -> paidUserEntry

// IsPaidUser 判断用户是否值得发余额提醒：有过充值行为，或历史消费已达兜底线。
// 查询失败时按有效用户处理，宁可多发一封也不漏掉真实客户。
func IsPaidUser(userId int) bool {
	if userId <= 0 {
		return false
	}
	if v, ok := paidUserCache.Load(userId); ok {
		if entry, ok := v.(paidUserEntry); ok && time.Since(entry.at) < paidUserCacheTTL {
			return entry.paid
		}
	}

	paid, err := queryPaidRecord(userId)
	if err != nil {
		common.SysError(fmt.Sprintf("failed to check paid record for user %d: %s", userId, err.Error()))
		return true
	}

	paidUserCache.Store(userId, paidUserEntry{paid: paid, at: time.Now()})
	return paid
}

func queryPaidRecord(userId int) (bool, error) {
	var usedQuota int64
	if err := model.DB.Model(&model.User{}).
		Where("id = ?", userId).
		Select("used_quota").
		Scan(&usedQuota).Error; err != nil {
		return false, err
	}
	if usedQuota >= paidUserUsedQuotaFallback {
		return true, nil
	}

	var cnt int64

	if err := model.DB.Model(&model.TopUp{}).
		Where("user_id = ? AND status = ?", userId, common.TopUpStatusSuccess).
		Count(&cnt).Error; err != nil {
		return false, err
	}
	if cnt > 0 {
		return true, nil
	}

	if err := model.DB.Model(&model.Redemption{}).
		Where("used_user_id = ?", userId).
		Count(&cnt).Error; err != nil {
		return false, err
	}
	if cnt > 0 {
		return true, nil
	}

	if err := model.DB.Model(&model.SubscriptionOrder{}).
		Where("user_id = ? AND status = ?", userId, common.TopUpStatusSuccess).
		Count(&cnt).Error; err != nil {
		return false, err
	}
	return cnt > 0, nil
}
