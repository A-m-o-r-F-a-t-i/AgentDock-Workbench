package activity

import (
	"context"
	"errors"
	"time"
)

var (
	ErrRecoveryOrigin  = errors.New("recovery origin is not a retained failed MCP business call")
	ErrRecoveryScope   = errors.New("recovery must keep the authenticated conversation, workspace and server")
	ErrRecoveryExpired = errors.New("the connection failure is outside its recovery window")
	ErrRecoveryUsed    = errors.New("recovery or its single business retry has already been consumed")
	ErrRecoveryFailed  = errors.New("the recovery attempt ended without confirmed success; this origin cannot recover or replay again")
	ErrRecoveryUnsafe  = errors.New("business effects are unconfirmed; reconcile them before another operation")
	ErrRecoveryPending = errors.New("the claimed recovery step has not completed successfully")
)

type MCPRecoveryTransition struct {
	Phase, Server, SkillRef, Status string
	Now                             time.Time
}

func recoveryActorMatches(call *ExecutionCall, actor Binding) bool {
	return call != nil && call.ConversationID == actor.ConversationID && call.SourceOwnerKey == actor.SourceOwnerKey && call.WorkspaceID == actor.WorkspaceID
}
func validateRecoveryOrigin(origin *ExecutionCall, actor Binding, change MCPRecoveryTransition) error {
	if origin == nil || origin.DeletedAt != nil || origin.HistoryIncomplete || origin.ToolName != "mcp_tool_call" || origin.Status != "failed" || origin.RetryOfCallID != "" || !IsMCPConnectionError(origin.ErrorCode) || origin.ToolExecution == nil || origin.ToolExecution.Recovery == nil || origin.CompletedAt == nil {
		return ErrRecoveryOrigin
	}
	if actor.ConversationID == "" || actor.WorkspaceID == "" || actor.SourceOwnerKey == "" || !recoveryActorMatches(origin, actor) || origin.ToolExecution.Server != change.Server {
		return ErrRecoveryScope
	}
	if change.Phase != "finish" && change.Phase != "fail" && !change.Now.Before(origin.ToolExecution.Recovery.ExpiresAt) {
		return ErrRecoveryExpired
	}
	return nil
}
func successfulRecoverySkill(projection *callProjection, recovery *MCPRecoveryState, actor Binding) bool {
	step := projection.calls[recovery.CallID]
	return recovery.SkillRef != "" && recoveryActorMatches(step, actor) && step.ToolName == "exec_command" && step.Status == "succeeded" && step.CommandOK != nil && *step.CommandOK
}
func applyRecoveryTransition(projection *callProjection, origin *ExecutionCall, actor Binding, change MCPRecoveryTransition, recovery *MCPRecoveryState) error {
	caller := projection.calls[actor.CallID]
	if !recoveryActorMatches(caller, actor) {
		return ErrRecoveryScope
	}
	switch change.Phase {
	case "claim":
		if recovery.CallID != "" || recovery.ConsumedAt != nil {
			return ErrRecoveryUsed
		}
		want := "mcp_manage"
		if change.SkillRef != "" {
			want = "exec_command"
		}
		if caller.ToolName != want || caller.Status != "running" {
			return ErrRecoveryScope
		}
		recovery.CallID, recovery.SkillRef, recovery.State, recovery.ClaimedAt = actor.CallID, change.SkillRef, "claimed", copyValue(&change.Now)
	case "refresh":
		if caller.ToolName != "mcp_manage" || caller.Status != "running" {
			return ErrRecoveryScope
		}
		if recovery.ConsumedAt != nil {
			return ErrRecoveryUsed
		}
		if recovery.State == "refreshing" {
			return ErrRecoveryPending
		}
		if recovery.State != "claimed" || recovery.RefreshID != "" {
			return ErrRecoveryUsed
		}
		if recovery.SkillRef == "" {
			if recovery.CallID != actor.CallID {
				return ErrRecoveryScope
			}
		} else if !successfulRecoverySkill(projection, recovery, actor) {
			return ErrRecoveryPending
		}
		// Claim the reconnect before it can start. Concurrent resumes of a
		// successful recovery Skill must not each refresh the same connection.
		recovery.RefreshID, recovery.State = actor.CallID, "refreshing"
	case "ready":
		if caller.ToolName != "mcp_manage" || caller.Status != "running" || recovery.ConsumedAt != nil || recovery.State != "refreshing" {
			return ErrRecoveryUsed
		}
		if recovery.RefreshID != actor.CallID {
			return ErrRecoveryScope
		}
		recovery.State = "recovered"
	case "retry":
		if recovery.ConsumedAt != nil || recovery.RetryID != "" {
			return ErrRecoveryUsed
		}
		if caller.ToolName != "mcp_tool_call" || actor.RetryOfCallID != origin.CallID {
			return ErrRecoveryScope
		}
		if origin.ToolExecution.RetrySafe == nil || !*origin.ToolExecution.RetrySafe {
			return ErrRecoveryUnsafe
		}
		if recovery.State != "recovered" {
			return ErrRecoveryPending
		}
		recovery.RetryID, recovery.State, recovery.ConsumedAt = actor.CallID, "consumed", copyValue(&change.Now)
	case "fail":
		if recovery.CallID == "" || recovery.ConsumedAt != nil || (recovery.State != "claimed" && recovery.State != "refreshing") {
			return ErrRecoveryUsed
		}
		stepID := recovery.CallID
		if recovery.State == "refreshing" {
			stepID = recovery.RefreshID
			if stepID == "" {
				return ErrRecoveryScope
			}
		}
		if actor.CallID == stepID {
			want := "mcp_manage"
			if recovery.State == "claimed" && recovery.SkillRef != "" {
				want = "exec_command"
			}
			if caller.ToolName != want || (caller.Status != "running" && !CallTerminal(caller.Status)) {
				return ErrRecoveryScope
			}
		} else {
			step := projection.calls[stepID]
			if caller.ToolName != "mcp_manage" || caller.Status != "running" || !recoveryActorMatches(step, actor) {
				return ErrRecoveryScope
			}
			if !CallTerminal(step.Status) {
				return ErrRecoveryPending
			}
		}
		// A failed recovery consumes this origin without dispatching a business
		// retry. Keep RetryID empty so consumption never implies business execution.
		recovery.State, recovery.ConsumedAt = "recovery_failed", copyValue(&change.Now)
	case "finish":
		if recovery.RetryID != actor.CallID || recovery.ConsumedAt == nil {
			return ErrRecoveryScope
		}
		if !CallTerminal(change.Status) {
			return ErrRecoveryPending
		}
		recovery.State = "retry_" + change.Status
	default:
		return ErrRecoveryOrigin
	}
	return nil
}

