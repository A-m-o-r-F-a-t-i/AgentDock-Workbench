package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
	registry "github.com/uvwt/agentdock/internal/plugin"
)

func TestLazyPluginCatalogSurvivesRuntimeRebuild(t *testing.T) {
	home, source := t.TempDir(), t.TempDir()
	const pluginName, serverName = "catalog.plugin", "catalog-stdio"
	writeJSON := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := registry.Manifest{Schema: registry.ManifestSchema, Name: pluginName, Version: "1.0.0", Description: "Retained Plugin catalog fixture."}
	writeJSON(filepath.Join(source, registry.ManifestFilename), manifest)
	writeJSON(filepath.Join(source, registry.MCPFilename), registry.MCPConfig{Schema: registry.MCPSchema, MCPServers: map[string]registry.MCPServer{
		serverName: {Type: "stdio", Command: "test-helper", Args: []string{"-test.run=TestMCPStdioHelperProcess", "--", "${PLUGIN_ROOT}/entry"}, Env: map[string]string{"GO_WANT_MCP_HELPER": "1", "ASSET": "${PLUGIN_ROOT}/asset"}},
	}})
	store, err := registry.New(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if _, err = store.Install(source, false); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	writeJSON(filepath.Join(home, "plugins", ".config", pluginName+".json"), registry.HostConfig{MCPServers: map[string]registry.MCPHostConfig{serverName: {Command: executable}}})
	var preparations atomic.Int32
	openRuntime := func() (*mcpclient.Manager, registry.RuntimePackage, func()) {
		t.Helper()
		pkg, release, err := store.RuntimeMCPPackage(t.Context(), pluginName)
		if err != nil {
			t.Fatal(err)
		}
		manager, err := mcpclient.NewManager(home)
		if err != nil {
			release()
			t.Fatal(err)
		}
		closeRuntime := func() {
			_ = manager.Close()
			release()
		}
		t.Cleanup(closeRuntime)
		manager.SetExternalServerPreparation(func(ctx context.Context, cfg mcpclient.ServerConfig) error {
			preparations.Add(1)
			return store.PrepareMCPData(ctx, cfg)
		})
		if err = manager.SetExternalServerProvider(func(context.Context) (map[string]mcpclient.ServerConfig, error) { return pkg.MCPServers, nil }); err != nil {
			t.Fatal(err)
		}
		return manager, pkg, closeRuntime
	}
	first, previous, closeFirst := openRuntime()
	_, summary, err := first.Inspect(serverName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = first.Update(t.Context(), serverName, summary.Revision, "persistent", mcpclient.ConfigPatch{"lazy": json.RawMessage("true")}, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err = first.Refresh(t.Context(), serverName); err != nil {
		t.Fatal(err)
	}
	if preparations.Load() != 1 {
		t.Fatal("initial discovery did not prepare exactly one stdio service")
	}
	previousFingerprint, err := first.ExecutionFingerprint(t.Context(), serverName)
	if err != nil {
		t.Fatal(err)
	}
	closeFirst()
	if info, err := os.Stat(previous.Root); err != nil || !info.IsDir() {
		t.Fatalf("stable runtime cache was not retained: info=%v err=%v", info, err)
	}
	store.Close()
	manifestPath := filepath.Join(previous.Root, registry.ManifestFilename)
	originalManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(previous.Root, "runtime-rebuild-marker")
	if err = os.WriteFile(markerPath, []byte("remove on rebuild"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestPath, append(append([]byte(nil), originalManifest...), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err = registry.New(home)
	if err != nil {
		t.Fatal(err)
	}
	preparations.Store(0)
	second, current, closeSecond := openRuntime()
	if current.Root != previous.Root || current.Generation != previous.Generation || current.PackageDigest != previous.PackageDigest {
		t.Fatal("same immutable Plugin did not reuse its stable runtime identity")
	}
	if _, err = os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("invalid runtime cache was not rebuilt cleanly: %v", err)
	}
	if repairedManifest, err := os.ReadFile(manifestPath); err != nil || !bytes.Equal(repairedManifest, originalManifest) {
		t.Fatalf("runtime rebuild did not restore immutable package files: err=%v", err)
	}
	catalog, err := second.Catalog(t.Context(), serverName)
	if err != nil || !catalog.Complete || catalog.State.State != "cached" || !catalog.State.Lazy || catalog.Tools["echo"].Name != "echo" || preparations.Load() != 0 {
		t.Fatalf("retained discovery started a service or lost its catalog: state=%+v preparations=%d err=%v", catalog.State, preparations.Load(), err)
	}
	currentFingerprint, err := second.ExecutionFingerprint(t.Context(), serverName)
	if err != nil || currentFingerprint != previousFingerprint || preparations.Load() != 0 {
		t.Fatalf("stable runtime rebuild changed immutable execution identity or started the service: err=%v", err)
	}
	result, err := second.Call(t.Context(), serverName+":echo", map[string]any{"text": "first business call", "readEnv": "PLUGIN_ROOT"})
	if err != nil {
		t.Fatal(err)
	}
	structured, _ := result["structuredContent"].(map[string]any)
	if structured["echo"] != current.Root || preparations.Load() != 1 {
		t.Fatalf("business call did not start in the current runtime: result=%#v preparations=%d", result, preparations.Load())
	}
	_, summary, err = second.Inspect(serverName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = second.Update(t.Context(), serverName, summary.Revision, "runtime", mcpclient.ConfigPatch{"args": json.RawMessage(`["-test.run=TestMCPStdioHelperProcess","--","changed"]`)}, false); err != nil {
		t.Fatal(err)
	}
	if changed, _, err := second.CachedCatalog(serverName); err != nil || changed.Complete || preparations.Load() != 1 {
		t.Fatalf("changed arguments reused cached tools or started a lazy server: %+v err=%v", changed.State, err)
	}
	closeSecond()
	manifest.Version = "2.0.0"
	writeJSON(filepath.Join(source, registry.ManifestFilename), manifest)
	if _, err = store.Install(source, true); err != nil {
		t.Fatal(err)
	}
	preparations.Store(0)
	third, updated, _ := openRuntime()
	if updated.Generation == current.Generation || updated.PackageDigest == current.PackageDigest {
		t.Fatal("test update did not change immutable Plugin provenance")
	}
	if changed, _, err := third.CachedCatalog(serverName); err != nil || changed.Complete || preparations.Load() != 0 {
		t.Fatalf("updated Plugin reused an older catalog or started a lazy server: %+v err=%v", changed.State, err)
	}
}
