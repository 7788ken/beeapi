package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/contentbackup"

	"github.com/gin-gonic/gin"
)

type contentBackupTestSettings struct {
	enabled bool
}

type contentBackupFakeStorage struct {
	data   []byte
	reader *bytes.Reader

	mu          sync.Mutex
	closed      bool
	closeCalls  int
	readerCalls int
}

func newContentBackupFakeStorage(data []byte) *contentBackupFakeStorage {
	return &contentBackupFakeStorage{data: data, reader: bytes.NewReader(data)}
}

func (s *contentBackupFakeStorage) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, common.ErrStorageClosed
	}
	return s.reader.Read(p)
}

func (s *contentBackupFakeStorage) Seek(offset int64, whence int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, common.ErrStorageClosed
	}
	return s.reader.Seek(offset, whence)
}

func (s *contentBackupFakeStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCalls++
	s.closed = true
	return nil
}

func (s *contentBackupFakeStorage) Bytes() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, common.ErrStorageClosed
	}
	return s.data, nil
}

func (s *contentBackupFakeStorage) Size() int64 { return int64(len(s.data)) }

func (s *contentBackupFakeStorage) IsDisk() bool { return false }

func (s *contentBackupFakeStorage) NewReader() (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readerCalls++
	if s.closed {
		return nil, common.ErrStorageClosed
	}
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func (s *contentBackupFakeStorage) stats() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCalls, s.readerCalls
}

type contentBackupRig struct {
	mu              sync.Mutex
	enqueued        []*contentbackup.Capture
	releaseCounters map[string]*atomic.Int64
	acceptEnqueue   bool
	channelEnabled  func(contentBackupTestSettings) bool
	runtime         ContentBackupRuntime
}

func newContentBackupRig() *contentBackupRig {
	return &contentBackupRig{
		releaseCounters: make(map[string]*atomic.Int64),
		acceptEnqueue:   true,
		runtime: ContentBackupRuntime{
			Enabled:       true,
			SiteID:        "ai",
			StorageNodeID: "node-a",
			TargetID:      "target-1",
			ConfigVersion: 7,
			MaxBodyBytes:  contentbackup.MaxBodyBytesPerSide,
		},
	}
}

func (r *contentBackupRig) hooks() ContentBackupCaptureHooks {
	return ContentBackupCaptureHooks{
		Runtime: func() ContentBackupRuntime {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.runtime
		},
		Enqueuer: func(capture *contentbackup.Capture) bool {
			counter := &atomic.Int64{}
			original := capture.Release
			r.mu.Lock()
			r.enqueued = append(r.enqueued, capture)
			r.releaseCounters[capture.Meta.JobID] = counter
			accept := r.acceptEnqueue
			r.mu.Unlock()
			capture.Release = func() {
				counter.Add(1)
				if original != nil {
					original()
				}
			}
			return accept
		},
		ChannelEnabled: func(settings any) bool {
			typed, ok := settings.(contentBackupTestSettings)
			if !ok {
				return false
			}
			if r.channelEnabled != nil {
				return r.channelEnabled(typed)
			}
			return typed.enabled
		},
	}
}

func (r *contentBackupRig) install(t *testing.T, maxBytes int64, maxInflight int) {
	t.Helper()
	ConfigureContentBackupCapture(r.hooks())
	// Replace the process wide budget so a leak in one test cannot be mistaken
	// for a leak in the next one.
	contentBackupBudget = NewContentBackupBudget(maxBytes, maxInflight)
	ContentBackupResetCaptureCounters()
	t.Cleanup(func() {
		ConfigureContentBackupCapture(ContentBackupCaptureHooks{})
		ContentBackupResetCaptureCounters()
		contentBackupBudget = newDefaultContentBackupBudget()
	})
}

func TestContentBackupSetCaptureBudgetAppliesToGlobal(t *testing.T) {
	defer func() { contentBackupBudget = newDefaultContentBackupBudget() }()
	SetContentBackupCaptureBudget(1024, 2)
	stats := ContentBackupCaptureBudget().Stats()
	if stats.MaxBytes != 1024 || stats.MaxInflight != 2 {
		t.Fatalf("SetContentBackupCaptureBudget did not reach the process budget: %+v", stats)
	}
	if !ContentBackupCaptureBudget().AcquireCapture() || !ContentBackupCaptureBudget().AcquireCapture() {
		t.Fatal("two captures must fit the new inflight limit")
	}
	if ContentBackupCaptureBudget().AcquireCapture() {
		t.Fatal("the third capture must be rejected")
	}
}

func (r *contentBackupRig) taken() []*contentbackup.Capture {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*contentbackup.Capture, len(r.enqueued))
	copy(out, r.enqueued)
	return out
}

func (r *contentBackupRig) releaseCalls(jobID string) int64 {
	r.mu.Lock()
	counter := r.releaseCounters[jobID]
	r.mu.Unlock()
	if counter == nil {
		return -1
	}
	return counter.Load()
}

type contentBackupRound struct {
	snapshot ContentBackupChannelRound
	writes   []string
	writeErr error
	shortN   int
}

type contentBackupRequest struct {
	path        string
	body        []byte
	contentType string
	initial     ContentBackupChannelRound
	rounds      []contentBackupRound
	status      int
	storage     *contentBackupFakeStorage
	cancelBase  bool
}

func contentBackupTestContext(req contentBackupRequest) *gin.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, req.path, bytes.NewReader(req.body))
	c.Request.Header.Set("Content-Type", req.contentType)
	c.Set(common.RequestIdKey, "req-test-1")
	c.Set("id", 10086)
	c.Set("token_id", 42)
	c.Set(string(constant.ContextKeyOriginalModel), "gpt-test")
	c.Set(string(constant.ContextKeyRequestStartTime), time.Date(2026, 9, 15, 4, 34, 56, 0, time.UTC))
	c.Set(string(constant.ContextKeyChannelId), req.initial.ChannelID)
	c.Set(string(constant.ContextKeyChannelName), req.initial.ChannelName)
	c.Set(string(constant.ContextKeyChannelType), req.initial.ChannelType)
	c.Set(string(constant.ContextKeyChannelSetting), contentBackupTestSettings{enabled: req.initial.BackupEnabled})
	if req.storage != nil {
		c.Set(common.KeyBodyStorage, req.storage)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	if req.status != 0 {
		c.Writer.WriteHeader(req.status)
	}
	return c
}

