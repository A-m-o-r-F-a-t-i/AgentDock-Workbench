package command

import (
	"context"
	"errors"
	"maps"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

type observationKey struct {
	owner, conversation, task, thread, workspace, session string
	maxBytes, waitMS                                      int
	stdout, stderr                                        int
	explicitCursor                                        bool
}

type sharedObservation struct {
	done   chan struct{}
	result Result
	err    error
}

// Share only identical in-flight observations in the same authenticated scope.
// Completed pages are not cached: the next request must receive unread bytes.
func (s *Service) observeSession(ctx context.Context, request SessionObserveRequest) (result Result, failure error) {
	defer func() {
		if failure == nil {
			// The shared flight may already be visible to other readers. Never
			// decorate that published map in place with per-request diagnostics.
			result = maps.Clone(result)
			s.recordObservationFacts(ctx, request, result)
		}
	}()
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
	if request.Offset != nil {
		key.stdout, key.stderr, key.explicitCursor = request.Offset.Stdout, request.Offset.Stderr, true
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

// This bounded cache is diagnostic only. It owns neither output nor process state.
type observationScope struct{ owner, conversation, workspace, session string }
type observationReceipt struct {
	stdout, stderr, count int
	at                    time.Time
}

func (s *Service) recordObservationFacts(ctx context.Context, request SessionObserveRequest, result Result) {
	binding := activity.FromContext(ctx)
	if result == nil || binding.SourceOwnerKey == "" || binding.ConversationID == "" || result["observation_shared"] == true {
		return
	}
	key := observationScope{binding.SourceOwnerKey, binding.ConversationID, binding.WorkspaceID, request.SessionID}
	s.observationMu.Lock()
	defer s.observationMu.Unlock()
	if result["changed"] != false {
		delete(s.observationHistory, key)
		return
	}
	offset, _ := result["next_offset"].(map[string]any)
	stdout, _ := offset["stdout"].(int)
	stderr, _ := offset["stderr"].(int)
	last, exists := s.observationHistory[key]
	count := 1
	if exists && last.stdout == stdout && last.stderr == stderr {
		count += last.count
	}
	if s.observationHistory == nil {
		s.observationHistory = make(map[observationScope]observationReceipt)
	}
	if !exists && len(s.observationHistory) >= maxRetainedCommandSessions {
		var oldest observationScope
		var stamp time.Time
		for candidate, value := range s.observationHistory {
			if stamp.IsZero() || value.at.Before(stamp) {
				oldest, stamp = candidate, value.at
			}
		}
		delete(s.observationHistory, oldest)
	}
	s.observationHistory[key] = observationReceipt{stdout, stderr, count, time.Now()}
	result["unchanged_observation_count"] = count
}
