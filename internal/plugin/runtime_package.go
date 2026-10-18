package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
)

type RuntimePackage struct {
	Definition    Definition
	Name          string
	Version       string
	Generation    string
	PackageDigest string
	Root          string
	Skills        map[string]SkillMember
	MCPServers    map[string]mcpclient.ServerConfig
}

func (s *Store) runtimeRecordForState(name string, state State) (packageRecord, error) {
	if err := validateIdentifier("plugin", name); err != nil {
		return packageRecord{}, err
	}
	if err := validateGeneration(state.Generation); err != nil {
		return packageRecord{}, err
	}
	root, err := s.generationPath(name, state.Generation)
	if err != nil {
		return packageRecord{}, err
	}
	digest, exists, err := packageDigestAt(root)
	if err != nil || !exists {
		return packageRecord{}, newError("PLUGIN_GENERATION_DRIFT", "Plugin generation is unavailable", map[string]any{"plugin": name, "generation": state.Generation}, err)
	}
	record, err := readPackage(root, false)
	if err != nil {
		return packageRecord{}, err
	}
	if record.manifest.Name != name || digest != state.PackageDigest {
		return packageRecord{}, newError("PLUGIN_GENERATION_DRIFT", "Plugin generation identity or digest changed", map[string]any{"plugin": name, "generation": state.Generation}, nil)
	}
	state = normalizeState(state, record)
	dataRoot := filepath.Join(s.root, ".data", name)
	for serverName, cfg := range record.mcpConfigs {
		cfg = rebaseServerConfig(cfg, cfg.PluginRoot, root, cfg.PluginData, dataRoot)
		cfg.PluginVersion = record.manifest.Version
		if state.Source != nil {
			cfg = applySourceEnvironmentBindings(cfg)
		}
		record.mcpConfigs[serverName] = cfg
	}
	if err := s.applyRuntimeHostConfig(name, record.mcpConfigs); err != nil {
		return packageRecord{}, err
	}
	record.state = state
	heavy := record.definition.Heavy
	if state.Heavy != nil {
		heavy = *state.Heavy
	}
	record.definition = Definition{
		Source: state.Source, Compatibility: state.Compatibility,
		Name: record.manifest.Name, Description: record.manifest.Description, Version: record.manifest.Version,
		Path: filepath.Join(s.root, name), Generation: state.Generation, PackageDigest: state.PackageDigest,
		Enabled: state.Enabled, Heavy: heavy, Skills: sortedKeys(record.skillPaths), MCPServers: sortedKeys(record.mcpConfigs),
		Diagnostics: append([]string(nil), record.definition.Diagnostics...),
	}
	return record, nil
}

