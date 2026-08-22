package billinglifecycle

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

var (
	ErrAdmissionClosed         = errors.New("billing lifecycle root admission is closed")
	ErrProducerAdmissionClosed = errors.New("billing lifecycle producer admission is closed")
	ErrProducerAlreadyStarted  = errors.New("billing lifecycle producer already started")
	ErrDraining                = errors.New("billing lifecycle drain already started")
	ErrTicketReleased          = errors.New("billing lifecycle ticket is released")
	ErrForeignTicket           = errors.New("billing lifecycle ticket belongs to another coordinator")
	ErrSentinelMismatch        = errors.New("billing lifecycle drain sentinel does not match")
	ErrSentinelTicket          = errors.New("billing lifecycle drain sentinel cannot be submitted or released directly")
	ErrTicketAlreadySubmitted  = errors.New("billing lifecycle ticket is already submitted")
	ErrTicketRunning           = errors.New("billing lifecycle ticket is running")
)

type ticketKind uint8

const (
	ticketKindRoot ticketKind = iota
	ticketKindChild
	ticketKindSentinel
)

type ticketState struct {
	name    string
	kind    ticketKind
	running bool
}

// Ticket is a synchronously registered unit of billing work. A valid ticket
// may reserve child work even after root admission has begun draining.
type Ticket struct {
	coordinator *Coordinator
	id          uint64
}

type parentTicketContextKey struct{}

type producerState struct {
	cancel context.CancelFunc
}

// Coordinator owns billing producers and jobs without depending on the
// process-wide gopool.
type Coordinator struct {
	mu      sync.Mutex
	changed chan struct{}
	nextID  uint64

	rootAdmissionOpen     bool
	producerAdmissionOpen bool
	draining              bool
	closed                bool

	active     int
	tickets    map[uint64]*ticketState
	sentinelID uint64
	producers  map[string]producerState
}

func NewCoordinator() *Coordinator {
	return &Coordinator{
		changed:               make(chan struct{}),
		rootAdmissionOpen:     true,
		producerAdmissionOpen: true,
		tickets:               make(map[uint64]*ticketState),
		producers:             make(map[string]producerState),
	}
}

func (c *Coordinator) notifyLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *Coordinator) reserveTicketLocked(name string, kind ticketKind) *Ticket {
	c.nextID++
	ticket := &Ticket{coordinator: c, id: c.nextID}
	c.tickets[ticket.id] = &ticketState{name: name, kind: kind}
	c.active++
	c.notifyLocked()
	return ticket
}

// ReserveRoot synchronously registers externally initiated billing work.
func (c *Coordinator) ReserveRoot(name string) (*Ticket, error) {
	if name == "" {
		return nil, errors.New("billing lifecycle root ticket name is empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.rootAdmissionOpen || c.draining || c.closed {
		return nil, fmt.Errorf("%w: %s", ErrAdmissionClosed, name)
	}
	return c.reserveTicketLocked(name, ticketKindRoot), nil
}

// ContextWithParent carries explicit authority for work derived from an
// already-reserved ticket, such as a final flush started by the drain sentinel.
func ContextWithParent(ctx context.Context, parent *Ticket) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, parentTicketContextKey{}, parent)
}

// ReserveFromContext reserves a child when ctx explicitly carries a parent;
// otherwise it reserves a normal root ticket.
func (c *Coordinator) ReserveFromContext(ctx context.Context, name string) (*Ticket, error) {
	if ctx != nil {
		if parent, ok := ctx.Value(parentTicketContextKey{}).(*Ticket); ok && parent != nil {
			if parent.coordinator != c {
				return nil, ErrForeignTicket
			}
			return parent.ReserveChild(name)
		}
	}
	return c.ReserveRoot(name)
}

// SubmitRoot reserves and starts externally initiated billing work.
func (c *Coordinator) SubmitRoot(name string, job func(*Ticket)) error {
	ticket, err := c.ReserveRoot(name)
	if err != nil {
		return err
	}
	if err := ticket.Submit(job); err != nil {
		_ = ticket.Release()
		return err
	}
	return nil
}

func (c *Coordinator) ticketStateLocked(ticket *Ticket) (*ticketState, error) {
	if ticket == nil || ticket.coordinator != c {
		return nil, ErrForeignTicket
	}
	state, ok := c.tickets[ticket.id]
	if !ok {
		return nil, ErrTicketReleased
	}
	return state, nil
}

