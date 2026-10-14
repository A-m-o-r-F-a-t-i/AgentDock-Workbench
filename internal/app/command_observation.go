package app

import "github.com/uvwt/agentdock/internal/activity"

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
