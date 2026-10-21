package mcp

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/uvwt/agentdock/internal/app"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

const maxSessionObserveTextSessions = 16

func toolResultText(name string, structured any) string {
	if name == "session_observe" {
		return sessionObserveResultText(asMap(structured))
	}
	if name == "mcp_tool_search" || name == "mcp_tool_inspect" {
		result := asMap(structured)
		route := result
		if details := asMap(result["details"]); details["scope"] == "agentdock_builtin" || details["route_kind"] == "host_builtin" {
			route = details
		}
		if (route["scope"] == "agentdock_builtin" || route["route_kind"] == "host_builtin") && route["resolved"] == true && route["terminal"] == true {
			return hostBuiltinRouteText(route)
		}
	}
	return pretty(structured)
}

func compactToolEnvelopeText(name string, envelope map[string]any) map[string]any {
	if name == "view_image" {
		return envelope
	}
	output := maps.Clone(envelope)
	blocks := envelopeBlocks(envelope["content"])
	kept := make([]any, 0, len(blocks))
	removedDuplicate := false
	keptBusinessText := false
	var remoteStructured any
	if name == "mcp_tool_call" {
		remoteStructured = asMap(asMap(envelope["structuredContent"])["result"])["structuredContent"]
	}
	for _, block := range blocks {
		object := asMap(block)
		if object["type"] != "text" {
			kept = append(kept, block)
			continue
		}
		text, _ := object["text"].(string)
		if strings.HasPrefix(strings.TrimSpace(text), "[[AGENTDOCK_MCP_CATALOG_V1]]") {
			removedDuplicate = true
			continue
		}
		if name == "mcp_tool_call" {
			if remoteStructured != nil && jsonTextMatches(text, remoteStructured) {
				removedDuplicate = true
				continue
			}
			kept = append(kept, block)
			keptBusinessText = true
			continue
		}
		removedDuplicate = true
	}
	if name != "mcp_tool_call" || (!keptBusinessText && (remoteStructured != nil || removedDuplicate)) {
		kept = append([]any{map[string]any{"type": "text", "text": toolResultSummary(name, envelope)}}, kept...)
	}
	output["content"] = kept
	if name == "mcp_tool_call" {
		output = removeForwardedContentDuplicates(output, kept)
	}
	return output
}

// Dynamic MCP native blocks are already transported in the outer MCP content
// array. Remove only exact nested duplicates at the final text-presentation
// boundary; retain unique nested attachments and the immutable upstream result.
func removeForwardedContentDuplicates(envelope map[string]any, outer []any) map[string]any {
	structured := maps.Clone(asMap(envelope["structuredContent"]))
	result := maps.Clone(asMap(structured["result"]))
	if len(result) == 0 {
		return envelope
	}
	nested := envelopeBlocks(result["content"])
	if len(nested) == 0 {
		return envelope
	}
	kept := make([]any, 0, len(nested))
	removed := false
	for _, candidate := range nested {
		kind, _ := asMap(candidate)["type"].(string)
		if kind != "text" && slicesContainExact(outer, candidate) {
			removed = true
			continue
		}
		kept = append(kept, candidate)
	}
	if !removed {
		return envelope
	}
	if len(kept) == 0 {
		delete(result, "content")
	} else {
		result["content"] = kept
	}
	structured["result"] = result
	out := maps.Clone(envelope)
	out["structuredContent"] = structured
	return out
}

func slicesContainExact(items []any, candidate any) bool {
	for _, item := range items {
		if reflect.DeepEqual(item, candidate) {
			return true
		}
	}
	return false
}

func jsonTextMatches(text string, structured any) bool {
	return toolcore.JSONTextEquivalent(text, structured)
}

func toolResultSummary(name string, envelope map[string]any) string {
	result := asMap(envelope["structuredContent"])
	if name == "session_observe" {
		text := sessionObserveResultText(result)
		if hasResponseAdditions(result) {
			text += " " + app.InsertionCompatibilitySummary
		}
		return boundedToolSummary(text)
	}
	displayName := name
	if name == "mcp_tool_call" {
		if qualified, _ := result["name"].(string); qualified != "" {
			displayName = qualified
		}
	}
	if envelope["isError"] == true {
		message, _ := result["error"].(string)
		code, _ := result["code"].(string)
		label := displayName + " failed"
		if code != "" {
			label += " (" + code + ")"
		}
		if message != "" {
			label += ": " + message
		}
		if hasResponseAdditions(result) {
			label += " " + app.InsertionCompatibilitySummary
		}
		return boundedToolSummary(label)
	}
	verb := "succeeded"
	if (name == "exec_command" || name == "session_observe" || name == "session_act") && result["session_id"] != nil {
		verb = "returned"
	}
	parts := []string{displayName + " " + verb}
	if hasResponseAdditions(result) {
		parts = append(parts, app.InsertionCompatibilitySummary)
	}
	for _, key := range []string{"status", "outcome", "action", "session_id", "exit_code", "command_ok", "delivery", "unchanged", "changed", "files_changed", "count", "path", "new_path", "task_id", "workspace_id"} {
		if value := summaryField(result[key]); value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	parts = append(parts, "Read structuredContent for the complete result.")
	return boundedToolSummary(strings.Join(parts, " "))
}

func hasResponseAdditions(result map[string]any) bool {
	value := asMap(result["agentdock_guidance"])["response_additions"]
	switch items := value.(type) {
	case []any:
		return len(items) > 0
	case []app.UserResponseAddition:
		return len(items) > 0
	default:
		return false
	}
}

func summaryField(value any) string {
	switch typed := value.(type) {
	case string:
		return boundedText(strings.Join(strings.Fields(typed), " "), 160)
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return fmt.Sprint(typed)
	default:
		return ""
	}
}

func boundedToolSummary(text string) string {
	return boundedText(strings.Join(strings.Fields(text), " "), ToolSummaryMaxBytes)
}

func boundedText(text string, limit int) string {
	if limit < 4 || len(text) <= limit {
		return text
	}
	end := limit - 3
	for end > 0 && !utf8.ValidString(text[:end]) {
		end--
	}
	return text[:end] + "..."
}

func hostBuiltinRouteText(result map[string]any) string {
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
		label = "AgentDock direct device built-in"
	}
	return "Resolved host-tool handoff: " + label + ". Invoke the AgentDock direct device built-in from the caller tools/list namespace. Do not repeat nested dynamic MCP search or inspect. No business action ran."
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
	fmt.Fprintf(&text, "session_id=%v status=%v elapsed_ms=%v timed_out=%v", result["session_id"], result["status"], result["elapsed_ms"], result["timed_out"] == true)
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
