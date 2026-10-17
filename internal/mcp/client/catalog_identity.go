package client

import (
	"maps"
	"slices"
)

// Only the Plugin loader can attach this in-memory provenance. It records the
// exact fields rebased from an immutable generation into its leased runtime.
type pluginCatalogSource struct {
	server, plugin, generation, packageDigest string
	original, mapped                          pluginCatalogDefinition
}

type pluginCatalogDefinition struct {
	root, command, cwd         string
	args                       []string
	packageEnv, packageHeaders map[string]string
}

func pluginCatalogDefinitionFor(cfg ServerConfig) pluginCatalogDefinition {
	return pluginCatalogDefinition{
		root: cfg.PluginRoot, command: cfg.Command, cwd: cfg.Cwd,
		args: append([]string(nil), cfg.Args...), packageEnv: maps.Clone(cfg.PackageEnv), packageHeaders: maps.Clone(cfg.PackageHeaders),
	}
}

// WithPluginCatalogSource binds an already validated immutable Plugin definition
// to its runtime mapping. This provenance is used only for retained discovery;
// live, prepared-call and recovery fingerprints still use the actual config.
// Callers must preserve original before rebasing any shared slices or maps.
func WithPluginCatalogSource(cfg, original ServerConfig, generation, packageDigest string) ServerConfig {
	cfg.catalogSource = nil
	if cfg.SourceType != "plugin" || cfg.PluginName == "" || cfg.PluginName != original.PluginName ||
		cfg.Name != original.Name || cfg.PluginVersion != original.PluginVersion ||
		cfg.PluginRoot == "" || original.PluginRoot == "" || generation == "" || packageDigest == "" {
		return cfg
	}
	cfg, original = normalizeServerConfig(cfg), normalizeServerConfig(original)
	cfg.catalogSource = &pluginCatalogSource{
		server: cfg.Name, plugin: cfg.PluginName, generation: generation, packageDigest: packageDigest,
		original: pluginCatalogDefinitionFor(original), mapped: pluginCatalogDefinitionFor(cfg),
	}
	return cfg
}

func retainedDefinitionFingerprint(cfg ServerConfig) (string, error) {
	source := cfg.catalogSource
	if source == nil || cfg.SourceType != "plugin" || cfg.Name != source.server || cfg.PluginName != source.plugin || cfg.PluginRoot != source.mapped.root {
		return definitionFingerprint(cfg)
	}
	// An overlay owns a whole scalar, args array or map. Project only fields that
	// still equal the loader's mapping, so explicit changes retain their identity.
	var changedFields []string
	cfg.PluginRoot = source.original.root
	if cfg.Command == source.mapped.command {
		cfg.Command = source.original.command
	} else {
		changedFields = append(changedFields, "command")
	}
	if cfg.Cwd == source.mapped.cwd {
		cfg.Cwd = source.original.cwd
	} else {
		changedFields = append(changedFields, "cwd")
	}
	if slices.Equal(cfg.Args, source.mapped.args) {
		cfg.Args = source.original.args
	} else {
		changedFields = append(changedFields, "args")
	}
	if maps.Equal(cfg.PackageEnv, source.mapped.packageEnv) {
		cfg.PackageEnv = source.original.packageEnv
	} else {
		changedFields = append(changedFields, "package_env")
	}
	if maps.Equal(cfg.PackageHeaders, source.mapped.packageHeaders) {
		cfg.PackageHeaders = source.original.packageHeaders
	} else {
		changedFields = append(changedFields, "package_headers")
	}
	fingerprint, err := definitionFingerprint(cfg)
	if err != nil {
		return "", err
	}
	// An explicit path back into the immutable generation is different from the
	// default runtime path, even when projection yields the same original value.
	return digestJSON(struct {
		Definition, Plugin, Generation, PackageDigest string
		ChangedFields                                 []string
	}{fingerprint, source.plugin, source.generation, source.packageDigest, changedFields})
}