func driveContentBackupRequest(t *testing.T, req contentBackupRequest) (*gin.Context, *ContentBackupCapture) {
	t.Helper()
	c := contentBackupTestContext(req)
	capture := BeginContentBackupCapture(c)
	if capture == nil {
		return c, nil
	}
	if req.cancelBase {
		c.Request = c.Request.WithContext(contentBackupCancelledContext())
	}
	for _, round := range req.rounds {
		ContentBackupRecordChannelRound(c, round.snapshot)
		for _, payload := range round.writes {
			n := len(payload)
			if round.shortN > 0 && round.shortN < n {
				n = round.shortN
			}
			capture.ObserveResponseWrite([]byte(payload), n, round.writeErr)
		}
	}
	FinishContentBackupCapture(c)
	return c, capture
}

func contentBackupCancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func contentBackupRoundFor(id int, name string, enabled bool) ContentBackupChannelRound {
	return ContentBackupChannelRound{
		ChannelID:     id,
		ChannelName:   name,
		ChannelType:   1,
		BackupEnabled: enabled,
	}
}

func TestContentBackupEndpointWhitelistIsExact(t *testing.T) {
	allowed := []string{
		"/v1/chat/completions",
		"/v1/completions",
		"/v1/messages",
		"/v1/responses",
		"/v1/images/generations",
		"/v1/images/edits",
		"/v1/audio/transcriptions",
		"/v1/audio/translations",
		"/v1/audio/speech",
		"/v1/embeddings",
	}
	for _, path := range allowed {
		if !IsContentBackupEndpoint(path) {
			t.Fatalf("%s must install a capture", path)
		}
		if _, ok := ContentBackupSessionSource(path); !ok {
			t.Fatalf("%s must have a fixed session source", path)
		}
	}
	denied := []string{
		"/v1/realtime",
		"/v1/responses/compact",
		"/v1/edits",
		"/v1/rerank",
		"/v1/moderations",
		"/v1/images/variations",
		"/v1/files",
		"/v1/batches",
		"/pg/chat/completions",
		"/v1/chat/completions/",
		"/v1/chat/completions/extra",
		"/v1/images/generations/2",
		"/v1/models",
		"/mj/submit/imagine",
		"/suno/submit/music",
		"/v1beta/models/gemini-2.0-flash:generateContent",
		"/v1/videos",
		"",
	}
	for _, path := range denied {
		if IsContentBackupEndpoint(path) {
			t.Fatalf("%s must not install a capture", path)
		}
		if _, ok := ContentBackupSessionSource(path); ok {
			t.Fatalf("%s must not resolve a session source", path)
		}
	}
}

func TestContentBackupSessionSourceTable(t *testing.T) {
	cases := map[string]string{
		"/v1/messages":             contentbackup.SessionSourceMetadataUserID,
		"/v1/responses":            contentbackup.SessionSourcePromptCacheKey,
		"/v1/chat/completions":     contentbackup.SessionSourceUser,
		"/v1/completions":          contentbackup.SessionSourceUser,
		"/v1/images/generations":   contentbackup.SessionSourceUser,
		"/v1/images/edits":         contentbackup.SessionSourceUser,
		"/v1/audio/transcriptions": contentbackup.SessionSourceUser,
		"/v1/audio/translations":   contentbackup.SessionSourceUser,
		"/v1/audio/speech":         contentbackup.SessionSourceUser,
		"/v1/embeddings":           contentbackup.SessionSourceUser,
	}
	for path, want := range cases {
		got, ok := ContentBackupSessionSource(path)
		if !ok || got != want {
			t.Fatalf("%s: source=%q ok=%v, want %q", path, got, ok, want)
		}
	}
}

func TestContentBackupDisabledAllocatesNoBodyBlocks(t *testing.T) {
	rig := newContentBackupRig()
	rig.runtime.Enabled = false
	rig.install(t, 64<<20, 128)

	c := contentBackupTestContext(contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        []byte(`{"model":"gpt-test","user":"u-1"}`),
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		rounds:      []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true), writes: []string{`{"ok":true}`}}},
	})
	if capture := BeginContentBackupCapture(c); capture != nil {
		t.Fatal("a globally disabled feature must not create a capture")
	}
	stats := ContentBackupCaptureBudget().Stats()
	if stats.UsedBytes != 0 || stats.Inflight != 0 {
		t.Fatalf("disabled feature allocated budget: %+v", stats)
	}
	if len(rig.taken()) != 0 {
		t.Fatal("disabled feature must not hand off")
	}
}

func TestContentBackupChannelNeverEnabledAllocatesNoBodyBlocks(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)

	_, capture := driveContentBackupRequest(t, contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        []byte(`{"model":"gpt-test","user":"u-1"}`),
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", false),
		rounds: []contentBackupRound{
			{snapshot: contentBackupRoundFor(12, "example", false), writes: []string{`{"error":"upstream"}`}},
			{snapshot: contentBackupRoundFor(13, "backup", false), writes: []string{`{"error":"upstream2"}`}},
		},
	})
	if capture == nil {
		t.Fatal("collector state must exist so a later enabled round can arm it")
	}
	if capture.Snapshot().Armed {
		t.Fatal("capture must not arm while every channel switch is off")
	}
	stats := ContentBackupCaptureBudget().Stats()
	if stats.UsedBytes != 0 || stats.Inflight != 0 {
		t.Fatalf("no channel enabled but budget is held: %+v", stats)
	}
	if len(rig.taken()) != 0 {
		t.Fatal("a request whose channels never enabled backup must not be archived")
	}
}

