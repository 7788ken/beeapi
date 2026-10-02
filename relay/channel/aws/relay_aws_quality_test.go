package aws

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/gin-gonic/gin"
)

func writeBedrockChunk(w http.ResponseWriter, enc *eventstream.Encoder, claudeEvent string) error {
	payload := fmt.Sprintf(`{"bytes":%q}`, base64.StdEncoding.EncodeToString([]byte(claudeEvent)))
	err := enc.Encode(w, eventstream.Message{
		Headers: eventstream.Headers{
			{Name: ":message-type", Value: eventstream.StringValue("event")},
			{Name: ":event-type", Value: eventstream.StringValue("chunk")},
			{Name: ":content-type", Value: eventstream.StringValue("application/json")},
		},
		Payload: []byte(payload),
	})
	if err != nil {
		return err
	}
	w.(http.Flusher).Flush()
	return nil
}

// Bedrock 流不走 StreamScannerHandler：放行前命中道歉后也必须立即退出并关闭上游。
func TestAwsStreamStopsUpstreamAfterApologyBlock(t *testing.T) {
	prev := *operation_setting.GetResponseQualitySetting()
	t.Cleanup(func() { *operation_setting.GetResponseQualitySetting() = prev })
	cfg := operation_setting.GetResponseQualitySetting()
	cfg.ApplyAllChannels = true
	cfg.BlockApologyEnabled = true
	cfg.LowTokenThreshold = 300

	const upstreamRuns = 5 * time.Second
	upstreamStopped := make(chan bool, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
		enc := eventstream.NewEncoder()
		for _, event := range []string{
			`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":12,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"I'm sorry, I can't help with that."}}`,
		} {
			if writeBedrockChunk(w, enc, event) != nil {
				upstreamStopped <- true
				return
			}
		}
		deadline := time.Now().Add(upstreamRuns)
		for time.Now().Before(deadline) {
			select {
			case <-r.Context().Done():
				upstreamStopped <- true
				return
			case <-time.After(20 * time.Millisecond):
			}
			if writeBedrockChunk(w, enc, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"more text "}}`) != nil {
				upstreamStopped <- true
				return
			}
		}
		upstreamStopped <- false
	}))
	defer srv.Close()

	a := &Adaptor{
		AwsClient: bedrockruntime.New(bedrockruntime.Options{
			Region:       "us-east-1",
			BaseEndpoint: aws.String(srv.URL),
			Credentials:  credentials.NewStaticCredentialsProvider("AK", "SK", ""),
			HTTPClient:   srv.Client(),
			Retryer:      aws.NopRetryer{},
		}),
		AwsReq: &bedrockruntime.InvokeModelWithResponseStreamInput{
			ModelId:     aws.String("anthropic.claude-test"),
			Body:        []byte(`{}`),
			ContentType: aws.String("application/json"),
		},
	}

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-test"},
	}
	hold := service.BeginQualityHold(c, info)

	start := time.Now()
	apiErr, usage := awsStreamHandler(c, info, a)
	elapsed := time.Since(start)

	if apiErr != nil {
		t.Fatalf("unexpected handler error: %v", apiErr)
	}
	if qerr := hold.BlockedError(); qerr == nil || qerr.GetErrorCode() != types.ErrorCodeResponseQualityApology {
		t.Fatalf("apology must block before release, err=%v", qerr)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("kept reading Bedrock for %v after the block (upstream runs %v)", elapsed, upstreamRuns)
	}
	if got := info.StreamStatus.EndReason(); got != relaycommon.StreamEndReasonQualityBlocked {
		t.Fatalf("end reason = %q, want %q", got, relaycommon.StreamEndReasonQualityBlocked)
	}
	// 断开后按已收到的内容计输出，输入沿用上游 message_start 报告的值；计费快照不能停在 message_start 的 1 个输出。
	if usage == nil || usage.PromptTokens != 12 || usage.CompletionTokens <= 1 {
		t.Fatalf("usage after cut = %+v, want prompt=12 and completion>1", usage)
	}
	if b := usage.BillingUsage; b == nil || b.ClaudeUsage == nil || b.ClaudeUsage.InputTokens != 12 || b.ClaudeUsage.OutputTokens != usage.CompletionTokens {
		t.Fatalf("billing snapshot after cut = %+v, want input=12 output=%d", b, usage.CompletionTokens)
	}
	select {
	case stopped := <-upstreamStopped:
		if !stopped {
			t.Fatal("upstream ran to completion: Bedrock stream was not closed after the block")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Bedrock stream still open after the block")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("blocked stream leaked to client: %q", rec.Body.String())
	}
}
