package common

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStreamStatus_SetEndReason_FirstWins(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	s.SetEndReason(StreamEndReasonDone, nil)
	s.SetEndReason(StreamEndReasonTimeout, nil)
	s.SetEndReason(StreamEndReasonClientGone, fmt.Errorf("context canceled"))

	assert.Equal(t, StreamEndReasonDone, s.EndReason())
	assert.Nil(t, s.EndError())
}

func TestStreamStatus_SetEndReason_WithError(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	expectedErr := fmt.Errorf("read: connection reset")
	s.SetEndReason(StreamEndReasonScannerErr, expectedErr)

	assert.Equal(t, StreamEndReasonScannerErr, s.EndReason())
	assert.Equal(t, expectedErr, s.EndError())
}

func TestStreamStatus_SetEndReason_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	s.SetEndReason(StreamEndReasonDone, nil)
}

func TestStreamStatus_SetEndReason_Concurrent(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	reasons := []StreamEndReason{
		StreamEndReasonDone,
		StreamEndReasonTimeout,
		StreamEndReasonClientGone,
		StreamEndReasonShutdown,
		StreamEndReasonScannerErr,
		StreamEndReasonHandlerStop,
		StreamEndReasonEOF,
		StreamEndReasonPanic,
		StreamEndReasonPingFail,
	}

	var wg sync.WaitGroup
	for _, r := range reasons {
		wg.Add(1)
		go func(reason StreamEndReason) {
			defer wg.Done()
			s.SetEndReason(reason, nil)
		}(r)
	}
	wg.Wait()

	assert.NotEqual(t, StreamEndReasonNone, s.EndReason())
}

func TestStreamStatus_RecordError_Basic(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	s.RecordError("bad json")
	s.RecordError("another bad json")
	s.RecordError("client gone")

	assert.True(t, s.HasErrors())
	assert.Equal(t, 3, s.TotalErrorCount())
	assert.Len(t, s.ErrorMessages(), 3)
}

func TestStreamStatus_RecordError_CapAtMax(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	for i := 0; i < 30; i++ {
		s.RecordError(fmt.Sprintf("error_%d", i))
	}

	assert.Equal(t, maxStreamErrorEntries, len(s.ErrorMessages()))
	assert.Equal(t, 30, s.TotalErrorCount())
}

func TestStreamStatus_RecordError_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	s.RecordError("should not panic")
}

func TestStreamStatus_RecordError_Concurrent(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s.RecordError(fmt.Sprintf("error_%d", idx))
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 100, s.TotalErrorCount())
	assert.LessOrEqual(t, len(s.ErrorMessages()), maxStreamErrorEntries)
}

// 结束原因由 relay 主循环写入、由计费免单判定与日志落库读取，两者分属不同 goroutine。
// 曾经 endReason/endError 是导出字段且只靠 sync.Once 写入，读方完全无锁——sync.Once
// 只保证"写一次"，不为无锁读方建立 happens-before，-race 下必然报 data race。
func TestStreamStatus_ConcurrentReadWrite_NoRace(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		s.SetEndReason(StreamEndReasonClientGone, fmt.Errorf("context canceled"))
		s.RecordError("write failed")
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = s.EndReason()
			_ = s.EndError()
			_ = s.IsNormalEnd()
			_ = s.ErrorMessages()
			_ = s.Summary()
		}
	}()
	wg.Wait()
}

func TestStreamStatus_HasErrors_Empty(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()
	assert.False(t, s.HasErrors())
	assert.Equal(t, 0, s.TotalErrorCount())
}

func TestStreamStatus_HasErrors_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	assert.False(t, s.HasErrors())
	assert.Equal(t, 0, s.TotalErrorCount())
}

func TestStreamStatus_IsNormalEnd(t *testing.T) {
	t.Parallel()
	tests := []struct {
		reason StreamEndReason
		normal bool
	}{
		{StreamEndReasonDone, true},
		{StreamEndReasonEOF, true},
		{StreamEndReasonHandlerStop, true},
		{StreamEndReasonTimeout, false},
		{StreamEndReasonClientGone, false},
		{StreamEndReasonShutdown, false},
		{StreamEndReasonScannerErr, false},
		{StreamEndReasonPanic, false},
		{StreamEndReasonPingFail, false},
		{StreamEndReasonNone, false},
	}
	for _, tt := range tests {
		s := NewStreamStatus()
		s.SetEndReason(tt.reason, nil)
		assert.Equal(t, tt.normal, s.IsNormalEnd(), "reason=%s", tt.reason)
	}
}

func TestStreamStatus_IsNormalEnd_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	assert.True(t, s.IsNormalEnd())
}

func TestStreamStatus_Summary(t *testing.T) {
	t.Parallel()

	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonDone, nil)
	summary := s.Summary()
	assert.Contains(t, summary, "reason=done")
	assert.NotContains(t, summary, "soft_errors")

	s2 := NewStreamStatus()
	s2.SetEndReason(StreamEndReasonTimeout, nil)
	s2.RecordError("bad json")
	s2.RecordError("write failed")
	summary2 := s2.Summary()
	assert.Contains(t, summary2, "reason=timeout")
	assert.Contains(t, summary2, "soft_errors=2")
}

func TestStreamStatus_Summary_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	assert.Equal(t, "StreamStatus<nil>", s.Summary())
}
