package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

func (svc *Service) SetActivityStore(store *activity.Store) { svc.activity = store }

func (svc *Service) SessionBinding(id string) (activity.Binding, bool) {
	s, ok := svc.sessions.Get(id)
	if !ok {
		svc.activityMu.Lock()
		for _, candidate := range svc.activeCommands {
			if candidate.ID == id {
				s, ok = candidate, true
				break
			}
		}
		svc.activityMu.Unlock()
		if !ok {
			return activity.Binding{}, false
		}
	}
	return s.Summary().Binding, true
}

// ActivitySessionRunning is a non-consuming availability check. The kill operation
// checks completion again so a process exiting between observation and action is safe.
func (svc *Service) ActivitySessionRunning(id string) bool {
	s, ok := svc.sessions.Get(id)
	if !ok {
		return false
	}
	select {
	case <-s.Done:
		return false
	default:
		return true
	}
}

func (svc *Service) trackCommandActivity(s *session.Session, request ExecRequest, journal *activity.AppendReservation) <-chan struct{} {
	done := make(chan struct{})
	svc.registerTerminalFuture(s)
	if svc.activity == nil {
		svc.activityWG.Add(1)
		go func() {
			defer svc.activityWG.Done()
			defer close(done)
			select {
			case <-s.Done:
				s.PublishTerminal(commandTerminalOutcome(s.ActivitySnapshot(&session.OutputCursor{})), nil)
			case <-svc.activityCtx.Done():
				select {
				case <-s.Done:
					s.PublishTerminal(commandTerminalOutcome(s.ActivitySnapshot(&session.OutputCursor{})), nil)
				default:
					s.PublishTerminal(unconfirmedCommandTerminal(s.ID, "Command was still running when the runtime closed; its final outcome is unknown."), nil)
				}
			}
		}()
		return done
	}
	secrets := append([]string{}, request.AuditSecrets...)
	for _, value := range request.Env {
		secrets = append(secrets, value)
	}
	// Only in-memory values are used. Neither the environment map nor its keys are journaled.
	for _, pair := range s.Command.Env {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(key)
		for _, marker := range []string{"TOKEN", "PASSWORD", "PASSWD", "SECRET", "API_KEY", "APIKEY", "AUTHORIZATION", "PRIVATE_KEY"} {
			if strings.Contains(upper, marker) {
				secrets = append(secrets, value)
				break
			}
		}
	}
	redactor := activity.NewRedactor(secrets...)
	auditTool := "exec_command"
	if request.AuditToolName == "android_device_read" || request.AuditToolName == "android_device_act" {
		auditTool = request.AuditToolName
	}
	base := activity.Event{Binding: request.Binding, ToolName: auditTool, SessionID: s.ID, DisplayCommand: request.Cmd, Title: request.Label}
	execution := s.Summary()
	base.Runtime, base.Workdir = execution.Runtime, execution.Workdir
	appendEvent := func(event activity.Event) (activity.Event, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var saved activity.Event
		var err error
		if journal != nil && (event.Kind == "command.started" || event.Kind == "command.completed") {
			saved, err = journal.Append(ctx, redactor.Event(event))
		} else {
			saved, err = svc.activity.Append(ctx, redactor.Event(event))
		}
		if err != nil {
			s.SetActivityWarning("Command execution continues; its activity journal is incomplete. Inspect the local activity store.")
			slog.Warn("record command activity", "session_id", s.ID, "kind", event.Kind, "error", err)
		}
		return saved, err
	}
	started := base
	started.Kind, started.Status = "command.started", "running"
	_, _ = appendEvent(started)
	svc.activityMu.Lock()
	svc.activeCommands[s.ID] = s
	svc.activityMu.Unlock()
	svc.activityWG.Add(1)
	go func() {
		if journal != nil {
			defer journal.Close()
		}
		defer func() { svc.activityMu.Lock(); delete(svc.activeCommands, s.ID); svc.activityMu.Unlock() }()
		defer svc.activityWG.Done()
		defer close(done)
		cursor := session.OutputCursor{}
		stdout, stderr := activity.LineBuffer{}, activity.LineBuffer{}
		outTruncated, errTruncated := false, false
		// An eviction may cut a private-key block or credential line. Stop previewing that stream
		// after a gap, while preserving the true completion status and explicit truncation marker.
		stdoutUnsafe, stderrUnsafe := false, false
		tick := time.NewTicker(300 * time.Millisecond)
		defer tick.Stop()
		appendOutput := func(snap session.Snapshot) {
			stdoutUnsafe = stdoutUnsafe || snap.StdoutTruncated
			stderrUnsafe = stderrUnsafe || snap.StderrTruncated
			var out, errout string
			var outCut, errCut bool
			if !stdoutUnsafe {
				out, outCut = stdout.Feed(snap.Stdout, snap.Completed)
			}
			if !stderrUnsafe {
				errout, errCut = stderr.Feed(snap.Stderr, snap.Completed)
			}
			outTruncated = outTruncated || outCut || stdoutUnsafe
			errTruncated = errTruncated || errCut || stderrUnsafe
			if out != "" || errout != "" || outCut || errCut || snap.StdoutTruncated || snap.StderrTruncated {
				event := base
				event.Kind, event.Status = "command.output", "running"
				event.ElapsedMS = snap.ElapsedMS
				event.OutputPreview, event.StderrPreview = out, errout
				event.StdoutTruncated, event.StderrTruncated = outTruncated, errTruncated
				_, _ = appendEvent(event)
			}
		}
		persistTerminal := func(snap session.Snapshot, outcome session.TerminalOutcome) {
			event := base
			event.Kind, event.Status = "command.completed", outcome.Status
			event.ExitCode, event.CommandOK = outcome.ExitCode, outcome.CommandOK
			event.TimedOut, event.ElapsedMS = outcome.TimedOut, snap.ElapsedMS
			event.StdoutTruncated, event.StderrTruncated = outTruncated, errTruncated
			event.Summary = outcome.Summary
			persisted, err := appendEvent(event)
			if persisted.EventID == "" && persisted.Seq == 0 {
				if err == nil {
					err = errors.New("command terminal append returned no persisted event")
				}
				fallback := unconfirmedCommandTerminal(s.ID, "Command terminal facts could not be persisted; verify the original operation before retrying.")
				s.PublishTerminal(fallback, fmt.Errorf("persist command terminal fact: %w", err))
				return
			}
			outcome.ActivityEventID = persisted.EventID
			s.PublishTerminal(outcome, nil)
		}
		for {
			snap := s.ActivitySnapshot(&cursor)
			appendOutput(snap)
			if snap.Completed {
				persistTerminal(snap, commandTerminalOutcome(snap))
				return
			}
			select {
			case <-s.Done:
			case <-tick.C:
			case <-svc.activityCtx.Done():
				final := s.ActivitySnapshot(&cursor)
				if final.Completed {
					appendOutput(final)
					persistTerminal(final, commandTerminalOutcome(final))
				} else {
					// The activity owner is ending, so flush any final partial line even
					// though the underlying process did not yield a confirmed exit.
					final.Completed = true
					appendOutput(final)
					persistTerminal(final, unconfirmedCommandTerminal(s.ID, "Command was still running when the runtime closed; its final outcome is unknown."))
				}
				return
			}
		}
	}()
	return done
}