func TestContentBackupChannelGateCombinations(t *testing.T) {
	cases := []struct {
		name          string
		initial       ContentBackupChannelRound
		rounds        []contentBackupRound
		expectHandoff bool
		expectChannel int
		expectBody    string
	}{
		{
			name:          "initial_on_final_on",
			initial:       contentBackupRoundFor(12, "example", true),
			rounds:        []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true), writes: []string{`{"ok":1}`}}},
			expectHandoff: true,
			expectChannel: 12,
			expectBody:    `{"ok":1}`,
		},
		{
			name:          "initial_on_final_off_releases",
			initial:       contentBackupRoundFor(12, "example", true),
			rounds:        []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true)}, {snapshot: contentBackupRoundFor(13, "backup", false), writes: []string{`{"ok":2}`}}},
			expectHandoff: false,
			expectChannel: 0,
		},
		{
			name:          "initial_off_retry_on_archives_request_prefix_and_final_output",
			initial:       contentBackupRoundFor(12, "example", false),
			rounds:        []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", false)}, {snapshot: contentBackupRoundFor(13, "backup", true), writes: []string{`{"ok":3}`}}},
			expectHandoff: true,
			expectChannel: 13,
			expectBody:    `{"ok":3}`,
		},
		{
			name:          "last_selection_failed_keeps_previous_attempt",
			initial:       contentBackupRoundFor(12, "example", false),
			rounds:        []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", false)}, {snapshot: contentBackupRoundFor(14, "third", true), writes: []string{`{"ok":4}`}}},
			expectHandoff: true,
			expectChannel: 14,
			expectBody:    `{"ok":4}`,
		},
		{
			name:          "early_validation_failure_uses_distribute_channel",
			initial:       contentBackupRoundFor(12, "example", true),
			rounds:        nil,
			expectHandoff: true,
			expectChannel: 12,
			expectBody:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newContentBackupRig()
			rig.install(t, 64<<20, 128)
			body := []byte(`{"model":"gpt-test","user":"u-1"}`)
			_, capture := driveContentBackupRequest(t, contentBackupRequest{
				path:        "/v1/chat/completions",
				body:        body,
				contentType: "application/json",
				initial:     tc.initial,
				rounds:      tc.rounds,
				storage:     newContentBackupFakeStorage(body),
			})
			taken := rig.taken()
			if tc.expectHandoff {
				if len(taken) != 1 {
					t.Fatalf("expected one handoff, got %d", len(taken))
				}
				got := taken[0]
				if got.Meta.ChannelID != tc.expectChannel {
					t.Fatalf("attributed channel = %d, want %d", got.Meta.ChannelID, tc.expectChannel)
				}
				if string(joinContentBackupChunks(got.ResponseChunks)) != tc.expectBody {
					t.Fatalf("response body = %q, want %q", joinContentBackupChunks(got.ResponseChunks), tc.expectBody)
				}
				if tc.expectBody != "" && string(joinContentBackupChunks(got.RequestChunks)) != string(body) {
					t.Fatalf("request body = %q, want %q", joinContentBackupChunks(got.RequestChunks), body)
				}
				if got.Meta.RequestStartedAt.IsZero() {
					t.Fatal("request_started_at must be frozen from the request")
				}
				if err := contentbackup.ValidateMetadata(got.Meta); err != nil {
					t.Fatalf("frozen metadata must validate: %v", err)
				}
				if capture != nil && capture.Snapshot().AllocatedBytes == 0 {
					t.Fatal("an archived capture must have allocated body blocks")
				}
				got.Release()
			} else {
				if len(taken) != 0 {
					t.Fatalf("expected no handoff, got %d", len(taken))
				}
			}
			stats := ContentBackupCaptureBudget().Stats()
			if stats.UsedBytes != 0 || stats.Inflight != 0 {
				t.Fatalf("budget leaked after finish: %+v", stats)
			}
		})
	}
}

func TestContentBackupHandoffOwnershipReleaseExactlyOnce(t *testing.T) {
	t.Run("enqueue_rejected", func(t *testing.T) {
		rig := newContentBackupRig()
		rig.acceptEnqueue = false
		rig.install(t, 64<<20, 128)
		body := []byte(`{"model":"gpt-test","user":"u-1"}`)
		driveContentBackupRequest(t, contentBackupRequest{
			path:        "/v1/chat/completions",
			body:        body,
			contentType: "application/json",
			initial:     contentBackupRoundFor(12, "example", true),
			rounds:      []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true), writes: []string{`{"ok":1}`}}},
			storage:     newContentBackupFakeStorage(body),
		})
		taken := rig.taken()
		if len(taken) != 1 {
			t.Fatalf("enqueuer must be offered the capture once, got %d", len(taken))
		}
		if got := rig.releaseCalls(taken[0].Meta.JobID); got != 1 {
			t.Fatalf("Release calls = %d, want exactly 1 when TryEnqueue returns false", got)
		}
		stats := ContentBackupCaptureBudget().Stats()
		if stats.UsedBytes != 0 || stats.Inflight != 0 {
			t.Fatalf("rejected capture must free its budget: %+v", stats)
		}
		if counters := ContentBackupCaptureCounters(); counters.EnqueueRejected != 1 {
			t.Fatalf("enqueue_rejected counter = %d, want 1", counters.EnqueueRejected)
		}
	})

	t.Run("enqueue_accepted_worker_releases_once", func(t *testing.T) {
		rig := newContentBackupRig()
		rig.install(t, 64<<20, 128)
		body := []byte(`{"model":"gpt-test"}`)
		driveContentBackupRequest(t, contentBackupRequest{
			path:        "/v1/chat/completions",
			body:        body,
			contentType: "application/json",
			initial:     contentBackupRoundFor(12, "example", true),
			rounds:      []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true), writes: []string{`{"ok":1}`}}},
			storage:     newContentBackupFakeStorage(body),
		})
		taken := rig.taken()
		if len(taken) != 1 {
			t.Fatalf("expected one handoff, got %d", len(taken))
		}
		if got := rig.releaseCalls(taken[0].Meta.JobID); got != 0 {
			t.Fatalf("the collector must not release an accepted capture, Release calls = %d", got)
		}
		stats := ContentBackupCaptureBudget().Stats()
		if stats.UsedBytes == 0 || stats.Inflight != 1 {
			t.Fatalf("an accepted capture must stay charged until the worker releases it: %+v", stats)
		}
		taken[0].Release()
		if got := rig.releaseCalls(taken[0].Meta.JobID); got != 1 {
			t.Fatalf("worker Release calls = %d, want exactly 1", got)
		}
		stats = ContentBackupCaptureBudget().Stats()
		if stats.UsedBytes != 0 || stats.Inflight != 0 {
			t.Fatalf("budget must return to zero after the worker releases: %+v", stats)
		}
		taken[0].Release()
		if stats = ContentBackupCaptureBudget().Stats(); stats.UsedBytes != 0 || stats.Inflight != 0 {
			t.Fatalf("Release must stay idempotent for the budget: %+v", stats)
		}
	})

	t.Run("no_enqueuer_configured_releases", func(t *testing.T) {
		rig := newContentBackupRig()
		rig.install(t, 64<<20, 128)
		ConfigureContentBackupCapture(ContentBackupCaptureHooks{
			Runtime:        rig.hooks().Runtime,
			ChannelEnabled: rig.hooks().ChannelEnabled,
		})
		releases := &atomic.Int64{}
		capture := &contentbackup.Capture{Release: func() { releases.Add(1) }}
		if ContentBackupHandoff(capture) {
			t.Fatal("handoff must report failure without an enqueuer")
		}
		if releases.Load() != 1 {
			t.Fatalf("Release calls = %d, want 1", releases.Load())
		}
	})
}

