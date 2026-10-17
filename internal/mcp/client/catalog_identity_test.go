package client

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestPluginCatalogIdentityRetainsDefinitionAndOverlayBoundaries(t *testing.T) {
	root := t.TempDir()
	immutable, firstRoot, secondRoot := filepath.Join(root, "immutable"), filepath.Join(root, "first"), filepath.Join(root, "second")
	original := ServerConfig{
		Name: "cache", SourceType: "plugin", PluginName: "demo.plugin", PluginVersion: "1.0.0",
		Description: "Plugin catalog identity fixture",
		PluginRoot:  immutable, PluginData: filepath.Join(root, "private"), Transport: TransportStdio,
		Command: filepath.Join(immutable, "server"), Args: []string{filepath.Join(immutable, "entry")}, Cwd: immutable,
		PackageEnv: map[string]string{"ASSET": filepath.Join(immutable, "asset")},
		Enabled:    true,
	}
	mapRuntime := func(root, generation, digest string) ServerConfig {
		cfg := normalizeServerConfig(original)
		cfg.PluginRoot, cfg.Command, cfg.Cwd = root, filepath.Join(root, "server"), root
		cfg.Args = []string{filepath.Join(root, "entry")}
		cfg.PackageEnv["ASSET"] = filepath.Join(root, "asset")
		return WithPluginCatalogSource(cfg, original, generation, digest)
	}
	first, second := mapRuntime(firstRoot, "generation-1", "digest-1"), mapRuntime(secondRoot, "generation-1", "digest-1")
	identity := func(cfg ServerConfig) string {
		t.Helper()
		value, err := catalogCacheIdentity(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := identity(first)
	if before != identity(second) {
		t.Fatal("rebuilding the same immutable Plugin invalidated its retained catalog")
	}
	liveFirst, _ := definitionFingerprint(first)
	liveSecond, _ := definitionFingerprint(second)
	if liveFirst == liveSecond || first.Command != filepath.Join(firstRoot, "server") || first.PackageEnv["ASSET"] != filepath.Join(firstRoot, "asset") {
		t.Fatal("cache identity changed or weakened the actual execution configuration")
	}
	for _, cfg := range []ServerConfig{mapRuntime(secondRoot, "generation-2", "digest-1"), mapRuntime(secondRoot, "generation-1", "digest-2")} {
		if before == identity(cfg) {
			t.Fatal("changed immutable Plugin provenance reused the catalog")
		}
	}
	for name, change := range map[string]struct {
		field string
		value any
	}{
		"command": {"command", "other-server"}, "args": {"args", []string{"--other"}},
		"cwd": {"cwd", filepath.Join(root, "other")}, "env_from_env": {"env_from_env", map[string]string{"TOKEN": "OTHER_TOKEN"}},
		"immutable-command": {"command", original.Command}, "immutable-args": {"args", original.Args}, "immutable-cwd": {"cwd", original.Cwd},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(change.value)
			if err != nil {
				t.Fatal(err)
			}
			patch := ConfigPatch{change.field: encoded}
			one, err := patchConfig(first, patch)
			if err != nil {
				t.Fatal(err)
			}
			two, err := patchConfig(second, patch)
			if err != nil {
				t.Fatal(err)
			}
			if identity(one) == before || identity(one) != identity(two) {
				t.Fatal("catalog identity lost the overlay or retained an unrelated runtime path")
			}
		})
	}
	first.catalogSource, second.catalogSource = nil, nil
	if identity(first) == identity(second) {
		t.Fatal("unmanaged definitions were allowed to discard runtime path identity")
	}
}
