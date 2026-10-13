package app

import (
	"errors"
	"maps"
	"strings"

	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
)

// normalizeBuiltinDiscoveryRoute converts a locally authenticated dynamic
// discovery rejection into a successful terminal host route. The dynamic MCP
// service keeps the rejection internally so repeated misuse remains observable,
// while callers receive a stable route instead of a generic tool failure that
// can trigger another search loop.
func normalizeBuiltinDiscoveryRoute(result Result, err error) (Result, error) {
	if err == nil || !toolmcp.IsBuiltinDiscoveryError(err) {
		return result, err
	}
	var routeErr *ToolError
	if !errors.As(err, &routeErr) {
		return result, err
	}
	route := Result(maps.Clone(routeErr.Details))
	names := builtinRouteNames(route)
	label := strings.Join(names, ", ")
	if label == "" {
		label = "AgentDock host tool"
	}
	route["route_code"] = routeErr.Code
	route["route_status"] = "resolved"
	route["route_summary"] = "Use host tool · " + label
	route["message"] = "Use the resolved AgentDock host tool from the caller tools/list namespace. Dynamic MCP discovery and filesystem, source-code, or activity-log searches are not recovery paths. No business action ran."
	route["filesystem_lookup_required"] = false
	route["dynamic_discovery_required"] = false
	route["recovery_source"] = "caller_tools_list"
	return route, nil
}

func builtinRouteNames(route Result) []string {
	switch values := route["required_host_tools"].(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if name, ok := value.(string); ok && strings.TrimSpace(name) != "" {
				result = append(result, name)
			}
		}
		return result
	}
	if name := strings.TrimSpace(stringArg(route, "required_host_tool")); name != "" {
		return []string{name}
	}
	return nil
}
