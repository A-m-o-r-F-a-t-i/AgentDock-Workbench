package mcp

import (
	"strings"

	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
)

const (
	// ScopeAgentDockBuiltin identifies tools exposed directly by the AgentDock
	// gateway. They are never members of a nested dynamic MCP catalog.
	ScopeAgentDockBuiltin = "agentdock_builtin"
	// ScopeAgentDockMCP identifies tools provided by dynamic MCP servers that are
	// registered inside AgentDock.
	ScopeAgentDockMCP = "agentdock_mcp"
	// InvokeDirect means the caller invokes the already exposed AgentDock tool.
	InvokeDirect = "direct"
)

func dynamicMCPOrigin(server string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return ScopeAgentDockMCP
	}
	return ScopeAgentDockMCP + ":" + server
}

func annotateDynamicMCPRoute(result map[string]any, server string) {
	if result == nil {
		return
	}
	result["scope"] = ScopeAgentDockMCP
	result["origin"] = dynamicMCPOrigin(server)
	result["discover_via"] = ToolList
	result["inspect_via"] = ToolInspect
	result["invoke_via"] = ToolCall
}

func annotateDynamicMCPTool(result map[string]any, server, qualifiedName string) {
	annotateDynamicMCPRoute(result, server)
	result["qualified_name"] = qualifiedName
}

func annotateDynamicMCPToolSummary(item *mcpclient.ToolSummary) {
	if item == nil {
		return
	}
	item.Scope = ScopeAgentDockMCP
	item.Origin = dynamicMCPOrigin(item.Server)
	item.InvokeVia = ToolCall
}
