package session

import (
	"errors"
	"testing"
	"time"
)

func TestAgentToolsCommandSuccessRequiresCleanCompletion(t *testing.T) {
	for _, test := range []struct {
		name      string
		completed bool
		exitCode  int
		killed    bool
		timedOut  bool
		waitErr   error
		wantOK    bool
	}{
		{name: "clean_exit", completed: true, wantOK: true},
		{name: "nonzero_exit", completed: true, exitCode: 7},
		{name: "killed_zero_exit", completed: true, killed: true},
		{name: "timeout_zero_exit", completed: true, timedOut: true},
		{name: "wait_error_zero_exit", completed: true, waitErr: errors.New("output copy failed")},
		{name: "still_running", completed: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now()
			s := &Session{ID: "session-success", StartedAt: now.Add(-time.Second), FinishedAt: now,
				completed: test.completed, exitCode: test.exitCode, terminationRequested: test.killed,
				TimedOut: test.timedOut, waitErr: test.waitErr}
			for _, snapshot := range []Snapshot{s.Peek("running", 1024), s.Snapshot("running", 1024)} {
				if snapshot.CommandOK != test.wantOK {
					t.Errorf("status=%s completed=%t exit_code=%d command_ok=%t; want %t", snapshot.Status, snapshot.Completed, snapshot.ExitCode, snapshot.CommandOK, test.wantOK)
				}
			}
		})
	}
}
