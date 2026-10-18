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
