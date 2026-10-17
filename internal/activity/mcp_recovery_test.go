package activity

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMCPRecoveryConcurrentRefreshAndSingleRetry(t *testing.T) {
	safe := true
	store, ctx, origin := recoveryFixture(t, &safe)
	skill := recoveryCall(t, store, ctx, origin, "exec_command", "")
	recoveryTransition(t, store, ctx, origin, skill, MCPRecoveryTransition{Phase: "claim", SkillRef: "skill://recover-demo"})
	if _, err := store.Append(ctx, Event{Binding: skill, Kind: "command.completed", ToolName: "exec_command", Status: "succeeded", CommandOK: &safe}); err != nil {
		t.Fatal(err)
	}
	actors := []Binding{
		recoveryCall(t, store, ctx, origin, "mcp_manage", ""),
		recoveryCall(t, store, ctx, origin, "mcp_manage", ""),
	}
	type outcome struct {
		actor Binding
		err   error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, len(actors))
	for _, actor := range actors {
		go func(actor Binding) {
			<-start
			_, err := store.TransitionMCPRecovery(ctx, origin.CallID, actor, MCPRecoveryTransition{Phase: "refresh", Server: "demo"})
			outcomes <- outcome{actor: actor, err: err}
		}(actor)
	}
	close(start)
	var winner, loser Binding
	for range actors {
		result := <-outcomes
		if result.err == nil {
			if winner.CallID != "" {
				t.Fatal("two concurrent requests acquired the same refresh")
			}
			winner = result.actor
		} else if errors.Is(result.err, ErrRecoveryPending) {
			loser = result.actor
		} else {
			t.Fatalf("refresh error: %v", result.err)
		}
	}
	if winner.CallID == "" || loser.CallID == "" {
		t.Fatal("refresh did not select exactly one owner")
	}
	if _, err := store.TransitionMCPRecovery(ctx, origin.CallID, loser, MCPRecoveryTransition{Phase: "ready", Server: "demo"}); !errors.Is(err, ErrRecoveryScope) {
		t.Fatalf("non-owner completed the refresh: %v", err)
	}
	recoveryTransition(t, store, ctx, origin, winner, MCPRecoveryTransition{Phase: "ready"})
	first := recoveryCall(t, store, ctx, origin, "mcp_tool_call", origin.CallID)
	recoveryTransition(t, store, ctx, origin, first, MCPRecoveryTransition{Phase: "retry"})
	second := recoveryCall(t, store, ctx, origin, "mcp_tool_call", origin.CallID)
	if _, err := store.TransitionMCPRecovery(ctx, origin.CallID, second, MCPRecoveryTransition{Phase: "retry", Server: "demo"}); !errors.Is(err, ErrRecoveryUsed) {
		t.Fatalf("a second business retry was admitted: %v", err)
	}
	call, err := store.Call(ctx, origin.CallID)
	if err != nil || call.ToolExecution.Recovery.RefreshID != winner.CallID || call.ToolExecution.Recovery.RetryID != first.CallID {
		t.Fatalf("recovery ownership was not retained: %+v %v", call.ToolExecution, err)
	}
}

func TestMCPRecoveryFailureIsTerminalWithoutBusinessRetry(t *testing.T) {
	for _, mode := range []string{"cancelled_before_refresh", "refresh_failed", "skill_start_failed"} {
		t.Run(mode, func(t *testing.T) {
			safe := true
			store, ctx, origin := recoveryFixture(t, &safe)
			tool, skill := "mcp_manage", ""
			if mode == "skill_start_failed" {
				tool, skill = "exec_command", "skill://recover-demo"
			}
			actor := recoveryCall(t, store, ctx, origin, tool, "")
			recoveryTransition(t, store, ctx, origin, actor, MCPRecoveryTransition{Phase: "claim", SkillRef: skill})
			switch mode {
			case "cancelled_before_refresh":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				if _, err := store.TransitionMCPRecovery(cancelled, origin.CallID, actor, MCPRecoveryTransition{Phase: "refresh", Server: "demo"}); !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled refresh was admitted: %v", err)
				}
			case "refresh_failed":
				recoveryTransition(t, store, ctx, origin, actor, MCPRecoveryTransition{Phase: "refresh"})
			case "skill_start_failed":
				// Startup failed after authorization, before a command session existed.
				if _, err := store.Append(ctx, Event{Binding: actor, Kind: "call.completed", ToolName: tool, Status: "failed", ErrorCode: "COMMAND_START_FAILED"}); err != nil {
					t.Fatal(err)
				}
			}
			// Failure settlement must still work after cancellation or expiry.
			saved := recoveryTransition(t, store, context.WithoutCancel(ctx), origin, actor, MCPRecoveryTransition{Phase: "fail", Now: time.Now().Add(2 * time.Hour)})
			recovery := saved.ToolExecution.Recovery
			if recovery.State != "recovery_failed" || recovery.ConsumedAt == nil || recovery.RetryID != "" {
				t.Fatalf("failure implied a retry or remained pending: %+v", recovery)
			}
			if saved.Status != "failed" || saved.ErrorCode != "MCP_CONNECTION_FAILED" {
				t.Fatalf("recovery rewrote the original failure: %+v", saved)
			}
			next := recoveryCall(t, store, ctx, origin, "mcp_manage", "")
			for _, phase := range []string{"claim", "refresh"} {
				if _, err := store.TransitionMCPRecovery(ctx, origin.CallID, next, MCPRecoveryTransition{Phase: phase, Server: "demo"}); !errors.Is(err, ErrRecoveryUsed) {
					t.Fatalf("failed origin accepted %s: %v", phase, err)
				}
			}
			retry := recoveryCall(t, store, ctx, origin, "mcp_tool_call", origin.CallID)
			if _, err := store.TransitionMCPRecovery(ctx, origin.CallID, retry, MCPRecoveryTransition{Phase: "retry", Server: "demo"}); !errors.Is(err, ErrRecoveryUsed) {
				t.Fatalf("failed recovery admitted business replay: %v", err)
			}
		})
	}
}

