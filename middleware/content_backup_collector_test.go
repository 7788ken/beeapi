package middleware

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
)

type contentBackupEngineOptions struct {
	collector      bool
	channelID      int
	channelEnabled bool
	handler        gin.HandlerFunc
}

type contentBackupEngineRun struct {
	ops                     []string
	body                    []byte
	code                    int
	header                  http.Header
	closesAfterCollector    int
	storagePresentInHandler bool
}

func newContentBackupEngine(opts contentBackupEngineOptions, state *contentBackupEngineState) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(BodyStorageCleanup())
	engine.Use(func(c *gin.Context) {
		raw, _ := io.ReadAll(c.Request.Body)
		state.storage = newContentBackupTestStorage(raw)
		c.Set(common.KeyBodyStorage, state.storage)
		c.Request.Body = io.NopCloser(state.storage)
		c.Set(common.RequestIdKey, "req-e2e-1")
		c.Set("id", 10086)
		c.Set("token_id", 42)
		c.Set(string(constant.ContextKeyOriginalModel), "gpt-test")
		c.Set(string(constant.ContextKeyRequestStartTime), time.Date(2026, 9, 15, 4, 34, 56, 0, time.UTC))
		c.Set(string(constant.ContextKeyChannelId), opts.channelID)
		c.Set(string(constant.ContextKeyChannelName), "example")
		c.Set(string(constant.ContextKeyChannelType), 1)
		c.Set(string(constant.ContextKeyChannelSetting), contentBackupTestChannelSettings{enabled: opts.channelEnabled})
	})
	engine.Use(func(c *gin.Context) {
		c.Next()
		state.closesAfterCollector = state.storage.closeCount()
	})
	if opts.collector {
		engine.Use(ContentBackupCollector())
	}
	register := func(method, path string) {
		engine.Handle(method, path, func(c *gin.Context) {
			_, state.storagePresentInHandler = c.Get(common.KeyBodyStorage)
			opts.handler(c)
		})
	}
	register(http.MethodPost, "/v1/chat/completions")
	register(http.MethodPost, "/v1/messages")
	register(http.MethodPost, "/v1/responses")
	register(http.MethodPost, "/v1/audio/transcriptions")
	register(http.MethodPost, "/v1/images/generations")
	register(http.MethodPost, "/v1/rerank")
	register(http.MethodGet, "/v1/realtime")
	return engine
}

type contentBackupEngineState struct {
	mu                      sync.Mutex
	storage                 *contentBackupTestStorage
	closesAfterCollector    int
	storagePresentInHandler bool
}

func runContentBackupEngine(t *testing.T, opts contentBackupEngineOptions, path, contentType string, body []byte) (contentBackupEngineRun, *contentBackupEngineState) {
	t.Helper()
	state := &contentBackupEngineState{}
	engine := newContentBackupEngine(opts, state)
	recorder := newContentBackupOrderRecorder()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if path == "/v1/realtime" {
		request = httptest.NewRequest(http.MethodGet, path, nil)
	}
	engine.ServeHTTP(recorder, request)
	ops, out, code := recorder.snapshot()
	return contentBackupEngineRun{
		ops:                     ops,
		body:                    out,
		code:                    code,
		header:                  recorder.Header().Clone(),
		closesAfterCollector:    state.closesAfterCollector,
		storagePresentInHandler: state.storagePresentInHandler,
	}, state
}

func contentBackupSSERounds(enabled ...bool) []service.ContentBackupChannelRound {
	rounds := make([]service.ContentBackupChannelRound, 0, len(enabled))
	for i, on := range enabled {
		rounds = append(rounds, service.ContentBackupChannelRound{
			ChannelID:     12 + i,
			ChannelName:   fmt.Sprintf("channel-%d", i),
			ChannelType:   1,
			BackupEnabled: on,
		})
	}
	return rounds
}

// contentBackupRelayLikeHandler mimics controller.Relay: per round channel
// snapshots plus an error response written from a defer.
func contentBackupRelayLikeHandler(rounds []service.ContentBackupChannelRound, apiErr error, payload func(c *gin.Context)) gin.HandlerFunc {
	return func(c *gin.Context) {
		var relayErr error
		defer func() {
			if relayErr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": relayErr.Error(), "type": "invalid_request_error"}})
			}
		}()
		if len(rounds) > 0 {
			service.ContentBackupRecordChannelRound(c, rounds[0])
		}
		if apiErr != nil {
			relayErr = apiErr
			return
		}
		for _, round := range rounds[1:] {
			service.ContentBackupRecordChannelRound(c, round)
		}
		payload(c)
	}
}

func contentBackupSSEPayload(events []string) func(c *gin.Context) {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.WriteHeaderNow()
		for _, event := range events {
			c.Render(-1, &common.CustomEvent{Data: event})
			c.Writer.Flush()
		}
	}
}