func TestContentBackupBudgetRejectsWholeArchiveWhenFull(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<10, 128)
	body := []byte(`{"model":"gpt-test","user":"u-1"}`)
	_, capture := driveContentBackupRequest(t, contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        body,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		rounds:      []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true), writes: []string{strings.Repeat("x", 256<<10)}}},
		storage:     newContentBackupFakeStorage(body),
	})
	if capture == nil || !capture.Snapshot().Denied {
		t.Fatalf("an exhausted byte budget must deny the capture: %+v", capture.Snapshot())
	}
	if len(rig.taken()) != 0 {
		t.Fatal("a denied capture must not be handed off")
	}
	stats := ContentBackupCaptureBudget().Stats()
	if stats.UsedBytes != 0 || stats.Inflight != 0 {
		t.Fatalf("denied capture must free everything: %+v", stats)
	}
	if counters := ContentBackupCaptureCounters(); counters.BudgetRejected == 0 {
		t.Fatal("budget rejections must be counted")
	}
}

func TestContentBackupInflightLimitRejectsNewCaptures(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 1)

	first := contentBackupTestContext(contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        []byte(`{"model":"a"}`),
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
	})
	firstCapture := BeginContentBackupCapture(first)
	if firstCapture == nil {
		t.Fatal("first capture must be admitted")
	}
	firstCapture.ObserveResponseWrite([]byte(`{"ok":1}`), 8, nil)

	second := contentBackupTestContext(contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        []byte(`{"model":"b"}`),
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
	})
	if BeginContentBackupCapture(second) != nil {
		t.Fatal("the inflight limit must reject the second capture without blocking the relay")
	}
	if counters := ContentBackupCaptureCounters(); counters.BudgetRejected == 0 {
		t.Fatal("inflight rejections must be counted")
	}

	FinishContentBackupCapture(first)
	if len(rig.taken()) != 1 {
		t.Fatalf("the first capture must still be handed off, got %d", len(rig.taken()))
	}
	rig.taken()[0].Release()
	stats := ContentBackupCaptureBudget().Stats()
	if stats.UsedBytes != 0 || stats.Inflight != 0 {
		t.Fatalf("budget leaked: %+v", stats)
	}
}

func TestContentBackupTruncationKeepsObservedGrowing(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)
	rig.runtime.MaxBodyBytes = 128 << 10

	requestBody := bytes.Repeat([]byte("r"), 300<<10)
	responsePayload := bytes.Repeat([]byte("s"), 300<<10)
	_, _ = driveContentBackupRequest(t, contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        requestBody,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		rounds:      []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true), writes: []string{string(responsePayload[:100<<10]), string(responsePayload[100<<10:])}}},
		storage:     newContentBackupFakeStorage(requestBody),
	})
	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("expected one handoff, got %d", len(taken))
	}
	got := taken[0]
	defer got.Release()

	if got.Meta.Request.CapturedBytes != 128<<10 {
		t.Fatalf("request captured = %d, want the 128 KiB cap", got.Meta.Request.CapturedBytes)
	}
	if got.Meta.Request.ObservedBytes != int64(len(requestBody)) {
		t.Fatalf("request observed = %d, want %d", got.Meta.Request.ObservedBytes, len(requestBody))
	}
	if !got.Meta.Request.Truncated || !got.Meta.Request.Complete {
		t.Fatalf("request meta = %+v, want truncated and complete", got.Meta.Request)
	}
	if got.Meta.Response.CapturedBytes != 128<<10 {
		t.Fatalf("response captured = %d, want the 128 KiB cap", got.Meta.Response.CapturedBytes)
	}
	if got.Meta.Response.ObservedBytes != int64(len(responsePayload)) {
		t.Fatalf("response observed = %d, want %d", got.Meta.Response.ObservedBytes, len(responsePayload))
	}
	if !got.Meta.Response.Truncated || !got.Meta.Response.Complete {
		t.Fatalf("response meta = %+v, want truncated and complete", got.Meta.Response)
	}
	if err := contentbackup.ValidateMetadata(got.Meta); err != nil {
		t.Fatalf("truncated metadata must still validate: %v", err)
	}
	if int64(len(joinContentBackupChunks(got.RequestChunks))) != got.Meta.Request.CapturedBytes {
		t.Fatal("request chunks must sum to captured_bytes")
	}
	if int64(len(joinContentBackupChunks(got.ResponseChunks))) != got.Meta.Response.CapturedBytes {
		t.Fatal("response chunks must sum to captured_bytes")
	}
	if !bytes.Equal(joinContentBackupChunks(got.RequestChunks), requestBody[:128<<10]) {
		t.Fatal("request chunks must hold the body prefix")
	}
	if !bytes.Equal(joinContentBackupChunks(got.ResponseChunks), responsePayload[:128<<10]) {
		t.Fatal("response chunks must hold the written prefix")
	}
}

