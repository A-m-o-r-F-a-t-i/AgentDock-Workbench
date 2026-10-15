package command

import (
	"context"
	"errors"
	"maps"

	"github.com/uvwt/agentdock/internal/activity"
)

type observationKey struct {
	owner, conversation, task, thread, workspace, session string
	maxBytes, waitMS                                      int
}

type sharedObservation struct {
	done   chan struct{}
	result Result
	err    error
}

// Share only identical in-flight observations in the same authenticated scope.
// Completed pages are not cached: the next request must receive unread bytes.
func (s *Service) observeSession(ctx context.Context, request SessionObserveRequest) (Result, error) {
	binding := activity.FromContext(ctx)
	if binding.SourceOwnerKey == "" || binding.ConversationID == "" {
		return s.sessionStatusContext(ctx, request)
	}
	key := observationKey{
		owner: binding.SourceOwnerKey, conversation: binding.ConversationID,
		task: binding.TaskID, thread: binding.ThreadID, workspace: binding.WorkspaceID,
		session: request.SessionID, maxBytes: commandOutputLimit(request.MaxOutputBytes),
		waitMS: intValue(request.WaitMS, -1),
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.observationMu.Lock()
		if current := s.observations[key]; current != nil {
			s.observationMu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-current.done:
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// Cancellation belongs to the original reader, not its followers.
			// A remaining reader may await the same session with its own context.
			if errors.Is(current.err, context.Canceled) || errors.Is(current.err, context.DeadlineExceeded) {
				continue
			}
			result := cloneObservationResult(current.result)
			if result != nil {
				result["observation_shared"] = true
			}
			return result, current.err
		}
		if len(s.observations) >= maxRetainedCommandSessions {
			s.observationMu.Unlock()
			return s.sessionStatusContext(ctx, request)
		}
		current := &sharedObservation{done: make(chan struct{})}
		if s.observations == nil {
			s.observations = make(map[observationKey]*sharedObservation)
		}
		s.observations[key] = current
		s.observationMu.Unlock()

		result, err := s.sessionStatusContext(ctx, request)
		s.observationMu.Lock()
		current.result, current.err = cloneObservationResult(result), err
		delete(s.observations, key)
		close(current.done)
		s.observationMu.Unlock()
		return result, err
	}
}

func cloneObservationResult(result Result) Result {
	cloned := maps.Clone(result)
	for key, value := range cloned {
		if object, ok := value.(map[string]any); ok {
			cloned[key] = maps.Clone(object)
		}
	}
	return cloned
}
