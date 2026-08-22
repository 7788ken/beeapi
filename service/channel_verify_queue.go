package service

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
)

// VerifyQueue 用信号量限制对外测评（外部测评网关 /api/verify/{protocol}）的并发数。
// 单次测评 30~120s，下游限流 60s/10次/IP；不限并发会被 429。
//
// 用法：
//
//	pos := VerifyQueueAcquire(ctx)
//	if pos < 0 { return ctx.Err() }
//	defer VerifyQueueRelease()
//
// pos == 0 表示直接拿到资源；> 0 表示排队过且当时身前还有 pos 个等待者。
var (
	verifyCap = func() int {
		n := common.GetEnvOrDefault("VERIFY_MAX_CONCURRENT", 2)
		if n < 1 {
			return 1
		}
		return n
	}()
	verifySem     = make(chan struct{}, verifyCap)
	verifyWaiting int64 // 当前在等待的任务数（用于给前端返回 position）
)

// VerifyQueueAcquire 阻塞直到拿到槽位或 ctx 取消。
// onWait 在确认需要排队后、进入阻塞等待前回调（带身前的位置），可为 nil。
// 返回值：
//
//	pos >= 0：拿到，pos 是 acquire 时的等待者数量（0=立即拿到，1+=曾排过队）
//	pos == -1：ctx 取消
func VerifyQueueAcquire(ctx context.Context, onWait func(position int)) int {
	// 先尝试非阻塞拿
	select {
	case verifySem <- struct{}{}:
		return 0
	default:
	}
	// 排队：把自己加入等待队伍，position 是含自己的序号
	pos := atomic.AddInt64(&verifyWaiting, 1)
	defer atomic.AddInt64(&verifyWaiting, -1)
	if onWait != nil {
		onWait(int(pos))
	}
	select {
	case verifySem <- struct{}{}:
		return int(pos)
	case <-ctx.Done():
		return -1
	}
}

func VerifyQueueRelease() {
	select {
	case <-verifySem:
	default:
	}
}

// VerifyQueueWaiting 当前等待数（监控/调试用）。
func VerifyQueueWaiting() int {
	return int(atomic.LoadInt64(&verifyWaiting))
}

// VerifyGatewayURL 外部测评网关地址。未配置 VERIFY_GATEWAY_BASE 时返回空串，
// 调用方据此判定该功能未启用。
func VerifyGatewayURL(protocol string) string {
	if protocol == "claude" {
		if legacyURL := common.GetEnvOrDefaultString("VERIFY_GATEWAY_URL", ""); legacyURL != "" {
			return legacyURL
		}
	}
	base := common.GetEnvOrDefaultString("VERIFY_GATEWAY_BASE", "")
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/api/verify/" + protocol
}

// VerifyTimeoutSeconds 单次测评最长时间（秒）。
// 默认 600s（10 分钟）：上游测评网关在大模型 / 慢账户场景下偶发 5-8 分钟，
// 180s 容易把正常请求过早判 cancelled。env VERIFY_TIMEOUT_SEC 可覆盖。
func VerifyTimeoutSeconds() int {
	n := common.GetEnvOrDefault("VERIFY_TIMEOUT_SEC", 600)
	if n < 30 {
		return 30
	}
	return n
}

// 运行中报告的 cancel 注册表：report_id → context.CancelFunc。
// 用于支持前端"终止 running 测试"操作。进程重启会清空，需配合 DB 兜底。
var (
	verifyCancelMu sync.Mutex
	verifyCancels  = make(map[int64]context.CancelFunc)
)

// RegisterVerifyCancel 注册一个 running 报告的 cancel 函数。
// 同一 reportId 重复注册视为替换。
func RegisterVerifyCancel(reportId int64, cancel context.CancelFunc) {
	verifyCancelMu.Lock()
	verifyCancels[reportId] = cancel
	verifyCancelMu.Unlock()
}

// UnregisterVerifyCancel 反注册。测评结束 / FINALIZE 后调用，防止泄漏。
func UnregisterVerifyCancel(reportId int64) {
	verifyCancelMu.Lock()
	delete(verifyCancels, reportId)
	verifyCancelMu.Unlock()
}

// CancelVerify 主动取消一个 running 报告。
// 返回 true 表示找到了对应的 cancel 函数并已调用；false 表示未找到（已结束或进程重启丢失）。
// 调用者在 false 时应做 DB 兜底（把 status=running 直接改成 cancelled）。
func CancelVerify(reportId int64) bool {
	verifyCancelMu.Lock()
	cancel, ok := verifyCancels[reportId]
	if ok {
		delete(verifyCancels, reportId)
	}
	verifyCancelMu.Unlock()
	if ok && cancel != nil {
		cancel()
	}
	return ok
}

// VerifyHistoryLimit 每渠道保留最近多少条历史报告。
func VerifyHistoryLimit() int {
	n := common.GetEnvOrDefault("VERIFY_HISTORY_LIMIT", 50)
	if n < 1 {
		return 1
	}
	return n
}
