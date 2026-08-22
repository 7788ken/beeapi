package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newSensitiveAuditPipelineForTest(
	t *testing.T,
	queueCapacity int,
	write func(SensitiveAuditJob, []byte) (string, error),
	process func(SensitiveAuditJob) error,
) *SensitiveAuditPipeline {
	t.Helper()
	pipeline := NewSensitiveAuditPipeline(queueCapacity)
	pipeline.asyncEnabled = func() bool { return true }
	pipeline.dumpEnabled = func() bool { return true }
	pipeline.writeDump = write
	pipeline.process = process
	if err := pipeline.Start(1); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return pipeline
}

func waitSensitiveSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func stopSensitivePipeline(t *testing.T, pipeline *SensitiveAuditPipeline) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pipeline.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestSensitiveAuditStopWaitsAcceptedWriterAndCanRetryAfterTimeout(t *testing.T) {
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	processed := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseWrite) })
	})
	pipeline := newSensitiveAuditPipelineForTest(
		t,
		1,
		func(SensitiveAuditJob, []byte) (string, error) {
			close(writeStarted)
			<-releaseWrite
			return "accepted.dump", nil
		},
		func(SensitiveAuditJob) error {
			close(processed)
			return nil
		},
	)

	if err := pipeline.Submit(SensitiveAuditJob{RequestID: "accepted"}, []byte(`{"prompt":"test"}`)); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	waitSensitiveSignal(t, writeStarted, "accepted writer did not start")

	timeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err := pipeline.Stop(timeoutCtx)
	cancelTimeout()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Stop() error = %v, want DeadlineExceeded", err)
	}
	if err := pipeline.Submit(SensitiveAuditJob{RequestID: "late"}, []byte(`{}`)); !errors.Is(err, ErrSensitiveAuditNotAccepting) {
		t.Fatalf("late Submit() error = %v, want ErrSensitiveAuditNotAccepting", err)
	}

	releaseOnce.Do(func() { close(releaseWrite) })
	stopSensitivePipeline(t, pipeline)
	waitSensitiveSignal(t, processed, "accepted job was not processed")

	enqueued, processedCount, dropped, failed := pipeline.Stats()
	if enqueued != 1 || processedCount != 1 || dropped != 0 || failed != 0 {
		t.Fatalf("stats = (%d, %d, %d, %d), want (1, 1, 0, 0)", enqueued, processedCount, dropped, failed)
	}
	for i := 0; i < 100; i++ {
		if err := pipeline.Submit(SensitiveAuditJob{RequestID: "late"}, []byte(`{}`)); !errors.Is(err, ErrSensitiveAuditNotAccepting) {
			t.Fatalf("late Submit() #%d error = %v, want ErrSensitiveAuditNotAccepting", i, err)
		}
	}
}

