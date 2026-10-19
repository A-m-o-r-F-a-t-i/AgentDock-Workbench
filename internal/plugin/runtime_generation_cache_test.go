package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeGenerationMigratesAndRepairsPersistentCache(t *testing.T) {
	home := t.TempDir()
	store, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	source := writePluginPackage(t, t.TempDir(), "runtime-cache", "1.0.0", nil, []string{"runtime-cache-mcp"})
	if _, err = store.Install(source, false); err != nil {
		store.Close()
		t.Fatal(err)
	}
	state, err := store.readNamedState("runtime-cache")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	generationRoot, err := store.generationPath("runtime-cache", state.Generation)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	legacyRoot := filepath.Join(store.root, ".runtime", "runtime-cache", "generation-"+state.Generation+"-legacy")
	if err = copyPackageTree(generationRoot, legacyRoot); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(legacyRoot, "runtime-cache.txt"), []byte("keep"), 0o600); err != nil {
		store.Close()
		t.Fatal(err)
	}

	pkg, release, err := store.RuntimeMCPPackage(t.Context(), "runtime-cache")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	expectedRoot, _, err := store.runtimeGenerationPaths("runtime-cache", state.Generation)
	if err != nil {
		release()
		store.Close()
		t.Fatal(err)
	}
	if pkg.Root != expectedRoot {
		release()
		store.Close()
		t.Fatalf("legacy cache was not migrated to the stable path: %q != %q", pkg.Root, expectedRoot)
	}
	if content, err := os.ReadFile(filepath.Join(pkg.Root, "runtime-cache.txt")); err != nil || string(content) != "keep" {
		release()
		store.Close()
		t.Fatalf("legacy runtime cache was not preserved: content=%q error=%v", content, err)
	}
	release()
	store.Close()

	manifestPath := filepath.Join(expectedRoot, ManifestFilename)
	if err = os.WriteFile(manifestPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	repaired, releaseRepaired, err := reopened.RuntimeMCPPackage(t.Context(), "runtime-cache")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseRepaired()
	if repaired.Root != expectedRoot {
		t.Fatalf("repaired runtime cache moved: %q != %q", repaired.Root, expectedRoot)
	}
	if err = validateRuntimePackageFiles(t.Context(), generationRoot, repaired.Root); err != nil {
		t.Fatalf("corrupt persistent runtime cache was not rebuilt: %v", err)
	}
	if _, err = os.Stat(filepath.Join(repaired.Root, "runtime-cache.txt")); !os.IsNotExist(err) {
		t.Fatalf("runtime-generated cache survived a mandatory rebuild: %v", err)
	}
}

func TestCatalogMCPPackageDefersRuntimeMaterializationUntilSelected(t *testing.T) {
	home := t.TempDir()
	store, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := writePluginPackage(t, t.TempDir(), "lazy-runtime", "1.0.0", nil, []string{"lazy-runtime-mcp"})
	if _, err = store.Install(source, false); err != nil {
		t.Fatal(err)
	}
	state, err := store.readNamedState("lazy-runtime")
	if err != nil {
		t.Fatal(err)
	}
	expectedRoot, _, err := store.runtimeGenerationPaths("lazy-runtime", state.Generation)
	if err != nil {
		t.Fatal(err)
	}

	pkg, releaseCatalog, err := store.CatalogMCPPackage(t.Context(), "lazy-runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseCatalog()
	cfg, found := pkg.MCPServers["lazy-runtime-mcp"]
	if !found || cfg.PluginRoot != expectedRoot {
		t.Fatalf("catalog MCP config = %#v, expected runtime root %q", cfg, expectedRoot)
	}
	if _, err := os.Stat(expectedRoot); !os.IsNotExist(err) {
		t.Fatalf("catalog discovery materialized the runtime generation: %v", err)
	}

	if err := store.PrepareMCPData(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(expectedRoot); err != nil {
		t.Fatalf("selected MCP runtime was not materialized: %v", err)
	}
	marker := filepath.Join(expectedRoot, "runtime-generated.txt")
	if err := os.WriteFile(marker, []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	releaseRuntime, err := store.AcquireMCPRuntime(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	releaseRuntime()
	if content, err := os.ReadFile(marker); err != nil || string(content) != "cache" {
		t.Fatalf("live runtime acquisition rebuilt an already prepared cache: content=%q error=%v", content, err)
	}
}

func TestCatalogMCPPackageDefersFullGenerationDigestUntilSelection(t *testing.T) {
	home := t.TempDir()
	store, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := writePluginPackage(t, t.TempDir(), "lazy-digest", "1.0.0", nil, []string{"lazy-digest-mcp"})
	if _, err = store.Install(source, false); err != nil {
		t.Fatal(err)
	}
	state, err := store.readNamedState("lazy-digest")
	if err != nil {
		t.Fatal(err)
	}
	generationRoot, err := store.generationPath("lazy-digest", state.Generation)
	if err != nil {
		t.Fatal(err)
	}
	tamper := filepath.Join(generationRoot, "catalog-must-not-hash-this.txt")
	if err := os.WriteFile(tamper, []byte("changed after install"), 0o600); err != nil {
		t.Fatal(err)
	}

	pkg, release, err := store.CatalogMCPPackage(t.Context(), "lazy-digest")
	if err != nil {
		t.Fatalf("metadata-only catalog performed a full generation digest: %v", err)
	}
	cfg := pkg.MCPServers["lazy-digest-mcp"]
	release()
	if err := store.PrepareMCPData(t.Context(), cfg); pluginErrorCode(err) != "PLUGIN_GENERATION_DRIFT" {
		t.Fatalf("selected service accepted changed generation: %v", err)
	}
	if err := os.Remove(tamper); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareMCPData(t.Context(), cfg); err != nil {
		t.Fatalf("restored generation did not prepare: %v", err)
	}
}

func TestStoreStartupDefersExistingGenerationDigestUntilSelection(t *testing.T) {
	home := t.TempDir()
	store, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	source := writePluginPackage(t, t.TempDir(), "startup-digest", "1.0.0", nil, []string{"startup-digest-mcp"})
	if _, err = store.Install(source, false); err != nil {
		store.Close()
		t.Fatal(err)
	}
	state, err := store.readNamedState("startup-digest")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	generationRoot, err := store.generationPath("startup-digest", state.Generation)
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	tamper := filepath.Join(generationRoot, "startup-must-not-hash-this.txt")
	if err := os.WriteFile(tamper, []byte("changed after install"), 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := New(home)
	if err != nil {
		t.Fatalf("Core startup performed a full existing-generation digest: %v", err)
	}
	defer reopened.Close()
	pkg, release, err := reopened.CatalogMCPPackage(t.Context(), "startup-digest")
	if err != nil {
		t.Fatal(err)
	}
	cfg := pkg.MCPServers["startup-digest-mcp"]
	release()
	if err := reopened.PrepareMCPData(t.Context(), cfg); pluginErrorCode(err) != "PLUGIN_GENERATION_DRIFT" {
		t.Fatalf("selected service accepted startup-time generation drift: %v", err)
	}
}
