package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 钱包预扣费记录状态。
const (
	WalletPreConsumeStatusReserved = "reserved" // 已预扣，等待结算或退款
	WalletPreConsumeStatusSettled  = "settled"  // 已按实际用量结算
	WalletPreConsumeStatusRefunded = "refunded" // 预扣费已全额退还
)

// WalletPreConsumeRecord 是钱包预扣费的持久化凭据。
//
// 预扣费扣减 users.quota 的同一事务内写入本记录，因此"钱已被扣走"这件事
// 永远有落库证据。请求失败时的退款只做两件事：把余额加回去、把状态置为
// refunded。若退款那一刻数据库不可用，记录会停留在 reserved 状态，由后台
// 清扫任务在数据库恢复后完成退款——这正是余额凭空消失的根因所在：
// 退款只在内存里做异步补偿，失败即永久丢失。
//
// 与 SubscriptionPreConsumeRecord 保持同一设计口径（request_id 唯一键 +
// status 流转），便于两条资金来源用同一套对账逻辑。
type WalletPreConsumeRecord struct {
	Id          int    `json:"id"`
	RequestId   string `json:"request_id" gorm:"type:varchar(64);uniqueIndex"`
	UserId      int    `json:"user_id" gorm:"index"`
	TokenId     int    `json:"token_id" gorm:"index"`
	PreConsumed int64  `json:"pre_consumed" gorm:"type:bigint;not null;default:0"`
	Status      string `json:"status" gorm:"type:varchar(32);index"`
	CreatedAt   int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt   int64  `json:"updated_at" gorm:"bigint;index"`
}

func (r *WalletPreConsumeRecord) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	r.CreatedAt = now
	r.UpdatedAt = now
	return nil
}

func (r *WalletPreConsumeRecord) BeforeUpdate(tx *gorm.DB) error {
	r.UpdatedAt = common.GetTimestamp()
	return nil
}

// createWalletPreConsumeRecordTx 在预扣费事务内写入凭据。
// request_id 唯一索引保证同一请求不会重复预扣。
func createWalletPreConsumeRecordTx(tx *gorm.DB, requestId string, userID int, tokenID int, delta int) error {
	record := WalletPreConsumeRecord{
		RequestId:   requestId,
		UserId:      userID,
		TokenId:     tokenID,
		PreConsumed: int64(delta),
		Status:      WalletPreConsumeStatusReserved,
	}
	if err := tx.Create(&record).Error; err != nil {
		return fmt.Errorf("create wallet pre-consume record for request %s: %w", requestId, err)
	}
	return nil
}

// addWalletPreConsumeTx 追加预扣额度到已有凭据（发送前补充预扣）。
func addWalletPreConsumeTx(tx *gorm.DB, requestId string, userID int, tokenID int, delta int) error {
	// 同 claimWalletPreConsumeTx：点查主键后按 PK 更新，统一本表锁序。
	var existing WalletPreConsumeRecord
	err := tx.Select("id").Where("request_id = ?", requestId).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("wallet pre-consume record not found for request %s", requestId)
	}
	if err != nil {
		return fmt.Errorf("extend wallet pre-consume record for request %s: %w", requestId, err)
	}
	result := tx.Model(&WalletPreConsumeRecord{}).
		Where("id = ? AND user_id = ? AND token_id = ? AND status = ?",
			existing.Id, userID, tokenID, WalletPreConsumeStatusReserved).
		Update("pre_consumed", gorm.Expr("pre_consumed + ?", delta))
	if result.Error != nil {
		return fmt.Errorf("extend wallet pre-consume record for request %s: %w", requestId, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("wallet pre-consume record not found for request %s", requestId)
	}
	return nil
}

