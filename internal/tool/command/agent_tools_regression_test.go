package command

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/tool/command/session"
)

func TestAgentToolsExecRejectsCancelledPreparation(t *testing.T) {
	for _, phase := range []string{"before_prepare", "during_prepare", "prepare_deadline"} {
		t.Run(phase, func(t *testing.T) {
			svc, cfg := newCommandTestService(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var acquired, released atomic.Int32
			if phase == "before_prepare" {
				cancel()
			}
			svc.resolveSkill = func(preparing context.Context, _ string) (SkillLease, error) {
				acquired.Add(1)
				if phase == "prepare_deadline" {
					<-preparing.Done()
				} else {
					cancel()
				}
				// A lease can become available at the same instant as cancellation.
				return SkillLease{Name: "audit", Root: cfg.AgentDockDefaultDir, Release: func() { released.Add(1) }}, nil
			}
			command := "printf spawned > late-start.txt"
			if runtime.GOOS == "windows" {
				command = "Set-Content -LiteralPath 'late-start.txt' -Value 'spawned'"
			}
			timeout := 10000
			wantErr := error(context.Canceled)
			if phase == "prepare_deadline" {
				timeout = 30
				wantErr = context.DeadlineExceeded
			}
			result, err := svc.Exec(ctx, ExecRequest{Cmd: command, SkillRef: "audit", ExecutionMode: "sync", TimeoutMS: &timeout})
			if !errors.Is(err, wantErr) {
				t.Errorf("cancelled preparation returned result=%#v err=%v; want %v without dispatch", result, err, wantErr)
			}
			// Drain any wrongly started command before checking its side effect.
			for _, s := range svc.sessions.List() {
				select {
				case <-s.Done:
				case <-time.After(5 * time.Second):
					t.Fatal("wrongly dispatched command did not finish")
				}
			}
			if _, statErr := os.Stat(filepath.Join(cfg.AgentDockDefaultDir, "late-start.txt")); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("cancelled request executed its command: marker stat=%v", statErr)
			}
			if len(svc.sessions.List()) != 0 || svc.sessions.ReservationCount() != 0 || svc.sessions.StartingCount() != 0 {
				t.Error("cancelled preparation registered a session or leaked a reservation")
			}
			if phase == "before_prepare" && acquired.Load() != 0 {
				t.Error("already cancelled request still acquired a Skill lease")
			}
			if err != nil && released.Load() != acquired.Load() {
				t.Errorf("Skill lease leaked: acquired=%d released=%d", acquired.Load(), released.Load())
			}
		})
	}
}

type auditFailedStdin struct{ err error }

func (w auditFailedStdin) Write([]byte) (int, error) { return 0, w.err }
func (w auditFailedStdin) Close() error {
	return nil
}

func TestAgentToolsSessionWriteReportsClosedInput(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"closed_pipe", io.ErrClosedPipe},
		{"closed_file", os.ErrClosed},
		{"other_write_failure", errors.New("injected stdin failure")},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := newCommandTestService(t)
			s := &session.Session{ID: "session-audit-stdin", Stdin: auditFailedStdin{test.err}, Done: make(chan struct{}), Cancel: func() {}, StartedAt: time.Now()}
			svc.sessions.Add(s)
			t.Cleanup(func() { svc.sessions.Delete(s.ID) })
			result, err := svc.Act(SessionActRequest{Action: "write", SessionID: s.ID, Chars: "not delivered"})
			if !errors.Is(err, test.err) {
				t.Fatalf("failed stdin write was hidden: result=%#v err=%v; want %v", result, err, test.err)
			}
		})
	}
}