func TestContentBackupRequestBodyUsesIndependentReaderAndKeepsStorage(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)
	body := []byte(`{"model":"gpt-test","user":"u-1"}`)
	storage := newContentBackupFakeStorage(body)
	c, _ := driveContentBackupRequest(t, contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        body,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		rounds:      []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true), writes: []string{`{"ok":1}`}}},
		storage:     storage,
	})

	closeCalls, readerCalls := storage.stats()
	if closeCalls != 0 {
		t.Fatalf("the collector closed the shared BodyStorage %d times", closeCalls)
	}
	if readerCalls != 1 {
		t.Fatalf("the collector must replay through NewReader exactly once, got %d", readerCalls)
	}
	raw, exists := c.Get(common.KeyBodyStorage)
	if !exists || raw == nil {
		t.Fatal("BodyStorage must stay in the context for BodyStorageCleanup")
	}
	if _, ok := raw.(common.BodyStorage); !ok {
		t.Fatal("the context value must still be a BodyStorage")
	}
	// The original cleanup middleware is the only owner: it must still be able to close it.
	common.CleanupBodyStorage(c)
	if closeCalls2, _ := storage.stats(); closeCalls2 != 1 {
		t.Fatalf("BodyStorageCleanup closed the storage %d times, want 1", closeCalls2)
	}
	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("expected one handoff, got %d", len(taken))
	}
	defer taken[0].Release()
	if !bytes.Equal(joinContentBackupChunks(taken[0].RequestChunks), body) {
		t.Fatalf("archived request = %q, want %q", joinContentBackupChunks(taken[0].RequestChunks), body)
	}
}

func TestContentBackupSessionResolution(t *testing.T) {
	long := strings.Repeat("a", contentbackup.SessionValueMaxBytes+1)
	cases := []struct {
		name        string
		path        string
		contentType string
		body        string
		formUser    string
		formPresent bool
		wantSource  string
		wantValue   string
		wantMissing string
	}{
		{name: "chat_top_level_user", path: "/v1/chat/completions", contentType: "application/json", body: `{"model":"m","user":"u-1"}`, wantSource: "user", wantValue: "u-1"},
		{name: "messages_metadata_user_id", path: "/v1/messages", contentType: "application/json", body: `{"model":"m","metadata":{"user_id":"claude-1"}}`, wantSource: "metadata.user_id", wantValue: "claude-1"},
		{name: "responses_prompt_cache_key", path: "/v1/responses", contentType: "application/json", body: `{"model":"m","prompt_cache_key":"pck-1"}`, wantSource: "prompt_cache_key", wantValue: "pck-1"},
		{name: "absent", path: "/v1/chat/completions", contentType: "application/json", body: `{"model":"m"}`, wantMissing: contentbackup.SessionMissingReasonAbsent},
		{name: "not_string_number", path: "/v1/chat/completions", contentType: "application/json", body: `{"model":"m","user":123}`, wantMissing: contentbackup.SessionMissingReasonNotString},
		{name: "not_string_bool", path: "/v1/chat/completions", contentType: "application/json", body: `{"model":"m","user":true}`, wantMissing: contentbackup.SessionMissingReasonNotString},
		{name: "not_string_object", path: "/v1/chat/completions", contentType: "application/json", body: `{"model":"m","user":{"id":"x"}}`, wantMissing: contentbackup.SessionMissingReasonNotString},
		{name: "too_long", path: "/v1/chat/completions", contentType: "application/json", body: `{"model":"m","user":"` + long + `"}`, wantMissing: contentbackup.SessionMissingReasonTooLong},
		{name: "empty_string_is_absent", path: "/v1/chat/completions", contentType: "application/json", body: `{"model":"m","user":""}`, wantMissing: contentbackup.SessionMissingReasonAbsent},
		{name: "invalid_json", path: "/v1/chat/completions", contentType: "application/json", body: `{not json`, wantMissing: contentbackup.SessionMissingReasonUnparseable},
		{name: "truncated_prefix", path: "/v1/chat/completions", contentType: "application/json", body: `{"model":"m","user":"u-1"}`, wantMissing: contentbackup.SessionMissingReasonUnparseable},
		{name: "multipart_from_parsed_form", path: "/v1/audio/transcriptions", contentType: "multipart/form-data; boundary=x", body: "raw-bytes", formUser: "form-user", formPresent: true, wantSource: "user", wantValue: "form-user"},
		{name: "multipart_without_parsed_form", path: "/v1/audio/transcriptions", contentType: "multipart/form-data; boundary=x", body: "raw-bytes", wantMissing: contentbackup.SessionMissingReasonAbsent},
		{name: "user_late_in_large_json", path: "/v1/chat/completions", contentType: "application/json", body: `{"model":"m","messages":[{"role":"user","content":"` + strings.Repeat("z", 200<<10) + `"}],"user":"late-user"}`, wantSource: "user", wantValue: "late-user"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newContentBackupRig()
			rig.install(t, 64<<20, 128)
			if tc.name == "truncated_prefix" {
				rig.runtime.MaxBodyBytes = 8
			}
			c := contentBackupTestContext(contentBackupRequest{
				path:        tc.path,
				body:        []byte(tc.body),
				contentType: tc.contentType,
				initial:     contentBackupRoundFor(12, "example", true),
				storage:     newContentBackupFakeStorage([]byte(tc.body)),
			})
			if tc.formPresent {
				c.Request.MultipartForm = &multipart.Form{
					Value: map[string][]string{"user": {tc.formUser}, "model": {"whisper-1"}},
				}
			}
			capture := BeginContentBackupCapture(c)
			if capture == nil {
				t.Fatal("capture must start")
			}
			ContentBackupRecordChannelRound(c, contentBackupRoundFor(12, "example", true))
			capture.ObserveResponseWrite([]byte(`{"ok":1}`), 8, nil)
			FinishContentBackupCapture(c)
			taken := rig.taken()
			if len(taken) != 1 {
				t.Fatalf("expected one handoff, got %d", len(taken))
			}
			defer taken[0].Release()
			meta := taken[0].Meta
			if tc.wantMissing != "" {
				if meta.SessionValue != nil {
					t.Fatalf("session_value = %q, want nil", *meta.SessionValue)
				}
				if meta.SessionMissingReason == nil || *meta.SessionMissingReason != tc.wantMissing {
					t.Fatalf("missing_reason = %v, want %q", meta.SessionMissingReason, tc.wantMissing)
				}
				if meta.SessionKey() != contentbackup.NoSessionKey {
					t.Fatalf("session key = %q, want nosession", meta.SessionKey())
				}
			} else {
				if meta.SessionValue == nil || *meta.SessionValue != tc.wantValue {
					t.Fatalf("session_value = %v, want %q", meta.SessionValue, tc.wantValue)
				}
				if meta.SessionSource == nil || *meta.SessionSource != tc.wantSource {
					t.Fatalf("session_source = %v, want %q", meta.SessionSource, tc.wantSource)
				}
				if meta.SessionMissingReason != nil {
					t.Fatalf("missing_reason must be nil for a resolved session, got %v", *meta.SessionMissingReason)
				}
				if meta.SessionKey() == contentbackup.NoSessionKey {
					t.Fatal("a resolved session must not hash to nosession")
				}
			}
			if err := contentbackup.ValidateMetadata(meta); err != nil {
				t.Fatalf("metadata must validate: %v", err)
			}
		})
	}
}

