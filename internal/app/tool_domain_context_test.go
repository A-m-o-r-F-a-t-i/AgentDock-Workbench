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
	assertNestedMCPRoute(t, modelObject(result["dynamic_mcp_summary"]), "", "")
	if summary := modelObject(result["dynamic_mcp_summary"]); summary["absence_authoritative"] != false || summary["state"] == "fresh" {
		t.Fatalf("unknown directory gained absence authority: %#v", summary)
	}
	page := result
	for attempts := 0; stringArg(page, "next_cursor") != ""; attempts++ {
		if attempts >= 16 {
			t.Fatal("Context continuation did not terminate")
		}
		page, err = runtime.Call(WithModelProjection(scopeHost("tool-domain-context")), "agentdock_context", map[string]any{"cursor": page["next_cursor"]})
		if err != nil {
			t.Fatal(err)
		}
	}
	reference, err := runtime.Call(WithModelProjection(scopeHost("tool-domain-context")), "agentdock_context", map[string]any{"known_context_revision": result["context_revision"]})
	if err != nil {
		t.Fatal(err)
	}
	if modelObject(reference["projection"])["mode"] != "reference" {
		t.Fatalf("repeat context did not use its delivered reference: %#v", reference)
	}
	assertNestedMCPRoute(t, modelObject(reference["dynamic_mcp_summary"]), "", "")
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

func TestUniqueDynamicMCPSelectionKeepsDirectCallTemplate(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	fixture := newCatalogFixture(t, 1)
	if _, err := runtime.dynamicMCP.Manage(t.Context(), toolmcp.ManageRequest{
		Action: "add", Name: "fixture", Description: "Exact selection fixture",
		Transport: "streamable_http", URL: fixture.server.URL,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Call(t.Context(), "mcp_tool_search", map[string]any{"query": "fixture:tool_000"})
	if err != nil {
		t.Fatal(err)
	}
	assertNestedMCPRoute(t, result, "fixture", "")
	selected := modelObject(result["selected"])
	assertNestedMCPRoute(t, selected, "fixture", "fixture:tool_000")
	schema := modelObject(selected["input_schema"])
	next := modelObject(result["next_call"])
	arguments := modelObject(next["arguments"])
	if result["resolution_status"] != "resolved" || schema["$defs"] == nil || schema["required"] == nil || next["tool"] != toolmcp.ToolCall || arguments["name"] != "fixture:tool_000" || next["required_arguments"] == nil || next["arguments_complete"] != false {
		t.Fatalf("unique selection lost full schema or formal invocation: %#v", result)
	}
	if fixture.lists.Load() != 1 || fixture.calls.Load() != 0 {
		t.Fatalf("search performed unexpected work: lists=%d calls=%d", fixture.lists.Load(), fixture.calls.Load())
	}
	arguments["arguments"] = map[string]any{"text": "ok"}
	called, err := runtime.Call(t.Context(), toolmcp.ToolCall, arguments)
	if err != nil {
		t.Fatal(err)
	}
	assertNestedMCPRoute(t, called, "fixture", "fixture:tool_000")
	assertNestedMCPRoute(t, modelObject(called["mcp_catalog"]), "fixture", "")
	if fixture.lists.Load() != 1 || fixture.calls.Load() != 1 {
		t.Fatalf("formal invocation rediscovered or replayed: lists=%d calls=%d", fixture.lists.Load(), fixture.calls.Load())
	}
}

func assertNestedMCPRoute(t *testing.T, result map[string]any, server, qualifiedName string) {
	t.Helper()
	origin := toolmcp.ScopeAgentDockMCP
	if server != "" {
		origin += ":" + server
	}
	for key, want := range map[string]string{
		"scope": toolmcp.ScopeAgentDockMCP, "origin": origin,
		"discover_via": toolmcp.ToolList, "inspect_via": toolmcp.ToolInspect, "invoke_via": toolmcp.ToolCall,
	} {
		if result[key] != want {
			t.Fatalf("%s=%v want %s: %#v", key, result[key], want, result)
		}
	}
	if qualifiedName != "" && result["qualified_name"] != qualifiedName {
		t.Fatalf("qualified_name=%v want %s: %#v", result["qualified_name"], qualifiedName, result)
	}
}