func TestContentBackupCollectorClientResponseIdenticalWhenEnabled(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-test","stream":true,"user":"u-1","messages":[{"role":"user","content":"hello"}]}`)
	events := []string{"data: {\"id\":\"1\"}", "data: {\"id\":\"2\"}", "data: [DONE]"}

	scenarios := []struct {
		name    string
		path    string
		body    []byte
		handler gin.HandlerFunc
	}{
		{
			name: "sse_stream",
			path: "/v1/chat/completions",
			body: requestBody,
			handler: contentBackupRelayLikeHandler(
				contentBackupSSERounds(true),
				nil,
				contentBackupSSEPayload(events),
			),
		},
		{
			name: "sse_stream_with_retry_round",
			path: "/v1/chat/completions",
			body: requestBody,
			handler: contentBackupRelayLikeHandler(
				contentBackupSSERounds(false, true),
				nil,
				contentBackupSSEPayload(events),
			),
		},
		{
			name: "validation_failure_written_from_defer",
			path: "/v1/chat/completions",
			body: requestBody,
			handler: contentBackupRelayLikeHandler(
				contentBackupSSERounds(true),
				fmt.Errorf("model is required"),
				nil,
			),
		},
		{
			name: "tool_call_json",
			path: "/v1/chat/completions",
			body: requestBody,
			handler: contentBackupRelayLikeHandler(contentBackupSSERounds(true), nil, func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{
					"id":    "chatcmpl-tool",
					"model": "gpt-test",
					"choices": []gin.H{{
						"index": 0,
						"message": gin.H{
							"role": "assistant",
							"tool_calls": []gin.H{{
								"id":   "call_1",
								"type": "function",
								"function": gin.H{
									"name":      "get_weather",
									"arguments": "{\"city\":\"上海\"}",
								},
							}},
						},
					}},
					"usage": gin.H{"prompt_tokens": 3, "completion_tokens": 7},
				})
			}),
		},
		{
			name: "tool_call_sse",
			path: "/v1/chat/completions",
			body: requestBody,
			handler: contentBackupRelayLikeHandler(contentBackupSSERounds(true), nil, contentBackupSSEPayload([]string{
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"get_weather"}}]}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"ci"}}]}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":\"上海\"}"}}]}}]}`,
				"data: [DONE]",
			})),
		},
		{
			name: "binary_multimedia_response",
			path: "/v1/images/generations",
			body: []byte(`{"model":"dall-e","prompt":"cat"}`),
			handler: contentBackupRelayLikeHandler(contentBackupSSERounds(true), nil, func(c *gin.Context) {
				c.Writer.Header().Set("Content-Type", "audio/mpeg")
				c.Writer.WriteHeader(http.StatusOK)
				c.Writer.WriteHeaderNow()
				_, _ = c.Writer.Write([]byte{0x00, 0xff, 0xfe, 0x80, 0xc3, 0x28, 0xa0, 0x00})
				c.Writer.Flush()
				_, _ = c.Writer.Write([]byte{0xe2, 0x82, 0x28, 0x00, 0x01})
			}),
		},
		{
			name: "passthrough_io_copy",
			path: "/v1/chat/completions",
			body: requestBody,
			handler: contentBackupRelayLikeHandler(contentBackupSSERounds(true), nil, func(c *gin.Context) {
				c.Writer.Header().Set("Content-Type", "application/json")
				c.Writer.WriteHeaderNow()
				_, _ = io.Copy(c.Writer, bytes.NewReader([]byte(`{"passthrough":"body"}`)))
				c.Writer.Flush()
			}),
		},
		{
			name: "claude_messages",
			path: "/v1/messages",
			body: []byte(`{"model":"claude","metadata":{"user_id":"claude-user"}}`),
			handler: contentBackupRelayLikeHandler(contentBackupSSERounds(true), nil, contentBackupSSEPayload([]string{
				"event: message_start",
				`data: {"type":"content_block_delta"}`,
				"event: message_stop",
			})),
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			rig := newContentBackupMiddlewareRig()
			rig.install(t)

			off, _ := runContentBackupEngine(t, contentBackupEngineOptions{
				collector:      false,
				channelID:      12,
				channelEnabled: true,
				handler:        scenario.handler,
			}, scenario.path, "application/json", scenario.body)

			rig.mu.Lock()
			rig.enqueued = nil
			rig.mu.Unlock()
			service.ContentBackupResetCaptureCounters()

			on, _ := runContentBackupEngine(t, contentBackupEngineOptions{
				collector:      true,
				channelID:      12,
				channelEnabled: true,
				handler:        scenario.handler,
			}, scenario.path, "application/json", scenario.body)

			if !bytes.Equal(off.body, on.body) {
				t.Fatalf("client bytes changed with the collector installed:\n off=%q\n on=%q", off.body, on.body)
			}
			if off.code != on.code {
				t.Fatalf("status changed: off=%d on=%d", off.code, on.code)
			}
			if !reflect.DeepEqual(off.ops, on.ops) {
				t.Fatalf("write/flush order changed:\n off=%v\n on=%v", off.ops, on.ops)
			}
			if !reflect.DeepEqual(off.header, on.header) {
				t.Fatalf("response headers changed:\n off=%v\n on=%v", off.header, on.header)
			}
		})
	}
}

func TestContentBackupCollectorArchivesFinalClientOutput(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)

	requestBody := []byte(`{"model":"gpt-test","stream":true,"user":"u-1"}`)
	events := []string{"data: {\"id\":\"1\"}", "data: {\"id\":\"2\"}", "data: [DONE]"}
	run, state := runContentBackupEngine(t, contentBackupEngineOptions{
		collector:      true,
		channelID:      12,
		channelEnabled: true,
		handler: contentBackupRelayLikeHandler(
			contentBackupSSERounds(false, true),
			nil,
			contentBackupSSEPayload(events),
		),
	}, "/v1/chat/completions", "application/json", requestBody)

	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("expected one archive, got %d", len(taken))
	}
	capture := taken[0]
	defer capture.Release()

	if !bytes.Equal(contentBackupJoin(capture.ResponseChunks), run.body) {
		t.Fatalf("archived response must equal the final client output:\n archived=%q\n client=%q",
			contentBackupJoin(capture.ResponseChunks), run.body)
	}
	if !bytes.Equal(contentBackupJoin(capture.RequestChunks), requestBody) {
		t.Fatalf("archived request = %q, want %q", contentBackupJoin(capture.RequestChunks), requestBody)
	}
	if capture.Meta.ChannelID != 13 {
		t.Fatalf("archive must be attributed to the final attempted channel, got %d", capture.Meta.ChannelID)
	}
	if capture.Meta.HTTPStatus != http.StatusOK {
		t.Fatalf("http_status = %d, want 200", capture.Meta.HTTPStatus)
	}
	if !capture.Meta.Stream {
		t.Fatal("an event-stream response must be marked as a stream")
	}
	if capture.Meta.SessionValue == nil || *capture.Meta.SessionValue != "u-1" {
		t.Fatalf("session = %v, want u-1", capture.Meta.SessionValue)
	}
	if capture.Meta.Endpoint != "/v1/chat/completions" || capture.Meta.RequestID != "req-e2e-1" {
		t.Fatalf("identity frozen wrong: %+v", capture.Meta)
	}
	if capture.Meta.UserID != 10086 || capture.Meta.TokenID != 42 {
		t.Fatalf("user/token identity frozen wrong: %+v", capture.Meta)
	}
	if err := contentbackup.ValidateMetadata(capture.Meta); err != nil {
		t.Fatalf("metadata must validate: %v", err)
	}
	if run.closesAfterCollector != 0 {
		t.Fatalf("the collector closed the shared BodyStorage %d times", run.closesAfterCollector)
	}
	if state.storage.closeCount() != 1 {
		t.Fatalf("BodyStorageCleanup must remain the only owner, closes = %d", state.storage.closeCount())
	}
	if !run.storagePresentInHandler {
		t.Fatal("the BodyStorage must still be reachable inside the handler")
	}
}

func TestContentBackupCollectorSSEInterruptionMarksIncomplete(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)
	requestBody := []byte(`{"model":"gpt-test","stream":true}`)

	handler := func(c *gin.Context) {
		service.ContentBackupRecordChannelRound(c, contentBackupSSERounds(true)[0])
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.WriteHeaderNow()
		for i := 0; i < 5; i++ {
			c.Render(-1, &common.CustomEvent{Data: fmt.Sprintf("data: chunk-%d", i)})
			if err := flushContentBackupWriter(c); err != nil {
				return
			}
		}
	}

	recorder := newContentBackupOrderRecorder()
	recorder.failAfter = 24
	state := &contentBackupEngineState{}
	engine := newContentBackupEngine(contentBackupEngineOptions{
		collector:      true,
		channelID:      12,
		channelEnabled: true,
		handler:        handler,
	}, state)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("an interrupted stream must still be archived, got %d", len(taken))
	}
	capture := taken[0]
	defer capture.Release()
	if capture.Meta.Response.Complete {
		t.Fatalf("response meta = %+v, want complete=false after a client disconnect", capture.Meta.Response)
	}
	if capture.Meta.TerminalReason != service.ContentBackupTerminalClientDisconnect {
		t.Fatalf("terminal_reason = %q, want %q", capture.Meta.TerminalReason, service.ContentBackupTerminalClientDisconnect)
	}
	if !bytes.Equal(contentBackupJoin(capture.ResponseChunks), recorder.body.Bytes()) {
		t.Fatalf("archived prefix = %q, client got %q", contentBackupJoin(capture.ResponseChunks), recorder.body.Bytes())
	}
	if err := contentbackup.ValidateMetadata(capture.Meta); err != nil {
		t.Fatalf("metadata must validate: %v", err)
	}
}

func flushContentBackupWriter(c *gin.Context) error {
	if c.Request != nil && c.Request.Context().Err() != nil {
		return c.Request.Context().Err()
	}
	c.Writer.Flush()
	return nil
}

func TestContentBackupCollectorValidationFailureIsArchivedFromDefer(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)
	requestBody := []byte(`{"model":"gpt-test"}`)

	run, _ := runContentBackupEngine(t, contentBackupEngineOptions{
		collector:      true,
		channelID:      12,
		channelEnabled: true,
		handler: contentBackupRelayLikeHandler(
			contentBackupSSERounds(true),
			fmt.Errorf("messages is required"),
			nil,
		),
	}, "/v1/chat/completions", "application/json", requestBody)

	if run.code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", run.code)
	}
	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("a validation failure after channel selection must be archived, got %d", len(taken))
	}
	capture := taken[0]
	defer capture.Release()
	if capture.Meta.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("http_status = %d, want 400", capture.Meta.HTTPStatus)
	}
	if !bytes.Equal(contentBackupJoin(capture.ResponseChunks), run.body) {
		t.Fatalf("the defer written error body must be archived: %q vs %q", contentBackupJoin(capture.ResponseChunks), run.body)
	}
	if !bytes.Equal(contentBackupJoin(capture.RequestChunks), requestBody) {
		t.Fatalf("request prefix = %q", contentBackupJoin(capture.RequestChunks))
	}
	if capture.Meta.Response.Complete != true || capture.Meta.Response.Truncated {
		t.Fatalf("response meta = %+v, want complete and not truncated", capture.Meta.Response)
	}
}

func TestContentBackupCollectorUpstreamErrorIsArchived(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)
	requestBody := []byte(`{"model":"gpt-test"}`)

	run, _ := runContentBackupEngine(t, contentBackupEngineOptions{
		collector:      true,
		channelID:      12,
		channelEnabled: true,
		handler: func(c *gin.Context) {
			service.ContentBackupRecordChannelRound(c, contentBackupSSERounds(true)[0])
			service.ContentBackupSetTerminalReason(c, "upstream_error")
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "upstream 502", "type": "upstream_error"}})
		},
	}, "/v1/chat/completions", "application/json", requestBody)

	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("expected one archive, got %d", len(taken))
	}
	defer taken[0].Release()
	if taken[0].Meta.HTTPStatus != http.StatusBadGateway {
		t.Fatalf("http_status = %d, want 502", taken[0].Meta.HTTPStatus)
	}
	if taken[0].Meta.TerminalReason != "upstream_error" {
		t.Fatalf("terminal_reason = %q, want the explicit value", taken[0].Meta.TerminalReason)
	}
	if !bytes.Equal(contentBackupJoin(taken[0].ResponseChunks), run.body) {
		t.Fatal("the client visible error body must be archived verbatim")
	}
}

func TestContentBackupCollectorSkipsRealtimeAndNonWhitelistEndpoints(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)

	cases := []struct {
		path        string
		method      string
		contentType string
		body        []byte
	}{
		{path: "/v1/realtime", contentType: "", body: nil},
		{path: "/v1/rerank", contentType: "application/json", body: []byte(`{"model":"rerank","user":"u-1"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			service.ContentBackupResetCaptureCounters()
			handler := func(c *gin.Context) {
				if _, ok := ContentBackupCaptureWriter(c.Writer); ok {
					t.Errorf("%s must not install a capture writer", tc.path)
				}
				if tc.path == "/v1/realtime" {
					if _, _, err := c.Writer.Hijack(); err == nil {
						c.Writer.WriteHeaderNow()
					}
					return
				}
				c.JSON(http.StatusOK, gin.H{"ok": true})
			}
			state := &contentBackupEngineState{}
			engine := newContentBackupEngine(contentBackupEngineOptions{
				collector:      true,
				channelID:      12,
				channelEnabled: true,
				handler:        handler,
			}, state)
			recorder := newContentBackupOrderRecorder()
			var request *http.Request
			if tc.method == http.MethodGet || tc.path == "/v1/realtime" {
				request = httptest.NewRequest(http.MethodGet, tc.path, nil)
			} else {
				request = httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(tc.body))
				request.Header.Set("Content-Type", tc.contentType)
			}
			engine.ServeHTTP(recorder, request)
			if counters := service.ContentBackupCaptureCounters(); counters.Started != 0 {
				t.Fatalf("%s started %d captures, want 0", tc.path, counters.Started)
			}
			stats := service.ContentBackupCaptureBudget().Stats()
			if stats.UsedBytes != 0 || stats.Inflight != 0 {
				t.Fatalf("%s allocated capture budget: %+v", tc.path, stats)
			}
			if len(rig.taken()) != 0 {
				t.Fatalf("%s must not hand off an archive", tc.path)
			}
		})
	}
}

