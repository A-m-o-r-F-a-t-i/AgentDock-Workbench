package mcp

import (
	"testing"

	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
)

func TestDynamicMCPRouteMetadataIsCanonical(t *testing.T) {
	entry := map[string]any{}
	annotateDynamicMCPTool(entry, "fixture", "fixture:echo")
	for key, want := range map[string]any{
		"scope": ScopeAgentDockMCP, "origin": "agentdock_mcp:fixture",
		"discover_via": ToolList, "inspect_via": ToolInspect, "invoke_via": ToolCall,
		"qualified_name": "fixture:echo",
	} {
		if entry[key] != want {
			t.Fatalf("%s=%v want %v: %#v", key, entry[key], want, entry)
		}
	}

	summary := mcpclient.ToolSummary{Server: "fixture", QualifiedName: "fixture:echo"}
	annotateDynamicMCPToolSummary(&summary)
	if summary.Scope != ScopeAgentDockMCP || summary.Origin != "agentdock_mcp:fixture" || summary.InvokeVia != ToolCall {
		t.Fatalf("dynamic summary metadata = %#v", summary)
	}
}

func TestCatalogResultCarriesDynamicMCPRouteMetadata(t *testing.T) {
	service := &Service{}
	result := service.catalogResult(mcpclient.Catalog{
		Server: "fixture", Revision: "rev", Complete: true,
		Tools: map[string]mcpclient.Tool{
			"echo": {Name: "echo", Description: "Echo", InputSchema: map[string]any{"type": "object"}},
		},
	})
	if result["scope"] != ScopeAgentDockMCP || result["origin"] != "agentdock_mcp:fixture" || result["invoke_via"] != ToolCall {
		t.Fatalf("catalog route metadata = %#v", result)
	}
	tools := result["tools"].([]map[string]any)
	if len(tools) != 1 || tools[0]["qualified_name"] != "fixture:echo" || tools[0]["scope"] != ScopeAgentDockMCP || tools[0]["invoke_via"] != ToolCall {
		t.Fatalf("catalog tool metadata = %#v", tools)
	}
}
