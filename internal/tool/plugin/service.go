package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/uvwt/agentdock/internal/capabilityrouting"
	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
	registry "github.com/uvwt/agentdock/internal/plugin"
)

type SkillItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	File        string `json:"file"`
	Bundled     bool   `json:"bundled"`
	Enabled     bool   `json:"enabled"`
	Plugin      string `json:"plugin,omitempty"`
}

type MCPItem struct {
	Revision       string        `json:"revision"`
	ServerVersion  string        `json:"server_version,omitempty"`
	ToolCountKnown bool          `json:"tool_count_known"`
	Name           string        `json:"name"`
	Description    string        `json:"description"`
	Plugin         string        `json:"plugin,omitempty"`
	Status         string        `json:"status"`
	ToolCount      int           `json:"tool_count"`
	LastErrorCode  string        `json:"last_error_code,omitempty"`
	ToolLoadError  string        `json:"tool_load_error,omitempty"`
	Enabled        bool          `json:"enabled"`
	Tools          []MCPToolItem `json:"tools"`
}

type MCPToolItem struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Title         string `json:"title,omitempty"`
	Description   string `json:"description,omitempty"`
	Server        string `json:"server"`
}

type SkillLookup func(string) (SkillItem, bool, error)
type MCPLookup func(context.Context, string, bool) (MCPItem, bool, error)

type Service struct {
	store       *registry.Store
	skillLookup SkillLookup
	mcpLookup   MCPLookup

	mcpClients     *mcpclient.Manager
	mcpReconcileMu sync.Mutex
	mcpMu          sync.Mutex
	mcpConfigs     map[string]mcpclient.ServerConfig
	mcpLeases      map[string]func()
}

func New(store *registry.Store, skillLookup SkillLookup, mcpLookup MCPLookup) *Service {
	return &Service{
		store: store, skillLookup: skillLookup, mcpLookup: mcpLookup,
		mcpConfigs: make(map[string]mcpclient.ServerConfig), mcpLeases: make(map[string]func()),
	}
}

func (s *Service) SetMCPManager(manager *mcpclient.Manager) {
	s.mcpClients = manager
}

func (s *Service) ReconcileMCP(ctx context.Context) error {
	return s.reconcileMCP(ctx, "", "", "")
}

func (s *Service) reconcileMCP(ctx context.Context, excludedPlugin, activationPlugin, activationOwner string) error {
	if s == nil || s.mcpClients == nil {
		return nil
	}
	s.mcpReconcileMu.Lock()
	defer s.mcpReconcileMu.Unlock()

	configs, leases, err := s.buildMCPSet(ctx, excludedPlugin, activationPlugin, activationOwner)
	if err != nil {
		releasePluginMCPLeases(leases)
		return err
	}
	s.mcpMu.Lock()
	previousConfigs := s.mcpConfigs
	previousLeases := s.mcpLeases
	s.mcpConfigs = configs
	s.mcpMu.Unlock()

	if err := s.mcpClients.SetExternalServerProvider(s.externalMCPProvider); err != nil {
		s.mcpMu.Lock()
		s.mcpConfigs = previousConfigs
		s.mcpMu.Unlock()
		restoreErr := s.mcpClients.SetExternalServerProvider(s.externalMCPProvider)
		releasePluginMCPLeases(leases)
		return errors.Join(err, restoreErr)
	}
	s.mcpMu.Lock()
	s.mcpLeases = leases
	s.mcpMu.Unlock()
	releasePluginMCPLeases(previousLeases)
	return nil
}