func TestContentBackupCollectorDisabledInstallsNoWriter(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)
	rig.mu.Lock()
	rig.enabled = false
	rig.mu.Unlock()

	handler := func(c *gin.Context) {
		if _, ok := ContentBackupCaptureWriter(c.Writer); ok {
			t.Error("a globally disabled feature must not wrap the writer")
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
	off, _ := runContentBackupEngine(t, contentBackupEngineOptions{
		collector:      true,
		channelID:      12,
		channelEnabled: false,
		handler:        handler,
	}, "/v1/chat/completions", "application/json", []byte(`{"model":"gpt-test","user":"u-1"}`))

	if counters := service.ContentBackupCaptureCounters(); counters.Started != 0 {
		t.Fatalf("started = %d, want 0 while disabled", counters.Started)
	}
	stats := service.ContentBackupCaptureBudget().Stats()
	if stats.UsedBytes != 0 || stats.Inflight != 0 {
		t.Fatalf("disabled feature allocated budget: %+v", stats)
	}
	if len(rig.taken()) != 0 {
		t.Fatal("disabled feature must not hand off")
	}
	if !strings.Contains(string(off.body), "ok") {
		t.Fatalf("relay response must be unaffected: %q", off.body)
	}
}

func TestContentBackupCollectorMultipartUsesAlreadyParsedForm(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)

	var raw bytes.Buffer
	writer := multipart.NewWriter(&raw)
	_ = writer.WriteField("model", "whisper-1")
	_ = writer.WriteField("user", "form-user")
	filePart, _ := writer.CreateFormFile("file", "audio.bin")
	_, _ = filePart.Write([]byte{0x00, 0xff, 0xfe, 0x80, 0x00, 0x01})
	_ = writer.Close()
	body := raw.Bytes()

	handler := func(c *gin.Context) {
		service.ContentBackupRecordChannelRound(c, contentBackupSSERounds(true)[0])
		// Emulates relay/helper.valid_request: the parsed form is already on the request.
		c.Request.MultipartForm = &multipart.Form{Value: map[string][]string{"model": {"whisper-1"}, "user": {"form-user"}}}
		c.JSON(http.StatusOK, gin.H{"text": "transcribed"})
	}
	run, _ := runContentBackupEngine(t, contentBackupEngineOptions{
		collector:      true,
		channelID:      12,
		channelEnabled: true,
		handler:        handler,
	}, "/v1/audio/transcriptions", writer.FormDataContentType(), body)

	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("expected one archive, got %d", len(taken))
	}
	capture := taken[0]
	defer capture.Release()
	if !bytes.Equal(contentBackupJoin(capture.RequestChunks), body) {
		t.Fatal("the raw multipart body must be archived byte for byte")
	}
	if capture.Meta.SessionValue == nil || *capture.Meta.SessionValue != "form-user" {
		t.Fatalf("session = %v, want the already parsed form user", capture.Meta.SessionValue)
	}
	if capture.Meta.SessionSource == nil || *capture.Meta.SessionSource != contentbackup.SessionSourceUser {
		t.Fatalf("session source = %v, want user", capture.Meta.SessionSource)
	}
	if !bytes.Equal(contentBackupJoin(capture.ResponseChunks), run.body) {
		t.Fatal("response archive must equal the client output")
	}
	if capture.Meta.Request.ContentType != writer.FormDataContentType() {
		t.Fatalf("request content_type = %q", capture.Meta.Request.ContentType)
	}
}

