package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/contentbackup"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// ContentBackupChunkBytes is the fixed block every captured side grows by; the
// byte budget is charged for allocated capacity, never for appended length.
const ContentBackupChunkBytes = 64 * 1024

// terminal_reason 是两层词汇表：前四个由采集器在冻结时自行派生，无需调用方接线；
// 后三个只在调用方已经知道失败原因时由 ContentBackupSetTerminalReason 显式设置，
// 未设置时落到 error_response 兜底。
const (
	ContentBackupTerminalComplete         = "complete"
	ContentBackupTerminalClientDisconnect = "client_disconnect"
	ContentBackupTerminalErrorResponse    = "error_response"
	ContentBackupTerminalUnknown          = "unknown"

	ContentBackupTerminalValidationError = "validation_error"
	ContentBackupTerminalUpstreamError   = "upstream_error"
	ContentBackupTerminalRetryExhausted  = "retry_exhausted"
)

const (
	contentBackupCaptureKey      = "content_backup_capture"
	contentBackupLogIntervalSecs = 60
)

var errContentBackupRequestBodyUnavailable = errors.New("content backup: request body storage is unavailable")

// contentBackupEndpoints is the section 2.2 whitelist mapped to its section 5.1
// session source. Matching is exact on purpose: no /images/* style prefix, and
// realtime, files, batch, async task, /pg/* plus the relay routes that are not
// whitelisted (/v1/edits, /v1/rerank, /v1/moderations, /v1/images/variations,
// /v1/responses/compact) must never install a capture.
var contentBackupEndpoints = map[string]string{
	"/v1/chat/completions":     contentbackup.SessionSourceUser,
	"/v1/completions":          contentbackup.SessionSourceUser,
	"/v1/messages":             contentbackup.SessionSourceMetadataUserID,
	"/v1/responses":            contentbackup.SessionSourcePromptCacheKey,
	"/v1/images/generations":   contentbackup.SessionSourceUser,
	"/v1/images/edits":         contentbackup.SessionSourceUser,
	"/v1/audio/transcriptions": contentbackup.SessionSourceUser,
	"/v1/audio/translations":   contentbackup.SessionSourceUser,
	"/v1/audio/speech":         contentbackup.SessionSourceUser,
	"/v1/embeddings":           contentbackup.SessionSourceUser,
}

var contentBackupSessionJSONPaths = map[string]string{
	contentbackup.SessionSourceUser:           "user",
	contentbackup.SessionSourceMetadataUserID: "metadata.user_id",
	contentbackup.SessionSourcePromptCacheKey: "prompt_cache_key",
}

func IsContentBackupEndpoint(path string) bool {
	_, ok := contentBackupEndpoints[path]
	return ok
}

func ContentBackupSessionSource(path string) (string, bool) {
	source, ok := contentBackupEndpoints[path]
	return source, ok
}

// ContentBackupRuntime is the immutable per request snapshot of deployment
// identity plus the published config version. Node identity comes from
// deployment variables, never from the editable config or from common.NodeName.
type ContentBackupRuntime struct {
	Enabled       bool
	SiteID        string
	StorageNodeID string
	TargetID      string
	ConfigVersion int64
	MaxBodyBytes  int64
}

type ContentBackupRuntimeProvider func() ContentBackupRuntime

// ContentBackupEnqueuer is the non blocking handoff T08 injects; it transfers
// capture ownership to the fixed handoff worker and must never block the relay.
type ContentBackupEnqueuer func(capture *contentbackup.Capture) bool

// ContentBackupChannelSwitch reads dto.ChannelSettings.content_backup_enabled.
// It is injected because that field belongs to T08, not to this card.
type ContentBackupChannelSwitch func(channelSettings any) bool

type ContentBackupCaptureHooks struct {
	Runtime        ContentBackupRuntimeProvider
	Enqueuer       ContentBackupEnqueuer
	ChannelEnabled ContentBackupChannelSwitch
}

var (
	contentBackupHooksMu sync.RWMutex
	contentBackupHooks   ContentBackupCaptureHooks
)

func ConfigureContentBackupCapture(hooks ContentBackupCaptureHooks) {
	contentBackupHooksMu.Lock()
	contentBackupHooks = hooks
	contentBackupHooksMu.Unlock()
}

func contentBackupHooksSnapshot() ContentBackupCaptureHooks {
	contentBackupHooksMu.RLock()
	defer contentBackupHooksMu.RUnlock()
	return contentBackupHooks
}

func contentBackupSafeRuntime(hooks ContentBackupCaptureHooks) (runtime ContentBackupRuntime, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			runtime = ContentBackupRuntime{}
			ok = false
			contentBackupLogFailure("runtime provider panic: %v", r)
		}
	}()
	return hooks.Runtime(), true
}