// ReserveChild synchronously registers work derived from a still-valid parent.
func (ticket *Ticket) ReserveChild(name string) (*Ticket, error) {
	if ticket == nil || ticket.coordinator == nil {
		return nil, ErrForeignTicket
	}
	if name == "" {
		return nil, errors.New("billing lifecycle child ticket name is empty")
	}
	c := ticket.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.ticketStateLocked(ticket); err != nil {
		return nil, err
	}
	if c.closed {
		return nil, ErrAdmissionClosed
	}
	return c.reserveTicketLocked(name, ticketKindChild), nil
}

// SubmitChild reserves and starts work derived from this ticket.
func (ticket *Ticket) SubmitChild(name string, job func(*Ticket)) error {
	child, err := ticket.ReserveChild(name)
	if err != nil {
		return err
	}
	if err := child.Submit(job); err != nil {
		_ = child.Release()
		return err
	}
	return nil
}

// Submit starts a previously reserved ticket. Completion and panic paths both
// release it exactly once.
func (ticket *Ticket) Submit(job func(*Ticket)) error {
	if ticket == nil || ticket.coordinator == nil {
		return ErrForeignTicket
	}
	if job == nil {
		return errors.New("billing lifecycle ticket job is nil")
	}
	c := ticket.coordinator
	c.mu.Lock()
	state, err := c.ticketStateLocked(ticket)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	if state.kind == ticketKindSentinel {
		c.mu.Unlock()
		return ErrSentinelTicket
	}
	if state.running {
		c.mu.Unlock()
		return ErrTicketAlreadySubmitted
	}
	state.running = true
	name := state.name
	c.notifyLocked()
	c.mu.Unlock()

	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("billing lifecycle job %q panicked: %v", name, recovered)
			}
			if err := c.release(ticket, true); err != nil {
				log.Printf("billing lifecycle job %q release failed: %v", name, err)
			}
		}()
		job(ticket)
	}()
	return nil
}

// Release releases reserved synchronous work. Submitted work releases itself.
func (ticket *Ticket) Release() error {
	if ticket == nil || ticket.coordinator == nil {
		return ErrForeignTicket
	}
	return ticket.coordinator.release(ticket, false)
}

func (c *Coordinator) release(ticket *Ticket, fromRunner bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.ticketStateLocked(ticket)
	if err != nil {
		return err
	}
	if state.kind == ticketKindSentinel {
		return ErrSentinelTicket
	}
	if state.running && !fromRunner {
		return ErrTicketRunning
	}
	delete(c.tickets, ticket.id)
	c.active--
	c.notifyLocked()
	return nil
}

// StartProducer synchronously reserves a root ticket before starting a
// cancellable long-lived producer.
func (c *Coordinator) StartProducer(name string, producer func(context.Context, *Ticket)) error {
	if name == "" {
		return errors.New("billing lifecycle producer name is empty")
	}
	if producer == nil {
		return fmt.Errorf("billing lifecycle producer %q is nil", name)
	}

	c.mu.Lock()
	if !c.producerAdmissionOpen {
		c.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrProducerAdmissionClosed, name)
	}
	if !c.rootAdmissionOpen || c.draining || c.closed {
		c.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrAdmissionClosed, name)
	}
	if _, exists := c.producers[name]; exists {
		c.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrProducerAlreadyStarted, name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ticket := c.reserveTicketLocked(name, ticketKindRoot)
	c.tickets[ticket.id].running = true
	c.producers[name] = producerState{cancel: cancel}
	c.notifyLocked()
	c.mu.Unlock()

	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("billing lifecycle producer %q panicked: %v", name, recovered)
			}
			if err := c.release(ticket, true); err != nil {
				log.Printf("billing lifecycle producer %q release failed: %v", name, err)
			}
			c.mu.Lock()
			delete(c.producers, name)
			c.notifyLocked()
			c.mu.Unlock()
		}()
		producer(ctx, ticket)
	}()
	return nil
}