func TestContentBackupCollectorBinaryAndToolCallRoundTrip(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)

	requestBody := []byte{0x00, 0x01, 0xff, 0xfe, 0xc3, 0x28, 0xe2, 0x82, 0x28, 0x00}
	responseBody := append([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}, bytes.Repeat([]byte{0x00, 0xff}, 4096)...)

	run, _ := runContentBackupEngine(t, contentBackupEngineOptions{
		collector:      true,
		channelID:      12,
		channelEnabled: true,
		handler: func(c *gin.Context) {
			service.ContentBackupRecordChannelRound(c, contentBackupSSERounds(true)[0])
			c.Writer.Header().Set("Content-Type", "image/png")
			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.WriteHeaderNow()
			_, _ = c.Writer.Write(responseBody[:1000])
			c.Writer.Flush()
			_, _ = io.Copy(c.Writer, bytes.NewReader(responseBody[1000:]))
		},
	}, "/v1/images/generations", "application/octet-stream", requestBody)

	if !bytes.Equal(run.body, responseBody) {
		t.Fatal("the client must receive the exact binary payload")
	}
	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("expected one archive, got %d", len(taken))
	}
	capture := taken[0]
	defer capture.Release()
	if !bytes.Equal(contentBackupJoin(capture.ResponseChunks), responseBody) {
		t.Fatal("binary response must round trip byte for byte")
	}
	if !bytes.Equal(contentBackupJoin(capture.RequestChunks), requestBody) {
		t.Fatal("binary request must round trip byte for byte")
	}
	if capture.Meta.Response.ContentType != "image/png" {
		t.Fatalf("response content_type = %q", capture.Meta.Response.ContentType)
	}
	if capture.Meta.Request.ContentType != "application/octet-stream" {
		t.Fatalf("request content_type = %q", capture.Meta.Request.ContentType)
	}
	// A binary body has no session key to extract; it must be reported absent, never coerced.
	if capture.Meta.SessionValue != nil {
		t.Fatalf("session_value = %q, want nil for a non JSON body", *capture.Meta.SessionValue)
	}
	if capture.Meta.SessionMissingReason == nil {
		t.Fatal("a missing session must carry a reason")
	}
	if err := contentbackup.ValidateMetadata(capture.Meta); err != nil {
		t.Fatalf("metadata must validate: %v", err)
	}
}