func (s *Store) applyRuntimeHostConfig(pluginName string, configs map[string]mcpclient.ServerConfig) error {
	hostPath := filepath.Join(s.root, ".config", pluginName+".json")
	if _, err := os.Lstat(hostPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := containedPath(s.root, hostPath, false); err != nil {
		return err
	}
	var host HostConfig
	if err := readStrictJSON(hostPath, maxStateBytes, &host); err != nil {
		return newError("PLUGIN_HOST_CONFIG_INVALID", "read host plugin configuration", map[string]any{"plugin": pluginName, "path": hostPath}, err)
	}
	for rawName, local := range host.MCPServers {
		name := nativeServerName(pluginName, rawName)
		cfg, exists := configs[name]
		if !exists {
			continue
		}
		cfg.HeaderEnv = mergeStringValues(cfg.HeaderEnv, local.HeaderEnv)
		cfg.EnvFromEnv = mergeStringValues(cfg.EnvFromEnv, local.EnvFromEnv)
		if local.Description != "" {
			cfg.Description = local.Description
		}
		if local.TimeoutMS != 0 {
			cfg.TimeoutMS = local.TimeoutMS
		}
		if local.Command != "" {
			if cfg.Transport != mcpclient.TransportStdio || !filepath.IsAbs(local.Command) {
				return newError("PLUGIN_HOST_CONFIG_INVALID", "invalid host executable override", map[string]any{"plugin": pluginName, "mcp_server": rawName}, nil)
			}
			cfg.Command = local.Command
		}
		cfg = mcpclient.NormalizeServerConfig(cfg)
		if err := mcpclient.ValidateServerConfig(cfg); err != nil {
			return newError("PLUGIN_HOST_CONFIG_INVALID", "invalid host MCP configuration", map[string]any{"plugin": pluginName, "mcp_server": rawName}, err)
		}
		configs[name] = cfg
	}
	return nil
}

func rebaseServerConfig(cfg mcpclient.ServerConfig, oldRoot, newRoot, oldData, newData string) mcpclient.ServerConfig {
	rebase := func(value string) string {
		value = replacePathPrefix(value, oldRoot, newRoot)
		return replacePathPrefix(value, oldData, newData)
	}
	cfg.Command = rebase(cfg.Command)
	cfg.Cwd = rebase(cfg.Cwd)
	for index := range cfg.Args {
		cfg.Args[index] = rebase(cfg.Args[index])
	}
	for key, value := range cfg.PackageEnv {
		cfg.PackageEnv[key] = rebase(value)
	}
	for key, value := range cfg.PackageHeaders {
		cfg.PackageHeaders[key] = rebase(value)
	}
	cfg.PluginRoot = newRoot
	cfg.PluginData = newData
	return cfg
}

func replacePathPrefix(value, oldRoot, newRoot string) string {
	if oldRoot == "" || newRoot == "" {
		return value
	}
	oldRoot = filepath.Clean(oldRoot)
	newRoot = filepath.Clean(newRoot)
	if value == oldRoot {
		return newRoot
	}
	if strings.HasPrefix(value, oldRoot+string(filepath.Separator)) {
		return newRoot + strings.TrimPrefix(value, oldRoot)
	}
	return value
}

func (s *Store) runtimePackageForState(name string, state State) (RuntimePackage, error) {
	record, err := s.runtimeRecordForState(name, state)
	if err != nil {
		return RuntimePackage{}, err
	}
	pkg := RuntimePackage{
		Definition: cloneDefinition(record.definition), Name: name, Version: record.manifest.Version,
		Generation: state.Generation, PackageDigest: state.PackageDigest, Root: record.root,
		Skills: make(map[string]SkillMember, len(record.skillPaths)), MCPServers: make(map[string]mcpclient.ServerConfig, len(record.mcpConfigs)),
	}
	for skillName, path := range record.skillPaths {
		pkg.Skills[skillName] = SkillMember{
			PackageDigest: state.PackageDigest,
			Generation:    state.Generation, Heavy: record.definition.Heavy, Description: record.skillDescriptions[skillName],
			Name: skillName, Plugin: name, Path: path, Enabled: state.Enabled && memberEnabled(state.Skills, skillName),
		}
	}
	for serverName, cfg := range record.mcpConfigs {
		cfg.PluginVersion = record.manifest.Version
		cfg.Enabled = cfg.Enabled && state.Enabled && memberEnabled(state.MCPServers, serverName)
		pkg.MCPServers[serverName] = cfg
	}
	return pkg, nil
}

func (s *Store) definitionForState(name string, state State) (Definition, error) {
	pkg, err := s.runtimePackageForState(name, state)
	if err != nil {
		return Definition{}, err
	}
	return pkg.Definition, nil
}

func (s *Store) AcquireRuntimePackage(ctx context.Context, name string) (RuntimePackage, func(), error) {
	if err := ctx.Err(); err != nil {
		return RuntimePackage{}, nil, err
	}
	name = strings.TrimSpace(name)
	for attempt := 0; attempt < 2; attempt++ {
		state, err := s.readNamedState(name)
		if err != nil {
			return RuntimePackage{}, nil, err
		}
		releaseGeneration, err := s.AcquireGenerationRead(ctx, name, state.Generation)
		if err != nil {
			var pluginErr *Error
			if attempt == 0 && errors.As(err, &pluginErr) && pluginErr.Code == "PLUGIN_GENERATION_MISSING" {
				continue
			}
			return RuntimePackage{}, nil, err
		}
		pkg, err := s.runtimePackageForState(name, state)
		if err != nil {
			releaseGeneration()
			if attempt == 0 && (errors.Is(err, os.ErrNotExist)) {
				continue
			}
			return RuntimePackage{}, nil, err
		}
		return pkg, releaseGeneration, nil
	}
	return RuntimePackage{}, nil, newError("PLUGIN_GENERATION_MISSING", "Plugin generation changed while acquiring a read lease", map[string]any{"plugin": name}, nil)
}

func (s *Store) AcquireActivationRuntime(ctx context.Context, name, owner string) (RuntimePackage, func(), error) {
	if err := ctx.Err(); err != nil {
		return RuntimePackage{}, nil, err
	}
	releaseStore, err := s.acquireContext(ctx)
	if err != nil {
		return RuntimePackage{}, nil, err
	}
	transaction, err := s.loadOwnedLifecycle(strings.TrimSpace(name), owner)
	if err != nil {
		releaseStore()
		return RuntimePackage{}, nil, err
	}
	if transaction.Candidate == nil {
		releaseStore()
		return RuntimePackage{}, nil, newError("PLUGIN_ACTIVATION_NOT_FOUND", "Plugin lifecycle has no activation candidate", map[string]any{"plugin": name}, nil)
	}
	state := *transaction.Candidate
	releaseGeneration, err := s.acquireGenerationReadUnlocked(ctx, name, state.Generation)
	releaseStore()
	if err != nil {
		return RuntimePackage{}, nil, err
	}
	pkg, err := s.runtimePackageForState(name, state)
	if err != nil {
		releaseGeneration()
		return RuntimePackage{}, nil, err
	}
	return pkg, releaseGeneration, nil
}

func (s *Store) AcquireSkillMember(ctx context.Context, pluginName, skillName string) (SkillMember, func(), error) {
	pkg, release, err := s.AcquireRuntimePackage(ctx, pluginName)
	if err != nil {
		return SkillMember{}, nil, err
	}
	member, ok := pkg.Skills[skillName]
	if !ok {
		release()
		return SkillMember{}, nil, newError("PLUGIN_MEMBER_NOT_FOUND", "plugin Skill member was not found", map[string]any{"plugin": pluginName, "member": skillName}, nil)
	}
	if !member.Enabled {
		release()
		return SkillMember{}, nil, newError("PLUGIN_MEMBER_DISABLED", "plugin Skill member is disabled", map[string]any{"plugin": pluginName, "member": skillName}, nil)
	}
	return member, release, nil
}

// CreateSkillRuntime copies the already selected Skill generation for command
// execution. Resource and rule reads continue to use the immutable member path.
// The returned lease independently pins that generation, including when an
// update commits between resolving the Skill and preparing its command.
func (s *Store) CreateSkillRuntime(ctx context.Context, member SkillMember) (string, func(), error) {
	if !member.Enabled {
		return "", nil, newError("PLUGIN_MEMBER_DISABLED", "plugin Skill member is disabled", map[string]any{"plugin": member.Plugin, "member": member.Name}, nil)
	}
	generationRoot, err := s.generationPath(member.Plugin, member.Generation)
	if err != nil {
		return "", nil, err
	}
	releaseGeneration, err := s.AcquireGenerationRead(ctx, member.Plugin, member.Generation)
	if err != nil {
		return "", nil, err
	}
	releaseOnError := true
	defer func() {
		if releaseOnError {
			releaseGeneration()
		}
	}()
	digest, exists, err := packageDigestAt(generationRoot)
	if err != nil || !exists || member.PackageDigest == "" || digest != member.PackageDigest {
		return "", nil, newError("PLUGIN_GENERATION_DRIFT", "Plugin Skill generation no longer matches the selected package", map[string]any{"plugin": member.Plugin, "generation": member.Generation}, err)
	}
	record, err := readPackage(generationRoot, false)
	if err != nil {
		return "", nil, err
	}
	skillPath, found := record.skillPaths[member.Name]
	if !found || record.manifest.Name != member.Plugin || !sameFilesystemPath(skillPath, member.Path) {
		return "", nil, newError("PLUGIN_MEMBER_NOT_FOUND", "Plugin Skill does not belong to the selected generation", map[string]any{"plugin": member.Plugin, "member": member.Name}, nil)
	}
	resolvedGeneration, err := resolvedPath(generationRoot, false)
	if err != nil {
		return "", nil, err
	}
	relative, err := filepath.Rel(resolvedGeneration, skillPath)
	if err != nil || !filepath.IsLocal(relative) {
		return "", nil, errors.New("Plugin Skill path escapes its generation")
	}
	runtimeRoot, releaseRuntime, err := s.createRuntimeGeneration(ctx, member.Plugin, member.Generation, generationRoot)
	if err != nil {
		return "", nil, err
	}
	if err := validateRuntimePackageFiles(ctx, generationRoot, runtimeRoot); err != nil {
		releaseRuntime()
		return "", nil, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			releaseRuntime()
			releaseGeneration()
		})
	}
	releaseOnError = false
	return filepath.Join(runtimeRoot, relative), release, nil
}

