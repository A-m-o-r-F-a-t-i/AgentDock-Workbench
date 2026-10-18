package app

import (
	"errors"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
)

func TestRepeatedEmptySessionListStopsBeforeAnotherObservation(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := scopeHost("empty-session-list-repeat")

	first, err := r.Call(ctx, "session_observe", map[string]any{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	if count, ok := integerResult(first["count"]); !ok || count != 0 {
		t.Fatalf("first empty list = %#v", first)
	}

	_, err = r.Call(ctx, "session_observe", map[string]any{"action": "list"})
	var failure *ToolError
	if !errors.As(err, &failure) || failure.Code != "REPEATED_CALL_BLOCKED" || failure.Retryable {
		t.Fatalf("repeated empty list was not blocked: %#v", err)
	}
	action := modelObject(failure.Details["required_action"])
	if stringArg(action, "action") != "wait_for_running_session" || failure.Details["first_call_id"] == "" || failure.Details["previous_call_id"] == "" {
		t.Fatalf("repeated empty list lost its stop condition: %#v", failure.Details)
	}
}

func TestEmptySessionListGuardClearsForRunningCommand(t *testing.T) {
	guard := emptySessionListGuard{}
	first := testEmptySessionListBinding("call_11111111111111111111111111111111")
	guard.record(first, Result{"sessions": []map[string]any{}, "count": 0})

	second := testEmptySessionListBinding("call_22222222222222222222222222222222")
	if err := guard.reject(second, "session_observe", map[string]any{"action": "list"}); err == nil {
		t.Fatal("expected the repeated empty list to be blocked")
	}
	guard.clear(second)
	third := testEmptySessionListBinding("call_33333333333333333333333333333333")
	if err := guard.reject(third, "session_observe", map[string]any{"action": "list"}); err != nil {
		t.Fatalf("new running-session generation did not clear the guard: %v", err)
	}
}

func testEmptySessionListBinding(callID string) activity.Binding {
	return activity.Binding{
		SourceOwnerKey: "owner-empty-session-list",
		ConversationID: "conv_11111111111111111111111111111111",
		WorkspaceID:    "wsp_1111111111111111",
		CallID:         callID,
	}
}
