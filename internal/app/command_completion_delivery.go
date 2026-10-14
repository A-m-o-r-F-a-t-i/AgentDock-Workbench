package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/commandcompletion"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

// This state belongs to the outer response adapter. Business results and model
// arguments cannot populate it, including when a projector rewrites an envelope.
type commandCompletionResponse struct {
	prepared  bool
	committed bool
	binding   activity.Binding
	claimed   []commandcompletion.Event
	selected  []commandcompletion.Event
	observed  *commandcompletion.Identity
}

func (r *Runtime) enqueueCommandCompletion(binding activity.Binding, outcome session.TerminalOutcome) {
	if r.commandCompletions == nil || binding.ParentCallID != "" || binding.SourceOwnerKey == "" || binding.ConversationID == "" || outcome.ActivityEventID == "" {
		return
	}
	event := commandcompletion.Event{
		Identity: commandcompletion.Identity{OriginalCallID: binding.CallID, SessionID: outcome.SessionID},
		Type:     "command_completion", Version: 1, EventID: commandcompletion.ID(binding.CallID),
		ConversationID: binding.ConversationID, TaskID: binding.TaskID, ThreadID: binding.ThreadID, WorkspaceID: binding.WorkspaceID,
		Status: outcome.Status, ExitCode: outcome.ExitCode, CommandOK: outcome.CommandOK,
		TimedOut: outcome.TimedOut, CompletedAt: outcome.CompletedAt, ActivityEventID: outcome.ActivityEventID,
	}
	// Summaries are composed from typed terminal facts, never shell output or
	// error strings. Unknown results deliberately carry no invented exit code.
	switch event.Status {
	case "succeeded":
		event.Summary = "Command completed successfully."
	case "failed":
		event.Summary = "Command failed."
		if event.ExitCode != nil {
			event.Summary = fmt.Sprintf("Command exited with code %d.", *event.ExitCode)
		}
		if event.TimedOut {
			event.Summary = "Command timed out."
		}
	case "cancelled":
		event.Summary = "Command was stopped."
	default:
		event.Status, event.Summary, event.ExitCode, event.CommandOK = "unknown", "Command outcome is unknown; inspect the original activity before retrying.", nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.commandCompletions.Add(ctx, binding.SourceOwnerKey, event); err != nil {
		slog.Warn("queue command completion", "call_id", binding.CallID, "error", err)
		_ = r.appendExecution(activity.Event{Binding: binding, Kind: "command.notice", SessionID: outcome.SessionID, Summary: "Command completion notification was not queued; terminal facts remain in the original activity record."})
	}
}

// PrepareCommandCompletionResponse claims only pre-existing facts for a trusted
// external root. The current business response can be a success or an error.
func (r *Runtime) PrepareCommandCompletionResponse(ctx context.Context, response *ToolResponse, name string, args map[string]any, envelope map[string]any) {
	if response == nil || r.commandCompletions == nil || activity.IsDiagnostic(ctx) || activity.IsLocalManagement(ctx) {
		return
	}
	response.mu.Lock()
	defer response.mu.Unlock()
	state := &response.commandCompletion
	if state.prepared {
		return
	}
	state.prepared = true
	binding := response.auditBinding
	if binding.ParentCallID != "" || binding.CallID == "" || binding.ConversationID == "" || binding.SourceOwnerKey == "" || binding.SourceOwnerKey != activity.SourceOwnerKey(ctx) {
		return
	}
	if err := r.completionRecipientAllowed(ctx, binding); err != nil {
		return
	}
	state.binding = binding
	data, _ := envelope["structuredContent"].(map[string]any)
	if (name == "session_observe" || name == "session_act") && stringArg(args, "session_id") != "" && stringArg(data, "session_id") == stringArg(args, "session_id") {
		status := stringArg(data, "status")
		_, hasExit := data["exit_code"]
		if (status == "exited" || status == "killed" || status == "timeout" || status == "unknown") && (hasExit || data["outcome_unknown"] == true) {
			original, ok := r.command.SessionBinding(stringArg(data, "session_id"))
			if ok && original.ConversationID == binding.ConversationID && original.SourceOwnerKey == binding.SourceOwnerKey && original.CallID != "" {
				state.observed = &commandcompletion.Identity{OriginalCallID: original.CallID, SessionID: stringArg(data, "session_id")}
			}
		}
	}
	claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	items, err := r.commandCompletions.Reserve(claimCtx, binding.SourceOwnerKey, binding.ConversationID, binding.CallID, response.auditReceived)
	if err != nil {
		slog.Warn("reserve command completions", "call_id", binding.CallID, "error", err)
		return
	}
	state.claimed = items
	for _, item := range items {
		if state.observed == nil || item.Identity != *state.observed {
			state.selected = append(state.selected, item)
		}
	}
}

func (r *Runtime) completionRecipientAllowed(ctx context.Context, binding activity.Binding) error {
	if err := r.conversations.Owns(ctx, binding.ConversationID); err != nil {
		return err
	}
	record, err := r.conversations.Get(ctx, binding.ConversationID)
	if err != nil {
		return err
	}
	if record.TerminatedAt != nil || record.TrashedAt != nil {
		return activity.ErrConversationTerminated
	}
	return ctx.Err()
}

// CommitCommandCompletionResponse is called after the complete candidate has
// been encoded. Committing inclusion before it escapes gives at-most-once
// behavior across restarts without pretending an HTTP write is a model receipt.
func (r *Runtime) CommitCommandCompletionResponse(ctx context.Context, response *ToolResponse) error {
	if response == nil || r.commandCompletions == nil {
		return nil
	}
	response.mu.Lock()
	defer response.mu.Unlock()
	state := &response.commandCompletion
	if state.committed || (len(state.claimed) == 0 && state.observed == nil) {
		return nil
	}
	if err := r.completionRecipientAllowed(ctx, state.binding); err != nil {
		return err
	}
	commitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b := state.binding
	if err := r.commandCompletions.Finish(commitCtx, b.SourceOwnerKey, b.ConversationID, b.CallID, true, state.observed); err != nil {
		return err
	}
	state.committed = true
	return nil
}

func (r *Runtime) AbortCommandCompletionResponse(response *ToolResponse) {
	if response == nil || r.commandCompletions == nil {
		return
	}
	response.mu.Lock()
	defer response.mu.Unlock()
	state := &response.commandCompletion
	if state.committed || len(state.claimed) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b := state.binding
	if err := r.commandCompletions.Finish(ctx, b.SourceOwnerKey, b.ConversationID, b.CallID, false, nil); err != nil {
		slog.Warn("release command completion reservation", "call_id", b.CallID, "error", err)
	}
	state.selected = nil
}

func (r *Runtime) RecordCommandCompletionDeliveryFailure(response *ToolResponse) {
	if response == nil || r.commandCompletions == nil {
		return
	}
	response.mu.Lock()
	defer response.mu.Unlock()
	state := &response.commandCompletion
	if !state.committed || len(state.claimed) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b := state.binding
	if err := r.commandCompletions.DeliveryUnknown(ctx, b.SourceOwnerKey, b.ConversationID, b.CallID); err != nil {
		slog.Warn("record unknown completion delivery", "call_id", b.CallID, "error", err)
	}
}