func contentBackupSafeChannelEnabled(reader ContentBackupChannelSwitch, settings any) (enabled bool) {
	defer func() {
		if r := recover(); r != nil {
			enabled = false
		}
	}()
	return reader(settings)
}

func contentBackupSafeEnqueue(enqueuer ContentBackupEnqueuer, capture *contentbackup.Capture) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			contentBackupLogFailure("enqueuer panic job_id=%s: %v", capture.Meta.JobID, r)
			ok = false
		}
	}()
	return enqueuer(capture)
}

type ContentBackupBudgetStats struct {
	MaxBytes         int64
	MaxInflight      int
	UsedBytes        int64
	Inflight         int
	RejectedBytes    int64
	RejectedInflight int64
}

// ContentBackupBudget is the process wide capture_memory_mb plus
// max_inflight_captures account. Active captures, captures waiting for handoff
// and captures being handed off all charge the same budget.
type ContentBackupBudget struct {
	mu               sync.Mutex
	maxBytes         int64
	maxInflight      int
	usedBytes        int64
	inflight         int
	rejectedBytes    int64
	rejectedInflight int64
}

func NewContentBackupBudget(maxBytes int64, maxInflight int) *ContentBackupBudget {
	budget := &ContentBackupBudget{}
	budget.SetLimits(maxBytes, maxInflight)
	return budget
}

// SetLimits applies a newly published config. Shrinking never evicts in flight
// captures, it only stops new admissions.
func (b *ContentBackupBudget) SetLimits(maxBytes int64, maxInflight int) {
	if maxBytes < 0 {
		maxBytes = 0
	}
	if maxInflight < 0 {
		maxInflight = 0
	}
	b.mu.Lock()
	b.maxBytes = maxBytes
	b.maxInflight = maxInflight
	b.mu.Unlock()
}

func (b *ContentBackupBudget) AcquireCapture() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inflight >= b.maxInflight {
		b.rejectedInflight++
		return false
	}
	b.inflight++
	return true
}

func (b *ContentBackupBudget) ReleaseCapture() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inflight--
	if b.inflight < 0 {
		b.inflight = 0
	}
}

func (b *ContentBackupBudget) ReserveBytes(n int64) bool {
	if n <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.usedBytes+n > b.maxBytes {
		b.rejectedBytes++
		return false
	}
	b.usedBytes += n
	return true
}

func (b *ContentBackupBudget) ReleaseBytes(n int64) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.usedBytes -= n
	if b.usedBytes < 0 {
		b.usedBytes = 0
	}
}

func (b *ContentBackupBudget) Stats() ContentBackupBudgetStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return ContentBackupBudgetStats{
		MaxBytes:         b.maxBytes,
		MaxInflight:      b.maxInflight,
		UsedBytes:        b.usedBytes,
		Inflight:         b.inflight,
		RejectedBytes:    b.rejectedBytes,
		RejectedInflight: b.rejectedInflight,
	}
}

var contentBackupBudget = newDefaultContentBackupBudget()

func newDefaultContentBackupBudget() *ContentBackupBudget {
	cfg := contentbackup.DefaultConfig()
	return NewContentBackupBudget(int64(cfg.CaptureMemoryMB)<<20, cfg.MaxInflightCaptures)
}

func SetContentBackupCaptureBudget(maxBytes int64, maxInflight int) {
	contentBackupBudget.SetLimits(maxBytes, maxInflight)
}

func ContentBackupCaptureBudget() *ContentBackupBudget {
	return contentBackupBudget
}

type ContentBackupCounters struct {
	Started         int64 `json:"started"`
	Armed           int64 `json:"armed"`
	HandedOff       int64 `json:"handed_off"`
	EnqueueRejected int64 `json:"enqueue_rejected"`
	ChannelGated    int64 `json:"channel_gated"`
	BudgetRejected  int64 `json:"budget_rejected"`
	InvalidMetadata int64 `json:"invalid_metadata"`
	ProbeSkipped    int64 `json:"probe_skipped"`
}

var contentBackupCounters struct {
	started         atomic.Int64
	armed           atomic.Int64
	handedOff       atomic.Int64
	enqueueRejected atomic.Int64
	channelGated    atomic.Int64
	budgetRejected  atomic.Int64
	invalidMetadata atomic.Int64
	probeSkipped    atomic.Int64
}