// Validation, claim and consumption share the journal's existing process/file
// lock. An append failure never permits recovery dispatch.
func (s *Store) TransitionMCPRecovery(ctx context.Context, id string, actor Binding, change MCPRecoveryTransition) (saved ExecutionCall, resultErr error) {
	if change.Now.IsZero() {
		change.Now = time.Now().UTC()
	}
	reservation, err := s.ReserveAppend(ctx, 1)
	if err != nil {
		return saved, err
	}
	defer reservation.Close()
	ticket, err := reservation.take()
	if err != nil {
		return saved, err
	}
	var written Event
	defer func() { ticket.finish(written, resultErr) }()
	release, err := s.lock(ctx)
	if err != nil {
		return saved, err
	}
	defer release()
	if err = ctx.Err(); err != nil {
		return saved, err
	}
	projection, err := s.projectionLocked(ctx)
	if err != nil {
		return saved, err
	}
	origin := projection.calls[id]
	if err = validateRecoveryOrigin(origin, actor, change); err != nil {
		return saved, err
	}
	detail := origin.ToolExecution.clone()
	if err = applyRecoveryTransition(projection, origin, actor, change, detail.Recovery); err != nil {
		return saved, err
	}
	event, err := s.prepareAppend(ctx, Event{Binding: origin.Binding, Kind: "mcp.recovery_" + change.Phase, ToolName: origin.ToolName, ToolExecution: detail})
	if err != nil {
		return saved, err
	}
	request := &appendRequest{ctx: ctx, event: event, ticket: ticket, enqueued: time.Now()}
	started := time.Now()
	outcome, ok := s.appendBatchLocked([]*appendRequest{request})[request]
	s.measureAppend([]*appendRequest{request}, started)
	if !ok {
		return saved, errors.New("recovery append did not produce a receipt")
	}
	written = outcome.event
	if outcome.err != nil {
		return saved, outcome.err
	}
	saved = cloneCall(origin, true, true)
	saved.ToolExecution = detail
	return saved, nil
}

// MCPRecoveryOrigin reads only a caller-scoped eligible business failure.
func (s *Store) MCPRecoveryOrigin(ctx context.Context, id string, actor Binding, server string) (ExecutionCall, error) {
	release, err := s.lock(ctx)
	if err != nil {
		return ExecutionCall{}, err
	}
	defer release()
	projection, err := s.projectionLocked(ctx)
	if err != nil {
		return ExecutionCall{}, err
	}
	origin := projection.calls[id]
	if err = validateRecoveryOrigin(origin, actor, MCPRecoveryTransition{Server: server, Now: time.Now()}); err != nil {
		return ExecutionCall{}, err
	}
	return cloneCall(origin, true, true), nil
}
