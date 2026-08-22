package billinglifecycle

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

const coordinatorTestTimeout = time.Second

func testContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), coordinatorTestTimeout)
}

func waitSignal(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	ctx, cancel := testContext(t)
	defer cancel()
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal(message)
	}
}

func waitError(t *testing.T, ch <-chan error, message string) error {
	t.Helper()
	ctx, cancel := testContext(t)
	defer cancel()
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		t.Fatal(message)
		return nil
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestCoordinatorDrainRejectsNewRootsButAllowsExistingRootChildTree(t *testing.T) {
	coordinator := NewCoordinator()
	root, err := coordinator.ReserveRoot("root")
	if err != nil {
		t.Fatalf("ReserveRoot() error = %v", err)
	}

	sentinel, err := coordinator.BeginDrain()
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}
	if _, err := coordinator.ReserveRoot("late-root"); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatalf("ReserveRoot() during drain error = %v, want ErrAdmissionClosed", err)
	}

	child, err := root.ReserveChild("child")
	if err != nil {
		t.Fatalf("ReserveChild() during drain error = %v", err)
	}
	grandchild, err := child.ReserveChild("grandchild")
	if err != nil {
		t.Fatalf("nested ReserveChild() during drain error = %v", err)
	}

	for name, ticket := range map[string]*Ticket{
		"root":       root,
		"child":      child,
		"grandchild": grandchild,
	} {
		if err := ticket.Release(); err != nil {
			t.Fatalf("%s Release() error = %v", name, err)
		}
	}

	waitCtx, cancelWait := testContext(t)
	defer cancelWait()
	if err := coordinator.WaitOnly(waitCtx, sentinel); err != nil {
		t.Fatalf("WaitOnly() error = %v", err)
	}
	if err := sentinel.Submit(func(*Ticket) {}); !errors.Is(err, ErrSentinelTicket) {
		t.Fatalf("sentinel Submit() error = %v, want ErrSentinelTicket", err)
	}
	finalChild, err := sentinel.ReserveChild("final-flush-child")
	if err != nil {
		t.Fatalf("sentinel ReserveChild() error = %v", err)
	}
	if err := coordinator.CloseAdmissionAndWait(canceledContext(), sentinel); !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseAdmissionAndWait() with final child error = %v, want context.Canceled", err)
	}
	if err := finalChild.Release(); err != nil {
		t.Fatalf("final child Release() error = %v", err)
	}
	if err := sentinel.Release(); !errors.Is(err, ErrSentinelTicket) {
		t.Fatalf("sentinel Release() error = %v, want ErrSentinelTicket", err)
	}

	closeCtx, cancelClose := testContext(t)
	defer cancelClose()
	if err := coordinator.CloseAdmissionAndWait(closeCtx, sentinel); err != nil {
		t.Fatalf("CloseAdmissionAndWait() error = %v", err)
	}
	if _, err := root.ReserveChild("after-release"); !errors.Is(err, ErrTicketReleased) {
		t.Fatalf("released root ReserveChild() error = %v, want ErrTicketReleased", err)
	}
	if _, err := coordinator.ReserveRoot("after-close"); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatalf("ReserveRoot() after close error = %v, want ErrAdmissionClosed", err)
	}
}

func TestCoordinatorSubmittedChildDuringDrainDelaysClose(t *testing.T) {
	coordinator := NewCoordinator()
	rootStarted := make(chan struct{})
	submitChild := make(chan struct{})
	childStarted := make(chan struct{})
	releaseChild := make(chan struct{})
	childSubmitResult := make(chan error, 1)

	if err := coordinator.SubmitRoot("root", func(root *Ticket) {
		close(rootStarted)
		<-submitChild
		childSubmitResult <- root.SubmitChild("child", func(*Ticket) {
			close(childStarted)
			<-releaseChild
		})
	}); err != nil {
		t.Fatalf("SubmitRoot() error = %v", err)
	}
	waitSignal(t, rootStarted, "root job did not start")

	sentinel, err := coordinator.BeginDrain()
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}
	close(submitChild)
	if err := waitError(t, childSubmitResult, "child Submit did not return"); err != nil {
		t.Fatalf("SubmitChild() during drain error = %v", err)
	}
	waitSignal(t, childStarted, "child job did not start")

	closeResult := make(chan error, 1)
	closeCtx, cancelClose := testContext(t)
	defer cancelClose()
	go func() {
		closeResult <- coordinator.CloseAdmissionAndWait(closeCtx, sentinel)
	}()
	select {
	case err := <-closeResult:
		t.Fatalf("CloseAdmissionAndWait() returned before child completion: %v", err)
	default:
	}

	close(releaseChild)
	if err := waitError(t, closeResult, "CloseAdmissionAndWait did not finish"); err != nil {
		t.Fatalf("CloseAdmissionAndWait() error = %v", err)
	}
}