func ContentBackupCaptureCounters() ContentBackupCounters {
	return ContentBackupCounters{
		Started:         contentBackupCounters.started.Load(),
		Armed:           contentBackupCounters.armed.Load(),
		HandedOff:       contentBackupCounters.handedOff.Load(),
		EnqueueRejected: contentBackupCounters.enqueueRejected.Load(),
		ChannelGated:    contentBackupCounters.channelGated.Load(),
		BudgetRejected:  contentBackupCounters.budgetRejected.Load(),
		InvalidMetadata: contentBackupCounters.invalidMetadata.Load(),
		ProbeSkipped:    contentBackupCounters.probeSkipped.Load(),
	}
}

func ContentBackupResetCaptureCounters() {
	contentBackupCounters.started.Store(0)
	contentBackupCounters.armed.Store(0)
	contentBackupCounters.handedOff.Store(0)
	contentBackupCounters.enqueueRejected.Store(0)
	contentBackupCounters.channelGated.Store(0)
	contentBackupCounters.budgetRejected.Store(0)
	contentBackupCounters.invalidMetadata.Store(0)
	contentBackupCounters.probeSkipped.Store(0)
}

// ContentBackupChannelRound is one relay round's frozen channel identity. The
// archive is attributed to the last round recorded, so a round that only failed
// to select a channel never overwrites the previous actual attempt.
type ContentBackupChannelRound struct {
	ChannelID         int
	ChannelName       string
	ChannelType       int
	BackupEnabled     bool
	UpstreamRequestID string
}

type ContentBackupCaptureSnapshot struct {
	Armed            bool
	Denied           bool
	Hijacked         bool
	WriteFailed      bool
	Flushes          int
	Rounds           []ContentBackupChannelRound
	RequestCaptured  int64
	RequestObserved  int64
	ResponseCaptured int64
	ResponseObserved int64
	AllocatedBytes   int64
}

type contentBackupChunkSink struct {
	limit    int64
	chunks   [][]byte
	used     int
	captured int64
	reserved int64
	denied   bool
	readErr  error
}

func (s *contentBackupChunkSink) ensureRoom(budget *ContentBackupBudget) bool {
	if len(s.chunks) > 0 && s.used < len(s.chunks[len(s.chunks)-1]) {
		return true
	}
	size := s.limit - s.captured
	if size <= 0 {
		return false
	}
	if size > ContentBackupChunkBytes {
		size = ContentBackupChunkBytes
	}
	if !budget.ReserveBytes(size) {
		return false
	}
	s.reserved += size
	s.chunks = append(s.chunks, make([]byte, size))
	s.used = 0
	return true
}

// appendBytes stops filling chunks at the per side limit but the caller keeps
// counting the real observed bytes, otherwise a truncated side would report
// observed == captured and fail ValidateMetadata.
func (s *contentBackupChunkSink) appendBytes(budget *ContentBackupBudget, p []byte) {
	for len(p) > 0 && !s.denied {
		if s.captured >= s.limit {
			return
		}
		if !s.ensureRoom(budget) {
			s.denied = true
			return
		}
		last := s.chunks[len(s.chunks)-1]
		n := copy(last[s.used:], p)
		s.used += n
		s.captured += int64(n)
		p = p[n:]
	}
}

func (s *contentBackupChunkSink) appendString(budget *ContentBackupBudget, str string) {
	for len(str) > 0 && !s.denied {
		if s.captured >= s.limit {
			return
		}
		if !s.ensureRoom(budget) {
			s.denied = true
			return
		}
		last := s.chunks[len(s.chunks)-1]
		n := copy(last[s.used:], str)
		s.used += n
		s.captured += int64(n)
		str = str[n:]
	}
}

// readWhole fills one fixed block for the request side: its size is known from
// BodyStorage.Size(), and a single contiguous block lets the session source be
// read without copying the whole body again.
func (s *contentBackupChunkSink) readWhole(budget *ContentBackupBudget, reader io.Reader, want int64) {
	if want <= 0 {
		return
	}
	if !budget.ReserveBytes(want) {
		s.denied = true
		return
	}
	s.reserved += want
	block := make([]byte, want)
	var read int64
	for read < want {
		n, err := reader.Read(block[read:])
		read += int64(n)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.readErr = err
			}
			break
		}
	}
	s.chunks = [][]byte{block[:read]}
	s.used = int(read)
	s.captured = read
}

func (s *contentBackupChunkSink) snapshotChunks() [][]byte {
	if s.captured <= 0 || len(s.chunks) == 0 {
		return nil
	}
	out := make([][]byte, len(s.chunks))
	copy(out, s.chunks)
	if last := len(out) - 1; s.used < len(out[last]) {
		out[last] = out[last][:s.used]
	}
	return out
}

