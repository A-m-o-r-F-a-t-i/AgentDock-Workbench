package mcp

import (
	"strings"
	"testing"
)

func TestHostRouteTextUsesNestedTrustedDetails(t *testing.T) {
	text := toolResultText("mcp_tool_search", map[string]any{
		"code": "HOST_BUILTIN_NOT_DYNAMIC_MCP",
		"details": map[string]any{
			"scope": "agentdock_builtin", "route_kind": "host_builtin", "resolved": true, "terminal": true,
			"required_host_tools": []any{"exec_command"},
		},
	})
	if !strings.Contains(text, "Resolved host-tool handoff: exec_command") ||
		!strings.Contains(text, "caller tools/list namespace") ||
		!strings.Contains(text, "Do not repeat nested dynamic MCP search or inspect") ||
		!strings.Contains(text, "No business action ran") {
		t.Fatalf("host routing handoff was not compact and actionable: %q", text)
	}
}