func (s *Service) buildMCPSet(ctx context.Context, excludedPlugin, activationPlugin, activationOwner string) (map[string]mcpclient.ServerConfig, map[string]func(), error) {
	definitions, err := s.store.List()
	if err != nil {
		return nil, nil, err
	}
	configs := make(map[string]mcpclient.ServerConfig)
	leases := make(map[string]func())
	addPackage := func(pkg registry.RuntimePackage, release func()) error {
		if len(pkg.MCPServers) == 0 {
			release()
			return nil
		}
		for name, cfg := range pkg.MCPServers {
			if _, exists := configs[name]; exists {
				release()
				return fmt.Errorf("Plugin MCP server %q is owned by multiple runtime packages", name)
			}
			configs[name] = mcpclient.NormalizeServerConfig(cfg)
		}
		leases[pkg.Name] = release
		return nil
	}
	for _, definition := range definitions {
		if definition.Name == excludedPlugin || definition.Name == activationPlugin || len(definition.MCPServers) == 0 {
			continue
		}
		pkg, release, acquireErr := s.store.RuntimeMCPPackage(ctx, definition.Name)
		if acquireErr != nil {
			releasePluginMCPLeases(leases)
			return nil, nil, acquireErr
		}
		if err := addPackage(pkg, release); err != nil {
			releasePluginMCPLeases(leases)
			return nil, nil, err
		}
	}
	if activationPlugin != "" {
		pkg, release, acquireErr := s.store.ActivationMCPPackage(ctx, activationPlugin, activationOwner)
		if acquireErr != nil {
			releasePluginMCPLeases(leases)
			return nil, nil, acquireErr
		}
		if err := addPackage(pkg, release); err != nil {
			releasePluginMCPLeases(leases)
			return nil, nil, err
		}
	}
	return configs, leases, nil
}

func (s *Service) externalMCPProvider(ctx context.Context) (map[string]mcpclient.ServerConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	configs := make(map[string]mcpclient.ServerConfig, len(s.mcpConfigs))
	for name, cfg := range s.mcpConfigs {
		configs[name] = mcpclient.NormalizeServerConfig(cfg)
	}
	return configs, nil
}

func (s *Service) ReleaseMCPLeases() {
	if s == nil {
		return
	}
	s.mcpReconcileMu.Lock()
	defer s.mcpReconcileMu.Unlock()
	s.mcpMu.Lock()
	leases := s.mcpLeases
	s.mcpLeases = make(map[string]func())
	s.mcpConfigs = make(map[string]mcpclient.ServerConfig)
	s.mcpMu.Unlock()
	releasePluginMCPLeases(leases)
}

func releasePluginMCPLeases(leases map[string]func()) {
	for _, release := range leases {
		if release != nil {
			release()
		}
	}
}

func (s *Service) Definitions() ([]registry.Definition, error) {
	return s.store.List()
}

func (s *Service) CapabilityItems() ([]registry.Definition, error) {
	definitions, err := s.store.List()
	if err != nil {
		return nil, err
	}
	items := make([]registry.Definition, 0, len(definitions))
	for _, definition := range definitions {
		if definition.Enabled && (definition.Heavy || capabilityrouting.RequiresExplicitPluginLoad(definition.Name)) {
			definition.Description = capabilityrouting.PluginDescription(definition.Name, definition.Description)
			items = append(items, definition)
		}
	}
	return items, nil
}

func (s *Service) SkillMembership(name string) (registry.Membership, bool, error) {
	return s.store.SkillMembership(name)
}

func (s *Service) MCPMembership(name string) (registry.Membership, bool, error) {
	return s.store.MCPMembership(name)
}

func (s *Service) activatePreparedCandidate(ctx context.Context, candidate registry.PreparedSource, reviewToken string, replace, confirmSourceChange bool) (registry.Definition, error) {
	activation, err := s.store.BeginPreparedActivation(ctx, candidate, reviewToken, replace, confirmSourceChange)
	if err != nil {
		return registry.Definition{}, err
	}
	if activation.OwnerID == "" {
		if err := s.ReconcileMCP(ctx); err != nil {
			return registry.Definition{}, err
		}
		return activation.Definition, nil
	}
	if err := s.reconcileMCP(ctx, "", activation.Name, activation.OwnerID); err != nil {
		abortErr := s.store.AbortActivation(activation.Name, activation.OwnerID)
		restoreErr := s.ReconcileMCP(context.WithoutCancel(ctx))
		return registry.Definition{}, errors.Join(err, abortErr, restoreErr)
	}
	definition, err := s.store.FinalizeActivation(activation.Name, activation.OwnerID)
	if err != nil {
		restoreErr := s.ReconcileMCP(context.WithoutCancel(ctx))
		abortErr := s.store.AbortActivation(activation.Name, activation.OwnerID)
		return registry.Definition{}, errors.Join(err, restoreErr, abortErr)
	}
	return definition, nil
}

