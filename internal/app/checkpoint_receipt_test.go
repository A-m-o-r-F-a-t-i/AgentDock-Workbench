package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestCheckpointReceiptPreservesFullThreadReadback(t *testing.T) {
	r := newRuntimeValidationTestRuntime(t)
	id := activityTask(t, r)
	summary := strings.Repeat("verified checkpoint facts ", 80)
	next := strings.Repeat("continue authorized verification ", 40)
	for _, action := range []string{"thread_checkpoint", "checkpoint"} {
		t.Run(action, func(t *testing.T) {
			result, err := r.Call(context.Background(), "task_manage", map[string]any{
				"action": action, "task_id": id, "thread_id": "main", "summary": summary,
				"next_action": next, "current_step_id": "verify",
			})
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(result)
			t.Logf("checkpoint_response_json_bytes=%d", len(encoded))
			if result["thread"] != nil || result["state_dir"] != nil {
				t.Fatal("checkpoint repeats the full persistent thread")
			}
			receipt, ok := result["checkpoint"].(map[string]any)
			if !ok || receipt["saved"] != true || receipt["current_step_id"] != "verify" || receipt["step_count"] != 1 {
				t.Fatalf("invalid checkpoint receipt: %#v", result["checkpoint"])
			}
			if result["task_id"] != id || result["thread_id"] != "main" || result["call_id"] == "" || result["activity_event_id"] == "" {
				t.Fatal("checkpoint lost correlation fields")
			}
			full, err := r.Call(context.Background(), "task_manage", map[string]any{"action": "thread_get", "task_id": id, "thread_id": "main"})
			if err != nil {
				t.Fatal(err)
			}
			thread, ok := full["thread"].(taskstate.TaskThread)
			if !ok || thread.Summary != summary || thread.NextAction != next || thread.CurrentStepID != "verify" || thread.Goal == "" || len(thread.Steps) != 1 {
				t.Fatalf("full state lost after compact response: %#v", full["thread"])
			}
			if receipt["checkpoint_event_id"] != thread.CheckpointEventID || receipt["checkpoint_event_id"] != result["activity_event_id"] || receipt["updated_at"] != thread.UpdatedAt {
				t.Fatal("receipt does not describe persisted checkpoint")
			}
		})
	}
}