func TestSensitiveAuditStopDrainsBufferedJobs(t *testing.T) {
	var (
		mu       sync.Mutex
		finished = make(map[string]bool)
	)
	pipeline := newSensitiveAuditPipelineForTest(
		t,
		8,
		func(job SensitiveAuditJob, _ []byte) (string, error) {
			return job.RequestID + ".dump", nil
		},
		func(job SensitiveAuditJob) error {
			mu.Lock()
			finished[job.RequestID] = true
			mu.Unlock()
			return nil
		},
	)

	for _, requestID := range []string{"one", "two", "three", "four"} {
		if err := pipeline.Submit(SensitiveAuditJob{RequestID: requestID}, []byte(`{"body":"x"}`)); err != nil {
			t.Fatalf("Submit(%q) error = %v", requestID, err)
		}
	}
	stopSensitivePipeline(t, pipeline)

	enqueued, processed, dropped, failed := pipeline.Stats()
	if enqueued != 4 || processed != 4 || dropped != 0 || failed != 0 {
		t.Fatalf("stats = (%d, %d, %d, %d), want (4, 4, 0, 0)", enqueued, processed, dropped, failed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(finished) != 4 {
		t.Fatalf("finished jobs = %v, want four jobs", finished)
	}
}

func TestSensitiveAuditQueueFullRemovesNewDump(t *testing.T) {
	dumpRoot := t.TempDir()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseFirst) })
	})
	pipeline := newSensitiveAuditPipelineForTest(
		t,
		1,
		func(job SensitiveAuditJob, body []byte) (string, error) {
			path := filepath.Join(dumpRoot, job.RequestID+".dump")
			return path, os.WriteFile(path, body, 0o600)
		},
		func(job SensitiveAuditJob) error {
			if job.RequestID == "first" {
				close(firstStarted)
				<-releaseFirst
			}
			return nil
		},
	)

	if err := pipeline.Submit(SensitiveAuditJob{RequestID: "first"}, []byte(`1`)); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	pipeline.producers.Wait()
	waitSensitiveSignal(t, firstStarted, "first job did not start processing")

	if err := pipeline.Submit(SensitiveAuditJob{RequestID: "second"}, []byte(`2`)); err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}
	pipeline.producers.Wait()
	if depth := pipeline.QueueDepth(); depth != 1 {
		t.Fatalf("queue depth after second job = %d, want 1", depth)
	}

	if err := pipeline.Submit(SensitiveAuditJob{RequestID: "third"}, []byte(`3`)); err != nil {
		t.Fatalf("Submit(third) error = %v", err)
	}
	pipeline.producers.Wait()
	if _, err := os.Stat(filepath.Join(dumpRoot, "third.dump")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("queue-full dump still exists or stat failed: %v", err)
	}

	releaseOnce.Do(func() { close(releaseFirst) })
	stopSensitivePipeline(t, pipeline)
	enqueued, processed, dropped, failed := pipeline.Stats()
	if enqueued != 2 || processed != 2 || dropped != 1 || failed != 0 {
		t.Fatalf("stats = (%d, %d, %d, %d), want (2, 2, 1, 0)", enqueued, processed, dropped, failed)
	}
}

func TestSensitiveAuditStopBeforeStartIsTerminal(t *testing.T) {
	pipeline := NewSensitiveAuditPipeline(1)
	if err := pipeline.Submit(SensitiveAuditJob{RequestID: "before-start"}, []byte(`{}`)); !errors.Is(err, ErrSensitiveAuditNotAccepting) {
		t.Fatalf("Submit() before Start error = %v, want ErrSensitiveAuditNotAccepting", err)
	}
	stopSensitivePipeline(t, pipeline)
	if err := pipeline.Start(1); !errors.Is(err, ErrSensitiveAuditNotAccepting) {
		t.Fatalf("Start() after Stop error = %v, want ErrSensitiveAuditNotAccepting", err)
	}
	if err := pipeline.Stop(context.Background()); err != nil {
		t.Fatalf("repeat Stop() error = %v", err)
	}
}

func TestSensitiveAuditWriterPanicDropsAndDoesNotBlockStop(t *testing.T) {
	pipeline := newSensitiveAuditPipelineForTest(
		t,
		1,
		func(SensitiveAuditJob, []byte) (string, error) {
			panic("forced writer panic")
		},
		func(SensitiveAuditJob) error {
			t.Fatal("writer panic unexpectedly enqueued a job")
			return nil
		},
	)
	if err := pipeline.Submit(SensitiveAuditJob{RequestID: "writer-panic"}, []byte(`{}`)); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	stopSensitivePipeline(t, pipeline)

	enqueued, processed, dropped, failed := pipeline.Stats()
	if enqueued != 0 || processed != 0 || dropped != 1 || failed != 0 {
		t.Fatalf("stats = (%d, %d, %d, %d), want (0, 0, 1, 0)", enqueued, processed, dropped, failed)
	}
}

