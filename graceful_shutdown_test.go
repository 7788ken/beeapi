package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/pkg/billinglifecycle"
	"github.com/QuantumNous/new-api/pkg/httplifecycle"
	"github.com/gin-gonic/gin"
)

type fakeShutdownServer struct {
	shutdown func(context.Context) error
	close    func() error
}

func (s fakeShutdownServer) Shutdown(ctx context.Context) error {
	return s.shutdown(ctx)
}

func (s fakeShutdownServer) Close() error {
	if s.close == nil {
		return nil
	}
	return s.close()
}

func TestRunGracefulShutdownOrdersFinancialAndResourceDrain(t *testing.T) {
	var mu sync.Mutex
	var order []string
	record := func(name string) {
		mu.Lock()
		order = append(order, name)
		mu.Unlock()
	}

	coordinator := billinglifecycle.NewCoordinator()
	hooks := shutdownHooks{
		beginHTTPDrain:      func() { record("http-admission") },
		cancelLongLivedHTTP: func() { record("http-long-lived") },
		waitHTTPIdle: func(context.Context) error {
			record("http-idle")
			return nil
		},
		beginBillingDrain: func() (*billinglifecycle.Ticket, error) {
			record("billing-begin")
			return coordinator.BeginDrain()
		},
		stopBillingProducers: func(ctx context.Context) error {
			record("billing-producers")
			return coordinator.StopProducers(ctx)
		},
		waitBillingOnly: func(ctx context.Context, sentinel *billinglifecycle.Ticket) error {
			record("billing-roots")
			return coordinator.WaitOnly(ctx, sentinel)
		},
		stopBatchUpdater: func(ctx context.Context, sentinel *billinglifecycle.Ticket) error {
			record("batch")
			child, err := sentinel.ReserveChild("test-final-flush")
			if err != nil {
				return err
			}
			return child.Release()
		},
		closeBilling: func(ctx context.Context, sentinel *billinglifecycle.Ticket) error {
			record("billing-close")
			return coordinator.CloseAdmissionAndWait(ctx, sentinel)
		},
		stopDumpCleaner: func(context.Context) error {
			record("dump-cleaner")
			return nil
		},
		stopSensitiveAudit: func(context.Context) error {
			record("audit")
			return nil
		},
		stopBackground: func(context.Context) error {
			record("background")
			return nil
		},
		closeResources: func() error {
			record("resources")
			return nil
		},
	}
	server := fakeShutdownServer{
		shutdown: func(context.Context) error {
			record("http-shutdown")
			return nil
		},
	}
	cfg := shutdownConfig{
		httpGrace:       time.Second,
		httpForce:       time.Second,
		workTimeout:     time.Second,
		resourceTimeout: time.Second,
	}

	if err := runGracefulShutdown(cfg, server, nil, hooks); err != nil {
		t.Fatalf("runGracefulShutdown() error = %v", err)
	}
	want := []string{
		"http-admission",
		"http-shutdown",
		"http-idle",
		"billing-begin",
		"billing-producers",
		"billing-roots",
		"batch",
		"billing-close",
		"dump-cleaner",
		"audit",
		"background",
		"resources",
	}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q; full order=%v", i, order[i], want[i], order)
		}
	}
}

func TestRetryUntilContextRetriesFinalFlush(t *testing.T) {
	attempts := 0
	err := retryUntilContext(context.Background(), time.Millisecond, func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary flush failure")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retryUntilContext() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("retry attempts = %d, want 3", attempts)
	}
}

