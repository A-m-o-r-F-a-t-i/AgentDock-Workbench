package contextguide

import "maps"

// ProjectReference requires an explicit caller-held revision. An ordinary repeat
// still returns full context, including after host-side context compaction.
func ProjectReference(name, known string, payload map[string]any) map[string]any {
	if name != "agentdock_context" && name != "workspace_context" {
		return payload
	}
	revision, _ := payload["context_revision"].(string)
	diagnostics, _ := payload["context_diagnostics"].(map[string]any)
	if revision == "" || diagnostics["complete"] != true {
		return payload
	}
	if payload["delivery"] != "reference" && (known == "" || known != revision) {
		full := maps.Clone(payload)
		full["delivery"], full["unchanged"] = "full", false
		return full
	}
	compact := map[string]any{"complete": true}
	for _, key := range []string{"components_complete", "binding_status"} {
		if value, exists := diagnostics[key]; exists {
			compact[key] = value
		}
	}
	reference := map[string]any{
		"context_revision": revision, "delivery": "reference", "unchanged": true,
		"context_diagnostics": compact,
	}
	for _, key := range []string{
		"conversation_id", "call_id", "parent_call_id", "retry_of_call_id",
		"task_id", "thread_id", "step_id", "workspace_id", "workdir", "workspace_root",
		"binding_quality", "binding_updated", "binding_warning", "conversation_state",
		"activity_label", "activity_warning", "output_policy",
	} {
		if value, exists := payload[key]; exists {
			reference[key] = value
		}
	}
	if guidance, ok := payload["agentdock_guidance"].(map[string]any); ok {
		guidance = maps.Clone(guidance)
		for _, key := range []string{
			"conversation_id", "call_id", "parent_call_id", "retry_of_call_id",
			"task_id", "thread_id", "step_id", "workspace_id", "default_workspace_id",
			"constraints", "target_kind", "resolved_path", "rules_revision",
		} {
			delete(guidance, key)
		}
		if len(guidance) > 0 {
			reference["agentdock_guidance"] = guidance
		}
	}
	return reference
}
