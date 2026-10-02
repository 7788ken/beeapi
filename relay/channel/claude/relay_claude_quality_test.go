package claude

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// OpenAI 格式客户端打 Claude 渠道（线上 4m10s 那条的路径）：命中道歉后立即停流，
// 输出按已收到的内容计，输入沿用 message_start。
func TestClaudeStreamStopsUpstreamAfterApologyBlock(t *testing.T) {
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
	info := &relaycommon.RelayInfo{
		RelayFormat:        types.RelayFormatOpenAI,
		IsStream:           true,
		ShouldIncludeUsage: true,
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "claude-test"},
	}
	hold := service.BeginQualityHold(c, info)

	const upstreamRuns = 5 * time.Second
	pr, pw := io.Pipe()
	upstreamStopped := make(chan bool, 1)
	go func() {
		defer pw.Close()
		for _, event := range []string{
			`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":12,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"I'm sorry, I can't help with that."}}`,
		} {
			if _, err := io.WriteString(pw, "data: "+event+"\n\n"); err != nil {
				upstreamStopped <- true
				return
			}
		}
		deadline := time.Now().Add(upstreamRuns)
		for time.Now().Before(deadline) {
			if _, err := io.WriteString(pw, "data: "+`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"more text "}}`+"\n\n"); err != nil {
				upstreamStopped <- true
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		upstreamStopped <- false
	}()

	start := time.Now()
	usage, apiErr := ClaudeStreamHandler(c, &http.Response{Body: pr}, info)
	elapsed := time.Since(start)

	if apiErr != nil {
		t.Fatalf("unexpected handler error: %v", apiErr)
	}
	if qerr := hold.BlockedError(); qerr == nil || qerr.GetErrorCode() != types.ErrorCodeResponseQualityApology {
		t.Fatalf("apology must block before release, err=%v", qerr)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("kept reading upstream for %v after the block (upstream runs %v)", elapsed, upstreamRuns)
	}
	if got := info.StreamStatus.EndReason(); got != relaycommon.StreamEndReasonQualityBlocked {
		t.Fatalf("end reason = %q, want %q", got, relaycommon.StreamEndReasonQualityBlocked)
	}
	if usage == nil || usage.PromptTokens != 12 || usage.CompletionTokens <= 1 {
		t.Fatalf("usage after cut = %+v, want prompt=12 and completion>1", usage)
	}
	if b := usage.BillingUsage; b == nil || b.ClaudeUsage == nil || b.ClaudeUsage.InputTokens != 12 || b.ClaudeUsage.OutputTokens != usage.CompletionTokens {
		t.Fatalf("billing snapshot after cut = %+v, want input=12 output=%d", b, usage.CompletionTokens)
	}
	select {
	case stopped := <-upstreamStopped:
		if !stopped {
			t.Fatal("upstream ran to completion: body was not closed after the block")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream writer still running: body was not closed after the block")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("blocked stream leaked to client: %q", rec.Body.String())
	}
}
