package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/pkg/billinglifecycle"
	"github.com/QuantumNous/new-api/pkg/httplifecycle"
	"github.com/QuantumNous/new-api/service"
)

const (
	shutdownHTTPGrace       = 20 * time.Second
	shutdownHTTPForce       = 5 * time.Second
	shutdownWorkTimeout     = 90 * time.Second
	shutdownResourceTimeout = 10 * time.Second
	shutdownBatchRetry      = time.Second

	// shutdownUpperBound is kept below every Compose stop_grace_period so
	// Docker does not send SIGKILL while the application is still draining.
	shutdownUpperBound = shutdownHTTPGrace + shutdownHTTPForce + shutdownWorkTimeout + shutdownResourceTimeout
)

var errHTTPDrainIncomplete = errors.New("HTTP handlers did not drain")

type shutdownConfig struct {
	httpGrace       time.Duration
	httpForce       time.Duration
	workTimeout     time.Duration
	resourceTimeout time.Duration
}

func defaultShutdownConfig() shutdownConfig {
	return shutdownConfig{
		httpGrace:       shutdownHTTPGrace,
		httpForce:       shutdownHTTPForce,
		workTimeout:     shutdownWorkTimeout,
		resourceTimeout: shutdownResourceTimeout,
	}
}

type shutdownHTTPServer interface {
	Shutdown(context.Context) error
	Close() error
}

type shutdownHooks struct {
	beginHTTPDrain       func()
	cancelLongLivedHTTP  func()
	waitHTTPIdle         func(context.Context) error
	beginBillingDrain    func() (*billinglifecycle.Ticket, error)
	stopBillingProducers func(context.Context) error
	waitBillingOnly      func(context.Context, *billinglifecycle.Ticket) error
	stopBatchUpdater     func(context.Context, *billinglifecycle.Ticket) error
	closeBilling         func(context.Context, *billinglifecycle.Ticket) error
	stopDumpCleaner      func(context.Context) error
	stopSensitiveAudit   func(context.Context) error
	stopBackground       func(context.Context) error
	closeResources       func() error
}

func productionShutdownHooks() shutdownHooks {
	return shutdownHooks{
		beginHTTPDrain:       httplifecycle.BeginDrain,
		cancelLongLivedHTTP:  httplifecycle.CancelLongLived,
		waitHTTPIdle:         httplifecycle.WaitIdle,
		beginBillingDrain:    billinglifecycle.BeginDrain,
		stopBillingProducers: billinglifecycle.StopProducers,
		waitBillingOnly:      billinglifecycle.WaitOnly,
		stopBatchUpdater: func(ctx context.Context, sentinel *billinglifecycle.Ticket) error {
			return retryUntilContext(ctx, shutdownBatchRetry, func(ctx context.Context) error {
				return model.StopBatchUpdater(billinglifecycle.ContextWithParent(ctx, sentinel))
			})
		},
		closeBilling:       billinglifecycle.CloseAdmissionAndWait,
		stopDumpCleaner:    service.StopSensitiveDumpCleaner,
		stopSensitiveAudit: service.StopSensitiveAudit,
		stopBackground:     backgroundtask.Stop,
		closeResources:     closeApplicationResources,
	}
}

func retryUntilContext(ctx context.Context, interval time.Duration, run func(context.Context) error) error {
	if ctx == nil {
		return errors.New("retry context is nil")
	}
	if interval <= 0 {
		return errors.New("retry interval must be positive")
	}
	if run == nil {
		return errors.New("retry function is nil")
	}

	var lastErr error
	for {
		if err := run(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if ctx.Err() != nil {
			return errors.Join(lastErr, ctx.Err())
		}
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(lastErr, ctx.Err())
		}
	}
}

