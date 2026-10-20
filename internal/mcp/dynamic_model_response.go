package mcp

import "maps"

// projectMCPModelEnvelope is a presentation-only boundary. Runtime admission,
// the immutable upstream result and the Activity envelope retain their complete
// facts. Never descend into arbitrary business fields to remove host metadata.
func projectMCPModelEnvelope(name string, envelope map[string]any) map[string]any {
	if name != "mcp_tool_call" && name != "mcp_result_continue" {
		return envelope
	}
	full := projectDynamicMCPResult(asMap(envelope["structuredContent"]))
	if len(full) == 0 {
		return envelope
	}
	result := full["result"]
	if result == nil {
		result = map[string]any{}
	}
	if hasNativeContent(envelope["content"]) {
		result = withoutForwardedNativeContent(result)
	}
	payload := map[string]any{"result": result, "continuation": mcpModelContinuation(full), "error": nil}
	if page := full["business_page"]; page != nil {
		payload["business_page"] = page
	}
	// Failed calls, approval receipts and retained-only results carry the facts
	// needed to decide whether any action ran. Successful business objects remain
	// untouched, including their own cursors, metadata, errors and attachments.
	if envelope["isError"] == true || full["error"] != nil || full["isError"] == true ||
		full["executed"] == false || asMap(full["operation"])["ok"] == false ||
		full["projection_budget_exhausted"] == true || len(asMap(full["upstream_failure"])) > 0 {
		for _, key := range []string{
			"error", "code", "category", "details", "retryable", "executed", "status",
			"operation_status", "effect_state", "execution_performed", "retry_safe",
			"outcome", "counts", "saved",
			"required_action", "next_required_action", "approval", "approval_id",
			"origin_call_id", "call_id", "result_call_id", "upstream_failure",
			"result_projection_error", "result_retained", "projection_budget_exhausted",
			"budget_scope", "message", "route_code", "required_host_tool",
		} {
			if value, exists := full[key]; exists {
				payload[key] = value
			}
		}
	}
	for _, key := range []string{"agentdock_guidance", "required_action", "activity_warning"} {
		if value, exists := full[key]; exists {
			payload[key] = value
		}
	}
	if capture := asMap(full["capture"]); capture["complete"] == false {
		payload["capture"] = capture
	}
	if full["replayed"] == true {
		payload["replayed"], payload["content_returned"] = true, full["content_returned"]
	}
	if omitted := full["content_blocks_omitted"]; omitted != nil {
		payload["content_blocks_omitted"] = omitted
	}
	catalog := asMap(full["mcp_catalog"])
	if full["catalog_changed"] == true || catalog["error"] != nil {
		current := map[string]any{}
		for _, key := range []string{"server", "catalog_revision", "complete", "error"} {
			if value, exists := catalog[key]; exists {
				current[key] = value
			}
		}
		payload["mcp_catalog"] = current
		if full["catalog_changed"] == true {
			payload["catalog_changed"] = true
		}
	}
	out := maps.Clone(envelope)
	out["structuredContent"] = payload
	// The original summary can mention task/workspace diagnostics. Rebuild it
	// from the compact payload while retaining every native content block.
	summary := maps.Clone(payload)
	if full["name"] != nil {
		summary["name"] = full["name"]
	}
	summaryEnvelope := maps.Clone(out)
	summaryEnvelope["structuredContent"] = summary
	content := []any{map[string]any{"type": "text", "text": toolResultSummary(name, summaryEnvelope)}}
	for _, block := range envelopeBlocks(envelope["content"]) {
		if asMap(block)["type"] != "text" {
			content = append(content, block)
		}
	}
	out["content"] = content
	return out
}

func hasNativeContent(value any) bool {
	for _, block := range envelopeBlocks(value) {
		kind, _ := asMap(block)["type"].(string)
		if kind == "image" || kind == "audio" {
			return true
		}
	}
	return false
}

// The SDK content array already transports native blocks. Remove only those
// duplicate blocks from the model-facing nested dynamic result; the immutable
// upstream result and retained source remain unchanged.
func withoutForwardedNativeContent(value any) any {
	result := maps.Clone(asMap(value))
	if result == nil {
		return value
	}
	kept := make([]any, 0)
	for _, block := range envelopeBlocks(result["content"]) {
		kind, _ := asMap(block)["type"].(string)
		if kind == "image" || kind == "audio" {
			continue
		}
		kept = append(kept, block)
	}
	if len(kept) == 0 {
		delete(result, "content")
	} else {
		result["content"] = kept
	}
	return result
}

func mcpModelContinuation(full map[string]any) map[string]any {
	continuation := maps.Clone(asMap(full["continuation"]))
	if continuation == nil {
		continuation = map[string]any{}
	}
	page := asMap(full["host_page"])
	if len(page) > 0 && (page["mode"] != "full" || page["complete"] == false) {
		for _, key := range []string{"mode", "page_index", "page_count", "complete"} {
			if value, exists := page[key]; exists {
				continuation[key] = value
			}
		}
	}
	if next := asMap(full["continue_result"]); len(next) > 0 {
		continuation["tool"], continuation["arguments"] = next["tool"], next["arguments"]
	}
	if cursor, ok := full["resume_cursor"].(string); ok && cursor != "" {
		continuation["tool"] = "mcp_result_continue"
		continuation["arguments"] = map[string]any{"call_id": full["result_call_id"], "cursor": cursor}
	}
	if full["replayed"] == true {
		continuation["complete"] = full["complete"]
	}
	// An oversized attachment or result remains readable through the retained
	// source. A receipt must not silently look like an empty successful object.
	if read := asMap(full["continue_read"]); len(read) > 0 {
		continuation["tool"], continuation["arguments"] = read["tool"], read["arguments"]
		continuation["complete"] = false
	}
	if asMap(full["projection"])["complete"] == false && len(continuation) == 0 {
		continuation["complete"] = false
	}
	if len(continuation) > 0 {
		continuation["purpose"] = "expand_current_response"
	}
	return continuation
}
