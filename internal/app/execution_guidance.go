package app

import (
	"errors"

	toolcapability "github.com/uvwt/agentdock/internal/tool/capability"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
)

// Guidance is generated from the server's validated binding and observed result. It
// never incorporates instructions from terminal output, page text or nested MCP content.
func (r *Runtime) executionGuidance(name string, args map[string]any, state executionObservation, result Result, failed bool) map[string]any {
	var steps []map[string]any
	if !failed {
		steps = structuredOutputFollowups(name, args, result)
		if len(steps) == 0 {
			steps = r.hostBuiltinHandoff(name, result)
		}
	}
	if len(steps) > 0 {
		return map[string]any{"source": "agentdock", "schema_version": 1, "next_required": steps[:1]}
	}
	if failed || result["already_delivered"] == true || (name == "session_observe" && result["changed"] == false) {
		return nil
	}
	if stringArg(result, "status") != "running" || stringArg(result, "session_id") == "" || !(isCommandExecutionTool(name) || name == "session_observe" || name == "session_act") {
		return nil
	}
	arguments := map[string]any{"action": "status", "session_id": result["session_id"]}
	if cursor := result["next_offset"]; cursor != nil {
		arguments["offset"] = cursor
	}
	if wait := measuredInt(result, "observe_after_ms"); wait != nil && *wait >= 0 {
		arguments["wait_ms"] = *wait
	}
	delivery := "explicit_observation"
	if state.binding.ConversationID != "" && state.binding.SourceOwnerKey != "" {
		delivery = "next_root_call"
	}
	return map[string]any{"source": "agentdock", "schema_version": 1, "continuation": map[string]any{
		"kind": "command_session", "session_id": result["session_id"], "completion_delivery": delivery,
		"recommended_action":  "continue_independent_work",
		"observe_when_needed": map[string]any{"tool": "session_observe", "arguments": arguments},
		"text":                "Continue independent work. Observe this exact session only when the next decision requires its result; do not restart the command.",
	}}
}

func structuredOutputFollowups(name string, args map[string]any, result Result) []map[string]any {
	// Bootstrap already supports routing. Its explicit detail action is not a
	// conversation-wide prerequisite, and does not need a second guidance copy.
	if name == "agentdock_context" && result["bootstrap_complete"] == true {
		return nil
	}
	if toolcontract.ResolveOutputClass(name, args, result) != toolcontract.OutputRequiredFull {
		return nil
	}
	if name == "read_file" && result["has_more"] == true {
		if continuation := modelObject(result["continue_read"]); continuation != nil {
			return []map[string]any{{"action": "continue_required_content", "tool": "read_file", "arguments": continuation["arguments"], "text": "Continue this immutable contract from its exact byte cursor before relying on the complete contract."}}
		}
	}
	structured, ok := result["structured_output"].(map[string]any)
	if !ok {
		return nil
	}
	steps := make([]map[string]any, 0, 2)
	if result["has_more"] == true && stringArg(result, "next_cursor") == "" {
		if source := stringArg(structured, "source"); source != "" {
			return []map[string]any{{"action": "read_required_source", "tool": "read_file", "arguments": map[string]any{"path": source, "offset": 0, "limit_chars": 8000}, "text": "Continue the immutable required source. This reads retained data without re-executing the original tool."}}
		}
	}

	if structured["oversized_item"] != nil {
		if source, _ := structured["source"].(string); source != "" {
			steps = append(steps, map[string]any{
				"action": "read_structured_source", "tool": "read_file",
				"arguments": map[string]any{"path": source, "offset": 0, "limit_chars": 8000},
				"text":      "Read the retained activity source to recover the oversized structured item, then follow next_offset. This never re-executes the source tool.",
			})
		}
	}
	if cursor := stringArg(result, "next_cursor"); cursor != "" {
		arguments := map[string]any{}
		if !isContextTool(name) {
			arguments = structuredSelectorArguments(args)
			if budget, exists := args["max_response_bytes"]; exists {
				arguments["max_response_bytes"] = budget
			}
		}
		arguments["cursor"] = cursor
		if minimum := measuredInt(structured, "minimum_response_bytes"); !isContextTool(name) && minimum != nil && *minimum > 0 {
			arguments["max_response_bytes"] = *minimum
		}
		steps = append(steps, map[string]any{
			"action": "continue_structured_output", "tool": name, "arguments": arguments,
			"text": "Use these continuation arguments to read the retained result. If the host schema omits cursor, use structured_output.continue_read and follow next_offset instead. Never repeat the initial request to read more output.",
		})
	}
	return steps
}

// Only trusted top-level discovery results can issue a host handoff. Upstream
// MCP content and command output never supply tool names or execution arguments.
func (r *Runtime) hostBuiltinHandoff(name string, result Result) []map[string]any {
	if !isHostBuiltinRoute(name, result) || result["resolved"] != true || result["terminal"] != true {
		return nil
	}
	summaries, ok := result["builtin_tools"].([]toolmcp.BuiltinToolSummary)
	if !ok {
		// Repeated local routes retain exact names without full descriptions.
		names, compact := result["required_host_tools"].([]string)
		if !compact || result["repeated"] != true {
			return nil
		}
		for _, exact := range names {
			summaries = append(summaries, toolmcp.BuiltinToolSummary{
				Name: exact, Scope: toolmcp.ScopeAgentDockBuiltin, Origin: toolmcp.ScopeAgentDockBuiltin,
				InvokeVia: toolmcp.InvokeDirect,
			})
		}
	}
	steps := []map[string]any{}
	seen := map[string]bool{}
	for _, summary := range summaries {
		if seen[summary.Name] {
			continue
		}
		if _, available := r.ToolDefinition(summary.Name); !available {
			continue
		}
		seen[summary.Name] = true
		steps = append(steps, map[string]any{
			"action": "call_host_tool", "tool": summary.Name, "invoke_via": toolmcp.InvokeDirect,
			"scope": toolmcp.ScopeAgentDockBuiltin, "origin": toolmcp.ScopeAgentDockBuiltin,
			"text": "Call this AgentDock built-in from the caller's current tools/list if available; otherwise refresh the host tool directory. Do not search nested MCP again. No business action ran.",
		})
	}
	return steps
}

// Only a locally marked routing error can replace generic failure guidance.
func (r *Runtime) executionErrorGuidance(state executionObservation, err error) map[string]any {
	guidance := map[string]any{"source": "agentdock", "schema_version": 1}
	var routeErr *ToolError
	if errors.As(err, &routeErr) && routeErr.Code == "TOOL_BUNDLE_REQUIRED" {
		bundle := stringArg(routeErr.Details, "bundle")
		guidance["next_required"] = []map[string]any{{
			"action": "load_builtin_bundle", "tool": toolcapability.ToolLoad,
			"arguments": map[string]any{"action": toolcapability.ActionLoad, "name": bundle},
			"text":      "Load this conversation-scoped built-in bundle, refresh tools/list, then retry the required business tool once. No bundled action ran.",
		}}
		return guidance
	}
	if !toolmcp.IsBuiltinDiscoveryError(err) || !errors.As(err, &routeErr) {
		return nil
	}
	if steps := r.hostBuiltinHandoff("mcp_tool_search", Result(routeErr.Details)); len(steps) > 0 {
		guidance["next_required"] = steps[:1]
		return guidance
	}
	return nil
}