func runGracefulShutdown(
	cfg shutdownConfig,
	appServer shutdownHTTPServer,
	pprofServer *http.Server,
	hooks shutdownHooks,
) error {
	var shutdownErrors []error
	if err := drainHTTP(cfg, appServer, pprofServer, hooks); err != nil {
		shutdownErrors = append(shutdownErrors, err)
		if errors.Is(err, errHTTPDrainIncomplete) {
			return errors.Join(shutdownErrors...)
		}
	}

	workCtx, cancelWork := context.WithTimeout(context.Background(), cfg.workTimeout)
	defer cancelWork()

	sentinel, err := hooks.beginBillingDrain()
	if err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("begin billing drain: %w", err))
		return errors.Join(shutdownErrors...)
	}

	workSteps := []struct {
		name string
		run  func(context.Context) error
	}{
		{name: "billing producers", run: hooks.stopBillingProducers},
		{name: "billing roots", run: func(ctx context.Context) error {
			return hooks.waitBillingOnly(ctx, sentinel)
		}},
		{name: "batch final flush", run: func(ctx context.Context) error {
			return hooks.stopBatchUpdater(ctx, sentinel)
		}},
		{name: "billing child admission", run: func(ctx context.Context) error {
			return hooks.closeBilling(ctx, sentinel)
		}},
		{name: "sensitive dump cleaner", run: hooks.stopDumpCleaner},
		{name: "sensitive audit", run: hooks.stopSensitiveAudit},
		{name: "background tasks", run: hooks.stopBackground},
	}
	workDrainFailed := false
	for _, step := range workSteps {
		if err := step.run(workCtx); err != nil {
			workDrainFailed = true
			shutdownErrors = append(shutdownErrors, fmt.Errorf("drain %s: %w", step.name, err))
		}
	}

	if workCtx.Err() != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("application work drain timed out: %w", workCtx.Err()))
		return errors.Join(shutdownErrors...)
	}
	if workDrainFailed {
		return errors.Join(shutdownErrors...)
	}

	resourceCtx, cancelResources := context.WithTimeout(context.Background(), cfg.resourceTimeout)
	defer cancelResources()
	resourceResult := make(chan error, 1)
	go func() {
		resourceResult <- hooks.closeResources()
	}()
	select {
	case err := <-resourceResult:
		if err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
	case <-resourceCtx.Done():
		shutdownErrors = append(shutdownErrors, fmt.Errorf("resource close timed out: %w", resourceCtx.Err()))
	}
	return errors.Join(shutdownErrors...)
}

func drainHTTP(
	cfg shutdownConfig,
	appServer shutdownHTTPServer,
	pprofServer *http.Server,
	hooks shutdownHooks,
) error {
	hooks.beginHTTPDrain()

	var shutdownErrors []error
	graceCtx, cancelGrace := context.WithTimeout(context.Background(), cfg.httpGrace)
	defer cancelGrace()

	pprofResult := make(chan error, 1)
	if pprofServer != nil {
		go func() {
			pprofResult <- pprofServer.Shutdown(graceCtx)
		}()
	}

	serverErr := appServer.Shutdown(graceCtx)
	idleErr := hooks.waitHTTPIdle(graceCtx)
	if serverErr == nil && idleErr == nil {
		if err := finishPprofShutdown(pprofServer, pprofResult, true); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
		return errors.Join(shutdownErrors...)
	}

	if serverErr != nil &&
		!errors.Is(serverErr, context.DeadlineExceeded) &&
		!errors.Is(serverErr, context.Canceled) &&
		!errors.Is(serverErr, http.ErrServerClosed) {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("graceful HTTP shutdown: %w", serverErr))
	}
	if idleErr != nil &&
		!errors.Is(idleErr, context.DeadlineExceeded) &&
		!errors.Is(idleErr, context.Canceled) {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("wait HTTP idle: %w", idleErr))
	}

	hooks.cancelLongLivedHTTP()
	if closeErr := appServer.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("force close HTTP server: %w", closeErr))
	}
	forceCtx, cancelForce := context.WithTimeout(context.Background(), cfg.httpForce)
	defer cancelForce()
	if err := hooks.waitHTTPIdle(forceCtx); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("%w: %v", errHTTPDrainIncomplete, err))
	}

	if err := finishPprofShutdown(pprofServer, pprofResult, false); err != nil {
		shutdownErrors = append(shutdownErrors, err)
	}
	return errors.Join(shutdownErrors...)
}

func finishPprofShutdown(server *http.Server, result <-chan error, wait bool) error {
	if server == nil {
		return nil
	}

	if !wait {
		select {
		case err := <-result:
			return finishPprofShutdownResult(server, err)
		default:
			return forceClosePprof(server)
		}
	}
	return finishPprofShutdownResult(server, <-result)
}

func finishPprofShutdownResult(server *http.Server, shutdownErr error) error {
	if shutdownErr == nil || errors.Is(shutdownErr, http.ErrServerClosed) {
		return nil
	}
	if errors.Is(shutdownErr, context.DeadlineExceeded) || errors.Is(shutdownErr, context.Canceled) {
		return forceClosePprof(server)
	}
	return errors.Join(
		fmt.Errorf("shutdown pprof server: %w", shutdownErr),
		forceClosePprof(server),
	)
}

func forceClosePprof(server *http.Server) error {
	if err := server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("force close pprof server: %w", err)
	}
	return nil
}
