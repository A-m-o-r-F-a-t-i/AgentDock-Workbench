package mcp

import (
	"context"
	"errors"
	"maps"
	"strings"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/capabilityrouting"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/envstore"
	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
)

func (s *Service) Manage(ctx context.Context, request ManageRequest) (Result, error) {
	action := strings.ToLower(strings.TrimSpace(request.Action))
	if action == "" {
		action = "list"
	}
	switch action {
	case "list":
		servers := s.mcpClients.List()
		for index := range servers {
			servers[index].Plugin = s.pluginName(servers[index].Name)
		}
		return Result{"action": action, "servers": servers, "count": len(servers)}, nil
	case "inspect":
		name := request.Name
		cfg, summary, err := s.mcpClients.Inspect(name)
		if err != nil {
			return nil, dynamicMCPToolError(err)
		}
		summary.Plugin = s.pluginName(summary.Name)
		return Result{"action": action, "server": summary, "config": cfg}, nil
	case "update", "reset_override":
		if err := s.ensureAvailable(request.Name); err != nil {
			return nil, err
		}
		server, err := s.mcpClients.Update(ctx, request.Name, request.ExpectedRevision, request.Scope, request.Patch, action == "reset_override")
		if err != nil {
			return nil, dynamicMCPToolError(err)
		}
		server.Plugin = s.pluginName(server.Name)
		return Result{"action": action, "server": server}, nil
	case "add":
		cfg := mcpclient.ServerConfig{
			Name: request.Name,
			Lazy: request.Lazy, RecoverySkill: request.RecoverySkill,
			Description: request.Description,
			Transport:   request.Transport,
			URL:         request.URL,
			Command:     request.Command,
			Args:        append([]string(nil), request.Args...),
			Cwd:         request.CWD,
			HeaderEnv:   cloneStringMap(request.HeaderEnv),
			EnvFromEnv:  cloneStringMap(request.EnvFromEnv),
			Enabled:     boolValue(request.Enabled, true),
			TimeoutMS:   intValue(request.TimeoutMS, 30000),
		}
		server, err := s.mcpClients.Add(cfg)
		if err != nil {
			return nil, dynamicMCPToolError(err)
		}
		return Result{"action": action, "server": server}, nil
	case "remove":
		name := request.Name
		if err := s.mcpClients.Remove(name); err != nil {
			return nil, dynamicMCPToolError(err)
		}
		return Result{"action": action, "name": strings.TrimSpace(name), "removed": true}, nil
	case "enable", "disable":
		name := request.Name
		server, err := s.mcpClients.SetEnabled(name, action == "enable")
		if err != nil {
			return nil, dynamicMCPToolError(err)
		}
		return Result{"action": action, "server": server}, nil
	case "env_set", "env_unset", "env_list":
		name := strings.TrimSpace(request.Name)
		cfg, _, err := s.mcpClients.Inspect(name)
		if err != nil {
			return nil, dynamicMCPToolError(err)
		}
		storageKey := cfg.StorageKey
		if storageKey == "" {
			storageKey = cfg.Name
		}
		if cfg.SourceType == "plugin" && (action == "env_set" || action == "env_unset") &&
			config.IsReservedPluginEnvironmentKey(strings.TrimSpace(request.Key)) {
			return nil, toolErrorDetails(
				"VALIDATION_ERROR",
				"PLUGIN_DATA_DIR is reserved by the Plugin runtime",
				"validation",
				map[string]any{"name": cfg.Name, "plugin_name": cfg.PluginName, "key": strings.TrimSpace(request.Key)},
			)
		}
		return s.envAction(envstore.ScopeMCP, storageKey, action, request)
	case "refresh":
		name := request.Name
		server, tools, err := s.mcpClients.Refresh(ctx, name)
		if err != nil {
			return nil, dynamicMCPToolError(err)
		}
		return Result{"action": action, "server": server, "tools": tools, "tool_count": len(tools)}, nil
	default:
		return nil, toolErrorDetails(
			"INVALID_ACTION",
			"unsupported mcp_manage action",
			"validation",
			map[string]any{"action": action, "allowed": []string{"list", "inspect", "add", "remove", "enable", "disable", "env_set", "env_unset", "env_list", "refresh", "update", "reset_override"}},
		)
	}
}

