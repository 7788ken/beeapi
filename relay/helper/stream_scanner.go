package helper

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/httplifecycle"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
)

const (
	InitialScannerBufferSize    = 64 << 10 // 64KB (64*1024)
	DefaultMaxScannerBufferSize = 64 << 20 // 64MB (64*1024*1024) default SSE buffer size
	DefaultPingInterval         = 10 * time.Second
)

func getScannerBufferSize() int {
	if constant.StreamScannerMaxBufferMB > 0 {
		return constant.StreamScannerMaxBufferMB << 20
	}
	return DefaultMaxScannerBufferSize
}

// NewStreamScanner 创建带上游流式响应缓冲区上限的 scanner，避免超长 SSE 行触发 bufio 默认 64KB 限制
func NewStreamScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, InitialScannerBufferSize), getScannerBufferSize())
	return scanner
}

func StreamScannerHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, dataHandler func(data string, sr *StreamResult)) {

	if resp == nil || dataHandler == nil {
		return
	}

	// 无条件新建 StreamStatus
	info.StreamStatus = relaycommon.NewStreamStatus()

	streamingTimeout := time.Duration(constant.StreamingTimeout) * time.Second

	var (
		stopChan    = make(chan bool)
		stopOnce    sync.Once
		cleanupOnce sync.Once
		scanner     = NewStreamScanner(resp.Body)
		ticker      = time.NewTicker(streamingTimeout)
		pingTicker  *time.Ticker
		writeMutex  sync.Mutex     // Mutex to protect concurrent writes
		wg          sync.WaitGroup // 用于等待所有 goroutine 退出
	)
	stop := func() {
		stopOnce.Do(func() {
			close(stopChan)
		})
	}

	generalSettings := operation_setting.GetGeneralSetting()
	pingEnabled := generalSettings.PingIntervalEnabled && !info.DisablePing
	pingInterval := time.Duration(generalSettings.PingIntervalSeconds) * time.Second
	if pingInterval <= 0 {
		pingInterval = DefaultPingInterval
	}

	if pingEnabled {
		pingTicker = time.NewTicker(pingInterval)
	}

	if common.DebugEnabled {
		// print timeout and ping interval for debugging
		println("relay timeout seconds:", common.RelayTimeout)
		println("relay max idle conns:", common.RelayMaxIdleConns)
		println("relay max idle conns per host:", common.RelayMaxIdleConnsPerHost)
		println("streaming timeout seconds:", int64(streamingTimeout.Seconds()))
		println("ping interval seconds:", int64(pingInterval.Seconds()))
	}

	scanner.Split(bufio.ScanLines)
	SetEventStreamHeaders(c)

	ctx, cancel := context.WithCancel(httplifecycle.StreamContext(c.Request.Context()))
	cleanup := func() {
		cleanupOnce.Do(func() {
			cancel()
			stop()
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
			ticker.Stop()
			if pingTicker != nil {
				pingTicker.Stop()
			}
			done := make(chan struct{})
			gopool.Go(func() {
				wg.Wait()
				close(done)
			})
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				logger.LogError(c, "timeout waiting for goroutines to exit")
			}
		})
	}
	defer cleanup()

	ctx = context.WithValue(ctx, "stop_chan", stopChan)

	// Handle ping data sending with improved error handling
	if pingEnabled && pingTicker != nil {
		wg.Add(1)
		gopool.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					logger.LogError(c, fmt.Sprintf("ping goroutine panic: %v", r))
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("ping panic: %v", r))
					stop()
				}
				if common.DebugEnabled {
					println("ping goroutine exited")
				}
				wg.Done()
			}()

			// 添加超时保护，防止 goroutine 无限运行
			maxPingDuration := 30 * time.Minute // 最大 ping 持续时间
			pingTimeout := time.NewTimer(maxPingDuration)
			defer pingTimeout.Stop()

			for {
				select {
				case <-pingTicker.C:
					// 使用超时机制防止写操作阻塞
					done := make(chan error, 1)
					// 必须纳入 wg：本 goroutine 会写客户端响应（c.Writer），若不等它退出，
					// 它可能在 StreamScannerHandler 返回、gin 已结束该请求之后才落笔。
					// 外层 ping goroutine 此刻仍持有一个计数，Add 与 Wait 不会竞争。
					wg.Add(1)
					gopool.Go(func() {
						defer wg.Done()
						writeMutex.Lock()
						defer writeMutex.Unlock()
						done <- PingData(c)
					})

					select {
					case err := <-done:
						if err != nil {
							logger.LogError(c, "ping data error: "+err.Error())
							info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPingFail, err)
							return
						}
						if common.DebugEnabled {
							println("ping data sent")
						}
					case <-time.After(10 * time.Second):
						logger.LogError(c, "ping data send timeout")
						info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPingFail, fmt.Errorf("ping send timeout"))
						return
					case <-ctx.Done():
						return
					case <-stopChan:
						return
					}
				case <-ctx.Done():
					return
				case <-stopChan:
					return
				case <-c.Request.Context().Done():
					// 监听客户端断开连接
					return
				case <-pingTimeout.C:
					logger.LogError(c, "ping goroutine max duration reached")
					return
				}
			}
		})
	}

	dataChan := make(chan string, 10)

	wg.Add(1)
	gopool.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				logger.LogError(c, fmt.Sprintf("data handler goroutine panic: %v", r))
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("handler panic: %v", r))
			}
			stop()
			wg.Done()
		}()
		sr := newStreamResult(info.StreamStatus)
		for data := range dataChan {
			sr.reset()
			writeMutex.Lock()
			dataHandler(data, sr)
			writeMutex.Unlock()
			if sr.IsStopped() {
				return
			}
		}
	})

	// Scanner goroutine with improved error handling
	wg.Add(1)
	common.RelayCtxGo(ctx, func() {
		defer func() {
			close(dataChan)
			if r := recover(); r != nil {
				logger.LogError(c, fmt.Sprintf("scanner goroutine panic: %v", r))
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("scanner panic: %v", r))
			}
			stop()
			if common.DebugEnabled {
				println("scanner goroutine exited")
			}
			wg.Done()
		}()

		for scanner.Scan() {
			// 检查是否需要停止
			select {
			case <-stopChan:
				return
			case <-ctx.Done():
				return
			default:
			}

			ticker.Reset(streamingTimeout)
			data := scanner.Text()
			if common.DebugEnabled {
				println(data)
			}

			if len(data) < 6 {
				continue
			}
			if data[:5] != "data:" && data[:6] != "[DONE]" {
				continue
			}
			data = data[5:]
			data = strings.TrimSpace(data)
			if data == "" {
				continue
			}
			if !strings.HasPrefix(data, "[DONE]") {
				info.SetFirstResponseTime()
				info.ReceivedResponseCount++

				select {
				case dataChan <- data:
				case <-ctx.Done():
					return
				case <-stopChan:
					return
				}
			} else {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
				if common.DebugEnabled {
					println("received [DONE], stopping scanner")
				}
				return
			}
		}

		if err := scanner.Err(); err != nil {
			if err != io.EOF {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, err)
				// 仅当 scanner_error 是真正的首因时才记录错误日志
				// 如果 end_reason 已被设为 client_gone/timeout 等，说明此错误只是清理副作用
				if info.StreamStatus.EndReason() == relaycommon.StreamEndReasonScannerErr {
					logger.LogError(c, "scanner error: "+err.Error())
				}
			}
		}
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	})

	// 主循环等待完成或超时
	select {
	case <-ticker.C:
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonTimeout, nil)
	case <-stopChan:
		// EndReason already set by the goroutine that triggered stopChan
	case <-ctx.Done():
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonShutdown, ctx.Err())
	case <-c.Request.Context().Done():
		// 客户端提前断开：记录 client_gone 供计费免单护栏判定（防秒断刷 cache），
		// 但不中断 drain——继续等上游读到底拿真实 usage 完成计费，或 idle ticker 收口。
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, c.Request.Context().Err())
		select {
		case <-ticker.C:
		case <-stopChan:
		}
	}

	cleanup()
	if info.StreamStatus.IsNormalEnd() && !info.StreamStatus.HasErrors() {
		logger.LogInfo(c, fmt.Sprintf("stream ended: %s", info.StreamStatus.Summary()))
	} else {
		logger.LogError(c, fmt.Sprintf("stream ended: %s, received=%d", info.StreamStatus.Summary(), info.ReceivedResponseCount))
	}
}
