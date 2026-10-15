package command

import (
	"context"
	"github.com/uvwt/agentdock/internal/activity"
)

type modelOutputContextKey struct{}

// Only the Core's final bounded encoder requests the retained result. Direct
// command-service consumers keep their existing byte-limited output contract.
func WithModelOutputProjection(ctx context.Context) context.Context {
	return context.WithValue(ctx, modelOutputContextKey{}, true)
}

func modelOutputProjection(ctx context.Context) bool {
	enabled, _ := ctx.Value(modelOutputContextKey{}).(bool)
	return enabled
}

func (s *Service) persistedSessionResult(ctx context.Context, id string) (Result, error) {
	if s.activity == nil {
		return nil, toolError("SESSION_NOT_FOUND", "session not found", "not_found")
	}
	call, err := s.activity.CommandSession(ctx, id)
	if err != nil {
		return nil, toolError("SESSION_NOT_FOUND", "session not found in retained terminal history", "not_found")
	}
	binding := activity.FromContext(ctx)
	if call.SourceOwnerKey != "" && call.SourceOwnerKey != activity.SourceOwnerKey(ctx) {
		return nil, toolError("SESSION_OWNER_MISMATCH", "This command belongs to another authenticated client.", "permission")
	}
	if call.ConversationID != "" && binding.ConversationID != call.ConversationID && (binding.TaskID == "" || binding.TaskID != call.TaskID) {
		return nil, toolError("SESSION_CONVERSATION_MISMATCH", "Resume the owning task to inspect the retained terminal receipt.", "permission")
	}
	status := "exited"
	if call.TimedOut {
		status = "timeout"
	}
	result := Result{"session_id": id, "status": status, "operation_status": "succeeded", "terminal_retained": true, "no_change": true, "elapsed_ms": call.ElapsedMS, "runtime": call.Runtime, "workdir": call.Workdir}
	if call.ExitCode != nil {
		result["exit_code"] = *call.ExitCode
	}
	if call.CommandOK != nil {
		result["command_ok"] = *call.CommandOK
	}
	if call.Status == "unknown" || call.ExitCode == nil {
		result["status"] = "outcome_unknown"
		result["outcome_unknown"] = true
	}
	if call.TimedOut {
		result["timed_out"] = true
	}
	if call.Status == "cancelled" {
		result["status"], result["command_ok"] = "cancelled", false
	}
	if source := activity.SourceReference(call.CallID, call.OutputSource); source != "" {
		result["source_ref"] = source
		result["output_source"] = source
	}
	addBindingResult(result, call.Binding)
	return result, nil
}

// TerminalReceipt reads persisted facts without consuming or replaying output.
func (s *Service) TerminalReceipt(ctx context.Context, id string) (Result, error) {
	return s.persistedSessionResult(ctx, id)
}
