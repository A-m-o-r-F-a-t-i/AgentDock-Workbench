package app

import (
	"context"
	"encoding/json"

	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
)

func (r *Runtime) prepareMCPExecution(ctx context.Context, prepared *preparedExecution) error {
	if prepared == nil || prepared.spec.Name != "mcp_tool_call" {
		return nil
	}
	var request toolmcp.CallRequest
	if err := decodeToolInput("mcp_tool_call", prepared.args, &request); err != nil {
		return err
	}
	fixed, err := r.dynamicMCP.PrepareCall(ctx, request)
	if err != nil {
		return err
	}
	prepared.mcpCall = fixed
	return nil
}

func (r *Runtime) validatePreparedMCPExecution(ctx context.Context, prepared *preparedExecution) error {
	if prepared == nil || prepared.spec.Name != "mcp_tool_call" {
		return nil
	}
	if prepared.mcpCall == nil {
		return toolErrorDetails("MCP_PREPARED_CALL_REQUIRED", "prepared MCP call is missing", "validation", map[string]any{"executed": false})
	}
	return r.dynamicMCP.ValidatePreparedCall(ctx, prepared.mcpCall)
}

func bindPreparedMCPExecution(ctx context.Context, prepared *preparedExecution) context.Context {
	if prepared == nil || prepared.mcpCall == nil {
		return ctx
	}
	return toolmcp.WithPreparedCall(ctx, prepared.mcpCall)
}

func preparedMCPExecutionSummary(prepared *preparedExecution) string {
	if prepared == nil || prepared.mcpCall == nil {
		return ""
	}
	raw, err := json.Marshal(prepared.mcpCall.Summary())
	if err != nil {
		return ""
	}
	return string(raw)
}
