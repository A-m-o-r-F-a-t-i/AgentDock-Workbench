package app

import (
	"context"
	"testing"

	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
)

func TestAgentDockContextSeparatesDirectBuiltinsFromNestedMCP(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	enabled := true
	if _, err := runtime.dynamicMCP.Manage(context.Background(), toolmcp.ManageRequest{
		Action: "add", Name: "fixture", Description: "Fixture dynamic MCP",
		Transport: "stdio", Command: "fixture-mcp-do-not-run", Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Call(WithModelProjection(scopeHost("tool-domain-context")), "agentdock_context", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	rawRoutes, ok := result["host_tools"].([]any)
	if !ok || len(rawRoutes) == 0 {
		t.Fatalf("missing direct AgentDock routes: %#v", result["host_tools"])
	}
	for _, raw := range rawRoutes {
		route := modelObject(raw)
		if route == nil {
			t.Fatalf("invalid direct route: %#v", raw)
		}
		if route["scope"] != toolmcp.ScopeAgentDockBuiltin || route["origin"] != toolmcp.ScopeAgentDockBuiltin || route["invoke_via"] != toolmcp.InvokeDirect || route["dynamic_mcp_search"] != false {
			t.Fatalf("ambiguous direct route: %#v", route)
		}
	}
	servers, err := runtime.dynamicMCPCapabilityIndex(false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, server := range servers {
		if server.Name != "fixture" {
			continue
		}
		found = true
		if server.Scope != toolmcp.ScopeAgentDockMCP || server.Origin != "agentdock_mcp:fixture" ||
			server.DiscoverVia != toolmcp.ToolList || server.InspectVia != toolmcp.ToolInspect || server.InvokeVia != toolmcp.ToolCall {
			t.Fatalf("ambiguous nested route: %#v", server)
		}
	}
	if !found {
		t.Fatalf("fixture dynamic MCP missing: %#v", servers)
	}
	sharedServers, err := runtime.dynamicMCPCapabilityIndex(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, server := range sharedServers {
		if server.Name == "fixture" && (server.Scope != "" || server.Origin != "" || server.DiscoverVia != "" || server.InspectVia != "" || server.InvokeVia != "") {
			t.Fatalf("standalone route metadata leaked into the shared Nexus context: %#v", server)
		}
	}
}
