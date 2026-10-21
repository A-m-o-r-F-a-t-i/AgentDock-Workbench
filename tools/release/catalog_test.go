package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/buildinfo"
)

func TestReleaseCatalogPublishesInstallersAndUpdaterPayloads(t *testing.T) {
	catalog := ReleaseCatalog()
	expected := map[string]bool{
		"AgentDock-Workbench-" + buildinfo.Version + "-Android.apk":         false,
		"AgentDock-Workbench-" + buildinfo.Version + "-macos-universal.dmg": false,
		"agentdock-workbench_" + buildinfo.Version + "_amd64.deb":           false,
		"agentdock-workbench_" + buildinfo.Version + "_arm64.deb":           false,
		"agentdock-workbench-" + buildinfo.Version + "-1.x86_64.rpm":        false,
		"agentdock-workbench-" + buildinfo.Version + "-1.aarch64.rpm":       false,
		"AgentDock-Workbench-" + buildinfo.Version + "-Setup-amd64.exe":     false,
		"AgentDock-Workbench-" + buildinfo.Version + "-Setup-arm64.exe":     false,
	}
	updatePayloads := []string{
		"agentdock_linux_amd64.tar.gz",
		"agentdock_linux_arm64.tar.gz",
		"agentdock_darwin_amd64.tar.gz",
		"agentdock_darwin_arm64.tar.gz",
		"agentdock_windows_amd64.zip",
		"agentdock_windows_arm64.zip",
		"AgentDock-macos-universal.zip",
	}
	for _, name := range updatePayloads {
		expected[name] = false
		expected[name+".sha256"] = false
	}
	for _, artifact := range catalog {
		if !artifact.PublicContract {
			continue
		}
		if artifact.Kind == "bootstrap" {
			t.Fatalf("bootstrap script exposed as public Release contract: %+v", artifact)
		}
		if _, ok := expected[artifact.Name]; !ok {
			t.Fatalf("unexpected public Release asset: %s", artifact.Name)
		}
		expected[artifact.Name] = true
	}
	for name, seen := range expected {
		if !seen {
			t.Fatalf("public Release asset missing: %s", name)
		}
	}
}

func TestReleaseCatalogUsesSingleStableAndroidInstaller(t *testing.T) {
	stableAPK := "AgentDock-Workbench-" + buildinfo.Version + "-Android.apk"
	legacyAPK := "AgentDock-Workbench-" + buildinfo.Version + "-Android-test-signed.apk"
	expected := map[string]bool{
		stableAPK:             false,
		stableAPK + ".sha256": false,
	}
	for _, artifact := range ReleaseCatalog() {
		if artifact.Name == legacyAPK || artifact.Name == legacyAPK+".sha256" {
			t.Fatalf("historical test-signed APK remains in the release catalog: %s", artifact.Name)
		}
		if strings.Contains(artifact.Name, "Android-Core-arm64") {
			t.Fatalf("separate Android Core attachment remains in the release catalog: %s", artifact.Name)
		}
		if _, ok := expected[artifact.Name]; ok {
			if !artifact.Required {
				t.Fatalf("Android release artifact is optional: %+v", artifact)
			}
			expected[artifact.Name] = true
		}
	}
	for name, seen := range expected {
		if !seen {
			t.Fatalf("Android release artifact missing: %s", name)
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
