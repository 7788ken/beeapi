package httplifecycle

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var ErrDraining = errors.New("HTTP server is draining")

type requestStateKey struct{}

type requestState struct {
	manager   *Manager
	id        uint64
	cancel    context.CancelFunc
	streaming bool
	close     func()
}

// Manager owns HTTP admission and tracks requests that net/http cannot drain
// by itself, especially SSE and hijacked WebSocket connections.
type Manager struct {
	mu                sync.Mutex
	ready             bool
	longLivedCanceled bool
	nextID            uint64
	active            map[uint64]*requestState
	changed           chan struct{}
	longLivedDone     chan struct{}
}

func NewManager() *Manager {
	return &Manager{
		ready:         true,
		active:        make(map[uint64]*requestState),
		changed:       make(chan struct{}),
		longLivedDone: make(chan struct{}),
	}
}

func (m *Manager) notifyLocked() {
	close(m.changed)
	m.changed = make(chan struct{})
}

func (m *Manager) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		m.mu.Lock()
		if !m.ready {
			m.mu.Unlock()
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"message": ErrDraining.Error(),
			})
			return
		}

		m.nextID++
		requestCtx, cancel := context.WithCancel(c.Request.Context())
		state := &requestState{
			manager: m,
			id:      m.nextID,
			cancel:  cancel,
		}
		m.active[state.id] = state
		c.Request = c.Request.WithContext(context.WithValue(requestCtx, requestStateKey{}, state))
		m.notifyLocked()
		m.mu.Unlock()

		defer func() {
			cancel()
			m.mu.Lock()
			delete(m.active, state.id)
			m.notifyLocked()
			m.mu.Unlock()
		}()
		c.Next()
	}
}

func (m *Manager) BeginDrain() {
	m.mu.Lock()
	if m.ready {
		m.ready = false
		m.notifyLocked()
	}
	m.mu.Unlock()
}

// CancelLongLived is the bounded fallback after the normal HTTP grace window.
// It cancels SSE request contexts and invokes registered hijacked-connection
// closers outside the manager lock.
func (m *Manager) CancelLongLived() {
	m.mu.Lock()
	if m.longLivedCanceled {
		m.mu.Unlock()
		return
	}
	m.longLivedCanceled = true
	close(m.longLivedDone)
	m.notifyLocked()
	type action struct {
		cancel context.CancelFunc
		close  func()
	}
	actions := make([]action, 0, len(m.active))
	for _, state := range m.active {
		if state.streaming || state.close != nil {
			actions = append(actions, action{cancel: state.cancel, close: state.close})
		}
	}
	m.mu.Unlock()

	for _, action := range actions {
		action.cancel()
		if action.close != nil {
			action.close()
		}
	}
}

func (m *Manager) WaitIdle(ctx context.Context) error {
	if ctx == nil {
		return errors.New("HTTP idle wait context is nil")
	}
	for {
		m.mu.Lock()
		if len(m.active) == 0 {
			m.mu.Unlock()
			return nil
		}
		changed := m.changed
		m.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (m *Manager) IsReady() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ready
}

func stateFromContext(ctx context.Context) *requestState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(requestStateKey{}).(*requestState)
	return state
}

func (m *Manager) markStream(state *requestState) {
	if state == nil || state.manager != m {
		return
	}
	m.mu.Lock()
	state.streaming = true
	shouldCancel := m.longLivedCanceled
	m.notifyLocked()
	m.mu.Unlock()
	if shouldCancel {
		state.cancel()
	}
}

func (m *Manager) registerHijacked(state *requestState, closeFn func()) func() {
	if state == nil || state.manager != m || closeFn == nil {
		return func() {}
	}
	m.mu.Lock()
	state.close = closeFn
	shouldClose := m.longLivedCanceled
	m.notifyLocked()
	m.mu.Unlock()
	if shouldClose {
		state.cancel()
		closeFn()
	}
	return func() {
		m.mu.Lock()
		if current, ok := m.active[state.id]; ok && current == state {
			state.close = nil
			m.notifyLocked()
		}
		m.mu.Unlock()
	}
}

type streamContext struct {
	context.Context
	done <-chan struct{}
}

func (c streamContext) Deadline() (deadline time.Time, ok bool) {
	return time.Time{}, false
}

func (c streamContext) Done() <-chan struct{} {
	return c.done
}

func (c streamContext) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

var defaultManager = NewManager()

func Middleware() gin.HandlerFunc {
	return defaultManager.Middleware()
}

func BeginDrain() {
	defaultManager.BeginDrain()
}

func CancelLongLived() {
	defaultManager.CancelLongLived()
}

func WaitIdle(ctx context.Context) error {
	return defaultManager.WaitIdle(ctx)
}

func MarkStream(ctx context.Context) {
	state := stateFromContext(ctx)
	if state != nil {
		state.manager.markStream(state)
	}
}

// StreamContext preserves request values while intentionally ignoring a
// client disconnect. Unlike context.WithoutCancel, it still cancels when the
// application ends long-lived requests during shutdown.
func StreamContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	state := stateFromContext(ctx)
	if state == nil {
		return context.WithoutCancel(ctx)
	}
	state.manager.markStream(state)
	return streamContext{
		Context: context.WithoutCancel(ctx),
		done:    state.manager.longLivedDone,
	}
}

func RegisterHijacked(ctx context.Context, closeFn func()) func() {
	state := stateFromContext(ctx)
	if state == nil {
		return func() {}
	}
	return state.manager.registerHijacked(state, closeFn)
}
