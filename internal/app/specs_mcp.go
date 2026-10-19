package app

import (
	"context"

	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
)

func dynamicMCPToolSpecs() []ToolSpec {
	return []ToolSpec{
		{Name: "mcp_manage", Contract: mcpToolContract, Title: "Manage dynamic MCP servers", Description: "Manage only dynamic MCP servers registered inside AgentDock. This control plane never manages ChatGPT host tools or AgentDock direct device built-ins.", Annotations: mutatingToolAnnotations(true, true), Handler: typedToolHandler("mcp_manage", func(ctx context.Context, r *Runtime, request toolmcp.ManageRequest) (Result, error) {
			if request.Action == "recover" {
				return r.recoverManagedMCP(ctx, request)
			}
			return r.dynamicMCP.Manage(ctx, request)
		})},
		{Name: "mcp_tool_list", Contract: mcpToolContract, Title: "List a dynamic MCP catalog", Description: "List tools from one known dynamic MCP server returned by agentdock_context. It never lists ChatGPT host tools or AgentDock direct device built-ins. Repeated catalog revisions may return compact references; use force_full after model-context loss.", Annotations: readOnlyToolAnnotations(true), Handler: typedToolHandler("mcp_tool_list", func(ctx context.Context, r *Runtime, request toolmcp.ListRequest) (Result, error) {
			return r.dynamicMCP.List(ctx, request)
		})},
		{Name: "mcp_tool_search", Contract: mcpToolContract, Title: "Search dynamic MCP tools", Description: "Search AgentDock dynamic MCP catalogs: exact names precede up to 5 fuzzy candidates. A unique result includes its full input schema and next call; follow retained pages for a large schema. Cached or stale zero results do not prove absence. Direct device built-ins return a terminal wrong-scope error.", Annotations: readOnlyToolAnnotations(true), Handler: typedToolHandler("mcp_tool_search", func(ctx context.Context, r *Runtime, request toolmcp.SearchRequest) (Result, error) {
			return r.dynamicMCP.Search(ctx, request)
		})},
		{Name: "mcp_tool_inspect", Contract: mcpToolContract, Title: "Inspect a dynamic MCP tool", Description: "Inspect only qualified nested MCP selectors such as server:tool or server:*. Bare AgentDock direct device built-ins return a terminal wrong-scope result; mixed batches retain valid dynamic schemas and report each wrong-scope selector. Repeated schemas use compact references unless force_full=true.", Annotations: readOnlyToolAnnotations(true), Handler: typedToolHandler("mcp_tool_inspect", func(ctx context.Context, r *Runtime, request toolmcp.InspectRequest) (Result, error) {
			if modelProjectionRequested(ctx) {
				ctx = toolmcp.WithDeferredSchemaDelivery(ctx)
			}
			return r.dynamicMCP.Inspect(ctx, request)
		})},
		{Name: "mcp_tool_call", Contract: mcpToolContract, Title: "Call a dynamic MCP tool", Description: "Invoke one exact qualified nested dynamic MCP tool using server:tool from the current AgentDock catalog. Direct AgentDock built-ins and ChatGPT host tools are rejected. The request is validated against the cached schema; oversized results use an AgentDock result cursor without changing any domain cursor. Computer Use additionally requires interaction_intent=desktop_gui.", Annotations: mutatingToolAnnotations(true, true), Handler: typedToolHandler("mcp_tool_call", func(ctx context.Context, r *Runtime, request toolmcp.CallRequest) (Result, error) {
			result, err := r.dynamicMCP.Call(ctx, request)
			if result != nil {
				callID, _ := result["call_id"].(string)
				if raw, ok := r.dynamicMCP.RawResult(callID); ok {
					capture := r.retainMCPRawResult(ctx, callID, "mcp_tool_call", raw, request.Arguments)
					result["capture"] = capture
					if reference := stringArg(capture, "source_ref"); reference != "" {
						result["raw_result_ref"] = reference
						r.dynamicMCP.SetRawResultReference(callID, reference)
					}
				}
			}
			return result, err
		})},
		{Name: "mcp_result_continue", Contract: mcpToolContract, Title: "Continue an MCP result", Description: "Continue the normalized projection of one prior mcp_tool_call using its opaque AgentDock host cursor. This never modifies or substitutes a domain cursor inside the MCP business result.", Annotations: readOnlyToolAnnotations(true), Handler: typedToolHandler("mcp_result_continue", func(ctx context.Context, r *Runtime, request toolmcp.ResultContinueRequest) (Result, error) {
			return r.dynamicMCP.ContinueResult(ctx, request)
		})},
		{Name: "mcp_result_raw", Contract: mcpToolContract, Title: "Inspect a raw MCP result", Description: "Read bounded UTF-8 JSON fragments of the complete original upstream response for explicit diagnostics. This is not a normal business-result continuation path.", Annotations: readOnlyToolAnnotations(true), Handler: typedToolHandler("mcp_result_raw", func(ctx context.Context, r *Runtime, request toolmcp.ResultRawRequest) (Result, error) {
			return r.dynamicMCP.RawResultPage(ctx, request)
		})},
	}
}