func TestSensitiveAuditConcurrentSubmitAndMultipleStops(t *testing.T) {
	const (
		stopCount   = 8
		submitCount = 32
	)
	seedWriteStarted := make(chan struct{})
	releaseSeedWrite := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseSeedWrite) })
	})

	pipeline := NewSensitiveAuditPipeline(64)
	pipeline.asyncEnabled = func() bool { return true }
	pipeline.dumpEnabled = func() bool { return true }
	pipeline.writeDump = func(job SensitiveAuditJob, _ []byte) (string, error) {
		if job.RequestID == "seed" {
			close(seedWriteStarted)
			<-releaseSeedWrite
		}
		return job.RequestID + ".dump", nil
	}
	pipeline.process = func(SensitiveAuditJob) error { return nil }
	if err := pipeline.Start(4); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := pipeline.Submit(SensitiveAuditJob{RequestID: "seed"}, []byte(`{}`)); err != nil {
		t.Fatalf("Submit(seed) error = %v", err)
	}
	waitSensitiveSignal(t, seedWriteStarted, "seed writer did not start")

	start := make(chan struct{})
	stopErrors := make(chan error, stopCount)
	var stopWG sync.WaitGroup
	for i := 0; i < stopCount; i++ {
		stopWG.Add(1)
		go func() {
			defer stopWG.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			stopErrors <- pipeline.Stop(ctx)
		}()
	}

	var accepted atomic.Int64
	accepted.Store(1)
	var submitWG sync.WaitGroup
	for i := 0; i < submitCount; i++ {
		submitWG.Add(1)
		go func(index int) {
			defer submitWG.Done()
			<-start
			err := pipeline.Submit(
				SensitiveAuditJob{RequestID: "concurrent-" + string(rune('a'+index))},
				[]byte(`{}`),
			)
			if err == nil {
				accepted.Add(1)
				return
			}
			if !errors.Is(err, ErrSensitiveAuditNotAccepting) {
				t.Errorf("concurrent Submit() error = %v", err)
			}
		}(i)
	}
	close(start)
	submitWG.Wait()

	deadline := time.Now().Add(time.Second)
	for {
		pipeline.mu.Lock()
		state := pipeline.state
		pipeline.mu.Unlock()
		if state == sensitiveLifecycleStopping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pipeline did not enter stopping state")
		}
	}
	if err := pipeline.Submit(SensitiveAuditJob{RequestID: "late"}, []byte(`{}`)); !errors.Is(err, ErrSensitiveAuditNotAccepting) {
		t.Fatalf("late Submit() error = %v, want ErrSensitiveAuditNotAccepting", err)
	}

	releaseOnce.Do(func() { close(releaseSeedWrite) })
	stopWG.Wait()
	close(stopErrors)
	for err := range stopErrors {
		if err != nil {
			t.Fatalf("concurrent Stop() error = %v", err)
		}
	}

	enqueued, processed, dropped, failed := pipeline.Stats()
	if enqueued != accepted.Load() {
		t.Fatalf("enqueued = %d, want accepted %d", enqueued, accepted.Load())
	}
	if dropped != 0 || failed != 0 || processed != enqueued {
		t.Fatalf("stats = (%d, %d, %d, %d), want all accepted jobs processed", enqueued, processed, dropped, failed)
	}
}

