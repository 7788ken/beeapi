package service

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	// 预扣凭据超过该时长仍停留在 reserved，视为遗留预扣。
	// 必须显著大于单请求最长生命周期（relay 链路超时 6000s），
	// 否则会把仍在进行中的长请求误判为遗留并提前退款。
	walletReservationStaleAfter = 2 * time.Hour
	walletReservationSweepEvery = 5 * time.Minute
	walletReservationSweepBatch = 200
)

// StartWalletReservationSweeper 启动遗留预扣费清扫任务。
//
// 这是"扣了钱但退款失败"的最终兜底：退款路径与数据库同时不可用时，
// 凭据会停在 reserved 状态，本任务在数据库恢复后把钱退回去。
func StartWalletReservationSweeper() error {
	go func() {
		// 启动后先等一轮，避开实例启动时的在途请求与数据库预热。
		ticker := time.NewTicker(walletReservationSweepEvery)
		defer ticker.Stop()
		for range ticker.C {
			sweepStaleWalletReservations()
		}
	}()
	return nil
}

func sweepStaleWalletReservations() {
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysLog(fmt.Sprintf("wallet reservation sweeper panic: %v", recovered))
		}
	}()

	records, err := model.FindStaleReservedWalletPreConsumes(
		int64(walletReservationStaleAfter.Seconds()),
		walletReservationSweepBatch,
	)
	if err != nil {
		common.SysLog("wallet reservation sweeper: " + err.Error())
		return
	}
	if len(records) == 0 {
		return
	}

	refunded := 0
	var refundedQuota int64
	for _, record := range records {
		if err := model.RefundStaleWalletPreConsume(record); err != nil {
			common.SysLog(fmt.Sprintf(
				"wallet reservation sweeper failed to refund request=%s userId=%d quota=%d: %s",
				record.RequestId, record.UserId, record.PreConsumed, err.Error(),
			))
			continue
		}
		refunded++
		refundedQuota += record.PreConsumed
	}
	if refunded > 0 {
		common.SysLog(fmt.Sprintf(
			"wallet reservation sweeper recovered %d stranded pre-consumptions, refunded quota=%d",
			refunded, refundedQuota,
		))
	}
}
