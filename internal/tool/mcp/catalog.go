package mcp

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/uvwt/agentdock/internal/capabilityrouting"
	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
)

func (s *Service) List(ctx context.Context, request ListRequest) (Result, error) {
	server := strings.TrimSpace(request.Server)
	if err := s.ensureAvailable(server); err != nil {
		return nil, err
	}
	var catalog mcpclient.Catalog
	var err error
	if request.Refresh {
		catalog, err = s.mcpClients.RefreshCatalog(ctx, server)
	} else {
		catalog, err = s.mcpClients.Catalog(ctx, server)
	}
	if err != nil {
		return nil, dynamicMCPToolError(err)
	}
	result := s.catalogResult(catalog)
	return Result(s.catalogDeliveries.prepare(ctx, result, request.ForceFull || request.Refresh)), nil
}

func (s *Service) catalogResult(catalog mcpclient.Catalog) Result {
	pluginName := s.pluginName(catalog.Server)
	tools := make([]map[string]any, 0, len(catalog.Tools))
	for _, name := range catalog.Names() {
		tool := catalog.Tools[name]
		qualifiedName := catalog.Server + ":" + name
		description := capabilityrouting.ToolDescription(pluginName, qualifiedName, tool.Description)
		schemaDigest, _ := mcpclient.ToolSchemaDigest(tool)
		annotationsDigest, _ := mcpclient.ToolAnnotationsDigest(tool)
		entry := map[string]any{
			"server": catalog.Server, "name": qualifiedName, "tool_name": name,
			"title": tool.Title, "description": mcpclient.OneLineDescription(description),
			"server_fingerprint": catalog.ServerFingerprint, "catalog_revision": catalog.Revision,
			"schema_digest": schemaDigest, "annotations_digest": annotationsDigest,
		}
		if tool.Annotations != nil {
			entry["annotations"] = tool.Annotations
		}
		tools = append(tools, entry)
	}
	result := Result{"server": catalog.Server, "server_fingerprint": catalog.ServerFingerprint, "catalog_revision": catalog.Revision, "catalog_state": catalog.State, "complete": catalog.Complete, "total": len(tools), "tools": tools, "stale": catalog.State.State != "fresh"}
	if len(tools) == 0 && !catalogAbsenceAuthoritative(catalog) {
		result["resolution_status"], result["absence_authoritative"], result["execution_performed"] = "incomplete", false, false
		result["required_action"] = catalogRefreshAction(catalog.Server, "")
	}
	return result
}

func catalogAbsenceAuthoritative(catalog mcpclient.Catalog) bool {
	return catalog.Complete && catalog.State.State == "fresh" && catalog.State.AbsenceAuthoritative
}

func catalogRefreshAction(server, name string) map[string]any {
	tool := ToolSearch
	arguments := map[string]any{"query": server + ":" + name, "server": server, "refresh": true}
	if name == "" || name == "*" {
		tool, arguments = ToolList, map[string]any{"server": server, "refresh": true}
	}
	return map[string]any{"category": "protocol", "tool": tool, "arguments": arguments, "required": true, "execution_performed": false, "reason_code": "mcp_catalog_incomplete"}
}

func incompleteCatalogSelector(catalog mcpclient.Catalog, name string) map[string]any {
	return map[string]any{
		"selector": catalog.Server + ":" + name, "code": "MCP_CATALOG_INCOMPLETE", "status": "incomplete",
		"message":           "The retained catalog cannot establish the current tool set; refresh the selected server catalog.",
		"resolution_status": "incomplete", "absence_authoritative": false, "execution_performed": false,
		"catalog_state": catalog.State, "required_action": catalogRefreshAction(catalog.Server, name),
	}
}

