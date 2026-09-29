package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/androidbridge"
	"github.com/uvwt/agentdock/internal/permission"
)

func TestAndroidInitialInputFailureRetainsOriginalSession(t *testing.T) {
	r, ctx := profileRuntime(t, permission.DefaultSettings(), permission.Full)
	caps := map[string]androidbridge.Capability{"termux_host": {Ready: true, State: "verified"}}
	lease, err := r.androidExecutor.Register(androidbridge.RegisterRequest{Protocol: 1, WorkerID: strings.Repeat("d", 32), Backends: caps})
	if err != nil {
		t.Fatal(err)
	}
	type response struct {
		result Result
		err    error
	}
	returned := make(chan response, 1)
	go func() {
		result, e := r.Call(ctx, "exec_command", map[string]any{"backend": "termux_host", "cmd": "fake-peer-command", "workdir": "/data/data/com.termux/files/home", "stdin": "sample", "execution_mode": "async"})
		returned <- response{result, e}
	}()
	var command androidbridge.Instruction
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		batch, e := r.androidExecutor.Exchange(lease.LeaseToken, androidbridge.ExchangeRequest{Protocol: 1, Backends: caps})
		if e != nil {
			t.Fatal(e)
		}
		if len(batch.Commands) == 1 {
			command = batch.Commands[0]
			if len(command.Input) > 0 {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(command.Input) != 1 {
		t.Fatal("missing admitted command/input", command)
	}
	// The peer exits before confirming stdin. This closes the writer while the
	// original process still has an independently confirmed terminal result.
	code := 0
	_, err = r.androidExecutor.Exchange(lease.LeaseToken, androidbridge.ExchangeRequest{Protocol: 1, Backends: caps,
		Events: []androidbridge.Event{{OperationID: command.OperationID, Revision: command.Revision, State: "exited", ExitCode: &code, InputApplied: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-returned:
		if got.err != nil || stringArg(got.result, "session_id") == "" || stringArg(got.result, "stdin_error") == "" {
			t.Fatal(got.result, got.err)
		}
		assertToolResultMatchestestOutputSchema(t, "exec_command", got.result)
		_, e := r.Call(ctx, "session_observe", map[string]any{"action": "status", "session_id": got.result["session_id"]})
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("original request did not settle after peer exit")
	}
	wait, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = r.command.WaitActivity(wait); err != nil {
		t.Fatal(err)
	}
}
func TestOversizedAndroidInitialInputNeverStartsAProcess(t *testing.T) {
	r, ctx := profileRuntime(t, permission.DefaultSettings(), permission.Full)
	_, err := r.Call(ctx, "exec_command", map[string]any{"backend": "termux_host", "cmd": "must-not-run", "workdir": "/", "stdin": strings.Repeat("x", 16385)})
	if err == nil {
		t.Fatal("oversized input admitted")
	}
	if r.androidExecutor.Status()["inflight"] != 0 {
		t.Fatal("validation started a process")
	}
}
