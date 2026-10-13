package app

import (
	"maps"

	"github.com/uvwt/agentdock/internal/permission"
	"github.com/uvwt/agentdock/internal/taskstate"
)

// Project only confirmed ordinary success after state transitions and journaling.
// Approval, failure and bootstrap responses keep their complete diagnostics.
func compactExecutionSuccess(result Result, p *preparedExecution) Result {
	if result == nil || p == nil || p.decision.Effect != permission.Allow || p.approvalID != "" ||
		p.spec.Name == "agentdock_context" || p.spec.Name == "workspace_context" ||
		resultReportsFailure(result) || result["command_ok"] == false || result["executed"] == false {
		return result
	}
	switch stringArg(result, "status") {
	case "unknown", "partial", "failed", "error", "timeout", "killed", "cancelled", "pending_approval", "approval_rejected":
		return result
	}
	compact := maps.Clone(result)
	delete(compact, "permission")
	if guidance, ok := result["agentdock_guidance"].(map[string]any); ok {
		compact["agentdock_guidance"] = compactSuccessGuidance(guidance, result)
	}
	if p.spec.Name == "task_manage" {
		switch stringArg(p.args, "action") {
		case "thread_checkpoint", "checkpoint":
			if thread, ok := result["thread"].(taskstate.TaskThread); ok {
				compact["checkpoint"] = checkpointReceipt(thread)
				delete(compact, "thread")
				delete(compact, "state_dir")
			}
		}
	}
	return compact
}

func compactSuccessGuidance(guidance map[string]any, result Result) map[string]any {
	compact := maps.Clone(guidance)
	// Keep workspace_id as a direct routing hint for existing callers. Other
	// correlation identifiers already have their canonical top-level fields.
	for _, key := range []string{"conversation_id", "call_id", "parent_call_id", "retry_of_call_id", "task_id", "thread_id", "step_id"} {
		if value, ok := guidance[key].(string); ok && value != "" && result[key] == value {
			delete(compact, key)
		}
	}
	if result["workspace_target"] != nil {
		for _, key := range []string{"target_kind", "resolved_path", "rules_revision"} {
			delete(compact, key)
		}
	}
	if constraints, ok := guidance["constraints"].([]map[string]string); ok {
		remaining := make([]map[string]string, 0, len(constraints))
		for _, item := range constraints {
			if item["code"] != "SERVER_BINDING_SNAPSHOT" {
				remaining = append(remaining, item)
			}
		}
		if len(remaining) == 0 {
			delete(compact, "constraints")
		} else {
			compact["constraints"] = remaining
		}
	}
	// next_required, response_additions, warnings and unknown future guidance
	// remain untouched; nested third-party results are never projected here.
	return compact
}

func checkpointReceipt(thread taskstate.TaskThread) map[string]any {
	completed := 0
	for _, step := range thread.Steps {
		if step.Status == taskstate.StepCompleted {
			completed++
		}
	}
	receipt := map[string]any{
		"saved": true, "status": thread.Status, "step_count": len(thread.Steps),
		"completed_step_count": completed, "updated_at": thread.UpdatedAt,
	}
	if thread.CurrentStepID != "" {
		receipt["current_step_id"] = thread.CurrentStepID
	}
	if thread.CheckpointEventID != "" {
		receipt["checkpoint_event_id"] = thread.CheckpointEventID
	}
	return receipt
}