// Inspect normalizes the legacy single selector once, then runs the same grouped
// reader. No tool discovery is repeated per member; returned schema maps are
// independent of the dispatch validator's immutable catalog.
func (s *Service) Inspect(ctx context.Context, request InspectRequest) (Result, error) {
	legacy := len(request.Names) == 0 && strings.TrimSpace(request.Name) != ""
	names := append([]string{}, request.Names...)
	if legacy {
		names = []string{request.Name}
	}
	if len(names) == 0 || (len(request.Names) > 0 && request.Name != "") {
		return nil, toolErrorDetails("MCP_TOOL_NAMES_REQUIRED", "use names for tool selectors or service:*", "validation", nil)
	}
	includeSchema := request.ForceFull
	if request.IncludeSchema != nil {
		includeSchema = includeSchema || *request.IncludeSchema
	} else if legacy || (len(names) == 1 && !strings.HasSuffix(strings.TrimSpace(names[0]), ":*")) {
		includeSchema = true
	}
	selected := map[string][]string{}
	invalid := []map[string]any{}
	builtins := []BuiltinToolSummary{}
	seen := map[string]bool{}
	for _, selector := range names {
		selector = strings.TrimSpace(selector)
		if seen[selector] {
			continue
		}
		seen[selector] = true
		if match, available := s.resolveBuiltinRoute(selector); available {
			builtins = append(builtins, match.Tool)
			continue
		}
		server, tool, ok := strings.Cut(selector, ":")
		server, tool = strings.TrimSpace(server), strings.TrimSpace(tool)
		if !ok || server == "" || tool == "" || strings.Contains(server, "*") || (strings.Contains(tool, "*") && tool != "*") {
			invalid = append(invalid, map[string]any{"selector": selector, "code": "MCP_TOOL_NAME_INVALID", "status": "invalid", "message": "use server:tool or server:*"})
			continue
		}
		selected[server] = append(selected[server], tool)
	}
	builtins = sortedBuiltinTools(builtins)
	if len(builtins) > 0 && len(selected) == 0 && len(invalid) == 0 {
		return nil, s.rejectBuiltinDiscovery(ctx, builtins)
	}
	type serverResult struct {
		catalog mcpclient.Catalog
		err     error
	}
	results := map[string]serverResult{}
	var mutex sync.Mutex
	var workers sync.WaitGroup
	limit := make(chan struct{}, 4)
	for server := range selected {
		workers.Add(1)
		go func(server string) {
			defer workers.Done()
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				mutex.Lock()
				results[server] = serverResult{err: ctx.Err()}
				mutex.Unlock()
				return
			}
			var value serverResult
			value.err = s.ensureAvailable(server)
			if value.err == nil {
				value.catalog, value.err = s.mcpClients.Catalog(ctx, server)
			}
			mutex.Lock()
			results[server] = value
			mutex.Unlock()
		}(server)
	}
	workers.Wait()
	servers := make([]string, 0, len(selected))
	for name := range selected {
		servers = append(servers, name)
	}
	sort.Strings(servers)
	tools := []map[string]any{}
	catalogs := []map[string]any{}
	problems := invalid
	fullSchemas, schemaReferences, schemaSummaries := 0, 0, 0
	var singleError error
	for _, server := range servers {
		value := results[server]
		if value.err != nil {
			if legacy {
				singleError = dynamicMCPToolError(value.err)
			}
			code := "MCP_ERROR"
			var typed *mcpclient.Error
			if errors.As(value.err, &typed) {
				code = typed.Code
			}
			var toolErr *ToolError
			if errors.As(value.err, &toolErr) {
				code = toolErr.Code
			}
			status := "error"
			if strings.Contains(code, "DISABLED") {
				status = "disabled"
			}
			if strings.Contains(code, "NOT_FOUND") {
				status = "not_found"
			}
			if strings.Contains(code, "AUTH") {
				status = "unauthorized"
			}
			for _, name := range selected[server] {
				problems = append(problems, map[string]any{"selector": server + ":" + name, "code": code, "status": status, "message": value.err.Error()})
			}
			continue
		}
		catalog := value.catalog
		catalogs = append(catalogs, map[string]any{"server": server, "server_fingerprint": catalog.ServerFingerprint, "catalog_revision": catalog.Revision, "catalog_state": catalog.State, "server_version": catalog.ServerVersion, "complete": catalog.Complete, "stale": catalog.State.State != "fresh", "total": len(catalog.Tools)})
		chosen := map[string]bool{}
		for _, name := range selected[server] {
			if name == "*" {
				if !catalogAbsenceAuthoritative(catalog) {
					problems = append(problems, incompleteCatalogSelector(catalog, name))
				}
				for item := range catalog.Tools {
					chosen[item] = true
				}
			} else {
				chosen[name] = true
			}
		}
		ordered := make([]string, 0, len(chosen))
		for name := range chosen {
			ordered = append(ordered, name)
		}
		sort.Strings(ordered)
		for _, name := range ordered {
			tool, ok := catalog.Tools[name]
			if !ok {
				if !catalogAbsenceAuthoritative(catalog) {
					problem := incompleteCatalogSelector(catalog, name)
					problems = append(problems, problem)
					if legacy {
						singleError = toolErrorDetails("MCP_CATALOG_INCOMPLETE", problem["message"].(string), "validation", problem)
					}
					continue
				}
				problems = append(problems, map[string]any{"selector": server + ":" + name, "code": "MCP_TOOL_NOT_FOUND", "status": "not_found", "message": "MCP tool not found"})
				if legacy {
					singleError = toolErrorDetails("MCP_TOOL_NOT_FOUND", "MCP tool not found", "validation", map[string]any{"tool": server + ":" + name})
				}
				continue
			}
			qualifiedName := server + ":" + name
			description := capabilityrouting.ToolDescription(s.pluginName(server), qualifiedName, tool.Description)
			schemaDigest, _ := mcpclient.ToolSchemaDigest(tool)
			annotationsDigest, _ := mcpclient.ToolAnnotationsDigest(tool)
			entry := map[string]any{
				"name": qualifiedName, "server": server, "tool_name": name,
				"server_fingerprint": catalog.ServerFingerprint, "catalog_revision": catalog.Revision,
				"server_version": catalog.ServerVersion, "title": tool.Title,
				"description":   mcpclient.OneLineDescription(description),
				"schema_digest": schemaDigest, "annotations_digest": annotationsDigest,
			}
			if tool.Annotations != nil {
				entry["annotations"] = tool.Annotations
			}
			if metadata := projectToolMetadata(tool.StandardMetadata); len(metadata) > 0 {
				entry["metadata"] = metadata
			}
			if includeSchema {
				entry["input_schema"] = tool.InputSchema
				if tool.OutputSchema != nil {
					entry["output_schema"] = tool.OutputSchema
				}
				entry = s.schemaDeliveries.prepare(ctx, entry, request.ForceFull)
				if entry["delivery"] == "reference" {
					schemaReferences++
				} else {
					fullSchemas++
				}
			} else {
				entry["delivery"] = "summary"
				entry["schema_delivery"] = "summary"
				entry["schema_omitted"] = true
				schemaSummaries++
			}
			// Same-schema references still report the current directory state.
			entry["catalog_state"] = catalog.State
			tools = append(tools, entry)
		}
	}
	if len(builtins) > 0 {
		routeError := s.rejectBuiltinDiscovery(ctx, builtins)
		var typed *ToolError
		if !errors.As(routeError, &typed) {
			return nil, routeError
		}
		for _, item := range builtins {
			problems = append(problems, map[string]any{
				"selector": item.Name, "code": typed.Code, "status": "host_builtin",
				"message": typed.Message, "retryable": false,
				"required_host_tool": item.Name, "invoke_via": "host",
			})
		}
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i]["selector"].(string) < problems[j]["selector"].(string) })
	if legacy {
		if singleError != nil {
			return nil, singleError
		}

		if len(tools) == 1 && len(problems) == 0 {
			return Result(tools[0]), nil
		}
		return nil, toolErrorDetails("MCP_TOOL_NAME_INVALID", "use server:tool for a legacy single selector", "validation", nil)
	}
	result := Result{
		"tools": tools, "count": len(tools), "total": len(tools),
		"complete": len(problems) == 0, "errors": problems, "catalogs": catalogs,
		"builtin_match_count": len(builtins), "dynamic_match_count": len(tools),
		"schema_full_count": fullSchemas, "schema_reference_count": schemaReferences, "schema_summary_count": schemaSummaries,
	}
	if len(builtins) == 0 {
		result["route_kind"] = routeKindDynamicMCP
		return result, nil
	}
	result["builtin_tools"] = builtins
	result["route_kind"] = "mixed"
	return result, nil
}
