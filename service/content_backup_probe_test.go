package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
)

func TestContentBackupSkipPlatformAndNamedProbes(t *testing.T) {
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"please review this"}]}`)
	for _, tc := range []struct {
		name    string
		userID  int
		tokenID int
		token   string
		flag    bool
	}{
		{name: "platform identity", userID: model.ChannelTestUserId, tokenID: 0},
		{name: "model test token", userID: 9, tokenID: 8, token: model.ChannelTestTokenName},
		{name: "offline probe token", userID: 9, tokenID: 8, token: model.ChannelTestOfflineTokenName},
		{name: "named 测活 token", userID: 9, tokenID: 8, token: "客户测活"},
		{name: "named ping token", userID: 9, tokenID: 8, token: "ping"},
		{name: "channel test flag", userID: 9, tokenID: 8, flag: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newContentBackupRig()
			rig.install(t, 64<<20, 128)
			c := contentBackupTestContext(contentBackupRequest{
				path:        "/v1/chat/completions",
				body:        body,
				contentType: "application/json",
				initial:     contentBackupRoundFor(12, "example", true),
				storage:     newContentBackupFakeStorage(body),
			})
			c.Set("id", tc.userID)
			c.Set("token_id", tc.tokenID)
			c.Set("token_name", tc.token)
			if tc.flag {
				c.Set(string(constant.ContextKeyChannelTest), true)
			}
			if BeginContentBackupCapture(c) != nil {
				t.Fatal("probe requests must not start a capture")
			}
			if ContentBackupCaptureCounters().ProbeSkipped != 1 {
				t.Fatalf("probe skip = %d, want 1", ContentBackupCaptureCounters().ProbeSkipped)
			}
		})
	}
}

func TestContentBackupSkipDownstreamHiPing(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
	_, _ = driveContentBackupRequest(t, contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        body,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		rounds:      []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true), writes: []string{`{"ok":1}`}}},
		storage:     newContentBackupFakeStorage(body),
	})
	if got := len(rig.taken()); got != 0 {
		t.Fatalf("downstream hi ping must not be archived, got %d", got)
	}
	if ContentBackupCaptureCounters().ProbeSkipped != 1 {
		t.Fatalf("probe skip = %d, want 1", ContentBackupCaptureCounters().ProbeSkipped)
	}
}

func TestContentBackupKeepsImageAndRealChat(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)

	imageBody := []byte(`{"model":"gpt-image-2","prompt":"hi"}`)
	_, _ = driveContentBackupRequest(t, contentBackupRequest{
		path:        "/v1/images/generations",
		body:        imageBody,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		rounds:      []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true), writes: []string{`{"data":[]}`}}},
		storage:     newContentBackupFakeStorage(imageBody),
	})
	if got := len(rig.taken()); got != 1 {
		t.Fatalf("image generations must stay eligible, got %d", got)
	}
	rig.taken()[0].Release()

	rig2 := newContentBackupRig()
	rig2.install(t, 64<<20, 128)
	chatBody := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"please summarize this contract"}]}`)
	_, _ = driveContentBackupRequest(t, contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        chatBody,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		rounds:      []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true), writes: []string{`{"ok":1}`}}},
		storage:     newContentBackupFakeStorage(chatBody),
	})
	if got := len(rig2.taken()); got != 1 {
		t.Fatalf("real chat must stay eligible, got %d", got)
	}
	rig2.taken()[0].Release()
}

func TestContentBackupRedactsBrandIPAndChannelName(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"call BAPI at 203.0.113.10 via #relay-openai-2"}]}`)
	_, _ = driveContentBackupRequest(t, contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        body,
		contentType: "application/json",
		initial:     contentBackupRoundFor(297, "#relay-openai-2", true),
		rounds:      []contentBackupRound{{snapshot: contentBackupRoundFor(297, "#relay-openai-2", true), writes: []string{`{"reply":"use ExampleBrand"}`}}},
		storage:     newContentBackupFakeStorage(body),
	})
	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("expected one archive, got %d", len(taken))
	}
	defer taken[0].Release()
	if taken[0].Meta.ChannelName != "#XXX2" {
		t.Fatalf("channel_name = %q, want #XXX2", taken[0].Meta.ChannelName)
	}
	req := string(joinContentBackupChunks(taken[0].RequestChunks))
	resp := string(joinContentBackupChunks(taken[0].ResponseChunks))
	for _, part := range []string{"BXXXI", "20x.x.xxx.10", "#XXX2"} {
		if !strings.Contains(req, part) {
			t.Fatalf("request missing %q: %s", part, req)
		}
	}
	for _, part := range []string{"BAPI", "203.0.113.10", "#relay-openai-2"} {
		if strings.Contains(req, part) {
			t.Fatalf("request still has %q: %s", part, req)
		}
	}
	if !strings.Contains(resp, "EXXXd") || strings.Contains(resp, "ExampleBrand") {
		t.Fatalf("response was not redacted: %s", resp)
	}
	if err := contentbackup.ValidateMetadata(taken[0].Meta); err != nil {
		t.Fatalf("redacted metadata must still validate: %v", err)
	}
}