func TestAgentToolsMutationPreservesFinalOutput(t *testing.T) {
	for _, test := range []struct {
		name    string
		action  string
		running bool
	}{
		{"write_completed", "write", false},
		{"kill_completed", "kill", false},
		{"kill_running", "kill", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, cfg := newCommandTestService(t)
			command := "printf 'final-stdout'; printf 'final-stderr' >&2"
			if test.running {
				command += "; sleep 20"
			}
			if runtime.GOOS == "windows" {
				command = "[Console]::Out.Write('final-stdout'); [Console]::Error.Write('final-stderr')"
				if test.running {
					command += "; Start-Sleep -Seconds 20"
				}
			}
			s, _, err := session.Start(context.Background(), command, cfg.AgentDockDefaultDir, os.Environ(), 30*time.Second, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = s.Kill(); s.Cancel() })
			svc.sessions.Add(s)
			if test.running {
				deadline := time.Now().Add(5 * time.Second)
				for {
					peek := s.Peek("running", 1024)
					if peek.Stdout == "final-stdout" && peek.Stderr == "final-stderr" {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("fixture did not emit its readiness output")
					}
					time.Sleep(5 * time.Millisecond)
				}
			} else {
				select {
				case <-s.Done:
				case <-time.After(5 * time.Second):
					t.Fatal("fixture did not finish")
				}
			}
			result, err := svc.Act(SessionActRequest{Action: test.action, SessionID: s.ID})
			if err != nil {
				t.Fatal(err)
			}
			if result["stdout"] != "final-stdout" || result["stderr"] != "final-stderr" {
				t.Fatalf("mutation lost its output preview: %#v", result)
			}
			observed, err := svc.Observe(SessionObserveRequest{Action: "status", SessionID: s.ID})
			if err != nil {
				t.Fatal(err)
			}
			if observed["stdout"] != "final-stdout" || observed["stderr"] != "final-stderr" {
				t.Fatalf("mutation consumed output before status: %#v", observed)
			}
			again, err := svc.Observe(SessionObserveRequest{Action: "status", SessionID: s.ID})
			if err != nil || again["stdout"] != "" || again["stderr"] != "" {
				t.Fatalf("status no longer consumes output deltas: %#v %v", again, err)
			}
		})
	}
}

func TestAgentToolsStartedCommandSurvivesRequestCancellation(t *testing.T) {
	for _, mode := range []string{"async", "auto", "sync"} {
		t.Run(mode, func(t *testing.T) {
			svc, cfg := newCommandTestService(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			command := "printf ready > started; while [ ! -f continue ]; do sleep 0.02; done; printf survived"
			if runtime.GOOS == "windows" {
				command = "Set-Content -LiteralPath 'started' -Value 'ready'; while (-not (Test-Path -LiteralPath 'continue')) { Start-Sleep -Milliseconds 20 }; [Console]::Out.Write('survived')"
			}
			type response struct {
				result Result
				err    error
			}
			returned := make(chan response, 1)
			go func() {
				timeout, yield := 30000, 30000
				result, err := svc.Exec(ctx, ExecRequest{Cmd: command, ExecutionMode: mode, TimeoutMS: &timeout, YieldTimeMS: &yield})
				returned <- response{result, err}
			}()
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(cfg.AgentDockDefaultDir, "started")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("command did not reach the post-start barrier")
				}
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
			var got response
			select {
			case got = <-returned:
			case <-time.After(5 * time.Second):
				t.Fatal("request cancellation did not release the foreground waiter")
			}
			if got.err != nil || got.result["status"] != "running" {
				t.Fatalf("started command was not retained: %#v %v", got.result, got.err)
			}
			id, _ := got.result["session_id"].(string)
			s, ok := svc.sessions.Get(id)
			if !ok {
				t.Fatalf("retained session %q is missing", id)
			}
			if err := os.WriteFile(filepath.Join(cfg.AgentDockDefaultDir, "continue"), []byte("continue"), 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case <-s.Done:
			case <-time.After(5 * time.Second):
				t.Fatal("retained command did not finish after barrier release")
			}
			observed, err := svc.Observe(SessionObserveRequest{Action: "status", SessionID: id})
			if err != nil || observed["command_ok"] != true || observed["stdout"] != "survived" {
				t.Fatalf("request cancellation killed the already started process: %#v %v", observed, err)
			}
		})
	}
}
