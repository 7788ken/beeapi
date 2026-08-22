package main

import (
	"context"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

type fakeKeepAliveController struct {
	mu      sync.Mutex
	enabled []bool
	changed chan struct{}
}

func (f *fakeKeepAliveController) SetKeepAlivesEnabled(enabled bool) {
	f.mu.Lock()
	f.enabled = append(f.enabled, enabled)
	f.mu.Unlock()
	select {
	case f.changed <- struct{}{}:
	default:
	}
}

func TestRunKeepAliveDrainSignalDisablesKeepAliveWithoutCancelingContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	controller := &fakeKeepAliveController{changed: make(chan struct{}, 1)}
	drained := make(chan struct{}, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		runKeepAliveDrainSignal(ctx, controller, signals, func() {
			drained <- struct{}{}
		})
	}()

	signals <- syscall.SIGUSR1
	select {
	case <-controller.changed:
	case <-time.After(time.Second):
		t.Fatal("keep-alive drain signal was not handled")
	}
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("drain callback was not invoked")
	}

	controller.mu.Lock()
	if len(controller.enabled) != 1 || controller.enabled[0] {
		t.Fatalf("SetKeepAlivesEnabled calls = %v, want [false]", controller.enabled)
	}
	controller.mu.Unlock()

	select {
	case <-ctx.Done():
		t.Fatal("SIGUSR1 canceled the process context")
	default:
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("signal watcher did not stop after context cancellation")
	}
}
