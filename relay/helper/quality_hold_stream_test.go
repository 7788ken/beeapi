package helper

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func TestStringDataReleasesQualityHoldAfterVisibleThreshold(t *testing.T) {
	prev := *operation_setting.GetResponseQualitySetting()
	t.Cleanup(func() {
		*operation_setting.GetResponseQualitySetting() = prev
	})
	cfg := operation_setting.GetResponseQualitySetting()
	cfg.ApplyAllChannels = true
	cfg.BlockApologyEnabled = true
	cfg.BlockLowTokenEnabled = true
	cfg.LowTokenThreshold = 4

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-test"}}
	info.ChannelSetting.BlockApologyEnabled = true
	info.ChannelSetting.BlockLowTokenEnabled = true
	hold := service.BeginQualityHold(c, info)

	short := string(mustChatChunk(t, "Hi"))
	if err := StringData(c, short); err != nil {
		t.Fatal(err)
	}
	if hold.Released() || rec.Body.Len() != 0 {
		t.Fatalf("first short chunk must stay buffered, released=%v body=%q", hold.Released(), rec.Body.String())
	}

	if err := StringData(c, string(mustChatChunk(t, strings.Repeat("word ", 30)))); err != nil {
		t.Fatal(err)
	}
	if !hold.Released() || !strings.Contains(rec.Body.String(), "Hi") {
		t.Fatalf("threshold chunk must flush earlier SSE, released=%v body=%q", hold.Released(), rec.Body.String())
	}
	if strings.Count(info.QualityInspect.Text, "Hi") != 1 {
		t.Fatalf("visible text noted twice: %q", info.QualityInspect.Text)
	}
}

// 放行前命中道歉后必须立即停止读取并关闭上游，不能等上游把后面被丢弃的内容生成完。
func TestStreamScannerStopsUpstreamAfterApologyBlock(t *testing.T) {
	prev := *operation_setting.GetResponseQualitySetting()
	oldTimeout := constant.StreamingTimeout
	t.Cleanup(func() {
		*operation_setting.GetResponseQualitySetting() = prev
		constant.StreamingTimeout = oldTimeout
	})
	constant.StreamingTimeout = 30
	cfg := operation_setting.GetResponseQualitySetting()
	cfg.ApplyAllChannels = true
	cfg.BlockApologyEnabled = true
	cfg.LowTokenThreshold = 300

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-test"}}
	hold := service.BeginQualityHold(c, info)

	apology := "data: " + string(mustChatChunk(t, "I'm sorry, I can't help with that.")) + "\n"
	filler := "data: " + string(mustChatChunk(t, "more text ")) + "\n"
	const upstreamRuns = 5 * time.Second
	pr, pw := io.Pipe()
	writeErr := make(chan error, 1)
	go func() {
		defer pw.Close()
		if _, err := io.WriteString(pw, apology); err != nil {
			writeErr <- err
			return
		}
		deadline := time.Now().Add(upstreamRuns)
		for time.Now().Before(deadline) {
			if _, err := io.WriteString(pw, filler); err != nil {
				writeErr <- err
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		writeErr <- nil
	}()

	start := time.Now()
	StreamScannerHandler(c, &http.Response{Body: pr}, info, func(data string, sr *StreamResult) {
		_ = StringData(c, data)
	})
	elapsed := time.Since(start)

	if qerr := hold.BlockedError(); qerr == nil || qerr.GetErrorCode() != types.ErrorCodeResponseQualityApology {
		t.Fatalf("apology must block before release, err=%v", qerr)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("scanner kept reading upstream for %v after the block (upstream runs %v)", elapsed, upstreamRuns)
	}
	if got := info.StreamStatus.EndReason(); got != relaycommon.StreamEndReasonQualityBlocked {
		t.Fatalf("end reason = %q, want %q", got, relaycommon.StreamEndReasonQualityBlocked)
	}
	select {
	case err := <-writeErr:
		if err == nil {
			t.Fatal("upstream finished writing: body was not closed after the block")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream writer still running: body was not closed after the block")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("blocked stream leaked to client: %q", rec.Body.String())
	}
}

func mustChatChunk(t *testing.T, content string) []byte {
	t.Helper()
	body, err := common.Marshal(dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &content},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}
