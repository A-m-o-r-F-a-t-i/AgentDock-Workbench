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
	if guidance["next_required"] != nil {
		t.Fatal("running command demanded mandatory polling")
	}
	continuation, ok := guidance["continuation"].(map[string]any)
	if !ok || continuation["recommended_action"] != "continue_independent_work" {
		t.Fatalf("continuation = %#v", guidance["continuation"])
	}
	observe := continuation["observe_when_needed"].(map[string]any)
	arguments, _ := observe["arguments"].(map[string]any)
	if arguments["action"] != "status" || arguments["session_id"] != "session-fixture" || arguments["wait_ms"] != 5000 {
		t.Fatalf("observe arguments = %#v", arguments)
	}
	text, _ := continuation["text"].(string)
	if !strings.Contains(text, "Continue independent work") || !strings.Contains(text, "do not restart") {
		t.Fatalf("observe guidance does not suppress tight polling: %q", text)
	}
}
