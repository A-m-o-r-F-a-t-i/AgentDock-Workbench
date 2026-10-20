package app

import (
	"maps"
	"strings"
)

// executionOutputSchema preserves the complete business success contract and
// adds a separate, unmistakable not-yet-executed approval alternative.
func executionOutputSchemaForTool(name string, original map[string]any) map[string]any {
	if name == "mcp_result_continue" || name == "mcp_result_raw" {
		result := cloneJSONSchemaMap(original)
		if result == nil {
			result = map[string]any{"type": "object"}
		}
		properties, _ := result["properties"].(map[string]any)
		properties = maps.Clone(properties)
		if properties == nil {
			properties = map[string]any{}
		}
		properties["agentdock_guidance"] = executionGuidanceSchema(properties["agentdock_guidance"])
		result["properties"] = properties
		result["additionalProperties"] = true
		return result
	}
	return executionOutputSchema(original)
}

func executionOutputSchema(original map[string]any) map[string]any {
	return buildExecutionOutputSchema(original, true)
}

// Canonical results must satisfy the business contract before projection. A
// compact receipt must never satisfy a missing or malformed business result.
func canonicalExecutionOutputSchema(name string, original map[string]any) map[string]any {
	if name == "mcp_result_continue" || name == "mcp_result_raw" {
		return executionOutputSchemaForTool(name, original)
	}
	return buildExecutionOutputSchema(original, false)
}

