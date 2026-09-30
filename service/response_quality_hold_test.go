package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func TestQualityHoldDiscardKeepsClientClean(t *testing.T) {
	prev := *operation_setting.GetResponseQualitySetting()
	t.Cleanup(func() {
		*operation_setting.GetResponseQualitySetting() = prev
	})
	operation_setting.GetResponseQualitySetting().BlockApologyEnabled = true

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.ChannelSetting = dto.ChannelSettings{BlockApologyEnabled: true}

	hold := BeginQualityHold(c, info)
	if !hold.active {
		t.Fatal("hold must wrap the writer when both switches are on")
	}
	_, _ = c.Writer.Write([]byte("data: should not leak\n\n"))
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Set(qualityEventStreamHeadersKey, true)
	hold.Discard()

	if rec.Body.Len() != 0 {
		t.Fatalf("discarded hold leaked body: %q", rec.Body.String())
	}
	if rec.Header().Get("Content-Type") == "text/event-stream" {
		t.Fatal("discarded hold must not keep SSE headers on the real writer")
	}
	if _, exists := c.Get(qualityEventStreamHeadersKey); exists {
		t.Fatal("stream header flag must be cleared so the next attempt can set headers")
	}
}

func TestQualityHoldFlushWritesBufferedBody(t *testing.T) {
	prev := *operation_setting.GetResponseQualitySetting()
	t.Cleanup(func() {
		*operation_setting.GetResponseQualitySetting() = prev
	})
	operation_setting.GetResponseQualitySetting().BlockLowTokenEnabled = true

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.ChannelSetting = dto.ChannelSettings{BlockLowTokenEnabled: true}

	hold := BeginQualityHold(c, info)
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write([]byte(`{"ok":true}`))
	if err := hold.Flush(); err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != `{"ok":true}` {
		t.Fatalf("flushed body = %q", rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestQualityHoldReleasesAfterVisibleThreshold(t *testing.T) {
	cfg, info, c, rec := qualityHoldFixture(t)
	cfg.BlockApologyEnabled = true
	cfg.BlockLowTokenEnabled = true
	cfg.LowTokenThreshold = 4
	info.ChannelSetting.BlockApologyEnabled = true
	info.ChannelSetting.BlockLowTokenEnabled = true

	hold := BeginQualityHold(c, info)
	NoteQualityStreamDelta(c, QualityStreamDelta{Visible: "Hi"})
	_, _ = c.Writer.Write([]byte("chunk-1"))
	if rec.Body.Len() != 0 || hold.Released() {
		t.Fatalf("short visible text must stay buffered, released=%v body=%q", hold.Released(), rec.Body.String())
	}

	NoteQualityStreamDelta(c, QualityStreamDelta{Visible: strings.Repeat("word ", 30)})
	if !hold.Released() || !strings.Contains(rec.Body.String(), "chunk-1") {
		t.Fatalf("crossing the threshold must flush the buffer, released=%v body=%q", hold.Released(), rec.Body.String())
	}
	_, _ = c.Writer.Write([]byte("chunk-2"))
	NoteQualityStreamDelta(c, QualityStreamDelta{Visible: " I'm sorry, I cannot"})
	_, _ = c.Writer.Write([]byte("sorry-chunk"))
	if hold.BlockedError() != nil || !strings.Contains(rec.Body.String(), "chunk-2") || !strings.Contains(rec.Body.String(), "sorry-chunk") {
		t.Fatalf("text after release must pass through, blocked=%v body=%q", hold.BlockedError(), rec.Body.String())
	}
}

func TestQualityHoldBlocksApologyBeforeRelease(t *testing.T) {
	cfg, info, c, rec := qualityHoldFixture(t)
	cfg.BlockApologyEnabled = true
	cfg.LowTokenThreshold = 300
	info.ChannelSetting.BlockApologyEnabled = true

	hold := BeginQualityHold(c, info)
	_, _ = c.Writer.Write([]byte("prefix"))
	NoteQualityStreamDelta(c, QualityStreamDelta{Visible: "I'm sorry, I cannot help with that."})
	_, _ = c.Writer.Write([]byte("leak"))
	if hold.BlockedError() == nil || hold.BlockedError().GetErrorCode() != types.ErrorCodeResponseQualityApology {
		t.Fatalf("apology before release must block, err=%v", hold.BlockedError())
	}
	before := hold.writer.buf.Len()
	_, _ = c.Writer.Write([]byte("LEAK"))
	if hold.writer.buf.Len() != before {
		t.Fatal("writes after an apology block must be dropped")
	}
	hold.Discard()
	if rec.Body.Len() != 0 {
		t.Fatalf("blocked hold leaked body: %q", rec.Body.String())
	}
}

func TestQualityHoldReasoningDoesNotReleaseOrApologize(t *testing.T) {
	cfg, info, c, rec := qualityHoldFixture(t)
	cfg.BlockApologyEnabled = true
	cfg.BlockLowTokenEnabled = true
	cfg.LowTokenThreshold = 4
	info.ChannelSetting.BlockApologyEnabled = true
	info.ChannelSetting.BlockLowTokenEnabled = true

	hold := BeginQualityHold(c, info)
	NoteQualityStreamDelta(c, QualityStreamDelta{Reasoning: "I'm sorry " + strings.Repeat("word ", 40)})
	_, _ = c.Writer.Write([]byte("think"))
	if hold.Released() || hold.BlockedError() != nil || rec.Body.Len() != 0 {
		t.Fatalf("reasoning must stay buffered, released=%v blocked=%v body=%q", hold.Released(), hold.BlockedError(), rec.Body.String())
	}
	NoteQualityStreamDelta(c, QualityStreamDelta{Visible: strings.Repeat("word ", 30)})
	if !hold.Released() || !strings.Contains(rec.Body.String(), "think") {
		t.Fatalf("visible text must release the buffered reasoning, released=%v body=%q", hold.Released(), rec.Body.String())
	}
}

func TestQualityHoldToolCallReleasesImmediately(t *testing.T) {
	cfg, info, c, rec := qualityHoldFixture(t)
	cfg.BlockLowTokenEnabled = true
	cfg.LowTokenThreshold = 300
	info.ChannelSetting.BlockLowTokenEnabled = true

	hold := BeginQualityHold(c, info)
	_, _ = c.Writer.Write([]byte("partial"))
	NoteQualityStreamDelta(c, QualityStreamDelta{ToolCalls: 1})
	if !hold.Released() || !strings.Contains(rec.Body.String(), "partial") {
		t.Fatalf("tool call must release, released=%v body=%q", hold.Released(), rec.Body.String())
	}
	if err := ApplyResponseQualityFilter(c, info, &dto.Usage{CompletionTokens: 3}); err != nil {
		t.Fatalf("tool call must skip the end filter: %+v", err)
	}
}

func qualityHoldFixture(t *testing.T) (*operation_setting.ResponseQualitySetting, *relaycommon.RelayInfo, *gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	prev := *operation_setting.GetResponseQualitySetting()
	t.Cleanup(func() {
		*operation_setting.GetResponseQualitySetting() = prev
	})
	cfg := operation_setting.GetResponseQualitySetting()
	cfg.ApplyAllChannels = true
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-test"}}
	return cfg, info, c, rec
}
