package mcp

import (
	"sort"
	"strings"
	"unicode"
)

const (
	routeKindHostBuiltin = "host_builtin"
	routeKindDynamicMCP  = "dynamic_mcp"
)

var builtinToolAliases = map[string]string{
	"edit_file":     "file_edit",
	"run_command":   "exec_command",
	"shell_command": "exec_command",
	"task_update":   "task_manage",
	"write_file":    "file_edit",
}

var builtinRouteModifiers = map[string]struct{}{
	"agentdock": {},
	"built":     {},
	"built-in":  {},
	"builtin":   {},
	"direct":    {},
	"exact":     {},
	"host":      {},
	"in":        {},
	"internal":  {},
	"native":    {},
	"tool":      {},
}

type builtinRouteMatch struct {
	Tool  BuiltinToolSummary
	Alias string
}

func (s *Service) resolveBuiltinRoute(query string) (builtinRouteMatch, bool) {
	if s.builtinLookup == nil {
		return builtinRouteMatch{}, false
	}
	normalized := strings.ToLower(strings.TrimSpace(query))
	if normalized == "" {
		return builtinRouteMatch{}, false
	}
	if match, ok := s.lookupBuiltinCandidate(normalized); ok {
		return match, true
	}
	candidate := ""
	for _, token := range builtinRouteTokens(normalized) {
		if _, modifier := builtinRouteModifiers[token]; modifier {
			continue
		}
		if candidate != "" {
			return builtinRouteMatch{}, false
		}
		candidate = token
	}
	if candidate == "" || candidate == normalized {
		return builtinRouteMatch{}, false
	}
	return s.lookupBuiltinCandidate(candidate)
}

func (s *Service) lookupBuiltinCandidate(candidate string) (builtinRouteMatch, bool) {
	name := candidate
	alias := ""
	if target, ok := builtinToolAliases[candidate]; ok {
		name, alias = target, candidate
	}
	tool, available := s.builtinLookup(name)
	if !available {
		return builtinRouteMatch{}, false
	}
	if tool.InvokeVia == "" {
		tool.InvokeVia = "host"
	}
	return builtinRouteMatch{Tool: tool, Alias: alias}, true
}

func builtinRouteTokens(query string) []string {
	return strings.FieldsFunc(query, func(value rune) bool {
		return !unicode.IsLetter(value) && !unicode.IsDigit(value) && value != '_' && value != '-' && value != ':' && value != '.'
	})
}

func hostBuiltinProblem(selector string, match builtinRouteMatch) map[string]any {
	return map[string]any{
		"selector":   selector,
		"code":       "HOST_BUILTIN_NOT_DYNAMIC_MCP",
		"status":     "host_builtin",
		"message":    "AgentDock built-in tools must be loaded or invoked through the host tool namespace, not mcp_tool_inspect",
		"route_kind": routeKindHostBuiltin,
		"tool_name":  match.Tool.Name,
		"invoke_via": "host",
	}
}

func sortedBuiltinTools(input []BuiltinToolSummary) []BuiltinToolSummary {
	result := append([]BuiltinToolSummary(nil), input...)
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func builtinToolNames(input []BuiltinToolSummary) []string {
	items := sortedBuiltinTools(input)
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}