func waitCommandActivity(done <-chan struct{}) {
	select {
	case <-done:
	case <-time.After(6 * time.Second):
	}
}

func (svc *Service) WaitActivity(ctx context.Context) error {
	done := make(chan struct{})
	go func() { svc.activityWG.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func addBindingResult(result Result, binding activity.Binding) {
	if binding.ConversationID != "" {
		result["conversation_id"] = binding.ConversationID
	}
	if binding.CallID != "" {
		result["call_id"] = binding.CallID
	}
	if binding.TaskID != "" {
		result["task_id"] = binding.TaskID
	}
	if binding.ThreadID != "" {
		result["thread_id"] = binding.ThreadID
	}
	if binding.StepID != "" {
		result["step_id"] = binding.StepID
	}
	if binding.WorkspaceID != "" {
		result["workspace_id"] = binding.WorkspaceID
	}
}

// TaskActivityRunning includes synchronous commands that have not yet returned
// a public session. It is used by the server-side task deletion guard.
func (svc *Service) TaskActivityRunning(taskID string) bool {
	svc.activityMu.Lock()
	defer svc.activityMu.Unlock()
	for _, command := range svc.activeCommands {
		if command.Summary().Binding.TaskID != taskID {
			continue
		}
		select {
		case <-command.Done:
		default:
			return true
		}
	}
	return false
}
func (svc *Service) CallActivityRunning(callID string) bool {
	svc.activityMu.Lock()
	defer svc.activityMu.Unlock()
	for _, command := range svc.activeCommands {
		if command.Summary().Binding.CallID != callID {
			continue
		}
		select {
		case <-command.Done:
		default:
			return true
		}
	}
	return false
}

// ActiveConversationBindings is an in-memory, non-consuming liveness snapshot.
// A stale durable 'running' label cannot keep a project open indefinitely.
func (svc *Service) ActiveConversationBindings() []activity.Binding {
	svc.activityMu.Lock()
	commands := make([]*session.Session, 0, len(svc.activeCommands))
	for _, command := range svc.activeCommands {
		commands = append(commands, command)
	}
	svc.activityMu.Unlock()
	result := make([]activity.Binding, 0, len(commands))
	for _, command := range commands {
		select {
		case <-command.Done:
		default:
			result = append(result, command.Summary().Binding)
		}
	}
	return result
}
func (svc *Service) StopActivityCall(ctx context.Context, callID string) (bool, error) {
	svc.activityMu.Lock()
	var target *session.Session
	for _, command := range svc.activeCommands {
		if command.Summary().Binding.CallID == callID {
			target = command
			break
		}
	}
	svc.activityMu.Unlock()
	if target == nil {
		return false, errors.New("the command is no longer running in this runtime")
	}
	select {
	case <-target.Done:
		return true, nil
	default:
	}
	target.Kill()
	target.Cancel()
	select {
	case <-target.Done:
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(3 * time.Second):
		return false, nil
	}
}
