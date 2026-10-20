package skilldata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

func TestEnsureMigratesLegacyDataIntoCanonicalRuntimeRoot(t *testing.T) {
	cfg := testConfig(t)
	legacyDir, err := config.LegacySkillDataDir(cfg, "demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "state.json"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Ensure(cfg, "demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	want, err := config.SkillDataDir(cfg, "demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Ensure() = %q, want %q", got, want)
	}
	if data, err := os.ReadFile(filepath.Join(got, "state.json")); err != nil || string(data) != "legacy" {
		t.Fatalf("migrated data = %q, err=%v", data, err)
	}
	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("legacy directory still exists: %v", err)
	}
}

func TestMigrateLegacyRootDropsEmptySkillDataDirectories(t *testing.T) {
	cfg := testConfig(t)
	legacyDir, err := config.LegacySkillDataDir(cfg, "empty-skill")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := MigrateLegacyRoot(cfg); err != nil {
		t.Fatal(err)
	}
	canonicalDir, err := config.SkillDataDir(cfg, "empty-skill")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{legacyDir, canonicalDir} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("empty Skill data directory still exists at %s: %v", path, err)
		}
	}
}

func TestCleanupRemovesEmptyPreservesNonEmptyAndPurgesExplicitly(t *testing.T) {
	cfg := testConfig(t)
	dataDir, err := Ensure(cfg, "demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	result, err := Cleanup(cfg, "demo-skill", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "removed_empty" || result.Preserved || !result.Removed {
		t.Fatalf("empty cleanup result = %#v", result)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("empty data directory still exists: %v", err)
	}

	dataDir, err = Ensure(cfg, "demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "state.json"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = Cleanup(cfg, "demo-skill", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "preserved" || !result.Preserved || result.Removed {
		t.Fatalf("preserve cleanup result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "state.json")); err != nil {
		t.Fatalf("non-empty data was not preserved: %v", err)
	}

	result, err = Cleanup(cfg, "demo-skill", true)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "purged" || result.Preserved || !result.Removed {
		t.Fatalf("purge cleanup result = %#v", result)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("purged data directory still exists: %v", err)
	}
}

func TestMigrateLegacyRootRejectsTwoNonEmptyDataTrees(t *testing.T) {
	cfg := testConfig(t)
	legacyDir, err := config.LegacySkillDataDir(cfg, "demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	canonicalDir, err := config.SkillDataDir(cfg, "demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{legacyDir: "legacy", canonicalDir: "canonical"} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "state.json"), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	err = MigrateLegacyRoot(cfg)
	if err == nil || !strings.Contains(err.Error(), "both non-empty") {
		t.Fatalf("migration conflict error = %v", err)
	}
	for _, path := range []string{legacyDir, canonicalDir} {
		if _, statErr := os.Stat(filepath.Join(path, "state.json")); statErr != nil {
			t.Fatalf("conflicting data tree was modified at %s: %v", path, statErr)
		}
	}
}

func TestEnsureRejectsSymlinkedDataRoot(t *testing.T) {
	cfg := testConfig(t)
	outside := t.TempDir()
	dataRoot, err := config.SkillDataRoot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dataRoot); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}
	if _, err := Ensure(cfg, "demo-skill"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Ensure() error = %v, want symlink rejection", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "demo-skill")); !os.IsNotExist(err) {
		t.Fatalf("data directory escaped through symlink: %v", err)
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Config{AgentDockHome: filepath.Join(t.TempDir(), ".agentdock")}
	if err := os.MkdirAll(cfg.AgentDockHome, 0o700); err != nil {
		t.Fatal(err)
	}
	return cfg
}