func (s *Service) removePlugin(ctx context.Context, name string) error {
	activation, err := s.store.BeginRemoval(ctx, name)
	if err != nil {
		return err
	}
	if err := s.reconcileMCP(ctx, activation.Name, "", ""); err != nil {
		abortErr := s.store.AbortActivation(activation.Name, activation.OwnerID)
		restoreErr := s.ReconcileMCP(context.WithoutCancel(ctx))
		return errors.Join(err, abortErr, restoreErr)
	}
	if err := s.store.FinalizeRemoval(activation.Name, activation.OwnerID); err != nil {
		restoreErr := s.ReconcileMCP(context.WithoutCancel(ctx))
		abortErr := s.store.AbortActivation(activation.Name, activation.OwnerID)
		return errors.Join(err, restoreErr, abortErr)
	}
	return nil
}

func (s *Service) Manage(ctx context.Context, request ManageRequest) (Result, error) {
	action := strings.ToLower(strings.TrimSpace(request.Action))
	if action == "" {
		action = "list"
	}
	switch action {
	case "list":
		items, err := s.store.List()
		if err != nil {
			return nil, pluginToolError(err)
		}
		return Result{"action": action, "plugins": items, "count": len(items)}, nil
	case "inspect":
		definition, err := s.store.Get(request.Name)
		if err != nil {
			return nil, pluginToolError(err)
		}
		return Result{"action": action, "plugin": definition}, nil
	case "catalog":
		catalog, err := s.store.LoadCatalog(ctx, sourceRequest(request))
		if err != nil {
			return nil, pluginToolError(err)
		}
		return Result{"action": action, "catalog": catalog, "count": len(catalog.Entries)}, nil
	case "validate":
		candidate, err := s.store.PrepareSource(ctx, sourceRequest(request))
		if err != nil {
			return nil, pluginToolError(err)
		}
		defer candidate.Cleanup()
		return Result{"action": action, "valid": candidate.Review.Valid, "plugin": candidate.Definition, "review": candidate.Review}, nil
	case "install", "update":
		candidate, err := s.store.PrepareSource(ctx, sourceRequest(request))
		if err != nil {
			return nil, pluginToolError(err)
		}
		defer candidate.Cleanup()
		if action == "update" {
			if strings.TrimSpace(request.Name) == "" {
				return nil, toolErrorDetails("PLUGIN_NAME_REQUIRED", "plugin name is required for update", "validation", nil)
			}
			if candidate.Definition.Name != strings.TrimSpace(request.Name) {
				return nil, toolErrorDetails("PLUGIN_UPDATE_NAME_MISMATCH", "selected package does not match the plugin being updated", "validation", map[string]any{"expected": strings.TrimSpace(request.Name), "actual": candidate.Definition.Name})
			}
		}
		if err := s.store.VerifyPreparedReview(candidate, request.ReviewToken); err != nil {
			return nil, pluginToolError(err)
		}
		if err := s.validateInstallOwnership(ctx, candidate.Definition); err != nil {
			return nil, err
		}
		candidate.Enabled = request.Enabled
		definition, err := s.activatePreparedCandidate(ctx, candidate, request.ReviewToken, action == "update", request.ConfirmedSourceChange)
		if err != nil {
			return nil, pluginToolError(err)
		}
		return Result{"action": action, "plugin": definition, "review": candidate.Review}, nil
	case "remove":
		if err := s.removePlugin(ctx, request.Name); err != nil {
			return nil, pluginToolError(err)
		}
		return Result{"action": action, "name": strings.TrimSpace(request.Name), "removed": true}, nil
	case "enable", "disable":
		definition, err := s.store.SetEnabled(request.Name, action == "enable")
		if err != nil {
			return nil, pluginToolError(err)
		}
		if err := s.ReconcileMCP(ctx); err != nil {
			return nil, pluginToolError(err)
		}
		return Result{"action": action, "plugin": definition}, nil
	case "heavy_enable", "heavy_disable":
		definition, err := s.store.SetHeavy(request.Name, action == "heavy_enable")
		if err != nil {
			return nil, pluginToolError(err)
		}
		return Result{"action": action, "plugin": definition}, nil
	case "member_enable", "member_disable":
		definition, err := s.store.SetMemberEnabled(request.Name, request.MemberType, request.Member, action == "member_enable")
		if err != nil {
			return nil, pluginToolError(err)
		}
		if err := s.ReconcileMCP(ctx); err != nil {
			return nil, pluginToolError(err)
		}
		return Result{"action": action, "plugin": definition, "member_type": request.MemberType, "member": request.Member}, nil
	default:
		return nil, toolErrorDetails("INVALID_ACTION", "unsupported plugin_manage action", "validation", map[string]any{
			"action":  action,
			"allowed": []string{"list", "inspect", "validate", "install", "update", "remove", "enable", "disable", "heavy_enable", "heavy_disable", "member_enable", "member_disable"},
		})
	}
}

