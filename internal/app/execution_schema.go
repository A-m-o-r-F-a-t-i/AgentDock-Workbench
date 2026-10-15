package app

import "maps"

// executionOutputSchema preserves the complete business success contract and
// adds a separate, unmistakable not-yet-executed approval alternative.
func executionOutputSchema(original map[string]any) map[string]any {
	success := maps.Clone(original)
	if success == nil {
		success = map[string]any{"type": "object"}
	}
	properties, _ := success["properties"].(map[string]any)
	properties = maps.Clone(properties)
	if properties == nil {
		properties = map[string]any{}
	}
	for _, name := range []string{"conversation_id", "call_id", "parent_call_id", "retry_of_call_id", "task_id", "thread_id", "step_id", "workspace_id", "activity_label", "activity_warning", "binding_quality", "binding_warning"} {
		if _, exists := properties[name]; !exists {
			properties[name] = map[string]any{"type": "string"}
		}
	}
	for _, name := range []string{"agentdock_guidance", "permission", "workspace_target", "conversation_state"} {
		if _, exists := properties[name]; !exists {
			properties[name] = map[string]any{"type": "object", "additionalProperties": true}
		}
	}
	for _, name := range []string{"operation", "capture", "projection", "model_projection", "activity_preview", "continue_read", "range"} {
		properties[name] = map[string]any{"type": "object", "additionalProperties": true}
	}
	properties["delivery"] = map[string]any{"anyOf": []any{map[string]any{"type": "string", "enum": []string{"full", "reference"}}, map[string]any{"type": "object", "required": []string{"class", "sequence_complete", "continuation_required", "continuation_available"}, "properties": map[string]any{"class": map[string]any{"enum": []string{"required_full", "recoverable_preview", "compact_receipt"}}, "sequence_complete": map[string]any{"type": "boolean"}, "continuation_required": map[string]any{"type": "boolean"}, "continuation_available": map[string]any{"type": "boolean"}}, "additionalProperties": true}}}
	for _, name := range []string{"truncated", "source_complete", "context_ready", "has_more", "unchanged"} {
		properties[name] = map[string]any{"type": "boolean"}
	}
	for _, name := range []string{"truncation_stage", "source_ref", "snapshot_id", "source_revision", "context_delivery"} {
		properties[name] = map[string]any{"type": "string"}
	}
	properties["binding_updated"] = map[string]any{"type": "boolean"}
	properties["output_policy"] = map[string]any{"type": "object", "additionalProperties": true, "description": "Budget for declared ordinary text fields, separate from control metadata and complete rules. Includes actual storage/continuation state when shortened."}
	properties["agentdock_guidance"] = responseGuidanceSchema(properties["agentdock_guidance"])
	// Reuse business fields as well as execution metadata. The visible union
	// widens these three fields for approval responses, so success retains their
	// original constraints instead of referencing the widened alternatives.
	success["properties"] = schemaPropertyReferences(properties, "status", "executed", "approval")
	pending := map[string]any{"type": "object", "properties": map[string]any{
		"status": map[string]any{"const": "pending_approval"}, "executed": map[string]any{"const": false},
		"approval_id": map[string]any{"type": "string", "pattern": "^approval_[a-f0-9]{32}$"},
		"call_id":     map[string]any{"type": "string", "pattern": "^call_[a-f0-9]{32}$"},
	}, "required": []string{"status", "executed", "approval_id", "call_id"}, "additionalProperties": true}
	visible := maps.Clone(properties)
	if old, exists := visible["status"]; exists {
		visible["status"] = map[string]any{"anyOf": []any{old, map[string]any{"const": "pending_approval"}}}
	}
	for _, name := range []string{"approval_id", "next_required_action"} {
		if _, exists := visible[name]; !exists {
			visible[name] = map[string]any{"type": "string"}
		}
	}
	visible["executed"] = map[string]any{"type": "boolean"}
	visible["approval"] = map[string]any{"type": "object"}
	projected := map[string]any{"type": "object", "required": []string{"operation", "capture", "projection", "delivery"}, "properties": map[string]any{
		"operation": properties["operation"], "capture": properties["capture"], "delivery": properties["delivery"],
		"projection": map[string]any{"type": "object", "required": []string{"mode", "complete"}, "properties": map[string]any{"mode": map[string]any{"enum": []string{"semantic_page", "continuous_page", "head_tail", "compact_receipt", "source_receipt", "reference"}}, "complete": map[string]any{"type": "boolean"}}, "additionalProperties": true},
	}, "additionalProperties": true}
	result := map[string]any{"type": "object", "properties": visible, "anyOf": []any{success, pending, projected}}
	for _, key := range []string{"$defs", "definitions"} {
		if value, exists := original[key]; exists {
			result[key] = value
		}
	}
	return result
}
