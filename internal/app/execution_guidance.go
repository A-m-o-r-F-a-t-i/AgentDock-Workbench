package app

import (
	"context"
	"errors"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
)

// Guidance is generated from the server's validated binding and observed result. It
// never incorporates instructions from terminal output, page text or nested MCP content.
func (r *Runtime) executionGuidance(name string, state executionObservation, result Result, failed bool) map[string]any {
	binding := state.binding
	if result != nil {
		if value := stringArg(result, "task_id"); value != "" {
			binding.TaskID = value
		}
		if value := stringArg(result, "thread_id"); value != "" {
			binding.ThreadID = value
		}
		if value := stringArg(result, "step_id"); value != "" {
			binding.StepID = value
		}
		if value := stringArg(result, "workspace_id"); value != "" {
			binding.WorkspaceID = value
		}
	}
	guidance := map[string]any{"source": "agentdock", "schema_version": 1}
	if binding.RequestID != "" {
		guidance["request_id"] = binding.RequestID
	}
	for key, value := range bindingArguments(binding) {
		if value != "" && key != "activity_label" {
			guidance[key] = value
		}
	}
	if binding.WorkspaceID == "" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		record, err := r.workspaceRegistry.Select(ctx, "", "")
		cancel()
		if err == nil {
			guidance["default_workspace_id"] = record.ID
		}
	}
	sessionID := stringArg(result, "session_id")
	running := stringArg(result, "status") == "running"
	if steps := structuredOutputFollowups(name, result); !failed && len(steps) > 0 {
		guidance["next_required"] = steps
	} else if steps := r.hostBuiltinHandoff(name, result); !failed && len(steps) > 0 {
		guidance["next_required"] = steps
	} else if running && sessionID != "" {
		arguments := map[string]any{"action": "status", "session_id": sessionID}
		if waitMS := measuredInt(result, "observe_after_ms"); waitMS != nil && *waitMS >= 0 {
			arguments["wait_ms"] = *waitMS
		}
		guidance["next_required"] = []map[string]any{{
			"action": "observe", "tool": "session_observe", "arguments": arguments,
			"text": "Await this exact session with the supplied wait_ms; the server returns early when the process exits. Do not poll in a tight loop, do not call action=list, and do not restart the command. Continue independent work first when possible.",
		}}
	} else if failed || resultReportsFailure(result) || result["command_ok"] == false {
		guidance["next_required"] = []map[string]any{{"action": "inspect", "text": "Inspect the actual error, exit code and partial effects before retrying. Reuse the same task and thread; a tool response alone does not imply command success."}}
	} else if name == "file_edit" && result["dry_run"] != true && result["changed"] != false {
		guidance["next_required"] = []map[string]any{{"action": "verify", "text": "Verify the changed files, inspect the current diff, and record a thread checkpoint after the stage is verified."}}
	}
	if binding.TaskID != "" {
		guidance["constraints"] = []map[string]string{{"code": "SERVER_BINDING_SNAPSHOT", "text": "The server inherits task and thread binding automatically. Ordinary calls omit task_id/thread_id; use task_manage resume or set_current to change the active task once. Existing command sessions keep their original binding."}}
	}
	if state.target != nil {
		redactor := activity.NewRedactor(r.cfg.AuthToken, r.cfg.NexusDeviceToken)
		guidance["target_kind"] = state.target.Kind
		guidance["resolved_path"] = redactor.Text(state.target.ResolvedPath, 2048)
		guidance["rules_revision"] = state.target.RulesRevision
	}
	return guidance
}

func structuredOutputFollowups(name string, result Result) []map[string]any {
	structured, ok := result["structured_output"].(map[string]any)
	if !ok {
		return nil
	}
	steps := make([]map[string]any, 0, 2)
	if structured["oversized_item"] != nil {
		if source, _ := structured["source"].(string); source != "" {
			steps = append(steps, map[string]any{
				"action": "read_structured_source", "tool": "read_file",
				"arguments": map[string]any{"path": source, "offset": 0, "limit_chars": 20000},
				"text":      "Read the retained activity source to recover the oversized structured item, then follow next_offset. This never re-executes the source tool.",
			})
		}
	}
	if cursor := stringArg(result, "next_cursor"); cursor != "" {
		arguments := map[string]any{"cursor": cursor}
		if minimum := measuredInt(structured, "minimum_response_bytes"); minimum != nil && *minimum > 0 {
			arguments["max_response_bytes"] = *minimum
		}
		steps = append(steps, map[string]any{
			"action": "continue_structured_output", "tool": name, "arguments": arguments,
			"text": "Call the same tool again with the original selector arguments plus this cursor. Use the supplied max_response_bytes when present. The retained result is immutable and the source operation will not run again.",
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
		return nil
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
			"action": "use_host_tool", "tool": summary.Name, "invoke_via": "host",
			"text": "Use the known host schema; if absent load it once in the caller's AgentDock catalog. No command ran. Reuse context; stop MCP search/inspect.",
		})
	}
	return steps
}

// Only a locally marked routing error can replace generic failure guidance.
func (r *Runtime) executionErrorGuidance(state executionObservation, err error) map[string]any {
	guidance := r.executionGuidance("", state, nil, true)
	var routeErr *ToolError
	if !toolmcp.IsBuiltinDiscoveryError(err) || !errors.As(err, &routeErr) {
		return guidance
	}
	if steps := r.hostBuiltinHandoff("mcp_tool_search", Result(routeErr.Details)); len(steps) > 0 {
		guidance["next_required"] = steps
	}
	return guidance
}