func TestContentBackupCollectorConcurrentRequestsStayIsolated(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)

	const requests = 24
	handler := func(c *gin.Context) {
		service.ContentBackupRecordChannelRound(c, contentBackupSSERounds(true)[0])
		tag := c.Request.Header.Get("X-Test-Tag")
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.WriteHeaderNow()

		// relay/helper.StreamScannerHandler writes SSE from a data goroutine and
		// a ping goroutine behind a write mutex; reproduce that here so the
		// capture is proven safe off the request goroutine.
		var writeMutex sync.Mutex
		var wg sync.WaitGroup
		for worker := 0; worker < 2; worker++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				for i := 0; i < 5; i++ {
					writeMutex.Lock()
					c.Render(-1, &common.CustomEvent{Data: fmt.Sprintf("data: %s-%d-%d", tag, worker, i)})
					c.Writer.Flush()
					writeMutex.Unlock()
				}
			}(worker)
		}
		wg.Wait()
	}

	engines := make([]*gin.Engine, requests)
	recorders := make([]*contentBackupOrderRecorder, requests)
	httpRequests := make([]*http.Request, requests)
	var wg sync.WaitGroup
	bodies := make([][]byte, requests)
	clients := make([][]byte, requests)
	states := make([]*contentBackupEngineState, requests)
	// Engines are built sequentially: gin.SetMode writes unsynchronised globals.
	for i := 0; i < requests; i++ {
		body := []byte(fmt.Sprintf(`{"model":"gpt-test","user":"user-%d"}`, i))
		bodies[i] = body
		states[i] = &contentBackupEngineState{}
		engines[i] = newContentBackupEngine(contentBackupEngineOptions{
			collector:      true,
			channelID:      12,
			channelEnabled: true,
			handler:        handler,
		}, states[i])
		recorders[i] = newContentBackupOrderRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Test-Tag", fmt.Sprintf("tag-%d", i))
		httpRequests[i] = request
	}
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			engines[i].ServeHTTP(recorders[i], httpRequests[i])
			clients[i] = append([]byte(nil), recorders[i].body.Bytes()...)
		}(i)
	}
	wg.Wait()

	taken := rig.taken()
	if len(taken) != requests {
		t.Fatalf("archives = %d, want %d", len(taken), requests)
	}
	seen := make(map[string]int, requests)
	for _, capture := range taken {
		if err := contentbackup.ValidateMetadata(capture.Meta); err != nil {
			t.Fatalf("metadata must validate: %v", err)
		}
		if capture.Meta.SessionValue == nil {
			t.Fatal("every concurrent request must keep its own session value")
		}
		session := *capture.Meta.SessionValue
		seen[session]++
		index, err := strconv.Atoi(strings.TrimPrefix(session, "user-"))
		if err != nil || index < 0 || index >= requests {
			t.Fatalf("unexpected session value %q", session)
		}
		if !bytes.Equal(contentBackupJoin(capture.RequestChunks), bodies[index]) {
			t.Fatalf("%s archived another request's body", session)
		}
		response := contentBackupJoin(capture.ResponseChunks)
		if !bytes.Equal(response, clients[index]) {
			t.Fatalf("%s archived response differs from what its client received", session)
		}
		if !strings.Contains(string(response), "tag-"+itoa(index)+"-") {
			t.Fatalf("%s archived another request's response", session)
		}
		if int64(len(response)) != capture.Meta.Response.CapturedBytes {
			t.Fatalf("chunks must sum to captured_bytes: %d vs %d", len(response), capture.Meta.Response.CapturedBytes)
		}
		capture.Release()
	}
	for i := 0; i < requests; i++ {
		if got := seen[fmt.Sprintf("user-%d", i)]; got != 1 {
			t.Fatalf("user-%d archived %d times, want exactly 1", i, got)
		}
		if states[i].storage.closeCount() != 1 {
			t.Fatalf("request %d: BodyStorageCleanup must remain the only owner, closes = %d", i, states[i].storage.closeCount())
		}
	}
	stats := service.ContentBackupCaptureBudget().Stats()
	if stats.UsedBytes != 0 || stats.Inflight != 0 {
		t.Fatalf("concurrent captures leaked budget: %+v", stats)
	}
}

func TestContentBackupCollectorTruncatesOversizedSides(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)
	rig.mu.Lock()
	rig.maxBody = 4096
	rig.mu.Unlock()

	requestBody := bytes.Repeat([]byte("q"), 9000)
	responseBody := bytes.Repeat([]byte("a"), 9000)
	run, _ := runContentBackupEngine(t, contentBackupEngineOptions{
		collector:      true,
		channelID:      12,
		channelEnabled: true,
		handler: func(c *gin.Context) {
			service.ContentBackupRecordChannelRound(c, contentBackupSSERounds(true)[0])
			c.Writer.Header().Set("Content-Type", "application/json")
			c.Writer.WriteHeaderNow()
			_, _ = c.Writer.Write(responseBody[:5000])
			c.Writer.Flush()
			_, _ = c.Writer.Write(responseBody[5000:])
		},
	}, "/v1/chat/completions", "application/json", requestBody)

	if !bytes.Equal(run.body, responseBody) {
		t.Fatal("truncation must never change the client visible bytes")
	}
	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("expected one archive, got %d", len(taken))
	}
	capture := taken[0]
	defer capture.Release()
	if capture.Meta.Request.CapturedBytes != 4096 || capture.Meta.Request.ObservedBytes != 9000 {
		t.Fatalf("request counters = %+v", capture.Meta.Request)
	}
	if capture.Meta.Response.CapturedBytes != 4096 || capture.Meta.Response.ObservedBytes != 9000 {
		t.Fatalf("response counters = %+v", capture.Meta.Response)
	}
	if !capture.Meta.Request.Truncated || !capture.Meta.Response.Truncated {
		t.Fatal("both sides must be marked truncated")
	}
	if err := contentbackup.ValidateMetadata(capture.Meta); err != nil {
		t.Fatalf("truncated metadata must validate: %v", err)
	}
	if capture.Meta.SessionMissingReason == nil || *capture.Meta.SessionMissingReason != contentbackup.SessionMissingReasonUnparseable {
		t.Fatalf("a bounded prefix that cannot be parsed must report unparseable, got %v", capture.Meta.SessionMissingReason)
	}
}

type contentBackupOrderRecorder struct {
	mu          sync.Mutex
	ops         []string
	body        bytes.Buffer
	header      http.Header
	code        int
	failAfter   int
	writeErr    error
	closeOnce   sync.Once
	closeNotify chan bool
}

func newContentBackupOrderRecorder() *contentBackupOrderRecorder {
	return &contentBackupOrderRecorder{header: http.Header{}, writeErr: io.ErrClosedPipe}
}

func (r *contentBackupOrderRecorder) log(entry string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops = append(r.ops, entry)
}

func (r *contentBackupOrderRecorder) Header() http.Header { return r.header }

func (r *contentBackupOrderRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	already := r.body.Len()
	failAfter := r.failAfter
	if failAfter > 0 && already >= failAfter {
		r.ops = append(r.ops, "write-error")
		r.mu.Unlock()
		return 0, r.writeErr
	}
	n, err := r.body.Write(p)
	r.ops = append(r.ops, "write:"+string(p[:min(n, len(p))]))
	r.mu.Unlock()
	return n, err
}

func (r *contentBackupOrderRecorder) WriteHeader(code int) {
	r.mu.Lock()
	r.code = code
	r.ops = append(r.ops, "status:"+itoa(code))
	r.mu.Unlock()
}

func (r *contentBackupOrderRecorder) Flush() { r.log("flush") }

func (r *contentBackupOrderRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hijack is not supported by the test recorder")
}

func (r *contentBackupOrderRecorder) CloseNotify() <-chan bool {
	r.closeOnce.Do(func() { r.closeNotify = make(chan bool, 1) })
	return r.closeNotify
}

