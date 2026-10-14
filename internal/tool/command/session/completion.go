package session

import (
	"context"
	"sync"
	"time"
)

// TerminalOutcome is the immutable terminal fact published after command
// completion has been durably recorded by the command activity owner.
type TerminalOutcome struct {
	SessionID   string
	Status      string
	ExitCode    *int
	CommandOK   *bool
	TimedOut    bool
	Summary     string
	CompletedAt time.Time
}

// TerminalFuture lets independent observers share one command terminal result.
// Cancelling one Wait call does not affect the command or any other observer.
type TerminalFuture interface {
	Done() <-chan struct{}
	Outcome() (TerminalOutcome, bool)
	Wait(context.Context) (TerminalOutcome, error)
}

type terminalFuture struct {
	done chan struct{}
	once sync.Once
	mu   sync.RWMutex

	outcome   TerminalOutcome
	err       error
	completed bool
}

func newTerminalFuture() *terminalFuture {
	return &terminalFuture{done: make(chan struct{})}
}

func (f *terminalFuture) Done() <-chan struct{} { return f.done }

func (f *terminalFuture) Outcome() (TerminalOutcome, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if !f.completed {
		return TerminalOutcome{}, false
	}
	return copyTerminalOutcome(f.outcome), true
}

func (f *terminalFuture) Wait(ctx context.Context) (TerminalOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Completion wins once it has already been published. This keeps late
	// observers deterministic even when they arrive with an already-cancelled
	// context.
	select {
	case <-f.done:
		return f.result()
	default:
	}
	select {
	case <-f.done:
		return f.result()
	case <-ctx.Done():
		return TerminalOutcome{}, ctx.Err()
	}
}

func (f *terminalFuture) result() (TerminalOutcome, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return copyTerminalOutcome(f.outcome), f.err
}

func (f *terminalFuture) complete(outcome TerminalOutcome, err error) bool {
	published := false
	f.once.Do(func() {
		published = true
		f.mu.Lock()
		f.outcome = copyTerminalOutcome(outcome)
		f.err = err
		f.completed = true
		f.mu.Unlock()
		close(f.done)
	})
	return published
}

func copyTerminalOutcome(outcome TerminalOutcome) TerminalOutcome {
	copy := outcome
	if outcome.ExitCode != nil {
		value := *outcome.ExitCode
		copy.ExitCode = &value
	}
	if outcome.CommandOK != nil {
		value := *outcome.CommandOK
		copy.CommandOK = &value
	}
	return copy
}

func (s *Session) terminalFutureState() *terminalFuture {
	s.terminalOnce.Do(func() {
		s.terminal = newTerminalFuture()
	})
	return s.terminal
}

// TerminalFuture returns the one reusable completion future owned by this
// Session. Observers may obtain it before or after publication.
func (s *Session) TerminalFuture() TerminalFuture {
	return s.terminalFutureState()
}

// PublishTerminal completes the Session future once. The command activity owner
// calls it only after persisting the authoritative terminal fact.
func (s *Session) PublishTerminal(outcome TerminalOutcome, err error) bool {
	return s.terminalFutureState().complete(outcome, err)
}