func TestMCPRecoveryUnknownEffectsNeverConsumeBusinessRetry(t *testing.T) {
	unsafe := false
	for _, safe := range []*bool{nil, &unsafe} {
		store, ctx, origin := recoveryFixture(t, safe)
		actor := recoveryCall(t, store, ctx, origin, "mcp_manage", "")
		for _, phase := range []string{"claim", "refresh", "ready"} {
			recoveryTransition(t, store, ctx, origin, actor, MCPRecoveryTransition{Phase: phase})
		}
		retry := recoveryCall(t, store, ctx, origin, "mcp_tool_call", origin.CallID)
		if _, err := store.TransitionMCPRecovery(ctx, origin.CallID, retry, MCPRecoveryTransition{Phase: "retry", Server: "demo"}); !errors.Is(err, ErrRecoveryUnsafe) {
			t.Fatalf("unknown effects admitted a retry: %v", err)
		}
		call, err := store.Call(ctx, origin.CallID)
		if err != nil {
			t.Fatal(err)
		}
		recovery := call.ToolExecution.Recovery
		if recovery.State != "recovered" || recovery.ConsumedAt != nil || recovery.RetryID != "" || call.ToolExecution.RetrySafe != nil && *call.ToolExecution.RetrySafe {
			t.Fatalf("reconnection became business execution: %+v", call.ToolExecution)
		}
	}
}

func recoveryFixture(t *testing.T, retrySafe *bool) (*Store, context.Context, Binding) {
	t.Helper()
	store, err := New(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithSource(context.Background(), Source{Principal: "recovery-test", Provider: "test"})
	conversation, err := NewExecutionID("conv_")
	if err != nil {
		t.Fatal(err)
	}
	origin := recoveryCall(t, store, ctx, Binding{ConversationID: conversation, WorkspaceID: "wsp_recovery", SourceOwnerKey: SourceOwnerKey(ctx)}, "mcp_tool_call", "")
	if _, err := store.Append(ctx, Event{Binding: origin, Kind: "call.completed", ToolName: "mcp_tool_call", Status: "failed", ErrorCode: "MCP_CONNECTION_FAILED", ToolExecution: &ToolExecutionDetails{Server: "demo", RetrySafe: retrySafe, EffectState: "unknown", Recovery: &MCPRecoveryState{State: "available", ExpiresAt: time.Now().Add(time.Hour)}}}); err != nil {
		t.Fatal(err)
	}
	return store, ctx, origin
}

func recoveryCall(t *testing.T, store *Store, ctx context.Context, scope Binding, tool, retryOf string) Binding {
	t.Helper()
	var err error
	scope.CallID, err = NewExecutionID("call_")
	if err != nil {
		t.Fatal(err)
	}
	scope.RetryOfCallID = retryOf
	for _, kind := range []string{"call.created", "call.started"} {
		if _, err := store.Append(ctx, Event{Binding: scope, Kind: kind, ToolName: tool}); err != nil {
			t.Fatal(err)
		}
	}
	return scope
}

func recoveryTransition(t *testing.T, store *Store, ctx context.Context, origin, actor Binding, change MCPRecoveryTransition) ExecutionCall {
	t.Helper()
	change.Server = "demo"
	saved, err := store.TransitionMCPRecovery(ctx, origin.CallID, actor, change)
	if err != nil {
		t.Fatalf("%s: %v", change.Phase, err)
	}
	return saved
}
