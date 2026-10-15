package activity

import "context"

// CommandSession reads retained terminal facts only. Request authorization is
// enforced by the calling Runtime and command service before exposing the result.
func (s *Store) CommandSession(ctx context.Context, sessionID string) (ExecutionCall, error) {
	release, err := s.lock(ctx)
	if err != nil {
		return ExecutionCall{}, err
	}
	defer release()
	projection, err := s.projectionLocked(ctx)
	if err != nil {
		return ExecutionCall{}, err
	}
	var selected *ExecutionCall
	for _, call := range projection.calls {
		if call.SessionID != sessionID || call.ToolName != "exec_command" || call.ParentCallID != "" || call.DeletedAt != nil || !CallTerminal(call.Status) {
			continue
		}
		if selected == nil || call.UpdatedSeq > selected.UpdatedSeq {
			selected = call
		}
	}
	if selected == nil {
		return ExecutionCall{}, ErrCallNotFound
	}
	return cloneCall(selected, false, true), nil
}