func TestContentBackupHandedOffCaptureDoesNotReferenceGinContext(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)
	body := []byte(`{"model":"gpt-test","user":"u-1"}`)
	storage := newContentBackupFakeStorage(body)
	c, _ := driveContentBackupRequest(t, contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        body,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		rounds:      []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true), writes: []string{`{"ok":1}`}}},
		storage:     storage,
	})
	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("expected one handoff, got %d", len(taken))
	}
	capture := taken[0]
	defer capture.Release()

	wantRequest := joinContentBackupChunks(capture.RequestChunks)
	wantResponse := joinContentBackupChunks(capture.ResponseChunks)
	wantMeta := capture.Meta

	// Wreck the request scope the way BodyStorageCleanup plus gin pooling would.
	common.CleanupBodyStorage(c)
	c.Request.MultipartForm = nil
	c.Request.Body = nil
	c.Set(string(constant.ContextKeyChannelId), 999)
	c.Set(string(constant.ContextKeyChannelName), "mutated")
	c.Set(common.RequestIdKey, "mutated")
	c.Set("id", 1)
	c.Writer.Header().Set("Content-Type", "mutated")
	c.Request = httptest.NewRequest(http.MethodPost, "/mutated", nil)

	read := make(chan struct{})
	go func() {
		defer close(read)
		if !bytes.Equal(joinContentBackupChunks(capture.RequestChunks), wantRequest) {
			t.Error("request chunks changed after the gin context was recycled")
		}
		if !bytes.Equal(joinContentBackupChunks(capture.ResponseChunks), wantResponse) {
			t.Error("response chunks changed after the gin context was recycled")
		}
		if capture.Meta.RequestID != wantMeta.RequestID || capture.Meta.ChannelID != wantMeta.ChannelID {
			t.Error("metadata changed after the gin context was recycled")
		}
		if capture.Meta.UserID != 10086 || capture.Meta.TokenID != 42 {
			t.Error("user/token identity must be frozen before handoff")
		}
	}()
	<-read
}

func TestContentBackupClientDisconnectMarksResponseIncomplete(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)
	body := []byte(`{"model":"gpt-test","stream":true}`)
	_, _ = driveContentBackupRequest(t, contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        body,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		rounds:      []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true), writes: []string{"data: partial\n\n"}, writeErr: errors.New("connection reset")}},
		storage:     newContentBackupFakeStorage(body),
		cancelBase:  true,
	})
	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("an interrupted stream must still be archived, got %d", len(taken))
	}
	defer taken[0].Release()
	meta := taken[0].Meta
	if meta.Response.Complete {
		t.Fatalf("response meta = %+v, want complete=false after a write error", meta.Response)
	}
	if meta.TerminalReason != ContentBackupTerminalClientDisconnect {
		t.Fatalf("terminal_reason = %q, want %q", meta.TerminalReason, ContentBackupTerminalClientDisconnect)
	}
	if string(joinContentBackupChunks(taken[0].ResponseChunks)) != "data: partial\n\n" {
		t.Fatalf("archived response = %q", joinContentBackupChunks(taken[0].ResponseChunks))
	}
	if err := contentbackup.ValidateMetadata(meta); err != nil {
		t.Fatalf("metadata must validate: %v", err)
	}
}

func TestContentBackupShortWriteCapturesOnlyAcceptedBytes(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)
	body := []byte(`{"model":"gpt-test"}`)
	c := contentBackupTestContext(contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        body,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		storage:     newContentBackupFakeStorage(body),
	})
	capture := BeginContentBackupCapture(c)
	if capture == nil {
		t.Fatal("capture must start")
	}
	ContentBackupRecordChannelRound(c, contentBackupRoundFor(12, "example", true))
	payload := []byte("0123456789")
	capture.ObserveResponseWrite(payload, 3, errors.New("short write"))
	FinishContentBackupCapture(c)
	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("expected one handoff, got %d", len(taken))
	}
	defer taken[0].Release()
	if string(joinContentBackupChunks(taken[0].ResponseChunks)) != "012" {
		t.Fatalf("captured = %q, want only the 3 accepted bytes", joinContentBackupChunks(taken[0].ResponseChunks))
	}
	meta := taken[0].Meta
	if meta.Response.CapturedBytes != 3 || meta.Response.ObservedBytes != 3 {
		t.Fatalf("response counters = %+v, want 3/3", meta.Response)
	}
	if meta.Response.Complete {
		t.Fatal("a short write must leave complete=false")
	}
	if meta.Response.Truncated {
		t.Fatal("observed == captured, truncated must stay false")
	}
}

func TestContentBackupRepeatedFlushAddsNoContent(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)
	body := []byte(`{"model":"gpt-test"}`)
	c := contentBackupTestContext(contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        body,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		storage:     newContentBackupFakeStorage(body),
	})
	capture := BeginContentBackupCapture(c)
	ContentBackupRecordChannelRound(c, contentBackupRoundFor(12, "example", true))
	capture.ObserveResponseWrite([]byte("data: 1\n\n"), 9, nil)
	capture.ObserveFlush()
	capture.ObserveFlush()
	if got := capture.Snapshot().Flushes; got != 2 {
		t.Fatalf("flush count = %d, want 2 delegated flushes", got)
	}
	if got := capture.Snapshot().ResponseCaptured; got != 9 {
		t.Fatalf("captured bytes = %d, want 9 (flush must not append content)", got)
	}
	FinishContentBackupCapture(c)
	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("expected one handoff, got %d", len(taken))
	}
	defer taken[0].Release()
	if string(joinContentBackupChunks(taken[0].ResponseChunks)) != "data: 1\n\n" {
		t.Fatalf("archived response = %q", joinContentBackupChunks(taken[0].ResponseChunks))
	}
}