func (s *Service) Search(ctx context.Context, request SearchRequest) (Result, error) {
	query, server, normalizeErr := mcpclient.NormalizeSearchSelector(request.Query, request.Server)
	if normalizeErr != nil {
		return nil, dynamicMCPToolError(normalizeErr)
	}
	limit := boundedInt(intValue(request.Limit, 5), 5, 1, 100)
	if server == "" {
		if match, available := s.resolveBuiltinRoute(query); available {
			err := s.rejectBuiltinDiscovery(ctx, []BuiltinToolSummary{match.Tool})
			var routeErr *ToolError
			if match.Alias != "" && errors.As(err, &routeErr) {
				routeErr.Details["matched_alias"] = match.Alias
			}
			return nil, err
		}
	}
	query, server, normalizeErr = s.mcpClients.ResolveSearchSelector(ctx, query, server)
	if normalizeErr != nil {
		return nil, dynamicMCPToolError(normalizeErr)
	}
	var allow func(string) bool
	if server == "" {
		pluginOwned := make(map[string]bool)
		if s.pluginMembership != nil {
			index, err := s.mcpClients.EnabledIndexContext(ctx)
			if err != nil {
				return nil, dynamicMCPToolError(err)
			}
			for _, item := range index {
				pluginName, _, owned, lookupErr := s.pluginMembership(item.Name)
				if lookupErr != nil {
					return nil, toolErrorCause("PLUGIN_STATE_INVALID", "read MCP plugin ownership", "runtime", map[string]any{"server": item.Name}, lookupErr)
				}
				hidden := owned
				if owned && s.pluginHeavy != nil {
					heavy, err := s.pluginHeavy(item.Name)
					if err != nil {
						return nil, err
					}
					hidden = heavy || capabilityrouting.RequiresExplicitMCPSelection(pluginName, item.Name)
				}
				pluginOwned[item.Name] = hidden
			}
		}
		allow = func(name string) bool { return !pluginOwned[name] }
	} else if err := s.ensureAvailable(server); err != nil {
		return nil, err
	}
	found, err := s.mcpClients.SearchCatalogs(ctx, request.Query, server, limit, request.Refresh, allow)
	if err != nil {
		return nil, dynamicMCPToolError(err)
	}
	for index := range found.Tools {
		pluginName := found.Tools[index].PluginName
		if pluginName == "" {
			pluginName = s.pluginName(found.Tools[index].Server)
		}
		found.Tools[index].Description = capabilityrouting.ToolDescription(pluginName, found.Tools[index].QualifiedName, found.Tools[index].Description)
	}
	result := Result{
		"query": found.Query, "server": found.Server, "tools": found.Tools, "count": len(found.Tools),
		"catalogs": found.Catalogs, "complete": found.Complete, "truncated": found.Truncated,
		"cache_only": found.CacheOnly, "route_kind": routeKindDynamicMCP,
		"builtin_match_count": 0, "dynamic_match_count": len(found.Tools), "match_kind": found.MatchKind,
	}
	result["match_count"], result["exact_count"] = found.MatchCount, found.ExactCount
	result["absence_authoritative"] = found.AbsenceAuthoritative
	result["execution_performed"] = false
	result["resolution_status"] = "ambiguous"
	if found.Selected != nil {
		result["resolution_status"], result["selected"] = "resolved", found.Selected
		schema, _ := found.Selected["input_schema"].(map[string]any)
		required := schema["required"]
		complete := found.ArgumentsComplete
		result["next_call"] = map[string]any{"tool": "mcp_tool_call", "arguments": map[string]any{"name": found.Selected["qualified_name"], "arguments": map[string]any{}}, "required_arguments": required, "arguments_complete": complete}
		result["catalog_state"] = found.Selected["catalog_state"]
		result["managed_target"] = managedTarget(ctx, stringValue(found.Selected["server"]), stringValue(found.Selected["catalog_revision"]), "registered_selection")
	} else if len(found.Tools) == 0 {
		result["resolution_status"] = "not_found"
		if !found.AbsenceAuthoritative {
			result["resolution_status"] = "incomplete"
			next := map[string]any{"query": found.Query}
			category := "model_decision"
			if found.Server != "" {
				next["server"], next["refresh"], category = found.Server, true, "protocol"
			}
			result["required_action"] = map[string]any{"category": category, "tool": "mcp_tool_search", "arguments": next, "required": true, "execution_performed": false, "reason_code": "mcp_catalog_incomplete"}
			if found.Server == "" {
				result["required_action"] = map[string]any{"category": "model_decision", "required": true, "execution_performed": false, "reason_code": "mcp_server_selection_required", "message": "Select the intended server from catalogs, then repeat this query with that server. Do not repeat the unscoped query unchanged."}
			}
		}
	}
	return result, nil
}