func (s *contentBackupChunkSink) release(budget *ContentBackupBudget) {
	if s.reserved > 0 {
		budget.ReleaseBytes(s.reserved)
		s.reserved = 0
	}
	s.chunks = nil
	s.used = 0
	s.captured = 0
}

// ContentBackupCapture is one request's bounded snapshot. Every field the
// handoff worker needs is frozen before FinishContentBackupCapture returns, so
// the worker never touches the gin context, Request.Body or a mutable RelayInfo.
type ContentBackupCapture struct {
	budget        *ContentBackupBudget
	runtime       ContentBackupRuntime
	beginTime     time.Time
	endpoint      string
	sessionSource string
	release       sync.Once

	mu               sync.Mutex
	rounds           []ContentBackupChannelRound
	armed            bool
	slotHeld         bool
	denied           bool
	hijacked         bool
	writeFailed      bool
	finished         bool
	terminalReason   string
	request          contentBackupChunkSink
	response         contentBackupChunkSink
	requestObserved  int64
	requestMissing   bool
	responseObserved int64
	flushes          int
}

func (cap *ContentBackupCapture) Snapshot() ContentBackupCaptureSnapshot {
	cap.mu.Lock()
	defer cap.mu.Unlock()
	rounds := make([]ContentBackupChannelRound, len(cap.rounds))
	copy(rounds, cap.rounds)
	return ContentBackupCaptureSnapshot{
		Armed:            cap.armed,
		Denied:           cap.denied,
		Hijacked:         cap.hijacked,
		WriteFailed:      cap.writeFailed,
		Flushes:          cap.flushes,
		Rounds:           rounds,
		RequestCaptured:  cap.request.captured,
		RequestObserved:  cap.requestObserved,
		ResponseCaptured: cap.response.captured,
		ResponseObserved: cap.responseObserved,
		AllocatedBytes:   cap.request.reserved + cap.response.reserved,
	}
}

func (cap *ContentBackupCapture) maxBodyBytesLocked() int64 {
	limit := cap.runtime.MaxBodyBytes
	if limit <= 0 || limit > contentbackup.MaxBodyBytesPerSide {
		limit = contentbackup.MaxBodyBytesPerSide
	}
	return limit
}

func (cap *ContentBackupCapture) armLocked() {
	if cap.armed || cap.denied {
		return
	}
	if !cap.budget.AcquireCapture() {
		cap.denied = true
		contentBackupCounters.budgetRejected.Add(1)
		return
	}
	cap.slotHeld = true
	cap.armed = true
	limit := cap.maxBodyBytesLocked()
	cap.request.limit = limit
	cap.response.limit = limit
	cap.response.chunks = make([][]byte, 0, int(limit/ContentBackupChunkBytes)+1)
	contentBackupCounters.armed.Add(1)
}

// BeginContentBackupCapture installs the request scope capture. It returns nil,
// allocating nothing at all, whenever the feature is off, the path is not
// whitelisted, no channel was selected or the admission budget is exhausted.
func BeginContentBackupCapture(c *gin.Context) (capture *ContentBackupCapture) {
	defer func() {
		if r := recover(); r != nil {
			capture = nil
			contentBackupLogFailure("begin panic: %v", r)
		}
	}()
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return nil
	}
	// A route that accidentally mounts the collector twice must not orphan the
	// first capture: that would leak its inflight slot and byte budget forever.
	if existing := contentBackupCaptureFrom(c); existing != nil {
		return existing
	}
	hooks := contentBackupHooksSnapshot()
	if hooks.Runtime == nil {
		return nil
	}
	runtime, ok := contentBackupSafeRuntime(hooks)
	if !ok || !runtime.Enabled {
		return nil
	}
	if runtime.SiteID == "" || runtime.StorageNodeID == "" || runtime.TargetID == "" || runtime.ConfigVersion < 1 {
		contentBackupLogFailure("runtime identity is incomplete, skipping capture")
		return nil
	}
	source, ok := contentBackupEndpoints[c.Request.URL.Path]
	if !ok {
		return nil
	}
	if contentBackupShouldSkipProbeContext(c) {
		contentBackupCounters.probeSkipped.Add(1)
		return nil
	}
	initial := contentBackupInitialRound(c, hooks)
	if initial.ChannelID <= 0 {
		return nil
	}
	capture = &ContentBackupCapture{
		budget:        contentBackupBudget,
		runtime:       runtime,
		beginTime:     time.Now(),
		endpoint:      c.Request.URL.Path,
		sessionSource: source,
		rounds:        []ContentBackupChannelRound{initial},
	}
	if initial.BackupEnabled {
		capture.armLocked()
	}
	if capture.denied {
		return nil
	}
	c.Set(contentBackupCaptureKey, capture)
	contentBackupCounters.started.Add(1)
	return capture
}

