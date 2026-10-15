package app

import (
	"context"
	"maps"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

// Retain the business result before any model-facing projection mutates it.
// Device collection limits remain independent from the response character limit.
func (r *Runtime) prepareModelOutput(p *preparedExecution, result Result) Result {
	if result == nil || p.state.binding.ParentCallID != "" {
		return result
	}
	out := maps.Clone(result)
	if p.structuredResume != nil {
		return out
	}
	if p.spec.Name == "agentdock_context" {
		routes := []map[string]any{}
		for _, name := range r.ToolNames() {
			routes = append(routes, map[string]any{"name": name, "invoke_via": "host", "dynamic_mcp_search": false})
		}
		out["host_tools"], out["permission"] = routes, p.decision
		result = out
	}
	if capture := modelObject(result["capture"]); capture != nil && p.spec.Name == "read_file" {
		out["operation"] = modelOperation(result, p.state.executed)
		return out
	}
	if p.spec.Name == "exec_command" || p.spec.Name == "session_observe" || p.spec.Name == "session_act" {
		origin := p.state.binding
		if p.spec.Name != "exec_command" {
			if binding, exists := r.command.SessionBinding(stringArg(result, "session_id")); exists {
				origin = binding
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		call, err := r.activity.Call(ctx, origin.CallID)
		cancel()
		if err == nil && activity.CallTerminal(call.Status) && call.OutputSource != nil {
			source := call.OutputSource
			out["capture"] = map[string]any{"state": source.State, "complete": source.State == "complete", "retained_bytes": source.Bytes, "source_ref": activity.SourceReference(origin.CallID, source)}
			out["operation"] = modelOperation(result, p.state.executed)
			return out
		}
	}
	complete, reason := sourceResultComplete(result)
	if captured, known := result["source_complete"].(bool); known {
		complete = captured
	}
	state := "complete"
	if !complete {
		state = "partial"
	}
	capture := map[string]any{"state": state, "complete": complete}
	if bytes, exists := result["retained_bytes"]; exists {
		capture["retained_bytes"] = bytes
	}
	if dropped, exists := result["dropped_before_bytes"]; exists {
		capture["dropped_bytes"] = dropped
	}
	smallCompleteLimit := 4000
	if p.outputPolicy.Enabled && p.outputPolicy.MaxChars > 0 {
		smallCompleteLimit = min(smallCompleteLimit, p.outputPolicy.MaxChars)
	}
	if chars, err := toolcontract.JSONChars(result); err == nil && chars <= smallCompleteLimit && p.spec.Name != "apply_patch" && !(p.spec.Name == "session_observe" && result["changed"] == false) && toolcontract.ResolveOutputClass(p.spec.Name, p.args, result) != toolcontract.OutputRequiredFull {
		// A complete small result is retained by the existing final response
		// journal. Avoid another fsync and duplicate payload for every small call.
		capture["retained_with"] = "response"
		out["capture"], out["operation"] = capture, modelOperation(result, p.state.executed)
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	payload := r.activity.CapturePayload(ctx, result, state, r.executionRedactor(p.args))
	if payload.Ref != "" {
		if _, err := r.activity.Append(ctx, activity.Event{Binding: p.state.binding, Kind: "call.payload", ToolName: p.spec.Name, OutputSource: payload}); err == nil {
			capture["source_ref"] = activity.SourceReference(p.state.binding.CallID, payload)
			if p.spec.Name == "apply_patch" {
				out["output_source"] = capture["source_ref"]
			}
			capture["retained_bytes"] = payload.Bytes
		} else {
			capture["storage_error"] = "The source could not be journaled. Verify the original operation before retrying."
		}
	}
	if payload.State != "complete" && payload.State != "partial" {
		capture["state"] = payload.State
		capture["complete"] = false
	}
	if payload.Reason != "" {
		capture["reason"] = payload.Reason
	} else if !complete && reason != "" {
		capture["reason"] = reason
	}
	out["capture"] = capture
	out["operation"] = modelOperation(result, p.state.executed)
	if p.spec.Name == "agentdock_context" || p.spec.Name == "workspace_context" {
		projectContextFacts(out)
	}
	return out
}

func modelOperation(result Result, executed bool) map[string]any {
	state := stringArg(result, "status")
	if state == "" {
		state = "completed"
	}
	operation := map[string]any{"state": state, "executed": executed}
	if state == "running" || state == "pending_approval" || state == "unknown" || state == "outcome_unknown" || state == "lost" {
		operation["ok"] = nil
	} else {
		operation["ok"] = !resultReportsFailure(result) && result["command_ok"] != false
	}
	for _, key := range []string{"exit_code", "timed_out", "cancelled", "signal", "error_code"} {
		if value, exists := result[key]; exists {
			operation[key] = value
		}
	}
	return operation
}

func modelObject(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case Result:
		return map[string]any(typed)
	default:
		return nil
	}
}

func (r *Runtime) finishModelOutput(p *preparedExecution, result Result) Result {
	if result == nil || p.state.binding.ParentCallID != "" {
		return result
	}
	class := toolcontract.ResolveOutputClass(p.spec.Name, p.args, result)
	structured := modelObject(result["structured_output"])
	policy := modelObject(result["output_policy"])
	capture := modelObject(result["capture"])
	complete := structured["truncated"] != true && policy["truncated"] != true
	mode := "full"
	if structured != nil {
		mode = "semantic_page"
	} else if policy["truncated"] == true {
		mode = "head_tail"
		if p.spec.Name == "read_file" {
			mode = "continuous_page"
		}
	} else if class == toolcontract.OutputCompactReceipt {
		mode = "compact_receipt"
	}
	if existing := modelObject(result["projection"]); existing != nil {
		complete = existing["complete"] != false && complete
		if value, ok := existing["mode"].(string); ok {
			mode = value
		}
	}
	if result["unchanged"] == true || result["context_delivery"] == "reference" || result["schema_omitted"] == true {
		mode = "reference"
	}
	chars, _ := toolcontract.JSONChars(result)
	projection := maps.Clone(modelObject(result["projection"]))
	if projection == nil {
		projection = map[string]any{}
	}
	projection["mode"], projection["complete"] = mode, complete
	if projection["visible_chars"] == nil {
		projection["visible_chars"] = chars
	}
	result["projection"] = projection
	hasMore := stringArg(result, "next_cursor") != "" || result["has_more"] == true
	available := hasMore || !complete && stringArg(capture, "source_ref") != ""
	required := class == toolcontract.OutputRequiredFull && hasMore
	delivery := map[string]any{
		"class": class, "sequence_complete": !required,
		"continuation_required": required, "continuation_available": available,
	}
	for _, key := range []string{"snapshot_id", "page_index", "page_count", "received_sections"} {
		if value, exists := structured[key]; exists {
			delivery[key] = value
		}
	}
	result["delivery"] = delivery
	result["truncated"] = !complete
	stage := "none"
	if capture != nil {
		result["source_complete"] = capture["complete"]
		if capture["complete"] == false {
			stage = "capture"
		}
	}
	if stage == "none" && !complete {
		stage = "projection"
		if required {
			stage = "delivery"
		}
	}
	result["truncation_stage"] = stage
	if p.spec.Name == "agentdock_context" || p.spec.Name == "workspace_context" {
		diagnostics := modelObject(result["context_diagnostics"])
		result["context_ready"] = !required && diagnostics["complete"] == true
	}
	if p.spec.Name == "session_observe" && (result["changed"] == false || result["already_delivered"] == true) {
		// Diagnostics and binding details remain in the canonical activity source.
		compact := Result{}
		for _, key := range []string{"session_id", "status", "operation_status", "changed", "waited_ms", "offset", "next_offset", "exit_code", "command_ok", "already_delivered", "output_source", "source_complete", "outcome_unknown", "activity_warning"} {
			if value, exists := result[key]; exists {
				compact[key] = value
			}
		}
		return compact
	}
	return result
}

// RetainResponseSource handles adapter-only errors or metadata overflow. It
// reuses an already retained business source instead of replacing it with a page.
func (r *Runtime) RetainResponseSource(response *ToolResponse, value any) (string, error) {
	if response == nil {
		return "", nil
	}
	response.mu.Lock()
	binding, name, redactor := response.auditBinding, response.auditName, response.auditRedactor
	for _, message := range response.additions.UserMessages {
		redactor = redactor.WithSecrets(message.ReceiptToken)
	}
	response.mu.Unlock()
	if binding.CallID == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if call, err := r.activity.Call(ctx, binding.CallID); err == nil && call.OutputSource != nil && call.OutputSource.Ref != "" {
		return activity.SourceReference(binding.CallID, call.OutputSource), nil
	}
	payload := r.activity.CapturePayload(ctx, value, "complete", redactor)
	if payload.Ref == "" {
		return "", nil
	}
	_, err := r.activity.Append(ctx, activity.Event{Binding: binding, Kind: "call.payload", ToolName: name, OutputSource: payload})
	if err != nil {
		return "", err
	}
	return activity.SourceReference(binding.CallID, payload), nil
}

func projectContextFacts(result Result) {
	if diagnostics := modelObject(result["context_diagnostics"]); diagnostics != nil {
		compact := map[string]any{}
		for _, key := range []string{"complete", "components_complete", "binding_status"} {
			if value, ok := diagnostics[key]; ok {
				compact[key] = value
			}
		}
		result["context_diagnostics"] = compact
	}
	delete(result, "host_tools")
	delete(result, "permission")
	for _, key := range []string{"skills", "plugins", "dynamic_mcp", "workflow_templates", "warnings", "running_sessions", "terminal_completions_pending"} {
		value := result[key]
		switch typed := value.(type) {
		case []any:
			if len(typed) == 0 {
				delete(result, key)
			}
		case []string:
			if len(typed) == 0 {
				delete(result, key)
			}
		case int:
			if typed == 0 {
				delete(result, key)
			}
		case float64:
			if typed == 0 {
				delete(result, key)
			}
		}
	}
}
