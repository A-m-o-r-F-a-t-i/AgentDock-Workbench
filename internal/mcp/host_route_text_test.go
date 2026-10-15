package mcp

import (
	"strings"
	"testing"
)

func TestHostRouteErrorTextUsesNestedTrustedDetails(t *testing.T) {
	text := toolResultText("mcp_tool_search", map[string]any{
		"code": "HOST_BUILTIN_NOT_DYNAMIC_MCP",
		"details": map[string]any{
			"route_kind": "host_builtin", "resolved": true, "terminal": true,
			"required_host_tools": []any{"exec_command"},
		},
	})
	if !strings.Contains(text, "Wrong tool namespace: exec_command") || !strings.Contains(text, "No business action ran") {
		t.Fatalf("host routing error was not compact and actionable: %q", text)
	}
}
