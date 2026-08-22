package backgroundtask

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrAdmissionClosed    = errors.New("background task admission is closed")
	ErrTaskAlreadyStarted = errors.New("background task already started")
)

// Group owns long-lived producers and finite detached work. Stop is retryable:
// it first closes and cancels producers, while finite-task admission remains
// open until accepted work and any work it derives have reached zero.
type Group struct {
	mu                    sync.Mutex
	producerAdmissionOpen bool
	taskAdmissionOpen     bool
	stopping              bool
	producers             map[string]context.CancelFunc
	activeTasks           int
	changed               chan struct{}
}

func NewGroup() *Group {
	return &Group{
		producerAdmissionOpen: true,
		taskAdmissionOpen:     true,
		producers:             make(map[string]context.CancelFunc),
		changed:               make(chan struct{}),
	}
}

func (g *Group) notifyLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}

func (g *Group) Start(name string, task func(context.Context)) error {
	return g.start(name, task, true)
}

func (g *Group) Submit(name string, task func(context.Context)) error {
	return g.start(name, task, false)
}

func (g *Group) start(name string, task func(context.Context), producer bool) error {
	if name == "" {
		return errors.New("background task name is empty")
	}
	if task == nil {
		return fmt.Errorf("background task %q is nil", name)
	}

	g.mu.Lock()
	if producer && !g.producerAdmissionOpen {
		g.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrAdmissionClosed, name)
	}
	if !producer && !g.taskAdmissionOpen {
		g.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrAdmissionClosed, name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if producer {
		if _, exists := g.producers[name]; exists {
			g.mu.Unlock()
			cancel()
			return fmt.Errorf("%w: %s", ErrTaskAlreadyStarted, name)
		}
		g.producers[name] = cancel
	} else {
		g.activeTasks++
	}
	g.notifyLocked()
	g.mu.Unlock()

	go func() {
		defer cancel()
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("background task %q panicked: %v", name, recovered)
			}
			g.mu.Lock()
			if producer {
				delete(g.producers, name)
			} else {
				g.activeTasks--
			}
			if g.stopping && len(g.producers) == 0 && g.activeTasks == 0 {
				g.taskAdmissionOpen = false
			}
			g.notifyLocked()
			g.mu.Unlock()
		}()
		task(ctx)
	}()
	return nil
}

func (g *Group) Stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("background task stop context is nil")
	}

	for {
		g.mu.Lock()
		if g.producerAdmissionOpen {
			g.stopping = true
			g.producerAdmissionOpen = false
			for _, cancel := range g.producers {
				cancel()
			}
			g.notifyLocked()
		}
		if len(g.producers) == 0 && g.activeTasks == 0 {
			g.taskAdmissionOpen = false
			g.mu.Unlock()
			return nil
		}
		changed := g.changed
		g.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// RunPeriodic starts no new round after cancellation. A round already in
// progress is allowed to finish, so Group.Stop can wait for a clean boundary.
func RunPeriodic(ctx context.Context, interval time.Duration, runImmediately bool, round func()) {
	if ctx == nil || interval <= 0 || round == nil {
		return
	}
	if runImmediately {
		select {
		case <-ctx.Done():
			return
		default:
			round()
		}
	}

	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			round()
			timer.Reset(interval)
		}
	}
}

var defaultGroup = NewGroup()
var submittedTaskID atomic.Uint64

func Start(name string, task func(context.Context)) error {
	return defaultGroup.Start(name, task)
}

// Submit registers finite detached work synchronously before starting it.
// The generated suffix allows concurrent work of the same kind.
func Submit(kind string, task func(context.Context)) error {
	id := submittedTaskID.Add(1)
	return defaultGroup.Submit(fmt.Sprintf("%s-%d", kind, id), task)
}

func Stop(ctx context.Context) error {
	return defaultGroup.Stop(ctx)
}
