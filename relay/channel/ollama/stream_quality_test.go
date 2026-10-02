package ollama

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// Ollama 自带读取循环：放行前命中道歉后必须立即退出并关闭上游，不能等上游生成完。
func TestOllamaStreamStopsUpstreamAfterApologyBlock(t *testing.T) {
	prev := *operation_setting.GetResponseQualitySetting()
	t.Cleanup(func() { *operation_setting.GetResponseQualitySetting() = prev })
	cfg := operation_setting.GetResponseQualitySetting()
	cfg.ApplyAllChannels = true
	cfg.BlockApologyEnabled = true
	cfg.LowTokenThreshold = 300

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "test-model"}}
	hold := service.BeginQualityHold(c, info)

	const upstreamRuns = 5 * time.Second
	pr, pw := io.Pipe()
	upstreamStopped := make(chan bool, 1)
	go func() {
		defer pw.Close()
		if _, err := io.WriteString(pw, "{\"model\":\"m\",\"message\":{\"role\":\"assistant\",\"content\":\"I'm sorry, I can't help with that.\"},\"done\":false}\n"); err != nil {
			upstreamStopped <- true
			return
		}
		deadline := time.Now().Add(upstreamRuns)
		for time.Now().Before(deadline) {
			if _, err := io.WriteString(pw, "{\"model\":\"m\",\"message\":{\"role\":\"assistant\",\"content\":\"more text \"},\"done\":false}\n"); err != nil {
				upstreamStopped <- true
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		upstreamStopped <- false
	}()

	start := time.Now()
	_, _ = ollamaStreamHandler(c, info, &http.Response{Body: pr})
	elapsed := time.Since(start)

	if hold.BlockedError() == nil {
		t.Fatal("apology must block before release")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("kept reading upstream for %v after the block (upstream runs %v)", elapsed, upstreamRuns)
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
