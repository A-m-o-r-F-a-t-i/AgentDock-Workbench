package mcp

import (
	"fmt"
	"strings"
)

const maxSessionObserveTextSessions = 16

func toolResultText(name string, structured any) string {
	if name == "session_observe" {
		return sessionObserveResultText(asMap(structured))
	}
	if name == "mcp_tool_search" || name == "mcp_tool_inspect" {
		if result := asMap(structured); result["route_kind"] == "host_builtin" && result["resolved"] == true && result["terminal"] == true {
			return hostBuiltinRouteResultText(result)
		}
	}
	return pretty(structured)
}

func hostBuiltinRouteResultText(result map[string]any) string {
	names := []string{}
	switch values := result["required_host_tools"].(type) {
	case []string:
		names = append(names, values...)
	case []any:
		for _, value := range values {
			if name, ok := value.(string); ok && strings.TrimSpace(name) != "" {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		if name, _ := result["required_host_tool"].(string); strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}
	label := strings.Join(names, ", ")
	if label == "" {
		label = "AgentDock host tool"
	}
	return "Host route resolved: " + label + ". Use the caller host tool from tools/list. Do not search dynamic MCP catalogs, files, source code, or activity logs for this schema. No business action ran."
}

func sessionObserveResultText(result map[string]any) string {
	if message, _ := result["error"].(string); message != "" {
		if code, _ := result["code"].(string); code != "" {
			return fmt.Sprintf("session_observe failed (%s): %s", code, message)
		}
		return "session_observe failed: " + message
	}
	if _, listed := result["sessions"]; listed || result["count"] != nil {
		return sessionObserveListText(result)
	}
	return sessionObserveStatusText(result)
}

func sessionObserveListText(result map[string]any) string {
	items := sessionObserveItems(result["sessions"])
	count, ok := result["count"].(int)
	if !ok {
		count = len(items)
	}
	if count == 0 {
		return "No command sessions are available. Do not call session_observe again until exec_command returns status=running; then use action=status with its session_id."
	}

	var text strings.Builder
	fmt.Fprintf(&text, "%d command session(s) available.", count)
	limit := len(items)
	if limit > maxSessionObserveTextSessions {
		limit = maxSessionObserveTextSessions
	}
	for _, item := range items[:limit] {
		fmt.Fprintf(&text, "\n- session_id=%v status=%v elapsed_ms=%v timed_out=%v", item["session_id"], item["status"], item["elapsed_ms"], item["timed_out"])
	}
	if count > limit {
		fmt.Fprintf(&text, "\n- %d additional session(s) are available in structuredContent.sessions.", count-limit)
	}
	return text.String()
}

func sessionObserveStatusText(result map[string]any) string {
	var text strings.Builder
	fmt.Fprintf(&text, "session_id=%v status=%v elapsed_ms=%v timed_out=%v", result["session_id"], result["status"], result["elapsed_ms"], result["timed_out"])
	if exitCode, exists := result["exit_code"]; exists {
		fmt.Fprintf(&text, " exit_code=%v", exitCode)
	}
	if commandOK, exists := result["command_ok"]; exists {
		fmt.Fprintf(&text, " command_ok=%v", commandOK)
	}
	if commandError, _ := result["command_error"].(string); commandError != "" {
		text.WriteString("\ncommand_error: ")
		text.WriteString(commandError)
	}
	if stdout, _ := result["stdout"].(string); stdout != "" {
		text.WriteString("\nstdout:\n")
		text.WriteString(stdout)
	}
	if stderr, _ := result["stderr"].(string); stderr != "" {
		text.WriteString("\nstderr:\n")
		text.WriteString(stderr)
	}
	if result["stdout_truncated"] == true || result["stderr_truncated"] == true {
		text.WriteString("\nOutput is truncated; inspect structuredContent byte counters before requesting a larger status window.")
	}
	return text.String()
}

func sessionObserveItems(value any) []map[string]any {
	switch items := value.(type) {
	case []map[string]any:
		return items
	case []any:
		result := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if object, ok := item.(map[string]any); ok {
				result = append(result, object)
			}
		}
		return result
	default:
		return nil
	}
}
