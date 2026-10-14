package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

type commandTerminalSource interface {
	TakeTerminalFuture(string) (session.TerminalFuture, bool)
}

// SettlementGuard makes competing completion, cancellation, shutdown and late
// callback paths converge on one authoritative root-call settlement.
type SettlementGuard struct {
	once sync.Once
	err  error
}

func (g *SettlementGuard) Settle(fn func() error) error {
	g.once.Do(func() { g.err = fn() })
	return g.err
}

// TerminalSettlement binds a detached command Session to its root call and
// optional approval. The Session future is published only after command.completed
// has been persisted, so a successful wait never needs to query the projection.
type TerminalSettlement struct {
	guard    SettlementGuard
	deferred bool

	runtime        *Runtime
	future         session.TerminalFuture
	initialErr     error
	completion     *activity.AppendReservation
	binding        activity.Binding
	toolName       string
	title          string
	sessionID      string
	approvalID     string
	ruleID         string
	permissionMode string
	started        time.Time
	executionMS    *int64
	waitMS         *int64
}

func (r *Runtime) startCommandTerminalSettlement(p *preparedExecution, sessionID string, completion *activity.AppendReservation, deferred bool) bool {
	future, found := commandTerminalSource(r.command).TakeTerminalFuture(sessionID)
	settlement := &TerminalSettlement{
		deferred:       deferred,
		runtime:        r,
		future:         future,
		completion:     completion,
		binding:        p.state.binding,
		toolName:       p.spec.Name,
		title:          r.describeExecution(p.spec.Name, p.args, p.state),
		sessionID:      sessionID,
		approvalID:     p.approvalID,
		ruleID:         p.decision.RuleID,
		permissionMode: p.decision.Mode,
		started:        p.state.started,
		executionMS:    copyInt64Pointer(p.state.executionMS),
		waitMS:         copyInt64Pointer(p.state.waitMS),
	}
	if !found {
		settlement.initialErr = fmt.Errorf("terminal future for session %s was not registered", sessionID)
	}
	ctx := r.commandSettlementCtx
	if ctx == nil {
		ctx = context.Background()
	}
	r.commandSettlementWG.Add(1)
	go func() {
		defer r.commandSettlementWG.Done()
		if err := settlement.Run(ctx); err != nil {
			slog.Error("settle command terminal outcome", "call_id", p.state.binding.CallID, "session_id", sessionID, "error", err)
		}
	}()
	return found
}

func (s *TerminalSettlement) Run(ctx context.Context) error {
	return s.guard.Settle(func() error { return s.settle(ctx) })
}

func (s *TerminalSettlement) settle(ctx context.Context) error {
	if s.completion != nil {
		defer s.completion.Close()
	}
	outcome, waitErr := s.wait(ctx)
	rootPersistErr := error(nil)
	if waitErr != nil {
		outcome = normalizeUnknownTerminalOutcome(s.sessionID, outcome, waitErr)
		rootPersistErr = s.persistUnknownRoot(outcome)
	}
	if waitErr == nil && s.deferred {
		s.runtime.enqueueCommandCompletion(s.binding, outcome)
	}

	var approvalErr error
	if s.approvalID != "" && s.runtime.permissions != nil {
		settleCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, approvalErr = s.runtime.permissions.Settle(settleCtx, s.approvalID, terminalApprovalStatus(outcome.Status), outcome.Summary)
		cancel()
	}
	return errors.Join(rootPersistErr, approvalErr)
}

func (s *TerminalSettlement) wait(ctx context.Context) (session.TerminalOutcome, error) {
	if s.initialErr != nil {
		return session.TerminalOutcome{}, s.initialErr
	}
	if s.future == nil {
		return session.TerminalOutcome{}, errors.New("command terminal future is unavailable")
	}
	outcome, err := s.future.Wait(ctx)
	if err == nil {
		return outcome, nil
	}
	// If cancellation raced a published result, prefer the already-persisted
	// terminal fact instead of degrading it to unknown.
	select {
	case <-s.future.Done():
		return s.future.Wait(context.Background())
	default:
		return outcome, err
	}
}

func (s *TerminalSettlement) persistUnknownRoot(outcome session.TerminalOutcome) error {
	elapsed := time.Since(s.started).Milliseconds()
	if !outcome.CompletedAt.IsZero() && outcome.CompletedAt.After(s.started) {
		elapsed = outcome.CompletedAt.Sub(s.started).Milliseconds()
	}
	event := activity.Event{
		Binding:        s.binding,
		Kind:           "call.completed",
		Status:         "unknown",
		ToolName:       s.toolName,
		Title:          s.title,
		SessionID:      s.sessionID,
		ApprovalID:     s.approvalID,
		RuleID:         s.ruleID,
		PermissionMode: s.permissionMode,
		ErrorCode:      "COMMAND_TERMINAL_UNAVAILABLE",
		ElapsedMS:      elapsed,
		Summary:        outcome.Summary,
	}
	event.OperationElapsedMS = &elapsed
	event.ExecutionElapsedMS, event.WaitElapsedMS = s.executionMS, s.waitMS
	return s.runtime.appendReservedExecution(s.completion, event)
}

func unconfirmedTerminalOutcome(sessionID string, cause error) session.TerminalOutcome {
	summary := "Command terminal facts were unavailable; verify the original operation before retrying."
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		summary = "Command terminal notification ended during runtime shutdown; the final outcome is unknown."
	}
	return session.TerminalOutcome{
		SessionID:   sessionID,
		Status:      "unknown",
		Summary:     summary,
		CompletedAt: time.Now(),
	}
}

func normalizeUnknownTerminalOutcome(sessionID string, outcome session.TerminalOutcome, cause error) session.TerminalOutcome {
	fallback := unconfirmedTerminalOutcome(sessionID, cause)
	if outcome.SessionID == "" {
		outcome.SessionID = fallback.SessionID
	}
	outcome.Status = "unknown"
	if outcome.Summary == "" {
		outcome.Summary = fallback.Summary
	}
	if outcome.CompletedAt.IsZero() {
		outcome.CompletedAt = fallback.CompletedAt
	}
	return outcome
}

func terminalApprovalStatus(status string) string {
	switch status {
	case "succeeded", "failed", "cancelled", "unknown":
		return status
	default:
		return "unknown"
	}
}

func copyInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (r *Runtime) drainCommandTerminalSettlements() error {
	done := make(chan struct{})
	go func() {
		r.commandSettlementWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		if r.commandSettlementCancel != nil {
			r.commandSettlementCancel()
		}
		return nil
	case <-time.After(5 * time.Second):
	}
	if r.commandSettlementCancel != nil {
		r.commandSettlementCancel()
	}
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("command terminal settlements did not stop in time")
	}
}
