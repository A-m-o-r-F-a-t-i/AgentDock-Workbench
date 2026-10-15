package app

import (
	"context"
	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/commandcompletion"
	toolcommand "github.com/uvwt/agentdock/internal/tool/command"
)

func (r *Runtime) commandObservation(p *preparedExecution, result Result, err error) activity.CommandObservation {
	relation := activity.CommandObservation{}
	if p.spec.Name != "session_observe" && p.spec.Name != "session_act" {
		return relation
	}
	id := stringArg(p.args, "session_id")
	if id == "" {
		return relation
	}
	origin, ok := r.command.SessionBinding(id)
	binding := p.state.binding
	if !ok || origin.CallID == "" || origin.CallID == binding.CallID || origin.ConversationID == "" || origin.ConversationID != binding.ConversationID || origin.SourceOwnerKey != binding.SourceOwnerKey {
		return relation
	}
	relation.OriginCallID = origin.CallID
	relation.RoutineObservation = p.spec.Name == "session_observe" && stringArg(p.args, "action") == "status" && quietCommandObservation(result, err)
	return relation
}

func quietCommandObservation(result Result, err error) bool {
	if err != nil || resultReportsFailure(result) || result["command_ok"] == false {
		return false
	}
	switch stringArg(result, "status") {
	case "running", "exited":
	default:
		return false
	}
	if stringArg(result, "stdout") != "" || stringArg(result, "stderr") != "" {
		return false
	}
	for _, key := range []string{"stdout_truncated", "stderr_truncated", "outcome_unknown"} {
		if result[key] == true {
			return false
		}
	}
	if result["source_complete"] == false || stringArg(result, "activity_warning") != "" {
		return false
	}
	for _, key := range []string{"stdout_output_bytes", "stderr_output_bytes", "stdout_dropped_bytes", "stderr_dropped_bytes", "dropped_before_bytes"} {
		if value := measuredInt(result, key); value != nil && *value > 0 {
			return false
		}
	}
	return true
}

func (r *Runtime) observeCommand(ctx context.Context, request toolcommand.SessionObserveRequest) (Result, error) {
	if request.Action == "status" && r.commandCompletions != nil {
		origin, found := r.command.SessionBinding(request.SessionID)
		binding := activity.FromContext(ctx)
		if found && origin.CallID != "" && origin.SourceOwnerKey != "" && origin.SourceOwnerKey == binding.SourceOwnerKey && origin.ConversationID == binding.ConversationID {
			delivered, err := r.commandCompletions.Delivered(ctx, origin.SourceOwnerKey, origin.ConversationID, commandcompletion.Identity{OriginalCallID: origin.CallID, SessionID: request.SessionID})
			if err != nil {
				return nil, toolError("COMPLETION_RECEIPT_UNAVAILABLE", err.Error(), "runtime")
			}
			if delivered {
				result, err := r.command.TerminalReceipt(ctx, request.SessionID)
				if err != nil {
					return nil, err
				}
				result["already_delivered"] = true
				return result, nil
			}
		}
	}
	return r.command.ObserveContext(ctx, request)
}