func TestDrainHTTPCancelsLongLivedAfterGrace(t *testing.T) {
	var mu sync.Mutex
	longLivedCanceled := false
	hooks := shutdownHooks{
		beginHTTPDrain: func() {},
		cancelLongLivedHTTP: func() {
			mu.Lock()
			longLivedCanceled = true
			mu.Unlock()
		},
		waitHTTPIdle: func(ctx context.Context) error {
			mu.Lock()
			canceled := longLivedCanceled
			mu.Unlock()
			if canceled {
				return nil
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}
	server := fakeShutdownServer{
		shutdown: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		close: func() error { return nil },
	}
	cfg := shutdownConfig{
		httpGrace: 10 * time.Millisecond,
		httpForce: time.Second,
	}
	if err := drainHTTP(cfg, server, nil, hooks); err != nil {
		t.Fatalf("drainHTTP() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !longLivedCanceled {
		t.Fatal("long-lived HTTP was not canceled after grace timeout")
	}
}

func TestDrainHTTPWaitsForHandlersAfterForceClose(t *testing.T) {
	connectionClosed := make(chan struct{})
	var waitCalls int
	hooks := shutdownHooks{
		beginHTTPDrain:      func() {},
		cancelLongLivedHTTP: func() {},
		waitHTTPIdle: func(ctx context.Context) error {
			waitCalls++
			if waitCalls == 1 {
				<-ctx.Done()
				return ctx.Err()
			}
			select {
			case <-connectionClosed:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	server := fakeShutdownServer{
		shutdown: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		close: func() error {
			close(connectionClosed)
			return nil
		},
	}
	cfg := shutdownConfig{
		httpGrace: 5 * time.Millisecond,
		httpForce: time.Second,
	}

	if err := drainHTTP(cfg, server, nil, hooks); err != nil {
		t.Fatalf("drainHTTP() error = %v", err)
	}
}

func TestDrainHTTPForceCloseCancelsRealShortRequestAndWaitsForHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := httplifecycle.NewManager()
	engine := gin.New()
	engine.Use(manager.Middleware())
	requestStarted := make(chan struct{})
	handlerDone := make(chan struct{})
	engine.GET("/slow", func(c *gin.Context) {
		close(requestStarted)
		<-c.Request.Context().Done()
		close(handlerDone)
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{Handler: engine}
	go func() {
		_ = server.Serve(listener)
	}()
	clientDone := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String() + "/slow")
		if response != nil {
			_ = response.Body.Close()
		}
		clientDone <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("slow request did not start")
	}

	hooks := shutdownHooks{
		beginHTTPDrain:      manager.BeginDrain,
		cancelLongLivedHTTP: manager.CancelLongLived,
		waitHTTPIdle:        manager.WaitIdle,
	}
	cfg := shutdownConfig{
		httpGrace: 10 * time.Millisecond,
		httpForce: time.Second,
	}
	if err := drainHTTP(cfg, server, nil, hooks); err != nil {
		t.Fatalf("drainHTTP() error = %v", err)
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("short handler remained after server force close")
	}
	select {
	case <-clientDone:
	case <-time.After(time.Second):
		t.Fatal("client remained blocked after server force close")
	}
}

func TestDrainHTTPSkipsDisabledPprofServer(t *testing.T) {
	hooks := shutdownHooks{
		beginHTTPDrain:      func() {},
		cancelLongLivedHTTP: func() {},
		waitHTTPIdle:        func(context.Context) error { return nil },
	}
	server := fakeShutdownServer{
		shutdown: func(context.Context) error { return nil },
	}
	cfg := shutdownConfig{
		httpGrace: time.Second,
		httpForce: time.Second,
	}

	if err := drainHTTP(cfg, server, nil, hooks); err != nil {
		t.Fatalf("drainHTTP() error = %v", err)
	}
}

func TestDrainHTTPForceClosesActivePprofRequestAfterGrace(t *testing.T) {
	requestStarted := make(chan struct{})
	handlerDone := make(chan struct{})
	testServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(handlerDone)
	}))
	testServer.Start()
	defer testServer.Close()

	conn, err := net.Dial("tcp", testServer.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial pprof test server: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /debug/pprof/ HTTP/1.1\r\nHost: test\r\n\r\n")); err != nil {
		t.Fatalf("write pprof request: %v", err)
	}
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("pprof request did not start")
	}

	hooks := shutdownHooks{
		beginHTTPDrain:      func() {},
		cancelLongLivedHTTP: func() {},
		waitHTTPIdle:        func(context.Context) error { return nil },
	}
	appServer := fakeShutdownServer{
		shutdown: func(context.Context) error { return nil },
	}
	cfg := shutdownConfig{
		httpGrace: 10 * time.Millisecond,
		httpForce: time.Second,
	}

	if err := drainHTTP(cfg, appServer, testServer.Config, hooks); err != nil {
		t.Fatalf("drainHTTP() error = %v", err)
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("active pprof handler remained after force close")
	}
}

func TestRunGracefulShutdownSkipsResourceCloseWhenWorkCannotDrain(t *testing.T) {
	coordinator := billinglifecycle.NewCoordinator()
	resourceClosed := false
	hooks := shutdownHooks{
		beginHTTPDrain:       func() {},
		cancelLongLivedHTTP:  func() {},
		waitHTTPIdle:         func(context.Context) error { return nil },
		beginBillingDrain:    coordinator.BeginDrain,
		stopBillingProducers: coordinator.StopProducers,
		waitBillingOnly: func(ctx context.Context, _ *billinglifecycle.Ticket) error {
			<-ctx.Done()
			return ctx.Err()
		},
		stopBatchUpdater:   func(ctx context.Context, _ *billinglifecycle.Ticket) error { return ctx.Err() },
		closeBilling:       func(ctx context.Context, _ *billinglifecycle.Ticket) error { return ctx.Err() },
		stopDumpCleaner:    func(ctx context.Context) error { return ctx.Err() },
		stopSensitiveAudit: func(ctx context.Context) error { return ctx.Err() },
		stopBackground:     func(ctx context.Context) error { return ctx.Err() },
		closeResources: func() error {
			resourceClosed = true
			return nil
		},
	}
	server := fakeShutdownServer{shutdown: func(context.Context) error { return nil }}
	cfg := shutdownConfig{
		httpGrace:       time.Second,
		httpForce:       time.Second,
		workTimeout:     10 * time.Millisecond,
		resourceTimeout: time.Second,
	}
	err := runGracefulShutdown(cfg, server, nil, hooks)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runGracefulShutdown() error = %v, want deadline exceeded", err)
	}
	if resourceClosed {
		t.Fatal("resources closed while lifecycle work was still active")
	}
}

