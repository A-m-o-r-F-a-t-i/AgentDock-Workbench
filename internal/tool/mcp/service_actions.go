package mcp

import (
	"context"
	"errors"
	"strings"

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
			Name:        request.Name,
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
	query := request.Query
	server := strings.TrimSpace(request.Server)
	limit := boundedInt(intValue(request.Limit, 10), 10, 1, 100)
	if server == "" && s.builtinLookup != nil {
		if definition, available := s.builtinLookup(strings.TrimSpace(query)); available {
			return Result{
				"query": query, "server": "", "count": 0,
				"tools": []any{}, "catalogs": []any{}, "builtin_tools": []any{definition},
				"complete": true, "truncated": false, "cache_only": true,
				"next_action": "Invoke the returned built-in tool through the host. Do not search for it in a dynamic MCP server or wrap it in mcp_tool_call.",
			}, nil
		}
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
	found, err := s.mcpClients.SearchCatalogsFiltered(ctx, query, server, limit, allow)
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
		"query": query, "server": server, "tools": found.Tools, "count": len(found.Tools),
		"catalogs": found.Catalogs, "complete": found.Complete, "truncated": found.Truncated,
		"cache_only": found.CacheOnly,
	}
	if !found.Complete {
		result["next_action"] = "Select a server from catalogs and use mcp_tool_list; an undiscovered or stale catalog is not evidence that a capability is absent."
	}
	return result, nil
}

func (s *Service) Call(ctx context.Context, request CallRequest) (Result, error) {
	qualifiedName := request.Name
	serverName, _, ok := strings.Cut(strings.TrimSpace(qualifiedName), ":")
	if !ok || strings.TrimSpace(serverName) == "" {
		return nil, toolErrorDetails("MCP_TOOL_NAME_INVALID", "MCP tool name must use <server>:<tool>", "validation", map[string]any{"tool": qualifiedName})
	}
	if err := s.ensureAvailable(serverName); err != nil {
		return nil, err
	}
	pluginName := s.pluginName(serverName)
	if capabilityrouting.RequiresDesktopGUIIntent(pluginName, qualifiedName) && !capabilityrouting.ValidDesktopGUIIntent(request.InteractionIntent, request.Reason) {
		return nil, toolErrorDetails(
			"MCP_EXPLICIT_INTENT_REQUIRED",
			"Computer Use calls require an explicit desktop GUI intent and a concrete user-task reason",
			"validation",
			map[string]any{
				"tool": qualifiedName, "plugin": pluginName,
				"required": map[string]any{"interaction_intent": capabilityrouting.DesktopGUIIntent, "reason": "specific user-task reason"},
			},
		)
	}
	arguments := request.Arguments
	if arguments == nil {
		arguments = map[string]any{}
	}
	result, err := s.mcpClients.Call(ctx, qualifiedName, arguments)
	catalog, catalogErr := s.mcpClients.CachedSummary(serverName)
	if catalogErr == nil {
		s.decorateCatalogSummary(pluginName, serverName, catalog)
	}
	if catalogErr != nil {
		catalog = map[string]any{"server": serverName, "catalog_revision": "", "complete": false, "stale": true, "total": 0, "tools": []map[string]any{}, "error": catalogErr.Error()}
	}
	response := Result{"name": qualifiedName, "mcp_catalog": catalog}
	if result != nil {
		response["result"] = result
	}
	if err != nil {
		return response, dynamicMCPToolError(err)
	}
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
		strings.Contains(mcpErr.Code, "COLLISION") {
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
