package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/envstore"
	skills "github.com/uvwt/agentdock/internal/skill"
)

func TestSkillPackageUninstallPreservesNonEmptyEnvironmentAndData(t *testing.T) {
	root := t.TempDir()
	runtime := installLifecycleTestSkill(t, root)
	secret := "configured-value"
	if _, err := runtime.packageTest(context.Background(), map[string]any{
		"action": "env_set", "skill": "demo-skill", "key": "TOKEN", "value": secret,
	}); err != nil {
		t.Fatal(err)
	}
	dataDir, err := config.SkillDataDir(runtime.cfg, "demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	dataPath := filepath.Join(dataDir, "state.json")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataPath, []byte("persistent"), 0o600); err != nil {
		t.Fatal(err)
	}

	response, err := runtime.packageTest(context.Background(), map[string]any{
		"action": "uninstall", "skill": "demo-skill",
	})
	if err != nil {
		t.Fatal(err)
	}
	result := requireUninstallResult(t, response)
	if result.Skill != "demo-skill" || len(result.RemovedVersions) != 1 || result.RemovedVersions[0] != "1.0.0" ||
		!result.PreservedEnvironment || !result.PreservedData || result.DataState != "preserved" || result.DataPath != dataDir {
		t.Fatalf("unexpected uninstall result: %#v", response)
	}
	if names, err := runtime.state.ListSkills(); err != nil || len(names) != 0 {
		t.Fatalf("installed Skills after uninstall = %#v, err=%v", names, err)
	}
	environment, err := runtime.packageTest(context.Background(), map[string]any{
		"action": "env_list", "skill": "demo-skill",
	})
	if err != nil {
		t.Fatal(err)
	}
	items, ok := environment["items"].([]envstore.Entry)
	if !ok || len(items) != 1 || items[0].Key != "TOKEN" || !items[0].Configured {
		t.Fatalf("Skill environment was not preserved: %#v", environment)
	}
	if data, err := os.ReadFile(dataPath); err != nil || string(data) != "persistent" {
		t.Fatalf("Skill data was not preserved: data=%q err=%v", data, err)
	}
}

func TestSkillPackageUninstallRemovesEmptyRuntimeData(t *testing.T) {
	root := t.TempDir()
	runtime := installLifecycleTestSkill(t, root)
	dataDir, err := config.SkillDataDir(runtime.cfg, "demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}

	response, err := runtime.packageTest(context.Background(), map[string]any{
		"action": "uninstall", "skill": "demo-skill",
	})
	if err != nil {
		t.Fatal(err)
	}
	result := requireUninstallResult(t, response)
	if result.PreservedData || result.DataState != "removed_empty" || result.DataPath != dataDir {
		t.Fatalf("unexpected empty-data cleanup result: %#v", response)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("empty runtime data directory still exists: %v", err)
	}
}

func TestSkillPackageUninstallPurgesNonEmptyRuntimeData(t *testing.T) {
	root := t.TempDir()
	runtime := installLifecycleTestSkill(t, root)
	dataDir, err := config.SkillDataDir(runtime.cfg, "demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "state.json"), []byte("delete-me"), 0o600); err != nil {
		t.Fatal(err)
	}

	response, err := runtime.packageTest(context.Background(), map[string]any{
		"action": "uninstall", "skill": "demo-skill", "purge_data": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := requireUninstallResult(t, response)
	if result.PreservedData || result.DataState != "purged" || result.DataPath != dataDir {
		t.Fatalf("unexpected purge result: %#v", response)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("purged runtime data directory still exists: %v", err)
	}
}

func TestSkillPackageRejectsPurgeForVersionOnlyUninstall(t *testing.T) {
	root := t.TempDir()
	runtime := installLifecycleTestSkill(t, root)
	_, err := runtime.packageTest(context.Background(), map[string]any{
		"action": "uninstall", "skill": "demo-skill", "version": "1.0.0", "purge_data": true,
	})
	if err == nil || !strings.Contains(err.Error(), "purge_data applies only") {
		t.Fatalf("version-only purge error = %v", err)
	}
}

func TestSkillServiceMigratesLegacyRuntimeDataRoot(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{AgentDockHome: filepath.Join(root, ".agentdock")}
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

	runtime, _ := newSkillTestServiceAtRoot(t, root)
	canonicalDir, err := config.SkillDataDir(runtime.cfg, "demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(canonicalDir, "state.json")); err != nil || string(data) != "legacy" {
		t.Fatalf("migrated data = %q, err=%v", data, err)
	}
	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("legacy Skill data directory still exists: %v", err)
	}
	legacyRoot, err := config.LegacySkillDataRoot(runtime.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("empty legacy Skill data root still exists: %v", err)
	}
}

func installLifecycleTestSkill(t *testing.T, root string) *Service {
	t.Helper()
	source := filepath.Join(root, "demo-skill")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	document := "---\nname: demo-skill\ndescription: Demo Skill.\nversion: 1.0.0\n---\n\n# Demo\n"
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, _ := newSkillTestServiceAtRoot(t, root)
	if _, err := runtime.packageTest(context.Background(), map[string]any{
		"action": "install", "source": "demo-skill", "activate": true,
	}); err != nil {
		t.Fatal(err)
	}
	return runtime
}

func requireUninstallResult(t *testing.T, response Result) skills.UninstallResult {
	t.Helper()
	result, ok := response["result"].(skills.UninstallResult)
	if !ok {
		t.Fatalf("unexpected uninstall result type: %#v", response)
	}
	return result
}
