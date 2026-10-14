package command

import (
	"context"
	"strings"
)

func (s *Service) Observe(request SessionObserveRequest) (Result, error) {
	return s.ObserveContext(context.Background(), request)
}

// ObserveContext cancels only this waiter, never the detached command process.
func (s *Service) ObserveContext(ctx context.Context, request SessionObserveRequest) (Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	action := strings.ToLower(strings.TrimSpace(request.Action))
	switch action {
	case "list":
		if strings.TrimSpace(request.SessionID) != "" || request.MaxOutputBytes != nil || request.WaitMS != nil {
			return nil, toolErrorDetails(
				"INVALID_ARGUMENT",
				"session_id, max_output_bytes and wait_ms are invalid for session_observe action=list",
				"validation",
				map[string]any{"action": action, "allowed_arguments": []string{"action"}},
			)
		}
		return s.listSessions()
	case "status":
		if strings.TrimSpace(request.SessionID) == "" {
			return nil, toolErrorDetails(
				"INVALID_ARGUMENT",
				"session_id is required for session_observe action=status",
				"validation",
				map[string]any{"action": action, "required": []string{"session_id"}},
			)
		}
		return s.sessionStatusContext(ctx, request)
	default:
		return nil, toolErrorDetails("INVALID_ACTION", "session_observe action must be list or status", "validation", map[string]any{"action": request.Action, "allowed": []string{"list", "status"}})
	}
}

func (s *Service) Act(request SessionActRequest) (Result, error) {
	action := strings.ToLower(strings.TrimSpace(request.Action))
	switch action {
	case "write":
		return s.writeStdin(request)
	case "kill":
		return s.killSession(request)
	case "kill_all":
		return s.killAll()
	default:
		return nil, toolErrorDetails("INVALID_ACTION", "unsupported session_act action", "validation", map[string]any{"action": request.Action, "allowed": []string{"write", "kill", "kill_all"}})
	}
}
