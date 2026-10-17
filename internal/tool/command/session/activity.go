package session

import (
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

// OutputCursor belongs to one observer. Absolute byte offsets survive ring-buffer eviction.
type OutputCursor struct {
	Stdout int `json:"stdout"`
	Stderr int `json:"stderr"`
}

func (s *Session) SetActivityBinding(binding activity.Binding) {
	s.mu.Lock()
	s.activityBinding = binding
	s.mu.Unlock()
}

func (s *Session) SetActivityWarning(message string) {
	s.mu.Lock()
	s.activityWarning = message
	s.mu.Unlock()
}

func (s *Session) ActivitySnapshot(cursor *OutputCursor) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, outGap := activityOutput(&s.stdout, s.stdoutTotalBytes, s.stdoutDroppedBytes, cursor.Stdout)
	errout, errGap := activityOutput(&s.stderr, s.stderrTotalBytes, s.stderrDroppedBytes, cursor.Stderr)
	cursor.Stdout, cursor.Stderr = s.stdoutTotalBytes, s.stderrTotalBytes
	finished, status := time.Now(), s.statusLocked()
	if s.completed {
		finished = s.FinishedAt
	}

	return Snapshot{
		Binding: s.activityBinding, ActivityWarning: s.activityWarning, SessionID: s.ID,
		Status: status, Stdout: out, Stderr: errout, ElapsedMS: finished.Sub(s.StartedAt).Milliseconds(),
		TimedOut: s.TimedOut, Terminal: s.Terminal, Completed: s.completed, CompletedAt: s.FinishedAt, ExitCode: s.exitCode, OutcomeUnknown: s.outcomeUnknown,
		CommandOK:            s.completed && s.exitCode == 0 && s.waitErr == nil && !s.TimedOut && !s.terminationRequested,
		TerminationRequested: s.terminationRequested,
		StdoutTotalBytes:     s.stdoutTotalBytes, StderrTotalBytes: s.stderrTotalBytes,
		StdoutDroppedBytes: s.stdoutDroppedBytes, StderrDroppedBytes: s.stderrDroppedBytes,
		StdoutTruncated: outGap, StderrTruncated: errGap,
		Runtime: s.execution.Runtime, WSLDistribution: s.execution.Distribution, Workdir: s.execution.Workdir,
	}
}

func activityOutput(buffer *outputBuffer, total, dropped, after int) (string, bool) {
	gap := after < dropped
	start := max(after, dropped) - dropped
	if start >= buffer.Len() || after >= total {
		return "", gap
	}
	value, _ := buffer.stringFrom(start, 0)
	return value, gap
}
