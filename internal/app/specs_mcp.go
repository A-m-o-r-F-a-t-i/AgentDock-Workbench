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
		{Name: "mcp_tool_list", Contract: mcpToolContract, Title: "List an MCP catalog", Description: "Return a known server's entire two-column tool catalog. Use this directly when the server is known or when a compact business-response reference names a catalog revision that is absent from the current context.", Annotations: readOnlyToolAnnotations(true), Handler: typedToolHandler("mcp_tool_list", func(ctx context.Context, r *Runtime, request toolmcp.ListRequest) (Result, error) {
			return r.dynamicMCP.List(ctx, request)
		})},
		{Name: "mcp_tool_search", Contract: mcpToolContract, Title: "Search dynamic MCP tools", Description: "Search cached dynamic MCP summaries without initializing unrelated services. Known AgentDock built-ins resolve to a successful terminal caller-host route; use that host tool directly and do not search files, source code, logs, or dynamic MCP catalogs for its schema. For a known dynamic service use mcp_tool_list.", Annotations: readOnlyToolAnnotations(true), Handler: typedToolHandler("mcp_tool_search", func(ctx context.Context, r *Runtime, request toolmcp.SearchRequest) (Result, error) {
			return normalizeBuiltinDiscoveryRoute(r.dynamicMCP.Search(ctx, request))
		})},
		{Name: "mcp_tool_inspect", Contract: mcpToolContract, Title: "Inspect a dynamic MCP tool", Description: "Read full dynamic MCP schemas in one request using names: [server:tool, server:*, other:*]. Unqualified AgentDock built-ins resolve to a successful terminal caller-host route; mixed batches retain dynamic schemas and per-selector problems. Services share one cached catalog; skip when valid schemas are already known.", Annotations: readOnlyToolAnnotations(true), Handler: typedToolHandler("mcp_tool_inspect", func(ctx context.Context, r *Runtime, request toolmcp.InspectRequest) (Result, error) {
			return normalizeBuiltinDiscoveryRoute(r.dynamicMCP.Inspect(ctx, request))
		})},
		{Name: "mcp_tool_call", Contract: mcpToolContract, Title: "Call a dynamic MCP tool", Description: "Execute a dynamic MCP tool identified as server:tool, validating against its current cached schema. Computer Use tools additionally require interaction_intent=desktop_gui and a concrete user-task reason. Prior inspect is not mandatory. A trusted execution scope receives the complete two-column catalog once per revision, then compact revision references until the scope or revision changes; clients without trusted conversation metadata retain the complete catalog on every response.", Annotations: mutatingToolAnnotations(true, true), Handler: typedToolHandler("mcp_tool_call", func(ctx context.Context, r *Runtime, request toolmcp.CallRequest) (Result, error) {
			return r.dynamicMCP.Call(ctx, request)
		})},
	}
}
