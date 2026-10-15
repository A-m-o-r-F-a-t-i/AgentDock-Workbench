package app

import (
	"errors"
	"strings"

	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
)

func isHostBuiltinRoute(tool string, result Result) bool {
	if tool != "mcp_tool_search" && tool != "mcp_tool_inspect" {
		return false
	}
	return result != nil && stringArg(result, "route_kind") == "host_builtin"
}

func executionResultTitle(tool, fallback string, result Result) string {
	if !isHostBuiltinRoute(tool, result) {
		return fallback
	}
	if summary, ok := result["route_summary"].(string); ok {
		if summary = strings.TrimSpace(summary); summary != "" {
			return summary
		}
	}
	return fallback
}

func executionErrorTitle(tool, fallback string, err error) string {
	if !toolmcp.IsBuiltinDiscoveryError(err) {
		return fallback
	}
	var routeErr *ToolError
	if !errors.As(err, &routeErr) {
		return fallback
	}
	return executionResultTitle(tool, fallback, Result(routeErr.Details))
}

func suppressResultCounts(tool string, result Result) bool {
	return isHostBuiltinRoute(tool, result)
}
