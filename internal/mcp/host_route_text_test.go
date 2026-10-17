package mcp

import (
	"strings"
	"testing"
)

func TestHostRouteErrorTextUsesNestedTrustedDetails(t *testing.T) {
	text := toolResultText("mcp_tool_search", map[string]any{
		"code": "HOST_BUILTIN_NOT_DYNAMIC_MCP",
		"details": map[string]any{
			"scope": "agentdock_builtin", "route_kind": "host_builtin", "resolved": true, "terminal": true,
			"required_host_tools": []any{"exec_command"},
		},
	})
	if !strings.Contains(text, "Wrong tool scope: exec_command") ||
		!strings.Contains(text, "AgentDock direct device built-in") ||
		!strings.Contains(text, "Do not repeat nested dynamic MCP search or inspect") ||
		!strings.Contains(text, "No business action ran") {
		t.Fatalf("host routing error was not compact and actionable: %q", text)
	}
}
