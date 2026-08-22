package common

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type StreamEndReason string

const (
	StreamEndReasonNone        StreamEndReason = ""
	StreamEndReasonDone        StreamEndReason = "done"
	StreamEndReasonTimeout     StreamEndReason = "timeout"
	StreamEndReasonClientGone  StreamEndReason = "client_gone"
	StreamEndReasonShutdown    StreamEndReason = "shutdown"
	StreamEndReasonScannerErr  StreamEndReason = "scanner_error"
	StreamEndReasonHandlerStop StreamEndReason = "handler_stop"
	StreamEndReasonEOF         StreamEndReason = "eof"
	StreamEndReasonPanic       StreamEndReason = "panic"
	StreamEndReasonPingFail    StreamEndReason = "ping_fail"
)

const maxStreamErrorEntries = 20

type StreamErrorEntry struct {
	Message   string
	Timestamp time.Time
}

// StreamStatus 由 relay 的主循环与 ping / scanner / dataHandler 三个 goroutine 并发访问，
// 且其结束原因参与计费免单判定，因此所有字段都必须经 mu 读写：字段保持私有以让编译器强制这一点。
type StreamStatus struct {
	mu         sync.Mutex
	endSet     bool
	endReason  StreamEndReason
	endError   error
	errors     []StreamErrorEntry
	errorCount int
}

func NewStreamStatus() *StreamStatus {
	return &StreamStatus{}
}

// SetEndReason 只记录首个结束原因，后续调用忽略。
func (s *StreamStatus) SetEndReason(reason StreamEndReason, err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.endSet {
		return
	}
	s.endSet = true
	s.endReason = reason
	s.endError = err
}

func (s *StreamStatus) EndReason() StreamEndReason {
	if s == nil {
		return StreamEndReasonNone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endReason
}

func (s *StreamStatus) EndError() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endError
}

// ErrorMessages 返回软错误文案的拷贝，避免调用方在锁外遍历内部 slice。
func (s *StreamStatus) ErrorMessages() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.errors))
	for _, e := range s.errors {
		out = append(out, e.Message)
	}
	return out
}

func (s *StreamStatus) RecordError(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errorCount++
	if len(s.errors) < maxStreamErrorEntries {
		s.errors = append(s.errors, StreamErrorEntry{
			Message:   msg,
			Timestamp: time.Now(),
		})
	}
}

func (s *StreamStatus) HasErrors() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errorCount > 0
}

func (s *StreamStatus) TotalErrorCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errorCount
}

func (s *StreamStatus) IsNormalEnd() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endReason == StreamEndReasonDone ||
		s.endReason == StreamEndReasonEOF ||
		s.endReason == StreamEndReasonHandlerStop
}

func (s *StreamStatus) Summary() string {
	if s == nil {
		return "StreamStatus<nil>"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b := &strings.Builder{}
	fmt.Fprintf(b, "reason=%s", s.endReason)
	if s.endError != nil {
		fmt.Fprintf(b, " end_error=%q", s.endError.Error())
	}
	if s.errorCount > 0 {
		fmt.Fprintf(b, " soft_errors=%d", s.errorCount)
	}
	return b.String()
}
