package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/envstore"
	skills "github.com/uvwt/agentdock/internal/skill"
	skillbundle "github.com/uvwt/agentdock/internal/skill/bundle"
	skillstate "github.com/uvwt/agentdock/internal/skill/state"
	"github.com/uvwt/agentdock/internal/updateengine"
)

func TestWriteCoreEnvironmentKeepsServiceDataDirectories(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "agentdock.env")
	home := filepath.Join(t.TempDir(), "custom-state")
	work := filepath.Join(t.TempDir(), "custom-work")

	if err := writeCoreEnvironment(envFile, Request{
		AgentDockHome:       home,
		AgentDockDefaultDir: work,
	}); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		values, err := envstore.ParseFile(envFile)
		if err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{
			"AGENTDOCK_HOME":        home,
			"AGENTDOCK_DEFAULT_DIR": work,
		} {
			if got := values[key]; got != want {
				t.Errorf("Core service env %s = %q, want %q", key, got, want)
			}
		}
	}
	check()

	if err := writeCoreEnvironment(envFile, Request{}); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestEngineLinuxUpgradeKeepsServiceDataDirectories(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux service data layout")
	}
	for _, tc := range []struct {
		name        string
		custom      bool
		interrupted bool
	}{
		{name: "custom-directories", custom: true},
		{name: "data-directory-defaults"},
		{name: "interrupted-upgrade", custom: true, interrupted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dataDir := filepath.Join(root, "data")
			home := filepath.Join(dataDir, ".agentdock")
			work := filepath.Join(dataDir, "AgentDock")
			if tc.custom {
				home = filepath.Join(root, "custom-state")
				work = filepath.Join(root, "custom-work")
			}
			payload := func(version string) string {
				t.Helper()
				path := writeUnixPayload(t, root, "payload-"+version, "#!/bin/sh\nexit 0\n")
				bundleDir := filepath.Join(path, "share", "agentdock", "core-skills")
				packageDir := filepath.Join(bundleDir, "home-regression")
				if err := os.MkdirAll(packageDir, 0o700); err != nil {
					t.Fatal(err)
				}
				document := "---\nname: home-regression\ndescription: Verify installer state selection.\nversion: " + version + "\n---\n\n# Installer regression\n"
				if err := os.WriteFile(filepath.Join(packageDir, "SKILL.md"), []byte(document), 0o644); err != nil {
					t.Fatal(err)
				}
				digest, err := skills.DigestDirectory(packageDir)
				if err != nil {
					t.Fatal(err)
				}
				manifest, err := json.Marshal(skillbundle.Manifest{Skills: []skillbundle.ManifestSkill{{
					Name: "home-regression", Version: version, Path: "home-regression", Digest: digest,
				}}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(bundleDir, skillbundle.ManifestFile), manifest, 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			}

			request := unixInstallRequest(filepath.Join(root, "install"), filepath.Join(root, "runtime"), payload("1.0.0"), "v1.0.0")
			request.DataDir = dataDir
			request.ServiceName = "agentdock-home-regression"
			request.SkipSkills = false
			if tc.custom {
				request.AgentDockHome = home
				request.AgentDockDefaultDir = work
			}
			var authToken string
			for _, version := range []string{"1.0.0", "1.0.1"} {
				if version == "1.0.1" {
					if tc.interrupted {
						trial := request
						trial.Version = "v2.0.0"
						trial.PayloadDir = payload("2.0.0")
						trial.AgentDockHome = filepath.Join(root, "uncommitted-state")
						trial.AgentDockDefaultDir = filepath.Join(root, "uncommitted-work")
						trial.DeferCommit = true
						result, err := (Engine{}).Run(t.Context(), trial)
						if err != nil || result.State != updateengine.StateTrial {
							t.Fatalf("interrupted install: state=%s err=%v", result.State, err)
						}
					}
					request.Version = "v" + version
					request.PayloadDir = payload(version)
					request.AgentDockHome = ""
					request.AgentDockDefaultDir = ""
				}
				result, err := (Engine{}).Run(t.Context(), request)
				if err != nil {
					t.Fatalf("install %s: %v", version, err)
				}
				if result.State != updateengine.StateCommitted {
					t.Fatalf("install %s state = %s", version, result.State)
				}
				if err := RequireInspection(request.RuntimeRoot, "v"+version); err != nil {
					t.Fatal(err)
				}
				values, err := envstore.ParseFile(filepath.Join(request.RuntimeRoot, "agentdock.env"))
				if err != nil {
					t.Fatal(err)
				}
				if values["AGENTDOCK_HOME"] != home || values["AGENTDOCK_DEFAULT_DIR"] != work {
					t.Fatalf("install %s changed service data directories: home=%q work=%q", version, values["AGENTDOCK_HOME"], values["AGENTDOCK_DEFAULT_DIR"])
				}
				if version == "1.0.0" {
					authToken = values["AGENTDOCK_AUTH_TOKEN"]
					if authToken == "" {
						t.Fatal("initial install did not generate an auth token")
					}
					if err := os.WriteFile(filepath.Join(home, "existing-state"), []byte("keep"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if values["AGENTDOCK_AUTH_TOKEN"] != authToken {
					t.Fatal("upgrade rotated the existing auth token")
				}
				cfg, err := config.StorageConfig(home)
				if err != nil {
					t.Fatal(err)
				}
				stateDir, err := config.SkillStateDir(cfg)
				if err != nil {
					t.Fatal(err)
				}
				state, err := skillstate.New(stateDir)
				if err != nil {
					t.Fatal(err)
				}
				if installed, err := state.IsInstalled("home-regression", version); err != nil || !installed {
					t.Fatalf("install %s did not bootstrap Skills into the service Home: installed=%v err=%v", version, installed, err)
				}
			}
			if content, err := os.ReadFile(filepath.Join(home, "existing-state")); err != nil || string(content) != "keep" {
				t.Fatalf("upgrade changed pre-existing state: %q, %v", content, err)
			}
			if tc.custom {
				if _, err := os.Stat(filepath.Join(dataDir, ".agentdock")); !os.IsNotExist(err) {
					t.Fatalf("upgrade populated a different Home under DataDir: %v", err)
				}
			}
		})
	}
}
