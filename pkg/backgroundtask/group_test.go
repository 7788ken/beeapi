package backgroundtask

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestGroupStopWaitsCurrentRoundAndRejectsLateStart(t *testing.T) {
	group := NewGroup()
	roundStarted := make(chan struct{})
	releaseRound := make(chan struct{})
	var rounds atomic.Int32

	if err := group.Start("periodic", func(ctx context.Context) {
		RunPeriodic(ctx, time.Hour, true, func() {
			rounds.Add(1)
			close(roundStarted)
			<-releaseRound
		})
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	<-roundStarted

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer shortCancel()
	if err := group.Stop(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want deadline exceeded", err)
	}
	if err := group.Start("late", func(context.Context) {}); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatalf("late Start() error = %v, want admission closed", err)
	}

	close(releaseRound)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := group.Stop(waitCtx); err != nil {
		t.Fatalf("retry Stop() error = %v", err)
	}
	if got := rounds.Load(); got != 1 {
		t.Fatalf("rounds = %d, want 1", got)
	}
	if err := group.Submit("late-task", func(context.Context) {}); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatalf("late Submit() error = %v, want admission closed", err)
	}
}

func TestGroupRecoversTaskPanic(t *testing.T) {
	group := NewGroup()
	if err := group.Start("panic", func(context.Context) {
		panic("boom")
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := group.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestGroupStopAllowsAcceptedProducerToSubmitAndWaitsChild(t *testing.T) {
	group := NewGroup()
	producerStarted := make(chan struct{})
	releaseProducer := make(chan struct{})
	childStarted := make(chan struct{})
	releaseChild := make(chan struct{})

	if err := group.Start("producer", func(context.Context) {
		close(producerStarted)
		<-releaseProducer
		if err := group.Submit("child", func(context.Context) {
			close(childStarted)
			<-releaseChild
		}); err != nil {
			t.Errorf("Submit(child) error = %v", err)
		}
	}); err != nil {
		t.Fatalf("Start(producer) error = %v", err)
	}
	<-producerStarted

	stopResult := make(chan error, 1)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	go func() {
		stopResult <- group.Stop(stopCtx)
	}()

	close(releaseProducer)
	<-childStarted
	select {
	case err := <-stopResult:
		t.Fatalf("Stop() returned before child completion: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseChild)
	if err := <-stopResult; err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestGroupClosesTaskAdmissionWhenTimedOutStopLaterDrains(t *testing.T) {
	group := NewGroup()
	taskStarted := make(chan struct{})
	releaseTask := make(chan struct{})
	if err := group.Submit("finite", func(context.Context) {
		close(taskStarted)
		<-releaseTask
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	<-taskStarted

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer shortCancel()
	if err := group.Stop(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want deadline exceeded", err)
	}

	close(releaseTask)
	deadline := time.Now().Add(time.Second)
	for {
		group.mu.Lock()
		admissionOpen := group.taskAdmissionOpen
		group.mu.Unlock()
		if !admissionOpen {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task admission did not close after timed-out Stop drained")
		}
		time.Sleep(time.Millisecond)
	}
	if err := group.Submit("late", func(context.Context) {}); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatalf("late Submit() error = %v, want admission closed", err)
	}
}