func TestReserveFromContextUsesSentinelChildDuringDrain(t *testing.T) {
	coordinator := NewCoordinator()
	sentinel, err := coordinator.BeginDrain()
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}
	if _, err := coordinator.ReserveFromContext(context.Background(), "late-root"); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatalf("ReserveFromContext() without parent error = %v, want ErrAdmissionClosed", err)
	}

	child, err := coordinator.ReserveFromContext(
		ContextWithParent(context.Background(), sentinel),
		"final-flush-inviter-reward",
	)
	if err != nil {
		t.Fatalf("ReserveFromContext() with sentinel error = %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	if err := child.Submit(func(*Ticket) {
		close(started)
		<-release
	}); err != nil {
		t.Fatalf("child Submit() error = %v", err)
	}
	waitSignal(t, started, "sentinel child did not start")

	closeResult := make(chan error, 1)
	closeCtx, cancelClose := testContext(t)
	defer cancelClose()
	go func() {
		closeResult <- coordinator.CloseAdmissionAndWait(closeCtx, sentinel)
	}()
	select {
	case err := <-closeResult:
		t.Fatalf("CloseAdmissionAndWait() returned before sentinel child completion: %v", err)
	default:
	}

	close(release)
	if err := waitError(t, closeResult, "CloseAdmissionAndWait did not wait for sentinel child"); err != nil {
		t.Fatalf("CloseAdmissionAndWait() error = %v", err)
	}
}

func TestCoordinatorWaitAndCloseTimeoutCanRetrySameSentinel(t *testing.T) {
	coordinator := NewCoordinator()
	root, err := coordinator.ReserveRoot("blocked-root")
	if err != nil {
		t.Fatalf("ReserveRoot() error = %v", err)
	}
	sentinel, err := coordinator.BeginDrain()
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}

	if err := coordinator.WaitOnly(canceledContext(), sentinel); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitOnly() canceled error = %v, want context.Canceled", err)
	}
	if err := coordinator.CloseAdmissionAndWait(canceledContext(), sentinel); !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseAdmissionAndWait() canceled error = %v, want context.Canceled", err)
	}

	if err := root.Release(); err != nil {
		t.Fatalf("root Release() error = %v", err)
	}
	waitCtx, cancelWait := testContext(t)
	defer cancelWait()
	if err := coordinator.WaitOnly(waitCtx, sentinel); err != nil {
		t.Fatalf("WaitOnly() retry error = %v", err)
	}
	closeCtx, cancelClose := testContext(t)
	defer cancelClose()
	if err := coordinator.CloseAdmissionAndWait(closeCtx, sentinel); err != nil {
		t.Fatalf("CloseAdmissionAndWait() retry error = %v", err)
	}
}

func TestCoordinatorRejectsReleasedForeignAndMismatchedTickets(t *testing.T) {
	coordinator := NewCoordinator()
	root, err := coordinator.ReserveRoot("root")
	if err != nil {
		t.Fatalf("ReserveRoot() error = %v", err)
	}
	if err := root.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := root.Release(); !errors.Is(err, ErrTicketReleased) {
		t.Fatalf("second Release() error = %v, want ErrTicketReleased", err)
	}
	if _, err := root.ReserveChild("late-child"); !errors.Is(err, ErrTicketReleased) {
		t.Fatalf("released ticket ReserveChild() error = %v, want ErrTicketReleased", err)
	}

	mismatched, err := coordinator.ReserveRoot("not-sentinel")
	if err != nil {
		t.Fatalf("ReserveRoot(not-sentinel) error = %v", err)
	}
	sentinel, err := coordinator.BeginDrain()
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}
	if _, err := coordinator.BeginDrain(); !errors.Is(err, ErrDraining) {
		t.Fatalf("second BeginDrain() error = %v, want ErrDraining", err)
	}
	if err := coordinator.WaitOnly(canceledContext(), mismatched); !errors.Is(err, ErrSentinelMismatch) {
		t.Fatalf("WaitOnly(mismatched) error = %v, want ErrSentinelMismatch", err)
	}

	foreignCoordinator := NewCoordinator()
	foreignSentinel, err := foreignCoordinator.BeginDrain()
	if err != nil {
		t.Fatalf("foreign BeginDrain() error = %v", err)
	}
	if err := coordinator.WaitOnly(canceledContext(), foreignSentinel); !errors.Is(err, ErrForeignTicket) {
		t.Fatalf("WaitOnly(foreign) error = %v, want ErrForeignTicket", err)
	}

	if err := mismatched.Release(); err != nil {
		t.Fatalf("mismatched root Release() error = %v", err)
	}
	closeCtx, cancelClose := testContext(t)
	defer cancelClose()
	if err := coordinator.CloseAdmissionAndWait(closeCtx, sentinel); err != nil {
		t.Fatalf("CloseAdmissionAndWait() error = %v", err)
	}
	foreignCloseCtx, cancelForeignClose := testContext(t)
	defer cancelForeignClose()
	if err := foreignCoordinator.CloseAdmissionAndWait(foreignCloseCtx, foreignSentinel); err != nil {
		t.Fatalf("foreign CloseAdmissionAndWait() error = %v", err)
	}
}