// StopProducers prevents new producers, cancels the current set, and waits for
// their current rounds and root tickets to finish.
func (c *Coordinator) StopProducers(ctx context.Context) error {
	c.mu.Lock()
	if c.producerAdmissionOpen {
		c.producerAdmissionOpen = false
		for _, producer := range c.producers {
			producer.cancel()
		}
		c.notifyLocked()
	}
	c.mu.Unlock()

	return c.wait(ctx, func() bool {
		return len(c.producers) == 0
	})
}

// BeginDrain atomically closes root admission and creates the sole authority
// that may spawn final child work during shutdown.
func (c *Coordinator) BeginDrain() (*Ticket, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.draining {
		return nil, ErrDraining
	}
	if c.closed {
		return nil, ErrAdmissionClosed
	}
	c.rootAdmissionOpen = false
	c.draining = true
	sentinel := c.reserveTicketLocked("drain-sentinel", ticketKindSentinel)
	c.sentinelID = sentinel.id
	return sentinel, nil
}

// WaitOnly waits until the supplied drain sentinel is the only active ticket.
func (c *Coordinator) WaitOnly(ctx context.Context, sentinel *Ticket) error {
	for {
		c.mu.Lock()
		if err := c.validSentinelLocked(sentinel); err != nil {
			c.mu.Unlock()
			return err
		}
		if c.active == 1 {
			c.mu.Unlock()
			return nil
		}
		changed := c.changed
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (c *Coordinator) validSentinelLocked(sentinel *Ticket) error {
	if sentinel == nil || sentinel.coordinator != c {
		return ErrForeignTicket
	}
	if sentinel.id != c.sentinelID {
		return ErrSentinelMismatch
	}
	state, ok := c.tickets[sentinel.id]
	if !ok {
		return ErrTicketReleased
	}
	if state.kind != ticketKindSentinel {
		return ErrSentinelMismatch
	}
	return nil
}

// CloseAdmissionAndWait waits for every root and descendant except sentinel,
// then closes child admission and releases sentinel in the same critical
// section. No child can register between the active==1 observation and close.
func (c *Coordinator) CloseAdmissionAndWait(ctx context.Context, sentinel *Ticket) error {
	for {
		c.mu.Lock()
		if err := c.validSentinelLocked(sentinel); err != nil {
			c.mu.Unlock()
			return err
		}
		if c.active == 1 {
			c.closed = true
			delete(c.tickets, sentinel.id)
			c.active = 0
			c.notifyLocked()
			c.mu.Unlock()
			return nil
		}
		changed := c.changed
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (c *Coordinator) wait(ctx context.Context, done func() bool) error {
	for {
		c.mu.Lock()
		if done() {
			c.mu.Unlock()
			return nil
		}
		changed := c.changed
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// RunPeriodic starts no new round after ctx is canceled. A round already in
// progress runs synchronously to completion before the function returns.
func RunPeriodic(ctx context.Context, interval time.Duration, runImmediately bool, round func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	runPeriodic(ctx, ticker.C, runImmediately, round)
}

func runPeriodic(ctx context.Context, ticks <-chan time.Time, runImmediately bool, round func()) {
	runRound := func() bool {
		select {
		case <-ctx.Done():
			return false
		default:
			round()
			return true
		}
	}

	if runImmediately && !runRound() {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			if !runRound() {
				return
			}
		}
	}
}

var defaultCoordinator = NewCoordinator()

func ReserveRoot(name string) (*Ticket, error) {
	return defaultCoordinator.ReserveRoot(name)
}

func ReserveFromContext(ctx context.Context, name string) (*Ticket, error) {
	return defaultCoordinator.ReserveFromContext(ctx, name)
}

func SubmitRoot(name string, job func(*Ticket)) error {
	return defaultCoordinator.SubmitRoot(name, job)
}

func StartProducer(name string, producer func(context.Context, *Ticket)) error {
	return defaultCoordinator.StartProducer(name, producer)
}

func StopProducers(ctx context.Context) error {
	return defaultCoordinator.StopProducers(ctx)
}

func BeginDrain() (*Ticket, error) {
	return defaultCoordinator.BeginDrain()
}

func WaitOnly(ctx context.Context, sentinel *Ticket) error {
	return defaultCoordinator.WaitOnly(ctx, sentinel)
}

func CloseAdmissionAndWait(ctx context.Context, sentinel *Ticket) error {
	return defaultCoordinator.CloseAdmissionAndWait(ctx, sentinel)
}
