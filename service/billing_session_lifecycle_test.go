package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/pkg/billinglifecycle"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

type blockingRefundFunding struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (f *blockingRefundFunding) Source() string       { return BillingSourceWallet }
func (f *blockingRefundFunding) PreConsume(int) error { return nil }
func (f *blockingRefundFunding) Settle(int) error     { return nil }
func (f *blockingRefundFunding) Refund() error {
	f.calls.Add(1)
	close(f.started)
	<-f.release
	return nil
}

func billingLifecycleTestContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), time.Second)
}

func waitBillingLifecycleSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	ctx, cancel := billingLifecycleTestContext(t)
	defer cancel()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatal(message)
	}
}

func TestBillingSessionRefundIsTrackedUntilFinancialActionCompletes(t *testing.T) {
	coordinator := billinglifecycle.NewCoordinator()
	previousReserveBillingRoot := reserveBillingRoot
	reserveBillingRoot = coordinator.ReserveRoot
	t.Cleanup(func() {
		reserveBillingRoot = previousReserveBillingRoot
	})

	funding := &blockingRefundFunding{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{
			UserId:       123,
			IsPlayground: true,
		},
		funding:       funding,
		tokenConsumed: 1,
	}
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())

	session.Refund(ginContext)
	waitBillingLifecycleSignal(t, funding.started, "refund financial action did not start")

	sentinel, err := coordinator.BeginDrain()
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := coordinator.WaitOnly(canceled, sentinel); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitOnly() while refund is blocked error = %v, want context.Canceled", err)
	}

	close(funding.release)
	waitCtx, cancelWait := billingLifecycleTestContext(t)
	defer cancelWait()
	if err := coordinator.WaitOnly(waitCtx, sentinel); err != nil {
		t.Fatalf("WaitOnly() after refund completion error = %v", err)
	}
	if got := funding.calls.Load(); got != 1 {
		t.Fatalf("funding Refund() calls = %d, want 1", got)
	}

	session.Refund(ginContext)
	if got := funding.calls.Load(); got != 1 {
		t.Fatalf("second BillingSession.Refund triggered funding Refund; calls = %d", got)
	}

	closeCtx, cancelClose := billingLifecycleTestContext(t)
	defer cancelClose()
	if err := coordinator.CloseAdmissionAndWait(closeCtx, sentinel); err != nil {
		t.Fatalf("CloseAdmissionAndWait() error = %v", err)
	}
}

func TestBillingSessionRefundReserveFailureDoesNotChangeRefundedState(t *testing.T) {
	coordinator := billinglifecycle.NewCoordinator()
	previousReserveBillingRoot := reserveBillingRoot
	reserveBillingRoot = coordinator.ReserveRoot
	t.Cleanup(func() {
		reserveBillingRoot = previousReserveBillingRoot
	})

	sentinel, err := coordinator.BeginDrain()
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}

	funding := &blockingRefundFunding{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{
			UserId:       456,
			IsPlayground: true,
		},
		funding:       funding,
		tokenConsumed: 1,
	}
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())

	session.Refund(ginContext)

	if got := funding.calls.Load(); got != 0 {
		t.Fatalf("funding Refund() calls after root admission closed = %d, want 0", got)
	}
	if !session.NeedsRefund() {
		t.Fatal("refund with failed reservation was marked complete; want retryable refund state")
	}

	closeCtx, cancelClose := billingLifecycleTestContext(t)
	defer cancelClose()
	if err := coordinator.CloseAdmissionAndWait(closeCtx, sentinel); err != nil {
		t.Fatalf("CloseAdmissionAndWait() error = %v", err)
	}
}