func contentBackupInitialRound(c *gin.Context, hooks ContentBackupCaptureHooks) ContentBackupChannelRound {
	round := ContentBackupChannelRound{
		ChannelID:   common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		ChannelName: common.GetContextKeyString(c, constant.ContextKeyChannelName),
		ChannelType: common.GetContextKeyInt(c, constant.ContextKeyChannelType),
	}
	if hooks.ChannelEnabled == nil || round.ChannelID <= 0 {
		return round
	}
	settings, exists := c.Get(string(constant.ContextKeyChannelSetting))
	if !exists || settings == nil {
		return round
	}
	round.BackupEnabled = contentBackupSafeChannelEnabled(hooks.ChannelEnabled, settings)
	return round
}

// ContentBackupCaptureActive 报告本请求是否已建立采集。调用方用它跳过为
// ContentBackupRecordChannelRound 准备参数的开销：那些参数在调用前就会求值，
// 而 RecordChannelRound 内部的 nil 短路发生在求值之后，所以功能关闭时
// 每个请求每轮都会白付一次渠道设置的 JSON 反序列化。
func ContentBackupCaptureActive(c *gin.Context) bool {
	return contentBackupCaptureFrom(c) != nil
}

// ContentBackupRecordChannelRound is called by controller.Relay once per retry
// round so the archive can be attributed to the final actually attempted
// channel without the background ever reading the gin context again.
func ContentBackupRecordChannelRound(c *gin.Context, round ContentBackupChannelRound) {
	capture := contentBackupCaptureFrom(c)
	if capture == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			contentBackupLogFailure("channel round panic: %v", r)
		}
	}()
	capture.mu.Lock()
	capture.rounds = append(capture.rounds, round)
	if round.BackupEnabled {
		capture.armLocked()
	}
	capture.mu.Unlock()
}

func ContentBackupSetTerminalReason(c *gin.Context, reason string) {
	capture := contentBackupCaptureFrom(c)
	if capture == nil || reason == "" {
		return
	}
	capture.mu.Lock()
	capture.terminalReason = reason
	capture.mu.Unlock()
}