func (s *Store) RuntimeMCPPackage(ctx context.Context, name string) (RuntimePackage, func(), error) {
	pkg, release, err := s.AcquireRuntimePackage(ctx, name)
	if err != nil {
		return RuntimePackage{}, nil, err
	}
	return s.prepareRuntimeMCPPackage(ctx, pkg, release)
}

// AcquireMCPRuntime pins the exact generation selected for a new connection.
// RuntimeMCPPackage verifies the immutable digest and reuses its existing copy;
// a concurrent update must not silently substitute the new generation's lease.
func (s *Store) AcquireMCPRuntime(ctx context.Context, cfg mcpclient.ServerConfig) (func(), error) {
	pkg, release, err := s.RuntimeMCPPackage(ctx, cfg.PluginName)
	if err != nil {
		return nil, err
	}
	member, found := pkg.MCPServers[cfg.Name]
	if !found || !member.Enabled || pkg.Version != cfg.PluginVersion ||
		!sameFilesystemPath(member.PluginRoot, cfg.PluginRoot) || !sameFilesystemPath(member.PluginData, cfg.PluginData) {
		release()
		return nil, newError("PLUGIN_GENERATION_DRIFT", "Plugin MCP runtime no longer matches the selected connection", map[string]any{"plugin": cfg.PluginName, "server": cfg.Name}, nil)
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (s *Store) ActivationMCPPackage(ctx context.Context, name, owner string) (RuntimePackage, func(), error) {
	pkg, release, err := s.AcquireActivationRuntime(ctx, name, owner)
	if err != nil {
		return RuntimePackage{}, nil, err
	}
	return s.prepareRuntimeMCPPackage(ctx, pkg, release)
}

func (s *Store) prepareRuntimeMCPPackage(ctx context.Context, pkg RuntimePackage, releaseGeneration func()) (RuntimePackage, func(), error) {
	runtimeRoot := pkg.Root
	cleanupRuntime := func() {}
	for _, cfg := range pkg.MCPServers {
		if cfg.Enabled && cfg.Transport == mcpclient.TransportStdio {
			var err error
			runtimeRoot, cleanupRuntime, err = s.createRuntimeGeneration(ctx, pkg.Name, pkg.Generation, pkg.Root)
			if err != nil {
				releaseGeneration()
				return RuntimePackage{}, nil, err
			}
			break
		}
	}
	for name, cfg := range pkg.MCPServers {
		original := mcpclient.NormalizeServerConfig(cfg)
		if runtimeRoot != pkg.Root {
			cfg = rebaseServerConfig(cfg, pkg.Root, runtimeRoot, cfg.PluginData, cfg.PluginData)
		}
		pkg.MCPServers[name] = mcpclient.WithPluginCatalogSource(cfg, original, pkg.Generation, pkg.PackageDigest)
	}
	pkg.Root = runtimeRoot
	var once sync.Once
	release := func() {
		once.Do(func() {
			cleanupRuntime()
			releaseGeneration()
		})
	}
	return pkg, release, nil
}

func (p RuntimePackage) String() string {
	return fmt.Sprintf("%s@%s[%s]", p.Name, p.Version, p.Generation)
}
