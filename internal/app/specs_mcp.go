package app

import (
	"context"

	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
)

func dynamicMCPToolSpecs() []ToolSpec {
	return []ToolSpec{
		{Name: "mcp_manage", Contract: mcpToolContract, Title: "Manage dynamic MCP servers", Description: "Register, inspect, enable, disable, refresh, remove, or manage the isolated environment of dynamic MCP servers. Dynamic MCP tools remain separate from AgentDock built-in tools.", Annotations: mutatingToolAnnotations(true, true), Handler: typedToolHandler("mcp_manage", func(ctx context.Context, r *Runtime, request toolmcp.ManageRequest) (Result, error) {
			return r.dynamicMCP.Manage(ctx, request)
		})},
		{Name: "mcp_tool_list", Contract: mcpToolContract, Title: "List an MCP catalog", Description: "Return a known server's two-column catalog once per conversation and revision; repeats are compact references. Use force_full after model-context loss.", Annotations: readOnlyToolAnnotations(true), Handler: typedToolHandler("mcp_tool_list", func(ctx context.Context, r *Runtime, request toolmcp.ListRequest) (Result, error) {
			return r.dynamicMCP.List(ctx, request)
		})},
		{Name: "mcp_tool_search", Contract: mcpToolContract, Title: "Search dynamic MCP tools", Description: "Search cached dynamic MCP summaries without initializing unrelated services. Searching a known AgentDock host tool is a terminal, non-retryable routing error; call that host tool directly. For a known dynamic service use mcp_tool_list.", Annotations: readOnlyToolAnnotations(true), Handler: typedToolHandler("mcp_tool_search", func(ctx context.Context, r *Runtime, request toolmcp.SearchRequest) (Result, error) {
			return r.dynamicMCP.Search(ctx, request)
		})},
		{Name: "mcp_tool_inspect", Contract: mcpToolContract, Title: "Inspect a dynamic MCP tool", Description: "Read dynamic MCP schemas in one request using names: [server:tool, server:*, other:*]. Unqualified AgentDock host tools return a terminal routing error; mixed batches retain dynamic schemas and per-selector problems. Repeated schemas use compact revision references unless force_full=true.", Annotations: readOnlyToolAnnotations(true), Handler: typedToolHandler("mcp_tool_inspect", func(ctx context.Context, r *Runtime, request toolmcp.InspectRequest) (Result, error) {
			if modelProjectionRequested(ctx) {
				ctx = toolmcp.WithDeferredSchemaDelivery(ctx)
			}
			return r.dynamicMCP.Inspect(ctx, request)
		})},
		{Name: "mcp_tool_call", Contract: mcpToolContract, Title: "Call a dynamic MCP tool", Description: "Execute a dynamic MCP tool identified as server:tool, validating against its cached schema. Computer Use additionally requires interaction_intent=desktop_gui and a concrete reason. List and call share one complete catalog delivery per trusted conversation and revision; later responses use compact references.", Annotations: mutatingToolAnnotations(true, true), Handler: typedToolHandler("mcp_tool_call", func(ctx context.Context, r *Runtime, request toolmcp.CallRequest) (Result, error) {
			return r.dynamicMCP.Call(ctx, request)
		})},
	}
}