func (s *Service) validateInstallOwnership(ctx context.Context, candidate registry.Definition) error {
	for _, name := range candidate.Skills {
		item, found, err := s.skillLookup(name)
		if err != nil {
			return toolErrorCause("PLUGIN_MEMBER_LOOKUP_FAILED", "check Skill ownership before plugin install", "runtime", map[string]any{"skill": name}, err)
		}
		if found && item.Plugin != candidate.Name {
			return toolErrorDetails("PLUGIN_MEMBER_CONFLICT", "plugin Skill conflicts with an existing standalone or plugin-owned Skill", "validation", map[string]any{"member_type": "skill", "member": name, "owner": item.Plugin})
		}
	}
	for _, name := range candidate.MCPServers {
		item, found, err := s.mcpLookup(ctx, name, false)
		if err != nil {
			return toolErrorCause("PLUGIN_MEMBER_LOOKUP_FAILED", "check MCP ownership before plugin install", "runtime", map[string]any{"mcp_server": name}, err)
		}
		if found && item.Plugin != candidate.Name {
			return toolErrorDetails("PLUGIN_MEMBER_CONFLICT", "plugin MCP server conflicts with an existing standalone or plugin-owned server", "validation", map[string]any{"member_type": "mcp_server", "member": name, "owner": item.Plugin})
		}
	}
	return nil
}

