package helper

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

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
