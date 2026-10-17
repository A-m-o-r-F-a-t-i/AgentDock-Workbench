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
	"call a dynamic mcp tool": "mcp_tool_call",
	"edit_file":               "file_edit",
	"run_command":             "exec_command",
	"shell_command":           "exec_command",
	"task_update":             "task_manage",
	"write_file":              "file_edit",
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
	tool.Scope = ScopeAgentDockBuiltin
	tool.Origin = ScopeAgentDockBuiltin
	tool.InvokeVia = InvokeDirect
	return builtinRouteMatch{Tool: tool, Alias: alias}, true
}

func builtinRouteTokens(query string) []string {
	return strings.FieldsFunc(query, func(value rune) bool {
		return !unicode.IsLetter(value) && !unicode.IsDigit(value) && value != '_' && value != '-' && value != ':' && value != '.'
	})
}

func sortedBuiltinTools(input []BuiltinToolSummary) []BuiltinToolSummary {
	result := make([]BuiltinToolSummary, 0, len(input))
	seen := make(map[string]bool, len(input))
	for _, item := range input {
		if !seen[item.Name] {
			seen[item.Name] = true
			result = append(result, item)
		}
	}
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
