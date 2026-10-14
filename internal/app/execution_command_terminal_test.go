package app

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

func TestSettlementGuardRunsOnlyOnce(t *testing.T) {
	var guard SettlementGuard
	var calls atomic.Int32
	want := errors.New("settled")
	var wait sync.WaitGroup
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := guard.Settle(func() error {
				calls.Add(1)
				return want
			}); !errors.Is(err, want) {
				t.Errorf("settlement error = %v", err)
			}
		}()
	}
	wait.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("settlement ran %d times", got)
	}
}

func TestTerminalSettlementPersistsUnknownFallbackOnce(t *testing.T) {
	r := executionTestRuntime(t)
	callID, err := activity.NewExecutionID("call_")
	if err != nil {
		t.Fatal(err)
	}
	binding := activity.Binding{CallID: callID}
	if err = r.appendExecution(activity.Event{Binding: binding, Kind: "call.created", Status: "created", ToolName: "exec_command", Title: "fallback"}); err != nil {
		t.Fatal(err)
	}
	if err = r.appendExecution(activity.Event{Binding: binding, Kind: "call.started", Status: "running", ToolName: "exec_command"}); err != nil {
		t.Fatal(err)
	}
	completion, err := r.activity.ReserveAppend(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	command := &session.Session{ID: "session-fallback"}
	future := command.TerminalFuture()
	command.PublishTerminal(session.TerminalOutcome{
		SessionID: command.ID, Status: "unknown", Summary: "journal failed", CompletedAt: time.Now(),
	}, errors.New("journal failure"))
	settlement := &TerminalSettlement{
		runtime: r, future: future, completion: completion, binding: binding,
		toolName: "exec_command", title: "fallback", sessionID: command.ID, started: time.Now().Add(-time.Second),
	}
	if err = settlement.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = settlement.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	call, err := r.activity.Call(context.Background(), callID)
	if err != nil {
		t.Fatal(err)
	}
	if call.Status != "unknown" || call.ErrorCode != "COMMAND_TERMINAL_UNAVAILABLE" || call.Summary != "journal failed" {
		t.Fatalf("fallback root call = %+v", call)
	}
	page, err := r.activity.Query(context.Background(), activity.Query{CallID: callID})
	if err != nil {
		t.Fatal(err)
	}
	completed := 0
	for _, event := range page.Events {
		if event.Kind == "call.completed" {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("call.completed count = %d", completed)
	}
}

func TestApprovedAsyncCommandSettlesFromTerminalFuture(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := scopeHost("command-terminal-approval")
	scopeRead(t, r, ctx)
	pending, err := r.Call(ctx, "exec_command", map[string]any{
		"cmd": "echo terminal-approval", "execution_mode": "async",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pending["status"] != "pending_approval" {
		t.Fatalf("command did not require approval: %+v", pending)
	}
	approvalID := stringArg(pending, "approval_id")
	decision, err := r.RuntimeApprovalDecision(ctx, approvalID, "approve", false)
	if err != nil || decision["dispatched"] != true {
		t.Fatalf("approval dispatch = %+v, %v", decision, err)
	}
	call := waitExecutionTerminal(t, r, stringArg(pending, "call_id"))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		approval, lookupErr := r.permissions.Approval(context.Background(), approvalID)
		if lookupErr == nil && approval.Status != "dispatched" {
			if approval.Status != call.Status {
				t.Fatalf("approval status %q != call status %q", approval.Status, call.Status)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("approval was not settled by the terminal notification")
}

func TestRuntimeCloseDrainsCommandTerminalSettlement(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := scopeHost("command-terminal-close")
	scopeRead(t, r, ctx)
	command := "sleep 20"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 20"
	}
	pending, err := r.Call(ctx, "exec_command", map[string]any{"cmd": command, "execution_mode": "async"})
	if err != nil || pending["status"] != "pending_approval" {
		t.Fatalf("pending command = %+v, %v", pending, err)
	}
	approvalID := stringArg(pending, "approval_id")
	decision, err := r.RuntimeApprovalDecision(ctx, approvalID, "approve", false)
	if err != nil || decision["dispatched"] != true {
		t.Fatalf("approval dispatch = %+v, %v", decision, err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	approval, err := r.permissions.Approval(context.Background(), approvalID)
	if err != nil {
		t.Fatal(err)
	}
	if approval.Status == "dispatched" || approval.Status == "pending" {
		t.Fatalf("approval remained unsettled after close: %+v", approval)
	}
	call, err := r.activity.Call(context.Background(), stringArg(pending, "call_id"))
	if err != nil {
		t.Fatal(err)
	}
	if !activity.CallTerminal(call.Status) {
		t.Fatalf("root call remained running after close: %+v", call)
	}
}