func TestSensitiveAuditProcessErrorAndPanicCountAsFailed(t *testing.T) {
	pipeline := newSensitiveAuditPipelineForTest(
		t,
		3,
		func(job SensitiveAuditJob, _ []byte) (string, error) {
			return job.RequestID + ".dump", nil
		},
		func(job SensitiveAuditJob) error {
			switch job.RequestID {
			case "error":
				return errors.New("forced process error")
			case "panic":
				panic("forced process panic")
			default:
				return nil
			}
		},
	)

	for _, requestID := range []string{"success", "error", "panic"} {
		if err := pipeline.Submit(SensitiveAuditJob{RequestID: requestID}, []byte(`{}`)); err != nil {
			t.Fatalf("Submit(%q) error = %v", requestID, err)
		}
	}

	const stopCount = 4
	stopResults := make(chan error, stopCount)
	for i := 0; i < stopCount; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			stopResults <- pipeline.Stop(ctx)
		}()
	}
	wantStopError := "sensitive audit pipeline jobs failed: count=2"
	for i := 0; i < stopCount; i++ {
		err := <-stopResults
		if !errors.Is(err, ErrSensitiveAuditJobsFailed) || err.Error() != wantStopError {
			t.Fatalf("concurrent Stop() error = %v, want %q", err, wantStopError)
		}
	}
	if err := pipeline.Stop(context.Background()); !errors.Is(err, ErrSensitiveAuditJobsFailed) || err.Error() != wantStopError {
		t.Fatalf("repeat Stop() error = %v, want %q", err, wantStopError)
	}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pipeline.Stop(canceledCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() with canceled context error = %v, want context.Canceled", err)
	}

	enqueued, processed, dropped, failed := pipeline.Stats()
	if enqueued != 3 || processed != 1 || dropped != 0 || failed != 2 {
		t.Fatalf("stats = (%d, %d, %d, %d), want (3, 1, 0, 2)", enqueued, processed, dropped, failed)
	}
	if enqueued != processed+failed {
		t.Fatalf("terminal jobs = %d, want enqueued %d", processed+failed, enqueued)
	}
}

func TestSensitiveDumpCleanerStopWaitsCurrentRoundAndCanRetry(t *testing.T) {
	cleanStarted := make(chan struct{})
	releaseClean := make(chan struct{})
	var (
		releaseOnce sync.Once
		runsMu      sync.Mutex
		runs        int
	)
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseClean) })
	})

	cleaner := NewSensitiveDumpCleaner(time.Hour, func() {
		runsMu.Lock()
		runs++
		currentRun := runs
		runsMu.Unlock()
		if currentRun == 1 {
			close(cleanStarted)
			<-releaseClean
		}
	})
	if err := cleaner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitSensitiveSignal(t, cleanStarted, "cleaner round did not start")

	timeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err := cleaner.Stop(timeoutCtx)
	cancelTimeout()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Stop() error = %v, want DeadlineExceeded", err)
	}

	releaseOnce.Do(func() { close(releaseClean) })
	stopCtx, cancelStop := context.WithTimeout(context.Background(), time.Second)
	defer cancelStop()
	if err := cleaner.Stop(stopCtx); err != nil {
		t.Fatalf("retry Stop() error = %v", err)
	}
	runsMu.Lock()
	defer runsMu.Unlock()
	if runs != 1 {
		t.Fatalf("cleaner runs = %d, want 1", runs)
	}
}

func TestSensitiveDumpCleanerImmediateStopBeforeFirstRound(t *testing.T) {
	startGate := make(chan struct{})
	var runs atomic.Int64
	cleaner := NewSensitiveDumpCleaner(time.Hour, func() {
		runs.Add(1)
	})
	cleaner.startGate = startGate
	if err := cleaner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	stopCtx, cancelStop := context.WithTimeout(context.Background(), time.Second)
	defer cancelStop()
	if err := cleaner.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := runs.Load(); got != 0 {
		t.Fatalf("cleaner runs = %d, want 0", got)
	}
}

func TestSensitiveDumpCleanerStopBeforeStartIsTerminal(t *testing.T) {
	cleaner := NewSensitiveDumpCleaner(time.Hour, func() {})
	if err := cleaner.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() before Start error = %v", err)
	}
	if err := cleaner.Start(); err == nil {
		t.Fatal("Start() after Stop succeeded")
	}
	if err := cleaner.Stop(context.Background()); err != nil {
		t.Fatalf("repeat Stop() error = %v", err)
	}
}
