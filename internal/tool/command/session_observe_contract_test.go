package command

import (
	"errors"
	"testing"

	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

func TestSessionObserveRequiresExplicitAction(t *testing.T) {
	runtime, _ := newCommandTestService(t)
	_, err := runtime.Observe(SessionObserveRequest{})
	assertCommandToolErrorCode(t, err, "INVALID_ACTION")
}

func TestSessionObserveRejectsArgumentsThatDoNotBelongToList(t *testing.T) {
	runtime, _ := newCommandTestService(t)
	limit, wait := 1024, 1000
	for _, request := range []SessionObserveRequest{
		{Action: "list", SessionID: "session-unexpected"},
		{Action: "list", MaxOutputBytes: &limit},
		{Action: "list", WaitMS: &wait},
	} {
		_, err := runtime.Observe(request)
		assertCommandToolErrorCode(t, err, "INVALID_ARGUMENT")
	}
}

func TestSessionObserveStatusRequiresSessionID(t *testing.T) {
	runtime, _ := newCommandTestService(t)
	_, err := runtime.Observe(SessionObserveRequest{Action: "status"})
	assertCommandToolErrorCode(t, err, "INVALID_ARGUMENT")
}

func TestSessionObserveAcceptsOnlyActionSpecificSchemaShapes(t *testing.T) {
	schema, ok := InputSchema(ToolSessionObserve)
	if !ok {
		t.Fatal("session_observe input schema is unavailable")
	}
	validator, err := toolcontract.CompileInputSchema(schema)
	if err != nil {
		t.Fatalf("compile session_observe schema: %v", err)
	}

	valid := []map[string]any{
		{"action": "list"},
		{"action": "status", "session_id": "session-123"},
		{"action": "status", "session_id": "session-123", "max_output_bytes": 4096},
		{"action": "status", "session_id": "session-123", "wait_ms": 0},
		{"action": "status", "session_id": "session-123", "wait_ms": MaxSessionObserveWaitMS},
	}
	for _, arguments := range valid {
		if err := validator.Validate(arguments); err != nil {
			t.Fatalf("valid arguments rejected: %#v: %v", arguments, err)
		}
	}

	invalid := []map[string]any{
		{},
		{"action": "list", "session_id": "session-123"},
		{"action": "list", "max_output_bytes": 4096},
		{"action": "list", "wait_ms": 1000},
		{"action": "status"},
		{"action": "status", "session_id": ""},
		{"action": "status", "session_id": "session-123", "wait_ms": -1},
		{"action": "status", "session_id": "session-123", "wait_ms": MaxSessionObserveWaitMS + 1},
	}
	for _, arguments := range invalid {
		if err := validator.Validate(arguments); err == nil {
			t.Fatalf("invalid arguments accepted: %#v", arguments)
		}
	}
}

func TestSessionObserveListReturnsCompactBusinessShape(t *testing.T) {
	runtime, _ := newCommandTestService(t)
	result, err := runtime.Observe(SessionObserveRequest{Action: "list"})
	if err != nil {
		t.Fatalf("session_observe list: %v", err)
	}
	if result["count"] != 0 {
		t.Fatalf("empty session count = %#v, want 0", result["count"])
	}
	items, ok := result["sessions"].([]map[string]any)
	if !ok || len(items) != 0 {
		t.Fatalf("empty sessions = %#v", result["sessions"])
	}
}

func assertCommandToolErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var toolErr *ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != code {
		t.Fatalf("tool error = %#v, want %s", err, code)
	}
}
