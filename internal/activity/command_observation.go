package activity

// CommandObservation links independent root RPCs for presentation only.
type CommandObservation struct {
	OriginCallID       string `json:"origin_call_id,omitempty"`
	RoutineObservation bool   `json:"routine_observation,omitempty"`
}

func applyCommandObservation(call *ExecutionCall, event Event) {
	if event.Kind != "call.completed" || event.OriginCallID == "" {
		return
	}
	if !validIdentifier(event.OriginCallID) || event.OriginCallID == event.CallID || (event.ToolName != "session_observe" && event.ToolName != "session_act") {
		call.HistoryIncomplete = true
		return
	}
	if call.OriginCallID != "" && call.OriginCallID != event.OriginCallID {
		call.HistoryIncomplete = true
		return
	}
	call.CommandObservation = event.CommandObservation
	call.RoutineObservation = call.RoutineObservation && event.ToolName == "session_observe" && event.Status == "succeeded"
}