func (r *contentBackupOrderRecorder) snapshot() ([]string, []byte, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ops := make([]string, len(r.ops))
	copy(ops, r.ops)
	body := append([]byte(nil), r.body.Bytes()...)
	return ops, body, r.code
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

type contentBackupMiddlewareRig struct {
	mu       sync.Mutex
	enqueued []*contentbackup.Capture
	enabled  bool
	maxBody  int64
}

func newContentBackupMiddlewareRig() *contentBackupMiddlewareRig {
	return &contentBackupMiddlewareRig{enabled: true, maxBody: contentbackup.MaxBodyBytesPerSide}
}

func (r *contentBackupMiddlewareRig) install(t *testing.T) {
	t.Helper()
	service.ConfigureContentBackupCapture(service.ContentBackupCaptureHooks{
		Runtime: func() service.ContentBackupRuntime {
			r.mu.Lock()
			defer r.mu.Unlock()
			return service.ContentBackupRuntime{
				Enabled:       r.enabled,
				SiteID:        "ai",
				StorageNodeID: "node-a",
				TargetID:      "target-1",
				ConfigVersion: 3,
				MaxBodyBytes:  r.maxBody,
			}
		},
		Enqueuer: func(capture *contentbackup.Capture) bool {
			r.mu.Lock()
			r.enqueued = append(r.enqueued, capture)
			r.mu.Unlock()
			// Stands in for the handoff worker: chunks stay readable after
			// Release, only the budget accounting is returned.
			if capture.Release != nil {
				capture.Release()
			}
			return true
		},
		ChannelEnabled: func(settings any) bool {
			typed, ok := settings.(contentBackupTestChannelSettings)
			if !ok {
				return false
			}
			return typed.enabled
		},
	})
	service.SetContentBackupCaptureBudget(64<<20, 128)
	service.ContentBackupResetCaptureCounters()
	t.Cleanup(func() {
		service.ConfigureContentBackupCapture(service.ContentBackupCaptureHooks{})
		service.ContentBackupResetCaptureCounters()
		defcfg := contentbackup.DefaultConfig()
		service.SetContentBackupCaptureBudget(int64(defcfg.CaptureMemoryMB)<<20, defcfg.MaxInflightCaptures)
	})
}

func (r *contentBackupMiddlewareRig) taken() []*contentbackup.Capture {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*contentbackup.Capture, len(r.enqueued))
	copy(out, r.enqueued)
	return out
}

type contentBackupTestChannelSettings struct{ enabled bool }

// contentBackupFakeWriter is a gin.ResponseWriter whose every client visible
// side effect lands in an ordered op log, so the wrapped and unwrapped runs can
// be compared byte for byte.
type contentBackupFakeWriter struct {
	ops           []string
	body          bytes.Buffer
	header        http.Header
	status        int
	size          int
	written       bool
	shortN        int
	writeErr      error
	readFromCalls int
	hijackErr     error
	closeNotify   chan bool
}

func newContentBackupFakeWriter() *contentBackupFakeWriter {
	return &contentBackupFakeWriter{header: http.Header{}, writeErr: errors.New("short write"), closeNotify: make(chan bool, 1)}
}

func (w *contentBackupFakeWriter) log(entry string) { w.ops = append(w.ops, entry) }

func (w *contentBackupFakeWriter) Header() http.Header { return w.header }

func (w *contentBackupFakeWriter) appendBody(p []byte) (int, error) {
	if w.shortN > 0 && len(p) > w.shortN {
		n, _ := w.body.Write(p[:w.shortN])
		w.size += n
		w.written = true
		return n, w.writeErr
	}
	n, err := w.body.Write(p)
	w.size += n
	w.written = true
	return n, err
}

func (w *contentBackupFakeWriter) Write(p []byte) (int, error) {
	w.WriteHeaderNow()
	n, err := w.appendBody(p)
	w.log("write:" + string(p[:min(n, len(p))]))
	if err != nil {
		w.log("write-error")
	}
	return n, err
}

func (w *contentBackupFakeWriter) WriteString(s string) (int, error) {
	w.WriteHeaderNow()
	n, err := w.appendBody([]byte(s))
	w.log("writestring:" + s[:min(n, len(s))])
	return n, err
}

func (w *contentBackupFakeWriter) WriteHeader(code int) {
	if code > 0 && w.status != code && !w.written {
		w.status = code
		w.log("writeheader:" + itoa(code))
	}
}

func (w *contentBackupFakeWriter) WriteHeaderNow() {
	if !w.written {
		w.written = true
		w.size = 0
		if w.status == 0 {
			w.status = http.StatusOK
		}
		w.log("writeheadernow:" + itoa(w.status))
	}
}

func (w *contentBackupFakeWriter) Status() int              { return w.status }
func (w *contentBackupFakeWriter) Size() int                { return w.size }
func (w *contentBackupFakeWriter) Written() bool            { return w.written }
func (w *contentBackupFakeWriter) Pusher() http.Pusher      { return nil }
func (w *contentBackupFakeWriter) Flush()                   { w.WriteHeaderNow(); w.log("flush") }
func (w *contentBackupFakeWriter) CloseNotify() <-chan bool { return w.closeNotify }
func (w *contentBackupFakeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, w.hijackErr
}

// contentBackupReaderFromWriter additionally implements io.ReaderFrom so the
// wrapper can be proven not to lose or double record bytes on that path.
type contentBackupReaderFromWriter struct {
	*contentBackupFakeWriter
}

func (w contentBackupReaderFromWriter) ReadFrom(r io.Reader) (int64, error) {
	w.readFromCalls++
	w.log("readfrom")
	n, err := io.Copy(&w.body, r)
	w.size += int(n)
	w.written = true
	return n, err
}

func contentBackupJoin(chunks [][]byte) []byte {
	var out []byte
	for _, chunk := range chunks {
		out = append(out, chunk...)
	}
	return out
}

