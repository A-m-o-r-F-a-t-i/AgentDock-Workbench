package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/buildinfo"
)

func TestReleaseCatalogPublishesOnlyInstallers(t *testing.T) {
	catalog := ReleaseCatalog()
	expected := map[string]bool{
		"AgentDock-Workbench-" + buildinfo.Version + "-Android-test-signed.apk": false,
		"AgentDock-macos-universal.dmg":                                         false,
		"agentdock-workbench_" + buildinfo.Version + "_amd64.deb":               false,
		"agentdock-workbench_" + buildinfo.Version + "_arm64.deb":               false,
		"agentdock-workbench-" + buildinfo.Version + "-1.x86_64.rpm":            false,
		"agentdock-workbench-" + buildinfo.Version + "-1.aarch64.rpm":           false,
		"AgentDockSetup-amd64.exe":                                              false,
		"AgentDockSetup-arm64.exe":                                              false,
	}
	for _, artifact := range catalog {
		if !artifact.PublicContract {
			continue
		}
		if strings.HasSuffix(artifact.Name, ".sha256") || artifact.Kind == "checksum" || artifact.Kind == "bootstrap" || artifact.Kind == "binary-archive" || artifact.Kind == "desktop-update" {
			t.Fatalf("non-installer exposed as public Release contract: %+v", artifact)
		}
		if _, ok := expected[artifact.Name]; !ok {
			t.Fatalf("unexpected public Release installer: %s", artifact.Name)
		}
		expected[artifact.Name] = true
	}
	for name, seen := range expected {
		if !seen {
			t.Fatalf("public Release installer missing: %s", name)
		}
	}
}

func TestVerifyDistRequiresCatalogArtifacts(t *testing.T) {
	dir := t.TempDir()
	if err := run([]string{"verify-dist", dir}, discard{}); err == nil {
		t.Fatal("empty dist must fail")
	}
	for _, artifact := range ReleaseCatalog() {
		if !artifact.Required {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, artifact.Name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := run([]string{"verify-dist", dir}, discard{}); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyVersionMatchesBuildInfo(t *testing.T) {
	if err := run([]string{"verify-version", "v" + strings.TrimPrefix(buildinfo.Version, "v")}, discard{}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify-version", "v0.0.0"}, discard{}); err == nil {
		t.Fatal("expected version mismatch")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
