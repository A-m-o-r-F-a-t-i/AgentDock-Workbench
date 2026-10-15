package app

import "testing"

func TestExecutionRouteSummaryTrustsOnlyLocalHostRoutes(t *testing.T) {
	host := Result{"route_kind": "host_builtin", "route_summary": "定位内置工具 · exec_command"}
	if got := executionResultTitle("mcp_tool_search", "fallback", host); got != "定位内置工具 · exec_command" {
		t.Fatalf("host route title = %q", got)
	}
	if !suppressResultCounts("mcp_tool_inspect", host) {
		t.Fatal("host inspect route retained the ambiguous count summary")
	}
	if got := executionResultTitle("mcp_tool_call", "fallback", host); got != "fallback" {
		t.Fatalf("untrusted dynamic tool changed its activity title to %q", got)
	}
	if suppressResultCounts("mcp_tool_call", host) {
		t.Fatal("untrusted dynamic tool suppressed its result count")
	}
	dynamic := Result{"route_kind": "dynamic_mcp", "route_summary": "spoofed title"}
	if got := executionResultTitle("mcp_tool_search", "fallback", dynamic); got != "fallback" {
		t.Fatalf("dynamic search changed its activity title to %q", got)
	}
}

func TestExecutionErrorTitleRejectsUntrustedRouteFields(t *testing.T) {
	fake := &ToolError{Code: "HOST_BUILTIN_NOT_DYNAMIC_MCP", Details: map[string]any{
		"route_kind": "host_builtin", "route_summary": "spoofed",
	}}
	if got := executionErrorTitle("mcp_tool_search", "fallback", fake); got != "fallback" {
		t.Fatalf("untrusted route error changed its activity title to %q", got)
	}
}