func (cap *ContentBackupCapture) ObserveResponseWrite(p []byte, n int, err error) {
	if n < 0 {
		n = 0
	}
	if n > len(p) {
		n = len(p)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	cap.responseObserved += int64(n)
	if err != nil || n < len(p) {
		cap.writeFailed = true
	}
	if !cap.armed || cap.denied || cap.hijacked {
		return
	}
	cap.response.appendBytes(cap.budget, p[:n])
	cap.noteDenialLocked()
}

func (cap *ContentBackupCapture) ObserveResponseWriteString(s string, n int, err error) {
	if n < 0 {
		n = 0
	}
	if n > len(s) {
		n = len(s)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	cap.responseObserved += int64(n)
	if err != nil || n < len(s) {
		cap.writeFailed = true
	}
	if !cap.armed || cap.denied || cap.hijacked {
		return
	}
	cap.response.appendString(cap.budget, s[:n])
	cap.noteDenialLocked()
}

// ObserveFlush only counts: a flush carries no content, so repeated flushes
// must never append bytes to the archive.
func (cap *ContentBackupCapture) ObserveFlush() {
	cap.mu.Lock()
	cap.flushes++
	cap.mu.Unlock()
}

func (cap *ContentBackupCapture) ObserveHijack() {
	cap.mu.Lock()
	cap.hijacked = true
	cap.mu.Unlock()
}

func (cap *ContentBackupCapture) noteDenialLocked() {
	if cap.response.denied && !cap.denied {
		cap.denied = true
		contentBackupCounters.budgetRejected.Add(1)
	}
}

func contentBackupCaptureFrom(c *gin.Context) *ContentBackupCapture {
	if c == nil {
		return nil
	}
	raw, exists := c.Get(contentBackupCaptureKey)
	if !exists || raw == nil {
		return nil
	}
	capture, _ := raw.(*ContentBackupCapture)
	return capture
}

// FinishContentBackupCapture freezes the snapshot, applies the final channel
// gate and hands the capture off. It must run before BodyStorageCleanup, and it
// releases the budget even when the handler panicked.
func FinishContentBackupCapture(c *gin.Context) {
	capture := contentBackupCaptureFrom(c)
	if capture == nil {
		return
	}
	c.Set(contentBackupCaptureKey, nil)
	release := capture.releaseFunc()
	defer func() {
		if r := recover(); r != nil {
			contentBackupLogFailure("finish panic: %v", r)
			release()
		}
	}()
	frozen, ok := capture.freeze(c)
	if !ok {
		release()
		return
	}
	if contentBackupShouldSkipProbeCapture(c, frozen) {
		contentBackupCounters.probeSkipped.Add(1)
		release()
		return
	}
	contentBackupRedactCapture(frozen)
	if err := contentbackup.ValidateMetadata(frozen.Meta); err != nil {
		contentBackupCounters.invalidMetadata.Add(1)
		contentBackupLogFailure("redacted metadata rejected request_id=%s: %v", frozen.Meta.RequestID, err)
		release()
		return
	}
	ContentBackupHandoff(frozen)
}

func (cap *ContentBackupCapture) releaseFunc() func() {
	return func() {
		cap.release.Do(func() {
			cap.mu.Lock()
			cap.request.release(cap.budget)
			cap.response.release(cap.budget)
			slot := cap.slotHeld
			cap.slotHeld = false
			cap.mu.Unlock()
			if slot {
				cap.budget.ReleaseCapture()
			}
		})
	}
}

func (cap *ContentBackupCapture) freeze(c *gin.Context) (*contentbackup.Capture, bool) {
	cap.mu.Lock()
	if cap.finished {
		cap.mu.Unlock()
		return nil, false
	}
	cap.finished = true
	if !cap.armed || cap.denied || cap.hijacked {
		denied := cap.denied
		hijacked := cap.hijacked
		cap.mu.Unlock()
		if !denied && !hijacked {
			contentBackupCounters.channelGated.Add(1)
		}
		return nil, false
	}
	rounds := make([]ContentBackupChannelRound, len(cap.rounds))
	copy(rounds, cap.rounds)
	requestLimit := cap.request.limit
	cap.mu.Unlock()

	// Gate before replaying the request body: a final channel with backup
	// switched off must not pay for an 8 MiB BodyStorage read it will discard.
	final, ok := contentBackupFinalRound(rounds)
	if !ok || !final.BackupEnabled {
		contentBackupCounters.channelGated.Add(1)
		return nil, false
	}

	// The replay runs with no lock held: holding cap.mu across up to 8 MiB of
	// disk reads would block ObserveResponseWrite, and stream_scanner only waits
	// 5 seconds for its writer goroutines before returning.
	request, requestObserved, requestMissing := cap.readRequest(c, requestLimit)

	cap.mu.Lock()
	// Adopt the local sink so releaseFunc still owns its budget reservation.
	cap.request = request
	cap.requestObserved = requestObserved
	cap.requestMissing = requestMissing
	cap.noteRequestDenialLocked()
	denied := cap.denied
	hijacked := cap.hijacked
	requestChunks := cap.request.snapshotChunks()
	responseChunks := cap.response.snapshotChunks()
	requestCaptured := cap.request.captured
	requestReadErr := cap.request.readErr
	responseCaptured := cap.response.captured
	responseObserved := cap.responseObserved
	writeFailed := cap.writeFailed
	reason := cap.terminalReason
	cap.mu.Unlock()

	if denied || hijacked {
		return nil, false
	}

	meta := cap.buildMetadata(c, final, reason, writeFailed, hijacked,
		requestCaptured, requestObserved, requestReadErr, requestMissing,
		responseCaptured, responseObserved, requestChunks)
	if err := contentbackup.ValidateMetadata(meta); err != nil {
		contentBackupCounters.invalidMetadata.Add(1)
		contentBackupLogFailure("metadata rejected request_id=%s: %v", meta.RequestID, err)
		return nil, false
	}
	return &contentbackup.Capture{
		Meta:           meta,
		RequestChunks:  requestChunks,
		ResponseChunks: responseChunks,
		Release:        cap.releaseFunc(),
	}, true
}

func (cap *ContentBackupCapture) noteRequestDenialLocked() {
	if cap.request.denied && !cap.denied {
		cap.denied = true
		contentBackupCounters.budgetRejected.Add(1)
	}
}

func contentBackupFinalRound(rounds []ContentBackupChannelRound) (ContentBackupChannelRound, bool) {
	if len(rounds) == 0 {
		return ContentBackupChannelRound{}, false
	}
	return rounds[len(rounds)-1], true
}

// contentBackupBodyStorage only looks the storage up. Falling back to
// common.GetBodyStorage here would re-read c.Request.Body (which relay pointed
// at that very storage) and overwrite the context key, orphaning the original
// storage from BodyStorageCleanup.
func contentBackupBodyStorage(c *gin.Context) (common.BodyStorage, bool) {
	raw, exists := c.Get(common.KeyBodyStorage)
	if !exists || raw == nil {
		return nil, false
	}
	storage, ok := raw.(common.BodyStorage)
	if !ok || storage == nil {
		return nil, false
	}
	return storage, true
}

// readRequest replays the original BodyStorage through NewReader into a caller
// owned sink. Only that independent reader is closed; the storage itself stays
// with middleware.BodyStorageCleanup.
func (cap *ContentBackupCapture) readRequest(c *gin.Context, limit int64) (contentBackupChunkSink, int64, bool) {
	sink := contentBackupChunkSink{limit: limit}
	storage, ok := contentBackupBodyStorage(c)
	if !ok {
		sink.readErr = errContentBackupRequestBodyUnavailable
		return sink, 0, true
	}
	size := storage.Size()
	if size < 0 {
		size = 0
	}
	if size == 0 {
		return sink, 0, false
	}
	want := size
	if want > limit {
		want = limit
	}
	reader, err := storage.NewReader()
	if err != nil {
		sink.readErr = err
		return sink, size, false
	}
	defer reader.Close()
	sink.readWhole(cap.budget, reader, want)
	if sink.captured > size {
		size = sink.captured
	}
	return sink, size, false
}

func (cap *ContentBackupCapture) buildMetadata(c *gin.Context, final ContentBackupChannelRound, reason string, writeFailed, hijacked bool,
	requestCaptured, requestObserved int64, requestReadErr error, requestMissing bool,
	responseCaptured, responseObserved int64, requestChunks [][]byte) contentbackup.Metadata {

	status := 0
	responseContentType := ""
	if c != nil && c.Writer != nil {
		status = c.Writer.Status()
		responseContentType = c.Writer.Header().Get("Content-Type")
	}
	if status < 100 || status > 599 {
		status = 0
	}

	requestContentType := ""
	upstreamID := final.UpstreamRequestID
	userID := 0
	tokenID := 0
	requestID := ""
	model := ""
	stream := false
	startedAt := cap.beginTime
	if c != nil {
		if c.Request != nil {
			requestContentType = c.Request.Header.Get("Content-Type")
		}
		if upstreamID == "" {
			upstreamID = c.GetString(common.UpstreamRequestIdKey)
		}
		requestID = c.GetString(common.RequestIdKey)
		userID = c.GetInt("id")
		tokenID = c.GetInt("token_id")
		model = common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
		if model == "" {
			model = c.GetString("model")
		}
		stream = common.GetContextKeyBool(c, constant.ContextKeyIsStream)
		if fromContext := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime); !fromContext.IsZero() {
			startedAt = fromContext
		}
	}
	if !stream && strings.HasPrefix(strings.ToLower(strings.TrimSpace(responseContentType)), "text/event-stream") {
		stream = true
	}
	if userID < 0 {
		userID = 0
	}
	if tokenID < 0 {
		tokenID = 0
	}
	if startedAt.IsZero() {
		startedAt = cap.beginTime
	}

	// 断连只认 relay 写出层置位的标记：baseCtx 在客户端收完响应后正常关连接时
	// 同样会被 net/http 取消，冻结时才去读它会把正常结束误记成 client_disconnect。
	clientGone := common.GetContextKeyBool(c, constant.ContextKeyClientDisconnected)
	if reason == "" {
		reason = contentBackupDeriveTerminalReason(writeFailed, clientGone, status, !writeFailed && !clientGone)
	}

	sessionRef := cap.resolveSession(c, requestChunks, requestObserved > requestCaptured, requestContentType)

	var upstreamPtr *string
	if upstreamID != "" {
		value := upstreamID
		upstreamPtr = &value
	}
	return contentbackup.Metadata{
		Version:              contentbackup.MetadataVersion,
		SiteID:               cap.runtime.SiteID,
		StorageNodeID:        cap.runtime.StorageNodeID,
		JobID:                contentbackup.NewJobID(),
		RequestID:            requestID,
		TargetID:             cap.runtime.TargetID,
		ConfigVersion:        cap.runtime.ConfigVersion,
		RequestStartedAt:     startedAt,
		UserID:               userID,
		TokenID:              tokenID,
		ChannelID:            final.ChannelID,
		ChannelName:          final.ChannelName,
		Model:                model,
		Endpoint:             cap.endpoint,
		TerminalReason:       reason,
		ChannelType:          final.ChannelType,
		HTTPStatus:           status,
		UpstreamRequestID:    upstreamPtr,
		SessionSource:        sessionRef.Source,
		SessionValue:         sessionRef.Value,
		SessionMissingReason: sessionRef.MissingReason,
		Stream:               stream,
		Request: contentbackup.BodyMeta{
			ContentType:   requestContentType,
			CapturedBytes: requestCaptured,
			ObservedBytes: requestObserved,
			Truncated:     requestObserved > requestCaptured,
			Complete:      !requestMissing && requestReadErr == nil,
		},
		Response: contentbackup.BodyMeta{
			ContentType:   responseContentType,
			CapturedBytes: responseCaptured,
			ObservedBytes: responseObserved,
			Truncated:     responseObserved > responseCaptured,
			Complete:      !writeFailed && !clientGone && !hijacked,
		},
	}
}

func contentBackupDeriveTerminalReason(writeFailed, clientGone bool, status int, complete bool) string {
	switch {
	case writeFailed || clientGone:
		return ContentBackupTerminalClientDisconnect
	case status >= 400:
		return ContentBackupTerminalErrorResponse
	case complete:
		return ContentBackupTerminalComplete
	default:
		return ContentBackupTerminalUnknown
	}
}

func (cap *ContentBackupCapture) resolveSession(c *gin.Context, requestChunks [][]byte, truncated bool, requestContentType string) contentbackup.SessionRef {
	source := cap.sessionSource
	if source == "" {
		return contentbackup.MissingSession(contentbackup.SessionMissingReasonAbsent)
	}
	contentType := strings.ToLower(strings.TrimSpace(requestContentType))
	if strings.Contains(contentType, "multipart/form-data") || strings.Contains(contentType, "application/x-www-form-urlencoded") {
		return contentBackupFormSession(c, source)
	}
	if contentType != "" && !strings.Contains(contentType, "json") {
		return contentbackup.MissingSession(contentbackup.SessionMissingReasonAbsent)
	}
	if truncated {
		return contentbackup.MissingSession(contentbackup.SessionMissingReasonUnparseable)
	}
	if len(requestChunks) == 0 {
		return contentbackup.ResolveSession(source, nil)
	}
	if len(requestChunks) != 1 {
		return contentbackup.MissingSession(contentbackup.SessionMissingReasonUnparseable)
	}
	body := requestChunks[0]
	if len(bytes.TrimSpace(body)) == 0 {
		return contentbackup.ResolveSession(source, nil)
	}
	if !gjson.ValidBytes(body) {
		return contentbackup.MissingSession(contentbackup.SessionMissingReasonUnparseable)
	}
	path, ok := contentBackupSessionJSONPaths[source]
	if !ok {
		return contentbackup.MissingSession(contentbackup.SessionMissingReasonAbsent)
	}
	result := gjson.GetBytes(body, path)
	if !result.Exists() {
		return contentbackup.ResolveSession(source, nil)
	}
	// gjson v1.18 exposes Value as a method: passing the method value itself
	// would make every session look like not_string.
	return contentbackup.ResolveSession(source, result.Value())
}

// contentBackupFormSession only reads an already parsed form; it never calls
// ParseMultipartForm, which would re-parse uploaded files just to find "user".
func contentBackupFormSession(c *gin.Context, source string) contentbackup.SessionRef {
	if c != nil && c.Request != nil {
		if form := c.Request.MultipartForm; form != nil {
			if values, ok := form.Value["user"]; ok && len(values) > 0 {
				return contentbackup.ResolveSession(source, values[0])
			}
		}
		if values, ok := c.Request.PostForm["user"]; ok && len(values) > 0 {
			return contentbackup.ResolveSession(source, values[0])
		}
	}
	return contentbackup.ResolveSession(source, nil)
}

// ContentBackupHandoff transfers ownership to the injected non blocking
// enqueuer. It returns true only when the handoff worker now owns the capture
// and must call Release exactly once; on every other path it releases here.
func ContentBackupHandoff(capture *contentbackup.Capture) bool {
	if capture == nil {
		return false
	}
	hooks := contentBackupHooksSnapshot()
	if hooks.Enqueuer == nil {
		contentBackupCounters.enqueueRejected.Add(1)
		if capture.Release != nil {
			capture.Release()
		}
		return false
	}
	if contentBackupSafeEnqueue(hooks.Enqueuer, capture) {
		contentBackupCounters.handedOff.Add(1)
		return true
	}
	contentBackupCounters.enqueueRejected.Add(1)
	if capture.Release != nil {
		capture.Release()
	}
	return false
}

var contentBackupLastLog atomic.Int64

// contentBackupLogFailure rate limits itself and never carries body bytes,
// credentials or raw session values.
func contentBackupLogFailure(format string, args ...any) {
	now := time.Now().Unix()
	last := contentBackupLastLog.Load()
	if now-last < contentBackupLogIntervalSecs {
		return
	}
	if !contentBackupLastLog.CompareAndSwap(last, now) {
		return
	}
	common.SysError("[content-backup] " + fmt.Sprintf(format, args...))
}