func TestRunGracefulShutdownSkipsResourceCloseWhenFinalFlushFails(t *testing.T) {
	expected := errors.New("final flush failed")
	coordinator := billinglifecycle.NewCoordinator()
	resourceClosed := false
	hooks := shutdownHooks{
		beginHTTPDrain:       func() {},
		cancelLongLivedHTTP:  func() {},
		waitHTTPIdle:         func(context.Context) error { return nil },
		beginBillingDrain:    coordinator.BeginDrain,
		stopBillingProducers: coordinator.StopProducers,
		waitBillingOnly:      coordinator.WaitOnly,
		stopBatchUpdater: func(context.Context, *billinglifecycle.Ticket) error {
			return expected
		},
		closeBilling:       coordinator.CloseAdmissionAndWait,
		stopDumpCleaner:    func(context.Context) error { return nil },
		stopSensitiveAudit: func(context.Context) error { return nil },
		stopBackground:     func(context.Context) error { return nil },
		closeResources: func() error {
			resourceClosed = true
			return nil
		},
	}
	server := fakeShutdownServer{shutdown: func(context.Context) error { return nil }}
	cfg := shutdownConfig{
		httpGrace:       time.Second,
		httpForce:       time.Second,
		workTimeout:     time.Second,
		resourceTimeout: time.Second,
	}

	err := runGracefulShutdown(cfg, server, nil, hooks)
	if !errors.Is(err, expected) {
		t.Fatalf("runGracefulShutdown() error = %v, want final flush error", err)
	}
	if resourceClosed {
		t.Fatal("resources closed after final flush failure")
	}
}

func TestRunGracefulShutdownDoesNotCloseDataStoresWhileHTTPHandlerRemains(t *testing.T) {
	billingStarted := false
	resourceClosed := false
	hooks := shutdownHooks{
		beginHTTPDrain:      func() {},
		cancelLongLivedHTTP: func() {},
		waitHTTPIdle: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		beginBillingDrain: func() (*billinglifecycle.Ticket, error) {
			billingStarted = true
			return nil, nil
		},
		closeResources: func() error {
			resourceClosed = true
			return nil
		},
	}
	server := fakeShutdownServer{
		shutdown: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		close: func() error { return nil },
	}
	cfg := shutdownConfig{
		httpGrace:       5 * time.Millisecond,
		httpForce:       5 * time.Millisecond,
		workTimeout:     time.Second,
		resourceTimeout: time.Second,
	}
	err := runGracefulShutdown(cfg, server, nil, hooks)
	if !errors.Is(err, errHTTPDrainIncomplete) {
		t.Fatalf("runGracefulShutdown() error = %v, want errHTTPDrainIncomplete", err)
	}
	if billingStarted {
		t.Fatal("billing drain started while an HTTP handler remained active")
	}
	if resourceClosed {
		t.Fatal("resources closed while an HTTP handler remained active")
	}
}

func TestComposeGraceExceedsApplicationShutdownUpperBound(t *testing.T) {
	if shutdownUpperBound >= 150*time.Second {
		t.Fatalf("shutdown upper bound = %s, must be below Compose 150s grace", shutdownUpperBound)
	}
}