func buildExecutionOutputSchema(original map[string]any, projectedAlternatives bool) map[string]any {
	success := maps.Clone(original)
	if success == nil {
		success = map[string]any{"type": "object"}
	}
	properties, _ := success["properties"].(map[string]any)
	properties = maps.Clone(properties)
	if properties == nil {
		properties = map[string]any{}
	}
	executionStringFields := []string{"conversation_id", "call_id", "parent_call_id", "retry_of_call_id", "task_id", "thread_id", "step_id", "workspace_id", "activity_label", "activity_warning", "binding_quality", "binding_warning"}
	patternFields := make([]string, 0, len(executionStringFields))
	for _, name := range executionStringFields {
		if _, exists := properties[name]; !exists {
			patternFields = append(patternFields, name)
		}
	}
	executionStringPattern := ""
	if len(patternFields) > 0 {
		executionStringPattern = "^(" + strings.Join(patternFields, "|") + ")$"
	}
	for _, name := range []string{"agentdock_guidance", "permission", "workspace_target", "conversation_state"} {
		if _, exists := properties[name]; !exists {
			properties[name] = map[string]any{"type": "object", "additionalProperties": true}
		}
	}
	for _, name := range []string{"operation", "capture", "model_projection", "activity_preview", "continue_read", "range", "source_delivery"} {
		properties[name] = map[string]any{"type": "object", "additionalProperties": true}
	}
	properties["projection"] = map[string]any{
		"type": "object", "required": []string{"mode", "complete"},
		"properties": map[string]any{
			"mode":     map[string]any{"enum": []string{"full", "semantic_page", "continuous_page", "head_tail", "compact_receipt", "source_receipt", "reference"}},
			"complete": map[string]any{"type": "boolean"},
		},
		"additionalProperties": true,
	}
	properties["delivery"] = map[string]any{"type": "object", "required": []string{"class", "sequence_complete", "continuation_required", "continuation_available"}, "properties": map[string]any{"class": map[string]any{"enum": []string{"required_full", "recoverable_preview", "compact_receipt"}}, "sequence_complete": map[string]any{"type": "boolean"}, "continuation_required": map[string]any{"type": "boolean"}, "continuation_available": map[string]any{"type": "boolean"}}, "additionalProperties": true}
	for _, name := range []string{"truncated", "source_complete", "context_ready", "has_more", "unchanged"} {
		properties[name] = map[string]any{"type": "boolean"}
	}
	for _, name := range []string{"truncation_stage", "source_ref", "snapshot_id", "source_revision", "context_delivery", "output_class", "preview_mode", "source_freshness"} {
		properties[name] = map[string]any{"type": "string"}
	}
	properties["binding_updated"] = map[string]any{"type": "boolean"}
	properties["output_policy"] = map[string]any{"type": "object", "additionalProperties": true}
	properties["agentdock_guidance"] = executionGuidanceSchema(properties["agentdock_guidance"])
	// Reuse business fields as well as execution metadata. The visible union
	// widens these three fields for approval responses, so success retains their
	// original constraints instead of referencing the widened alternatives.
	originalProperties, _ := original["properties"].(map[string]any)
	successProperties := make(map[string]any, len(properties))
	for name := range properties {
		// The root validates each field once. The success branch keeps property
		// names only so closed business objects admit execution metadata without
		// duplicating every business schema.
		successProperties[name] = true
	}
	for _, name := range []string{"status", "executed", "approval", "context_diagnostics"} {
		if property, exists := originalProperties[name]; exists {
			successProperties[name] = property
		}
	}
	success["properties"] = successProperties
	if executionStringPattern != "" {
		patterns, _ := success["patternProperties"].(map[string]any)
		patterns = maps.Clone(patterns)
		if patterns == nil {
			patterns = map[string]any{}
		}
		patterns[executionStringPattern] = map[string]any{"type": "string"}
		success["patternProperties"] = patterns
	}
	pending := map[string]any{"type": "object", "properties": map[string]any{
		"status": map[string]any{"const": "pending_approval"}, "executed": map[string]any{"const": false},
		"approval_id": map[string]any{"type": "string", "pattern": "^approval_[a-f0-9]{32}$"},
		"call_id":     map[string]any{"type": "string", "pattern": "^call_[a-f0-9]{32}$"},
	}, "required": []string{"status", "executed", "approval_id", "call_id"}, "additionalProperties": true}
	visible := maps.Clone(properties)
	if full, ok := properties["context_diagnostics"].(map[string]any); ok {
		_ = full
		visible["context_diagnostics"] = map[string]any{
			"type": "object", "required": []string{"complete", "components_complete", "binding_status"},
			"properties": map[string]any{
				"complete":            map[string]any{"type": "boolean"},
				"components_complete": map[string]any{"type": "boolean"},
				"binding_status":      map[string]any{"type": "string"},
			},
			"additionalProperties": true,
		}
	}

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
		"operation": map[string]any{"$ref": "#/properties/operation"}, "capture": map[string]any{"$ref": "#/properties/capture"}, "delivery": map[string]any{"$ref": "#/properties/delivery"},
		"projection": map[string]any{"$ref": "#/properties/projection"},
	}, "additionalProperties": true}
	alternatives := []any{success}
	if projectedAlternatives {
		alternatives = append(alternatives, pending, projected)
	}
	result := map[string]any{"type": "object", "properties": visible, "anyOf": alternatives}
	if executionStringPattern != "" {
		result["patternProperties"] = map[string]any{executionStringPattern: map[string]any{"type": "string"}}
	}
	for _, key := range []string{"$defs", "definitions"} {
		if value, exists := original[key]; exists {
			result[key] = value
		}
	}
	return result
}

// The adapter validates and constructs authenticated additions before they are
// attached. Per-tool result schemas keep only their bounded container shape;
// responseGuidanceSchema remains the single detailed contract for that boundary.
func executionGuidanceSchema(original any) map[string]any {
	input, _ := original.(map[string]any)
	result := maps.Clone(input)
	if result == nil {
		result = map[string]any{"type": "object", "additionalProperties": true}
	}
	inputProperties, _ := result["properties"].(map[string]any)
	properties := maps.Clone(inputProperties)
	if properties == nil {
		properties = map[string]any{}
	}
	properties["response_additions"] = map[string]any{"type": "array", "items": map[string]any{"type": "object"}}
	result["properties"] = properties
	return result
}