// claimWalletPreConsumeTx 用 CAS 把凭据从 reserved 推进到终态，并返回本次调用
// 是否"抢到"了这笔预扣的处置权。
//
// 返回 true 表示调用方拥有该预扣额度，必须继续完成对应的余额变动；返回 false
// 表示凭据已被别的路径（通常是清扫任务）推进到终态、钱已经动过了，调用方绝不
// 能再动一次余额——否则就是重复退款。
//
// 因此凭据的状态流转必须发生在余额变动之前：先抢占，再动钱。反过来做会让
// "清扫任务已退款" 与 "请求随后结算" 各自把钱动一遍。
func claimWalletPreConsumeTx(tx *gorm.DB, requestId string, status string) (bool, error) {
	// 无 request id 说明这条链路不走凭据（内部调用），余额变动照常进行。
	if strings.TrimSpace(requestId) == "" {
		return true, nil
	}
	// 先按唯一键点查主键，再按主键做状态 CAS。直接按 request_id+status UPDATE
	// 时优化器可能选 status 索引扫描，与走唯一索引的并发 claim 形成
	// PK/status 索引反序死锁（07-31 线上实证）；点查+主键更新使所有写入路径
	// 对本表的加锁顺序统一为 PK → 二级索引。
	var existing WalletPreConsumeRecord
	err := tx.Select("id", "status").Where("request_id = ?", requestId).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 没有凭据说明这条链路未走预扣（信任用户 / 预扣为 0），余额变动照常进行。
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect wallet pre-consume record %s: %w", requestId, err)
	}
	if existing.Status != WalletPreConsumeStatusReserved {
		// 凭据已是终态：钱已被处置过，本次不得重复变动余额。
		return false, nil
	}
	result := tx.Model(&WalletPreConsumeRecord{}).
		Where("id = ? AND status = ?", existing.Id, WalletPreConsumeStatusReserved).
		Update("status", status)
	if result.Error != nil {
		return false, fmt.Errorf("finalize wallet pre-consume record %s: %w", requestId, result.Error)
	}
	if result.RowsAffected == 1 {
		return true, nil
	}
	// 点查与更新之间被并发路径（清扫任务）抢先推进到终态。
	return false, nil
}

// FindStaleReservedWalletPreConsumes 取出超过 olderThan 秒仍停留在 reserved
// 状态的凭据，供后台清扫退款。
func FindStaleReservedWalletPreConsumes(olderThanSeconds int64, limit int) ([]WalletPreConsumeRecord, error) {
	if limit <= 0 {
		limit = 200
	}
	cutoff := common.GetTimestamp() - olderThanSeconds
	var records []WalletPreConsumeRecord
	err := DB.Where("status = ? AND updated_at < ?", WalletPreConsumeStatusReserved, cutoff).
		Order("updated_at asc").
		Limit(limit).
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("load stale wallet pre-consume records: %w", err)
	}
	return records, nil
}

// RefundStaleWalletPreConsume 在单个事务内退还一条遗留预扣费：余额与令牌
// 加回，凭据置为 refunded。status 条件保证与正常退款路径互斥。
func RefundStaleWalletPreConsume(record WalletPreConsumeRecord) error {
	if record.PreConsumed <= 0 {
		_, err := claimWalletPreConsumeTx(DB, record.RequestId, WalletPreConsumeStatusRefunded)
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		// 按主键 CAS：与 claimWalletPreConsumeTx 同理，统一 PK → 二级索引锁序。
		claim := tx.Model(&WalletPreConsumeRecord{}).
			Where("id = ? AND status = ?", record.Id, WalletPreConsumeStatusReserved).
			Update("status", WalletPreConsumeStatusRefunded)
		if claim.Error != nil {
			return fmt.Errorf("claim stale wallet pre-consume %s: %w", record.RequestId, claim.Error)
		}
		if claim.RowsAffected != 1 {
			// 已被正常退款或结算路径处理，跳过。
			return nil
		}
		userResult := tx.Unscoped().Model(&User{}).
			Where("id = ?", record.UserId).
			Update("quota", gorm.Expr("quota + ?", record.PreConsumed))
		if userResult.Error != nil {
			return fmt.Errorf("return user quota for request %s: %w", record.RequestId, userResult.Error)
		}
		if userResult.RowsAffected != 1 {
			return fmt.Errorf("return user quota: user %d not found", record.UserId)
		}
		if record.TokenId > 0 {
			if err := adjustTokenQuotaTx(tx, record.TokenId, record.UserId, -int(record.PreConsumed)); err != nil {
				return err
			}
		}
		return nil
	})
}
