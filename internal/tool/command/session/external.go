package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// ExternalProcess represents an admitted command whose process lives on a paired
// device. The existing Session remains the sole owner of observation and activity.
type ExternalProcess interface {
	Stdin() io.WriteCloser
	Wait() (int, error)
	Kill() error
}

type ExternalFactory func(context.Context, string, io.Writer, io.Writer, func(string)) (ExternalProcess, error)

var (
	ErrOutcomeUnknown  = errors.New("remote command outcome is unknown; reconcile the original operation before retrying")
	ErrRemoteTimeout   = errors.New("remote command deadline exceeded")
	ErrRemoteCancelled = errors.New("remote command cancellation confirmed")
)

func StartExternal(ctx context.Context, timeout time.Duration, factory ExternalFactory) (*Session, PreparationStatus, error) {
	if timeout <= 0 || factory == nil {
		return nil, PreparationStatus{}, fmt.Errorf("positive timeout and external factory required")
	}
	if err := ctx.Err(); err != nil {
		return nil, PreparationStatus{}, err
	}
	id, err := newID()
	if err != nil {
		return nil, PreparationStatus{}, err
	}
	lifetime, release := context.WithCancel(ctx)
	s := &Session{ID: id, Command: &exec.Cmd{}, StartedAt: time.Now(), Done: make(chan struct{}), exitCode: -1,
		Terminal: "remote-pipes", external: true, externalState: "queued"}
	process, err := factory(lifetime, id, sessionOutputWriter{session: s}, sessionOutputWriter{session: s, stderr: true}, s.setExternalState)
	if err != nil {
		release()
		return nil, PreparationStatus{}, err
	}
	s.runner, s.Stdin = process, process.Stdin()
	// Cancellation requests termination; it does not invent completion or abandon
	// a remote process while its provider can still reconcile it.
	s.Cancel = func() {
		if !s.Completed() {
			_, _ = s.Kill()
		}
	}
	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			_, _ = s.Kill()
		case <-ctx.Done():
			_, _ = s.Kill()
		case <-s.Done:
		}
	}()
	go func() {
		code, waitErr := process.Wait()
		s.mu.Lock()
		s.waitErr, s.exitCode = waitErr, code
		s.outcomeUnknown = errors.Is(waitErr, ErrOutcomeUnknown)
		s.TimedOut = errors.Is(waitErr, ErrRemoteTimeout) && !s.outcomeUnknown
		s.terminationRequested = errors.Is(waitErr, ErrRemoteCancelled) && !s.outcomeUnknown
		s.completed, s.FinishedAt = true, time.Now()
		s.mu.Unlock()
		close(s.Done)
		release()
	}()
	return s, PreparationStatus{Enabled: false, Mode: "paired-android", Policy: "core-admission",
		Warnings: []string{"Executes on the explicitly selected paired Android backend; no privilege fallback."}}, nil
}

func (s *Session) setExternalState(state string) {
	if state != "queued" && state != "running" && state != "cancel_requested" && state != "unknown" {
		return
	}
	s.mu.Lock()
	if !s.completed {
		s.externalState = state
	}
	s.mu.Unlock()
}