func TestCoordinatorSubmittedTicketCannotBeReleasedByCaller(t *testing.T) {
	coordinator := NewCoordinator()
	jobStarted := make(chan struct{})
	releaseJob := make(chan struct{})
	ticketChannel := make(chan *Ticket, 1)

	if err := coordinator.SubmitRoot("running", func(ticket *Ticket) {
		ticketChannel <- ticket
		close(jobStarted)
		<-releaseJob
	}); err != nil {
		t.Fatalf("SubmitRoot() error = %v", err)
	}
	waitSignal(t, jobStarted, "submitted job did not start")
	ticket := <-ticketChannel
	if err := ticket.Release(); !errors.Is(err, ErrTicketRunning) {
		t.Fatalf("running ticket Release() error = %v, want ErrTicketRunning", err)
	}

	sentinel, err := coordinator.BeginDrain()
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}
	close(releaseJob)
	closeCtx, cancelClose := testContext(t)
	defer cancelClose()
	if err := coordinator.CloseAdmissionAndWait(closeCtx, sentinel); err != nil {
		t.Fatalf("CloseAdmissionAndWait() error = %v", err)
	}
}

func TestCoordinatorPanicReleasesSubmittedTicket(t *testing.T) {
	coordinator := NewCoordinator()
	started := make(chan struct{})
	if err := coordinator.SubmitRoot("panic", func(*Ticket) {
		close(started)
		panic("expected test panic")
	}); err != nil {
		t.Fatalf("SubmitRoot() error = %v", err)
	}
	waitSignal(t, started, "panic job did not start")

	sentinel, err := coordinator.BeginDrain()
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}
	closeCtx, cancelClose := testContext(t)
	defer cancelClose()
	if err := coordinator.CloseAdmissionAndWait(closeCtx, sentinel); err != nil {
		t.Fatalf("CloseAdmissionAndWait() after panic error = %v", err)
	}
}

func TestCoordinatorStopProducersCancelsAndWaitsCurrentProducer(t *testing.T) {
	coordinator := NewCoordinator()
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})

	if err := coordinator.StartProducer("poller", func(ctx context.Context, _ *Ticket) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		close(finished)
	}); err != nil {
		t.Fatalf("StartProducer() error = %v", err)
	}
	waitSignal(t, started, "producer did not start")

	stopResult := make(chan error, 1)
	stopCtx, cancelStop := testContext(t)
	defer cancelStop()
	go func() {
		stopResult <- coordinator.StopProducers(stopCtx)
	}()

	waitSignal(t, canceled, "producer did not observe cancellation")
	select {
	case err := <-stopResult:
		t.Fatalf("StopProducers returned before current producer finished: %v", err)
	default:
	}

	close(release)
	waitSignal(t, finished, "producer did not finish")
	if err := waitError(t, stopResult, "StopProducers did not return"); err != nil {
		t.Fatalf("StopProducers() error = %v", err)
	}
	if err := coordinator.StartProducer("late", func(context.Context, *Ticket) {}); !errors.Is(err, ErrProducerAdmissionClosed) {
		t.Fatalf("StartProducer() after StopProducers error = %v, want ErrProducerAdmissionClosed", err)
	}

	sentinel, err := coordinator.BeginDrain()
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}
	closeCtx, cancelClose := testContext(t)
	defer cancelClose()
	if err := coordinator.CloseAdmissionAndWait(closeCtx, sentinel); err != nil {
		t.Fatalf("CloseAdmissionAndWait() error = %v", err)
	}
}

func TestRunPeriodicCancellationWaitsCurrentRoundAndStartsNoNextRound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 2)
	started := make(chan struct{})
	release := make(chan struct{})
	returned := make(chan struct{})
	var rounds atomic.Int32

	go func() {
		runPeriodic(ctx, ticks, false, func() {
			if rounds.Add(1) == 1 {
				close(started)
				<-release
			}
		})
		close(returned)
	}()

	ticks <- time.Now()
	waitSignal(t, started, "first periodic round did not start")
	cancel()
	ticks <- time.Now()
	select {
	case <-returned:
		t.Fatal("runPeriodic returned before the current round completed")
	default:
	}

	close(release)
	waitSignal(t, returned, "runPeriodic did not return after current round completed")
	if got := rounds.Load(); got != 1 {
		t.Fatalf("periodic rounds = %d, want 1 after cancellation", got)
	}
}
