package app

import (
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
)

func TestRunningSessionGuidanceUsesBoundedLongPoll(t *testing.T) {
	runtime := &Runtime{}
	result := Result{
		"session_id":       "session-fixture",
		"status":           "running",
		"observe_after_ms": int64(5000),
	}
	guidance := runtime.executionGuidance("session_observe", nil, executionObservation{
		binding: activity.Binding{WorkspaceID: "wsp-fixture"},
	}, result, false)
	steps, ok := guidance["next_required"].([]map[string]any)
	if !ok || len(steps) != 1 {
		t.Fatalf("next_required = %#v", guidance["next_required"])
	}
	arguments, _ := steps[0]["arguments"].(map[string]any)
	if arguments["action"] != "status" || arguments["session_id"] != "session-fixture" || arguments["wait_ms"] != 5000 {
		t.Fatalf("observe arguments = %#v", arguments)
	}
	text, _ := steps[0]["text"].(string)
	if !strings.Contains(text, "Do not poll") || !strings.Contains(text, "do not restart") {
		t.Fatalf("observe guidance does not suppress tight polling: %q", text)
	}
}