func (s *Service) Load(ctx context.Context, request LoadRequest) (Result, error) {
	definition, err := s.store.Get(request.Name)
	if err != nil {
		return nil, pluginToolError(err)
	}
	if !definition.Enabled {
		return nil, toolErrorDetails("PLUGIN_DISABLED", "plugin is disabled", "validation", map[string]any{"plugin": definition.Name})
	}

	skillItems := make([]SkillItem, 0, len(definition.Skills))
	mcpServers := make([]MCPItem, 0, len(definition.MCPServers))
	unavailable := make([]map[string]any, 0)
	for _, name := range definition.Skills {
		item, found, lookupErr := s.skillLookup(name)
		if lookupErr != nil {
			return nil, toolErrorCause("PLUGIN_MEMBER_LOOKUP_FAILED", "load plugin Skill member", "runtime", map[string]any{"plugin": definition.Name, "skill": name}, lookupErr)
		}
		if !found || !item.Enabled {
			reason := "missing"
			if found {
				reason = "disabled"
			}
			unavailable = append(unavailable, map[string]any{"type": "skill", "name": name, "reason": reason})
			continue
		}
		item.Description = capabilityrouting.SkillDescription(definition.Name, item.Name, item.Description)
		skillItems = append(skillItems, item)
	}
	for _, name := range definition.MCPServers {
		item, found, lookupErr := s.mcpLookup(ctx, name, true)
		if lookupErr != nil {
			return nil, toolErrorCause("PLUGIN_MEMBER_LOOKUP_FAILED", "load plugin MCP member", "runtime", map[string]any{"plugin": definition.Name, "mcp_server": name}, lookupErr)
		}
		if !found || !item.Enabled {
			reason := "missing"
			if found {
				reason = "disabled"
			}
			unavailable = append(unavailable, map[string]any{"type": "mcp_server", "name": name, "reason": reason})
			continue
		}
		if item.ToolLoadError != "" {
			unavailable = append(unavailable, map[string]any{
				"type": "mcp_server", "name": name, "reason": "tool_discovery_failed",
				"code": item.LastErrorCode, "message": item.ToolLoadError,
			})
		}
		item.Description = capabilityrouting.ServerDescription(definition.Name, item.Name, item.Description)
		for index := range item.Tools {
			item.Tools[index].Description = capabilityrouting.ToolDescription(definition.Name, item.Tools[index].QualifiedName, item.Tools[index].Description)
		}
		mcpServers = append(mcpServers, item)
	}

	instructions := []string{}
	if capabilityrouting.RequiresExplicitPluginLoad(definition.Name) {
		instructions = append(instructions, capabilityrouting.ComputerUseRule)
	}
	if len(skillItems) > 0 {
		instructions = append(instructions, "Read a returned Skill entry point before applying its domain workflow.")
	}
	if len(mcpServers) > 0 {
		instructions = append(instructions, "Reuse the returned MCP catalog. Inspect qualified names only when their schemas are unknown, then call them directly. Refresh only the selected service after a capability revision change; use mcp_tool_list if its catalog is missing.")
	}
	loadRequired := definition.Heavy || capabilityrouting.RequiresExplicitPluginLoad(definition.Name)
	return Result{
		"plugin": map[string]any{
			"name": definition.Name, "description": capabilityrouting.PluginDescription(definition.Name, definition.Description),
			"version": definition.Version, "path": definition.Path, "enabled": definition.Enabled,
			"heavy": definition.Heavy, "load_required": loadRequired,
		},
		"skills": skillItems, "mcp_servers": mcpServers,
		"unavailable_members": unavailable, "instructions": instructions,
	}, nil
}

func pluginToolError(err error) error {
	var registryErr *registry.Error
	if !errors.As(err, &registryErr) {
		return toolErrorCause("PLUGIN_ERROR", err.Error(), "runtime", nil, err)
	}
	category := "validation"
	switch registryErr.Code {
	case "PLUGIN_STORE_READ_FAILED", "PLUGIN_STORE_WRITE_FAILED", "PLUGIN_STORE_LOCK_FAILED", "PLUGIN_STATE_WRITE_FAILED", "PLUGIN_INSTALL_FAILED",
		"PLUGIN_RECOVERY_REQUIRED", "PLUGIN_ACTIVATION_FAILED", "PLUGIN_REMOVAL_FAILED", "PLUGIN_STATE_CHANGED",
		"PLUGIN_CANDIDATE_DRIFT", "PLUGIN_GENERATION_DRIFT", "PLUGIN_GENERATION_MISSING", "PLUGIN_ACTIVATION_IN_PROGRESS":
		category = "runtime"
	case "PLUGIN_NOT_FOUND", "PLUGIN_MEMBER_NOT_FOUND":
		category = "not_found"
	}
	message := registryErr.Message
	if message == "" {
		message = fmt.Sprintf("plugin operation failed: %s", registryErr.Code)
	}
	return toolErrorCause(registryErr.Code, message, category, registryErr.Details, err)
}

func sourceRequest(request ManageRequest) registry.SourceRequest {
	return registry.SourceRequest{Type: request.SourceType, Ref: request.Source, GitRef: request.GitRef, GitCommit: request.GitCommit, Subdir: request.Subdir, SHA256: request.SHA256, Adapter: request.SourceAdapter, Version: request.SourceVersion, Catalog: request.Catalog, CatalogItem: request.CatalogItem}
}