func (s *Service) Call(ctx context.Context, request CallRequest) (Result, error) {
	validated, err := s.validateCallRequest(request)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if prepared := preparedCallFromContext(ctx); prepared != nil {
		if !preparedCallMatches(prepared, validated) {
			err = preparedCallMismatch(validated)
		} else {
			result, err = s.mcpClients.CallPrepared(ctx, prepared.client)
		}
	} else {
		result, err = s.mcpClients.Call(ctx, validated.qualifiedName, validated.arguments)
	}
	catalog, catalogErr := s.mcpClients.CachedSummary(validated.serverName)
	if catalogErr == nil {
		s.decorateCatalogSummary(validated.pluginName, validated.serverName, catalog)
	}
	if catalogErr != nil {
		catalog = map[string]any{"server": validated.serverName, "catalog_revision": "", "complete": false, "stale": true, "total": 0, "tools": []map[string]any{}, "error": catalogErr.Error()}
	}
	catalog = maps.Clone(catalog)
	delete(catalog, "tools")
	catalog["delivery"], catalog["catalog_delivery"] = "summary", "summary"
	catalog["tools_omitted"], catalog["refresh_tool"] = true, ToolList
	response := Result{
		"name": validated.qualifiedName, "mcp_catalog": catalog,
		"managed_target": managedTarget(ctx, validated.serverName, stringValue(catalog["catalog_revision"]), "qualified_business_call"),
	}
	if prepared := preparedCallFromContext(ctx); prepared != nil {
		if class, mode := prepared.OutputPolicy(); class != "" {
			response["output_class"] = class
			response["preview_mode"] = mode
		}
	}
	if result != nil {
		normalized, contentKinds, parsedJSONText := normalizeDynamicMCPResult(result)
		projected, projectionErr := s.results.store(activity.FromContext(ctx), validated.qualifiedName, result, normalized, contentKinds, parsedJSONText)
		if projectionErr != nil {
			response["result"] = normalized
			response["result_projection_error"] = projectionErr.Error()
		} else {
			for key, value := range projected {
				response[key] = value
			}
		}
	}
	if err != nil {
		if errorReportsNotExecuted(err) {
			response["executed"] = false
		}
		return response, dynamicMCPToolError(err)
	}
	response["executed"] = true
	return response, nil
}

func (s *Service) decorateCatalogSummary(pluginName, serverName string, catalog map[string]any) {
	if catalog == nil || !capabilityrouting.IsComputerUseServer(pluginName, serverName) {
		return
	}
	tools, ok := catalog["tools"].([]map[string]any)
	if !ok {
		return
	}
	for _, item := range tools {
		name, _ := item["name"].(string)
		description, _ := item["description"].(string)
		item["description"] = capabilityrouting.ToolDescription(pluginName, name, description)
	}
}

func dynamicMCPToolError(err error) error {
	if err == nil {
		return nil
	}
	var existing *ToolError
	if errors.As(err, &existing) {
		return existing
	}
	var mcpErr *mcpclient.Error
	if !errors.As(err, &mcpErr) {
		return toolErrorCause("MCP_ERROR", err.Error(), "external", nil, err)
	}
	category := "external"
	if strings.Contains(mcpErr.Code, "INVALID") || strings.Contains(mcpErr.Code, "NOT_FOUND") ||
		strings.Contains(mcpErr.Code, "EXISTS") || strings.Contains(mcpErr.Code, "DISABLED") ||
		strings.Contains(mcpErr.Code, "REQUIRED") || strings.Contains(mcpErr.Code, "OWNED") ||
		strings.Contains(mcpErr.Code, "COLLISION") || strings.Contains(mcpErr.Code, "MISMATCH") {
		category = "validation"
	}
	if mcpErr.Code == "MCP_AUTH_REQUIRED" {
		category = "auth"
	}
	toolErr := toolErrorCause(mcpErr.Code, mcpErr.Message, category, mcpErr.Details, err)
	toolErr.Retryable = mcpErr.Retryable
	return toolErr
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for name, value := range values {
		out[name] = strings.TrimSpace(value)
	}
	return out
}

func managedTarget(ctx context.Context, server, revision, source string) map[string]any {
	result := map[string]any{"server": server, "catalog_revision": revision, "selection_source": source}
	if workspace := activity.FromContext(ctx).WorkspaceID; workspace != "" {
		result["workspace_id"] = workspace
	}
	return result
}
func stringValue(value any) string { text, _ := value.(string); return text }