func TestContentBackupWriterMatchesUnderlyingByteStatusFlushOrder(t *testing.T) {
	script := func(w gin.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("data: one\n\n"))
		w.Flush()
		w.Flush()
		_, _ = w.WriteString("data: two\n\n")
		w.WriteHeaderNow()
		_, _ = w.Write([]byte("data: three\n\n"))
		w.Flush()
	}

	bare := newContentBackupFakeWriter()
	script(bare)

	rig := newContentBackupMiddlewareRig()
	rig.install(t)
	capture := newContentBackupTestCapture(t, rig, "/v1/chat/completions")
	observed := newContentBackupFakeWriter()
	wrapped := ContentBackupWrapWriter(observed, capture)
	if got, ok := ContentBackupCaptureWriter(wrapped); !ok || got != capture {
		t.Fatal("ContentBackupWrapWriter must return an observer bound to the capture")
	}
	script(wrapped)

	if !bytes.Equal(bare.body.Bytes(), observed.body.Bytes()) {
		t.Fatalf("client bytes differ:\n bare=%q\n wrapped=%q", bare.body.Bytes(), observed.body.Bytes())
	}
	if !reflect.DeepEqual(bare.ops, observed.ops) {
		t.Fatalf("client side effect order differs:\n bare=%v\n wrapped=%v", bare.ops, observed.ops)
	}
	if bare.status != observed.Status() || bare.size != observed.Size() || bare.written != observed.Written() {
		t.Fatalf("status/size/written differ: bare=%d/%d/%v wrapped=%d/%d/%v",
			bare.status, bare.size, bare.written, observed.Status(), observed.Size(), observed.Written())
	}
	if !reflect.DeepEqual(bare.header, observed.Header()) {
		t.Fatalf("headers differ: bare=%v wrapped=%v", bare.header, observed.Header())
	}
	snapshot := capture.Snapshot()
	if snapshot.ResponseCaptured != int64(len(bare.body.Bytes())) || snapshot.ResponseObserved != int64(len(bare.body.Bytes())) {
		t.Fatalf("capture counters = %+v, want %d bytes", snapshot, len(bare.body.Bytes()))
	}
	if snapshot.Flushes != 3 {
		t.Fatalf("flush count = %d, want 3", snapshot.Flushes)
	}
}

func TestContentBackupWriterShortWriteCapturesOnlyAcceptedBytes(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)
	capture := newContentBackupTestCapture(t, rig, "/v1/chat/completions")
	fake := newContentBackupFakeWriter()
	fake.shortN = 3
	wrapped := ContentBackupWrapWriter(fake, capture)

	payload := []byte("0123456789")
	n, err := wrapped.Write(payload)
	if n != 3 || err == nil {
		t.Fatalf("wrapper must return the underlying result verbatim, got n=%d err=%v", n, err)
	}
	if got := fake.body.String(); got != "012" {
		t.Fatalf("underlying body = %q, want %q", got, "012")
	}
	snapshot := capture.Snapshot()
	if snapshot.ResponseCaptured != 3 || snapshot.ResponseObserved != 3 {
		t.Fatalf("capture counters = %+v, want 3/3", snapshot)
	}
	if !snapshot.WriteFailed {
		t.Fatal("a short write must mark the response incomplete")
	}
}

func TestContentBackupWriterRepeatedFlushAddsNoContent(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)
	capture := newContentBackupTestCapture(t, rig, "/v1/chat/completions")
	fake := newContentBackupFakeWriter()
	wrapped := ContentBackupWrapWriter(fake, capture)

	_, _ = wrapped.Write([]byte("data: 1\n\n"))
	wrapped.Flush()
	wrapped.Flush()
	wrapped.Flush()

	if got := capture.Snapshot().ResponseCaptured; got != 9 {
		t.Fatalf("captured = %d, repeated flush must not append content", got)
	}
	if flushes := countOps(fake.ops, "flush"); flushes != 3 {
		t.Fatalf("underlying flush calls = %d, want 3", flushes)
	}
	if got := capture.Snapshot().Flushes; got != 3 {
		t.Fatalf("observed flush count = %d, want 3", got)
	}
}

func TestContentBackupWriterReadFromRecordsExactlyOnce(t *testing.T) {
	source := []byte(strings.Repeat("payload-", 5000))

	t.Run("underlying_without_readfrom", func(t *testing.T) {
		rig := newContentBackupMiddlewareRig()
		rig.install(t)
		capture := newContentBackupTestCapture(t, rig, "/v1/chat/completions")
		fake := newContentBackupFakeWriter()
		wrapped := ContentBackupWrapWriter(fake, capture)
		n, err := io.Copy(wrapped, bytes.NewReader(source))
		if err != nil || n != int64(len(source)) {
			t.Fatalf("io.Copy = (%d, %v)", n, err)
		}
		if !bytes.Equal(fake.body.Bytes(), source) {
			t.Fatal("client bytes differ from the source")
		}
		if got := capture.Snapshot(); got.ResponseCaptured != int64(len(source)) || got.ResponseObserved != int64(len(source)) {
			t.Fatalf("capture counters = %+v, want %d", got, len(source))
		}
	})

	t.Run("underlying_with_readfrom", func(t *testing.T) {
		rig := newContentBackupMiddlewareRig()
		rig.install(t)
		capture := newContentBackupTestCapture(t, rig, "/v1/chat/completions")
		fake := &contentBackupFakeWriter{header: http.Header{}, writeErr: errors.New("short write"), closeNotify: make(chan bool, 1)}
		wrapped := ContentBackupWrapWriter(contentBackupReaderFromWriter{fake}, capture)
		n, err := io.Copy(wrapped, bytes.NewReader(source))
		if err != nil || n != int64(len(source)) {
			t.Fatalf("io.Copy = (%d, %v)", n, err)
		}
		if !bytes.Equal(fake.body.Bytes(), source) {
			t.Fatal("client bytes differ from the source")
		}
		if fake.readFromCalls != 0 {
			t.Fatalf("the observer must route ReadFrom through Write, underlying ReadFrom calls = %d", fake.readFromCalls)
		}
		if got := capture.Snapshot(); got.ResponseCaptured != int64(len(source)) || got.ResponseObserved != int64(len(source)) {
			t.Fatalf("ReadFrom must record exactly once: %+v, want %d", got, len(source))
		}
	})

	t.Run("source_with_writeto", func(t *testing.T) {
		rig := newContentBackupMiddlewareRig()
		rig.install(t)
		capture := newContentBackupTestCapture(t, rig, "/v1/chat/completions")
		fake := newContentBackupFakeWriter()
		wrapped := ContentBackupWrapWriter(fake, capture)
		n, err := io.Copy(wrapped, strings.NewReader(string(source)))
		if err != nil || n != int64(len(source)) {
			t.Fatalf("io.Copy = (%d, %v)", n, err)
		}
		if got := capture.Snapshot(); got.ResponseCaptured != int64(len(source)) {
			t.Fatalf("a WriterTo source must still be recorded once: %+v", got)
		}
	})
}

