package mcp

import (
	"context"
	"maps"
)

type knownContextRevisionKey struct{}

func withContextRevisionRequest(ctx context.Context, name string, args map[string]any) context.Context {
	if name != "agentdock_context" {
		return ctx
	}
	revision, _ := args["known_context_revision"].(string)
	if revision == "" {
		return ctx
	}
	return context.WithValue(ctx, knownContextRevisionKey{}, revision)
}

func projectContextReference(ctx context.Context, name string, result any, err error) any {
	if name != "agentdock_context" || err != nil {
		return result
	}
	known, _ := ctx.Value(knownContextRevisionKey{}).(string)
	payload := asMap(result)
	revision, _ := payload["context_revision"].(string)
	diagnostics := asMap(payload["context_diagnostics"])
	if known == "" || revision == "" || known != revision || payload["delivery"] != "full" || diagnostics["complete"] != true {
		return result
	}

	reference := map[string]any{
		"context_revision":    revision,
		"delivery":            "reference",
		"unchanged":           true,
		"context_diagnostics": compactContextDiagnostics(diagnostics),
	}
	for _, key := range []string{
		"conversation_id", "call_id", "parent_call_id", "retry_of_call_id",
		"task_id", "thread_id", "step_id", "workspace_id",
		"binding_quality", "binding_updated", "binding_warning", "conversation_state",
		"activity_label", "activity_warning", "output_policy",
	} {
		if value, exists := payload[key]; exists {
			reference[key] = value
		}
	}
	if guidance := compactContextReferenceGuidance(asMap(payload["agentdock_guidance"])); len(guidance) > 0 {
		reference["agentdock_guidance"] = guidance
	}
	return reference
}

func compactContextDiagnostics(diagnostics map[string]any) map[string]any {
	result := map[string]any{"complete": true}
	for _, key := range []string{"components_complete", "binding_status"} {
		if value, exists := diagnostics[key]; exists {
			result[key] = value
		}
	}
	return result
}

func compactContextReferenceGuidance(guidance map[string]any) map[string]any {
	if len(guidance) == 0 {
		return nil
	}
	result := maps.Clone(guidance)
	for _, key := range []string{
		"conversation_id", "call_id", "parent_call_id", "retry_of_call_id",
		"task_id", "thread_id", "step_id", "workspace_id", "default_workspace_id",
		"constraints", "target_kind", "resolved_path", "rules_revision",
	} {
		delete(result, key)
	}
	return result
}
