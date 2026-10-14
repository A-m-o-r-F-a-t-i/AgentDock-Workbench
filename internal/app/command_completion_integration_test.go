package app

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/commandcompletion"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

func completionRuntime(t *testing.T) *Runtime {
	t.Helper()
	r, err := newUnrestrictedTestRuntime(config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

// Exercise the same adapter-owned response lifetime as MCP without a transport.
func completionRoot(t *testing.T, r *Runtime, host context.Context, name string, args map[string]any, commit bool) (Result, []commandcompletion.Event) {
	t.Helper()
	ctx, pending := BeginToolResponse(host)
	defer r.AbortCommandCompletionResponse(pending)
	defer r.FinishToolResponse(ctx, pending, false)
	result, err := r.Call(ctx, name, args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	r.PrepareCommandCompletionResponse(ctx, pending, name, args, map[string]any{"structuredContent": map[string]any(result)})
	events := pending.CompletedAdditions().CommandCompletions
	if commit {
		if err := r.CommitCommandCompletionResponse(ctx, pending); err != nil {
			t.Fatal(err)
		}
	}
	return result, events
}
func completionCommand(code int) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("Start-Sleep -Milliseconds 200; Write-Output completion-output; exit %d", code)
	}
	return fmt.Sprintf("sleep 0.2; printf 'completion-output\\n'; exit %d", code)
}
func runDeferredCompletion(t *testing.T, r *Runtime, ctx context.Context, code int) Result {
	t.Helper()
	result, err := r.Call(ctx, "exec_command", map[string]any{"cmd": completionCommand(code), "execution_mode": "async", "timeout_ms": 15000})
	if err != nil || stringArg(result, "status") != "running" {
		t.Fatalf("async start: %v %v", result, err)
	}
	r.commandSettlementWG.Wait()
	return result
}

func TestCommandCompletionRealProcessNextRootAndOriginalTask(t *testing.T) {
	for _, code := range []int{0, 7} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			r := completionRuntime(t)
			ctx := scopeHost("real-completion")
			task := scopeTask(t, r, ctx, "original task")
			started := runDeferredCompletion(t, r, ctx, code)
			// A later task switch must not rewrite the completed command's provenance.
			_ = scopeTask(t, r, ctx, "different current task")
			_, events := completionRoot(t, r, ctx, "list_dir", map[string]any{"path": ".", "max_entries": 1}, true)
			if len(events) != 1 {
				t.Fatalf("events=%v", events)
			}
			e := events[0]
			if e.OriginalCallID != stringArg(started, "call_id") || e.SessionID != stringArg(started, "session_id") || e.TaskID != task || e.ActivityEventID == "" || e.ExitCode == nil || *e.ExitCode != code {
				t.Fatalf("wrong terminal fact: %+v", e)
			}
			want := "succeeded"
			if code != 0 {
				want = "failed"
			}
			if e.Status != want {
				t.Fatal(e.Status)
			}
			page, err := r.activity.Query(t.Context(), activity.Query{CallID: e.OriginalCallID})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, saved := range page.Events {
				found = found || saved.EventID == e.ActivityEventID && saved.Kind == "command.completed"
			}
			if !found {
				t.Fatal("notification did not reference persisted terminal")
			}
			// Notification construction must not consume the model output cursor.
			output, err := r.Call(ctx, "session_observe", map[string]any{"action": "status", "session_id": e.SessionID, "wait_ms": 0})
			if err != nil {
				t.Fatal(err)
			}
			if stringArg(output, "stdout") != "completion-output\n" && stringArg(output, "stdout") != "completion-output\r\n" {
				t.Fatalf("output cursor consumed: %v", output)
			}
			_, again := completionRoot(t, r, ctx, "list_dir", map[string]any{"path": "."}, true)
			if len(again) != 0 {
				t.Fatal("command completion repeated")
			}
		})
	}
}

func TestCommandCompletionDirectObservationSuppressesAttachment(t *testing.T) {
	r := completionRuntime(t)
	ctx := scopeHost("direct-observation")
	started := runDeferredCompletion(t, r, ctx, 0)
	_, events := completionRoot(t, r, ctx, "session_observe", map[string]any{"action": "status", "session_id": stringArg(started, "session_id"), "wait_ms": 0}, true)
	if len(events) != 0 {
		t.Fatal("same terminal fact duplicated within direct observation")
	}
	_, events = completionRoot(t, r, ctx, "list_dir", map[string]any{"path": "."}, true)
	if len(events) != 0 {
		t.Fatal("directly observed terminal was replayed")
	}
}

func TestCommandCompletionUnencodedResponseReleasesClaim(t *testing.T) {
	r := completionRuntime(t)
	ctx := scopeHost("unencoded-completion")
	runDeferredCompletion(t, r, ctx, 0)
	_, uncommitted := completionRoot(t, r, ctx, "list_dir", map[string]any{"path": "."}, false)
	_, committed := completionRoot(t, r, ctx, "list_dir", map[string]any{"path": "."}, true)
	if len(uncommitted) != 1 || len(committed) != 1 || uncommitted[0].EventID != committed[0].EventID {
		t.Fatal("unencoded response consumed or changed event")
	}
}

func TestCommandCompletionSynchronousResultNeedsNoLaterNotice(t *testing.T) {
	r := completionRuntime(t)
	ctx := scopeHost("sync-completion")
	result, err := r.Call(ctx, "exec_command", map[string]any{"cmd": "echo sync-output", "execution_mode": "sync", "timeout_ms": 15000})
	if err != nil || result["command_ok"] != true {
		t.Fatal(result, err)
	}
	r.commandSettlementWG.Wait()
	_, events := completionRoot(t, r, ctx, "list_dir", map[string]any{"path": "."}, true)
	if len(events) != 0 {
		t.Fatal("synchronous result redundantly queued")
	}
}

func TestCommandCompletionRequiresDurableTerminalFact(t *testing.T) {
	r := completionRuntime(t)
	ctx := scopeHost("durable-completion")
	entry := scopeRead(t, r, ctx)
	binding := activity.Binding{SourceOwnerKey: activity.SourceOwnerKey(ctx), ConversationID: stringArg(entry, "conversation_id"), CallID: "call_11111111111111111111111111111111"}
	result := session.TerminalOutcome{SessionID: "session-unpublished", Status: "unknown", CompletedAt: time.Now()}
	r.enqueueCommandCompletion(binding, result)
	_, events := completionRoot(t, r, ctx, "list_dir", map[string]any{"path": "."}, true)
	if len(events) != 0 {
		t.Fatal("unpersisted terminal fact was delivered")
	}
}