func TestContentBackupInvalidMetadataIsDroppedNotHandedOff(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)
	rig.runtime.SiteID = "BAD SITE"
	body := []byte(`{"model":"gpt-test"}`)
	driveContentBackupRequest(t, contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        body,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		rounds:      []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true), writes: []string{`{"ok":1}`}}},
		storage:     newContentBackupFakeStorage(body),
	})
	if len(rig.taken()) != 0 {
		t.Fatal("invalid metadata must never reach the handoff worker")
	}
	stats := ContentBackupCaptureBudget().Stats()
	if stats.UsedBytes != 0 || stats.Inflight != 0 {
		t.Fatalf("dropped capture must free its budget: %+v", stats)
	}
}

func TestContentBackupGatedOutRequestNeverReadsBodyStorage(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)
	body := []byte(`{"model":"gpt-test","user":"u-1"}`)
	storage := newContentBackupFakeStorage(body)

	driveContentBackupRequest(t, contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        body,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		rounds: []contentBackupRound{
			{snapshot: contentBackupRoundFor(12, "example", true)},
			{snapshot: contentBackupRoundFor(13, "backup", false), writes: []string{`{"ok":1}`}},
		},
		storage: storage,
	})

	if len(rig.taken()) != 0 {
		t.Fatal("a final channel with backup off must not hand off an archive")
	}
	if _, readers := storage.stats(); readers != 0 {
		t.Fatalf("the gate must run before the body replay, NewReader calls = %d", readers)
	}
	if stats := ContentBackupCaptureBudget().Stats(); stats.UsedBytes != 0 || stats.Inflight != 0 {
		t.Fatalf("gated capture must free its budget: %+v", stats)
	}
}

func TestContentBackupHookPanicsNeverBreakTheRequest(t *testing.T) {
	body := []byte(`{"model":"gpt-test","user":"u-1"}`)
	baseRequest := func(rig *contentBackupRig) contentBackupRequest {
		return contentBackupRequest{
			path:        "/v1/chat/completions",
			body:        body,
			contentType: "application/json",
			initial:     contentBackupRoundFor(12, "example", true),
			rounds:      []contentBackupRound{{snapshot: contentBackupRoundFor(12, "example", true), writes: []string{`{"ok":1}`}}},
			storage:     newContentBackupFakeStorage(body),
		}
	}

	t.Run("runtime_provider_panics", func(t *testing.T) {
		rig := newContentBackupRig()
		rig.install(t, 64<<20, 128)
		hooks := rig.hooks()
		hooks.Runtime = func() ContentBackupRuntime { panic("runtime exploded") }
		ConfigureContentBackupCapture(hooks)

		c := contentBackupTestContext(baseRequest(rig))
		if BeginContentBackupCapture(c) != nil {
			t.Fatal("a panicking runtime provider must degrade to no capture")
		}
		FinishContentBackupCapture(c)
		if len(rig.taken()) != 0 {
			t.Fatal("nothing may be handed off")
		}
		if stats := ContentBackupCaptureBudget().Stats(); stats.UsedBytes != 0 || stats.Inflight != 0 {
			t.Fatalf("budget leaked: %+v", stats)
		}
	})

	t.Run("channel_switch_panics", func(t *testing.T) {
		rig := newContentBackupRig()
		rig.install(t, 64<<20, 128)
		hooks := rig.hooks()
		hooks.ChannelEnabled = func(any) bool { panic("settings exploded") }
		ConfigureContentBackupCapture(hooks)

		// No explicit relay round: the injected switch reader is the only source
		// for the initial Distribute channel, which is what may panic.
		request := baseRequest(rig)
		request.rounds = nil
		_, capture := driveContentBackupRequest(t, request)
		if capture == nil {
			t.Fatal("the relay scope must still be observable")
		}
		if capture.Snapshot().Armed {
			t.Fatal("a panicking channel switch must be treated as backup off")
		}
		if len(rig.taken()) != 0 {
			t.Fatal("nothing may be handed off")
		}
		if stats := ContentBackupCaptureBudget().Stats(); stats.UsedBytes != 0 || stats.Inflight != 0 {
			t.Fatalf("budget leaked: %+v", stats)
		}
	})

	t.Run("enqueuer_panics", func(t *testing.T) {
		rig := newContentBackupRig()
		rig.install(t, 64<<20, 128)
		releases := &atomic.Int64{}
		capture := &contentbackup.Capture{
			Meta:    contentbackup.Metadata{JobID: contentbackup.NewJobID()},
			Release: func() { releases.Add(1) },
		}
		hooks := rig.hooks()
		hooks.Enqueuer = func(*contentbackup.Capture) bool { panic("handoff exploded") }
		ConfigureContentBackupCapture(hooks)

		if ContentBackupHandoff(capture) {
			t.Fatal("a panicking enqueuer must report a failed handoff")
		}
		if releases.Load() != 1 {
			t.Fatalf("Release calls = %d, want exactly 1 so the budget cannot leak", releases.Load())
		}
	})

	t.Run("accepted_handoff_returns_budget_on_release", func(t *testing.T) {
		rig := newContentBackupRig()
		rig.install(t, 64<<20, 128)
		driveContentBackupRequest(t, baseRequest(rig))
		taken := rig.taken()
		if len(taken) != 1 {
			t.Fatalf("expected one handoff, got %d", len(taken))
		}
		if stats := ContentBackupCaptureBudget().Stats(); stats.Inflight != 1 {
			t.Fatalf("an accepted capture must stay charged: %+v", stats)
		}
		taken[0].Release()
		if stats := ContentBackupCaptureBudget().Stats(); stats.UsedBytes != 0 || stats.Inflight != 0 {
			t.Fatalf("budget leaked after release: %+v", stats)
		}
	})
}