func TestContentBackupWriterWriteStringRecordsExactlyOnce(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)
	capture := newContentBackupTestCapture(t, rig, "/v1/chat/completions")
	fake := newContentBackupFakeWriter()
	wrapped := ContentBackupWrapWriter(fake, capture)

	n, err := wrapped.WriteString("event: message\n")
	if err != nil || n != 15 {
		t.Fatalf("WriteString = (%d, %v)", n, err)
	}
	if got := capture.Snapshot(); got.ResponseCaptured != 15 || got.ResponseObserved != 15 {
		t.Fatalf("capture counters = %+v, want 15/15", got)
	}
	if writes := countOps(fake.ops, "write:"); writes != 0 {
		t.Fatalf("WriteString must not also route through Write, write ops = %d", writes)
	}
	if writestrings := countOps(fake.ops, "writestring:"); writestrings != 1 {
		t.Fatalf("writestring ops = %d, want 1", writestrings)
	}
	if _, err := io.WriteString(wrapped, "tail"); err != nil {
		t.Fatalf("io.WriteString = %v", err)
	}
	if got := capture.Snapshot(); got.ResponseCaptured != 19 {
		t.Fatalf("capture counters = %+v, want 19", got)
	}
}

func TestContentBackupWriterDelegatesFullGinInterface(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)
	capture := newContentBackupTestCapture(t, rig, "/v1/chat/completions")
	fake := newContentBackupFakeWriter()
	fake.hijackErr = errors.New("no hijack")
	wrapped := ContentBackupWrapWriter(fake, capture)

	if wrapped.Header() == nil {
		t.Fatal("Header must delegate")
	}
	wrapped.WriteHeader(http.StatusTeapot)
	if wrapped.Status() != http.StatusTeapot {
		t.Fatalf("Status = %d, want 418", wrapped.Status())
	}
	wrapped.WriteHeaderNow()
	if !wrapped.Written() || wrapped.Size() != 0 {
		t.Fatalf("Written/Size must delegate, got %v/%d", wrapped.Written(), wrapped.Size())
	}
	if wrapped.Pusher() != nil {
		t.Fatal("Pusher must delegate")
	}
	if wrapped.CloseNotify() == nil {
		t.Fatal("CloseNotify must delegate")
	}
	if _, _, err := wrapped.Hijack(); err == nil {
		t.Fatal("Hijack must delegate the underlying error")
	}
	if capture.Snapshot().Hijacked {
		t.Fatal("a failed hijack must not disable the capture")
	}
	unwrapped, ok := wrapped.(interface{ Unwrap() http.ResponseWriter })
	if !ok || unwrapped.Unwrap() == nil {
		t.Fatal("Unwrap must expose the underlying writer for http.ResponseController")
	}
	if _, ok := wrapped.(io.ReaderFrom); !ok {
		t.Fatal("the observer must implement io.ReaderFrom")
	}
	if _, ok := wrapped.(http.Flusher); !ok {
		t.Fatal("the observer must implement http.Flusher")
	}
	if _, ok := wrapped.(http.CloseNotifier); !ok {
		t.Fatal("the observer must implement http.CloseNotifier")
	}
	if _, ok := wrapped.(http.Hijacker); !ok {
		t.Fatal("the observer must implement http.Hijacker")
	}
	if _, ok := wrapped.(io.StringWriter); !ok {
		t.Fatal("the observer must implement io.StringWriter")
	}
}

func TestContentBackupWriterHijackStopsCapture(t *testing.T) {
	rig := newContentBackupMiddlewareRig()
	rig.install(t)
	capture := newContentBackupTestCapture(t, rig, "/v1/chat/completions")
	fake := newContentBackupFakeWriter()
	fake.hijackErr = nil
	wrapped := ContentBackupWrapWriter(fake, capture)
	if _, _, err := wrapped.Hijack(); err != nil {
		t.Fatalf("hijack = %v", err)
	}
	if !capture.Snapshot().Hijacked {
		t.Fatal("a successful hijack must stop body capture")
	}
	_, _ = wrapped.Write([]byte("after-hijack"))
	if got := capture.Snapshot(); got.ResponseCaptured != 0 {
		t.Fatalf("post hijack bytes must not be captured, got %d", got.ResponseCaptured)
	}
}

func countOps(ops []string, prefix string) int {
	count := 0
	for _, op := range ops {
		if strings.HasPrefix(op, prefix) {
			count++
		}
	}
	return count
}

func newContentBackupTestCapture(t *testing.T, rig *contentBackupMiddlewareRig, path string) *service.ContentBackupCapture {
	t.Helper()
	c := contentBackupTestGinContext(path, []byte(`{"model":"gpt-test","user":"u-1"}`), "application/json")
	capture := service.BeginContentBackupCapture(c)
	if capture == nil {
		t.Fatal("the collector must start a capture for a whitelisted enabled request")
	}
	service.ContentBackupRecordChannelRound(c, service.ContentBackupChannelRound{
		ChannelID: 12, ChannelName: "example", ChannelType: 1, BackupEnabled: true,
	})
	t.Cleanup(func() { service.FinishContentBackupCapture(c) })
	return capture
}

func contentBackupTestGinContext(path string, body []byte, contentType string) *gin.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	c.Set(common.RequestIdKey, "req-mw-1")
	c.Set("id", 10086)
	c.Set("token_id", 42)
	c.Set(string(constant.ContextKeyOriginalModel), "gpt-test")
	c.Set(string(constant.ContextKeyChannelId), 12)
	c.Set(string(constant.ContextKeyChannelName), "example")
	c.Set(string(constant.ContextKeyChannelType), 1)
	c.Set(string(constant.ContextKeyChannelSetting), contentBackupTestChannelSettings{enabled: true})
	c.Set(common.KeyBodyStorage, newContentBackupTestStorage(body))
	c.Writer.Header().Set("Content-Type", contentType)
	return c
}

type contentBackupTestStorage struct {
	data   []byte
	reader *bytes.Reader
	mu     sync.Mutex
	closed bool
	closes int
}

func newContentBackupTestStorage(data []byte) *contentBackupTestStorage {
	return &contentBackupTestStorage{data: data, reader: bytes.NewReader(data)}
}

func (s *contentBackupTestStorage) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, common.ErrStorageClosed
	}
	return s.reader.Read(p)
}

func (s *contentBackupTestStorage) Seek(offset int64, whence int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reader.Seek(offset, whence)
}

func (s *contentBackupTestStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closes++
	s.closed = true
	return nil
}

func (s *contentBackupTestStorage) Bytes() ([]byte, error) { return s.data, nil }
func (s *contentBackupTestStorage) Size() int64            { return int64(len(s.data)) }
func (s *contentBackupTestStorage) IsDisk() bool           { return false }

func (s *contentBackupTestStorage) NewReader() (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, common.ErrStorageClosed
	}
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func (s *contentBackupTestStorage) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closes
}
