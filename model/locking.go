package model

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errFinancialStateConflict = errors.New("financial state changed concurrently")

// Financial transactions acquire existing rows in this order:
// request/order/redemption/topup -> plan/subscription -> user -> token.
// Subscription payment acquires SubscriptionOrder before its shared TopUp row,
// and multiple rows of the same type are acquired by ascending ID.
// Callers may skip levels but must never acquire an earlier level afterward.
//
// 已知例外：硬删除用户（deleteUserWithLedger）先锁 user 行，再回头结清
// wallet_pre_consume_records 等 request 层记录。先锁 user 是有意为之——它挡住
// 删除期间的并发扣费，让在途工作项的结清成为完整围栏；调到前面反而开新竞态。
// 代价是与 relay 侧的 wpcr -> user 顺序构成 ABBA，删活跃用户时可能死锁。
// 两侧都能干净恢复：清扫器失败只记日志下轮重试，删除失败由管理员重试，
// 且退款路径是 CAS，不会重复动钱。
func withForUpdate(tx *gorm.DB) *gorm.DB {
	return tx.Clauses(clause.Locking{Strength: "UPDATE"})
}

func requireSingleRow(result *gorm.DB, conflict error) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		if conflict != nil {
			return conflict
		}
		return errFinancialStateConflict
	}
	return nil
}