func TestContentBackupDoubleMountedCollectorReusesOneCapture(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)
	body := []byte(`{"model":"gpt-test","user":"u-1"}`)
	c := contentBackupTestContext(contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        body,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		storage:     newContentBackupFakeStorage(body),
	})

	first := BeginContentBackupCapture(c)
	second := BeginContentBackupCapture(c)
	if first == nil || second != first {
		t.Fatal("a double mounted collector must reuse the installed capture, not orphan one")
	}
	if stats := ContentBackupCaptureBudget().Stats(); stats.Inflight != 1 {
		t.Fatalf("two admissions must not charge two inflight slots: %+v", stats)
	}
	ContentBackupRecordChannelRound(c, contentBackupRoundFor(12, "example", true))
	first.ObserveResponseWrite([]byte(`{"ok":1}`), 8, nil)
	FinishContentBackupCapture(c)
	FinishContentBackupCapture(c)
	if len(rig.taken()) != 1 {
		t.Fatalf("exactly one archive may be handed off, got %d", len(rig.taken()))
	}
	for _, taken := range rig.taken() {
		taken.Release()
	}
	if stats := ContentBackupCaptureBudget().Stats(); stats.UsedBytes != 0 || stats.Inflight != 0 {
		t.Fatalf("budget leaked: %+v", stats)
	}
}

func TestContentBackupBudgetAccountsAllocatedCapacity(t *testing.T) {
	budget := NewContentBackupBudget(10<<20, 4)
	if !budget.AcquireCapture() {
		t.Fatal("first capture must be admitted")
	}
	if !budget.ReserveBytes(ContentBackupChunkBytes) {
		t.Fatal("a chunk inside the budget must be reserved")
	}
	stats := budget.Stats()
	if stats.UsedBytes != ContentBackupChunkBytes || stats.Inflight != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	budget.SetLimits(1, 1)
	if budget.ReserveBytes(1) {
		t.Fatal("shrinking the budget must stop new admissions")
	}
	if budget.AcquireCapture() {
		t.Fatal("shrinking the inflight limit must stop new admissions")
	}
	budget.ReleaseBytes(ContentBackupChunkBytes)
	budget.ReleaseCapture()
	stats = budget.Stats()
	if stats.UsedBytes != 0 || stats.Inflight != 0 {
		t.Fatalf("stats after release = %+v", stats)
	}
	budget.ReleaseBytes(1)
	budget.ReleaseCapture()
	if stats = budget.Stats(); stats.UsedBytes < 0 || stats.Inflight < 0 {
		t.Fatalf("over release must stay clamped: %+v", stats)
	}
}

// 客户端在网关写完整份响应后才关连接（curl 单发请求、浏览器读完即关都是这样），
// net/http 会随之取消请求 ctx。这跟"流式写到一半客户端跑了"是两回事，
// 归档必须记成 complete —— 文档第 235 行把 response.complete=false 明确限定给流式断连。
// 此前 freeze 时现读请求 ctx 的 Err()，本地实测三次请求里两次被误记成 client_disconnect +
// response_complete=0，而字节数 312==312 根本没截断，客查会据此错判"响应不完整"。
func TestContentBackupNormalCloseAfterFullWriteStaysComplete(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)
	body := []byte(`{"model":"gpt-test"}`)
	c := contentBackupTestContext(contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        body,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		storage:     newContentBackupFakeStorage(body),
	})
	capture := BeginContentBackupCapture(c)
	if capture == nil {
		t.Fatal("capture must start")
	}
	ContentBackupRecordChannelRound(c, contentBackupRoundFor(12, "example", true))
	payload := []byte(`{"choices":[{"message":{"content":"ok"}}]}`)
	capture.ObserveResponseWrite(payload, len(payload), nil)
	// 全部字节都写出去之后，客户端才关连接 —— 顺序就是这一条的全部意义。
	c.Request = c.Request.WithContext(contentBackupCancelledContext())
	FinishContentBackupCapture(c)

	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("expected one handoff, got %d", len(taken))
	}
	defer taken[0].Release()
	meta := taken[0].Meta
	if !meta.Response.Complete {
		t.Fatalf("response meta = %+v, want complete=true: 写出全部成功，收尾时 ctx 取消只是客户端正常关连接", meta.Response)
	}
	if meta.TerminalReason != ContentBackupTerminalComplete {
		t.Fatalf("terminal_reason = %q, want %q", meta.TerminalReason, ContentBackupTerminalComplete)
	}
}

// 流式写到一半客户端断开：relay/helper 在写出前就检查 ctx 并直接 return，
// 不会产生失败的写，所以 writeFailed 捕捉不到。此时必须靠 relay 显式打的标记，
// 否则真断连会被记成 complete —— 比误报更危险。
func TestContentBackupStreamAbortMarkedDisconnectWithoutWriteError(t *testing.T) {
	rig := newContentBackupRig()
	rig.install(t, 64<<20, 128)
	body := []byte(`{"model":"gpt-test","stream":true}`)
	c := contentBackupTestContext(contentBackupRequest{
		path:        "/v1/chat/completions",
		body:        body,
		contentType: "application/json",
		initial:     contentBackupRoundFor(12, "example", true),
		storage:     newContentBackupFakeStorage(body),
	})
	capture := BeginContentBackupCapture(c)
	if capture == nil {
		t.Fatal("capture must start")
	}
	ContentBackupRecordChannelRound(c, contentBackupRoundFor(12, "example", true))
	chunk := []byte("data: partial\n\n")
	capture.ObserveResponseWrite(chunk, len(chunk), nil)
	// relay 发现客户端已走、拒绝继续写：没有失败的写，只有这个标记。
	common.SetContextKey(c, constant.ContextKeyClientDisconnected, true)
	FinishContentBackupCapture(c)

	taken := rig.taken()
	if len(taken) != 1 {
		t.Fatalf("an interrupted stream must still be archived, got %d", len(taken))
	}
	defer taken[0].Release()
	meta := taken[0].Meta
	if meta.Response.Complete {
		t.Fatalf("response meta = %+v, want complete=false: 网关还有数据要发却发不出去", meta.Response)
	}
	if meta.TerminalReason != ContentBackupTerminalClientDisconnect {
		t.Fatalf("terminal_reason = %q, want %q", meta.TerminalReason, ContentBackupTerminalClientDisconnect)
	}
}
