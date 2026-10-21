//go:build windows

package command

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/envstore"
)

func TestResolveWSLWorkdirAcceptsWindowsAndLinuxPaths(t *testing.T) {
	service, _ := newCommandTestService(t)

	defaultPath, err := service.resolveWSLWorkdir("", "")
	if err != nil {
		t.Fatalf("resolve bound workspace: %v", err)
	}
	resolvedWorkspace, err := service.ws.ResolveExisting(".")
	if err != nil {
		t.Fatalf("resolve canonical bound workspace: %v", err)
	}
	wantDefault, ok := windowsPathToWSL(resolvedWorkspace.Abs)
	if !ok {
		t.Fatalf("test workspace could not be mapped into WSL: %s", resolvedWorkspace.Abs)
	}
	if defaultPath != wantDefault {
		t.Fatalf("default WSL workdir = %q, want bound workspace %q", defaultPath, wantDefault)
	}

	windowsPath, err := service.resolveWSLWorkdir(`D:\Project\synapse`, "")
	if err != nil {
		t.Fatalf("resolve Windows path: %v", err)
	}
	if windowsPath != "/mnt/d/Project/synapse" {
		t.Fatalf("Windows path resolved to %q", windowsPath)
	}

	extendedPath, err := service.resolveWSLWorkdir(`\\?\E:\Work`, "")
	if err != nil {
		t.Fatalf("resolve extended Windows path: %v", err)
	}
	if extendedPath != "/mnt/e/Work" {
		t.Fatalf("extended Windows path resolved to %q", extendedPath)
	}

	linuxPath, err := service.resolveWSLWorkdir("/home/a/project", "")
	if err != nil {
		t.Fatalf("resolve Linux path: %v", err)
	}
	if linuxPath != "/home/a/project" {
		t.Fatalf("Linux path resolved to %q", linuxPath)
	}

	if _, err := service.resolveWSLWorkdir(`\\server\share`, ""); err == nil {
		t.Fatal("expected UNC path to be rejected")
	}
}

func TestPrepareCommandInvocationRejectsWSLDistributionForWindowsRuntime(t *testing.T) {
	service, _ := newCommandTestService(t)
	_, err := service.prepareCommandInvocationArgs(map[string]any{
		"runtime":          "windows",
		"wsl_distribution": "Ubuntu",
	}, "Write-Output ok")
	if err == nil {
		t.Fatal("expected wsl_distribution validation error")
	}
}

func TestPrepareCommandInvocationUsesConfiguredWindowsShell(t *testing.T) {
	service, cfg := newCommandTestService(t)
	cfg.WindowsCommandShell = "git-bash"
	invocation, err := service.prepareCommandInvocationArgs(map[string]any{"runtime": "windows"}, "echo ok")
	if err != nil {
		t.Fatal(err)
	}
	if invocation.shell != "git-bash" {
		t.Fatalf("invocation shell = %q, want git-bash", invocation.shell)
	}
}

func TestPrepareWSLSkillInvocationUsesPrivateDataDirectory(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "wsl.exe"), []byte("not executed"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("WSLENV", "")
	for _, name := range []string{"plugin", "managed", "environment_only"} {
		t.Run(name, func(t *testing.T) {
			plugin, environmentOnly := name == "plugin", name == "environment_only"
			svc, cfg := newCommandTestService(t)
			const skillName = "demo-skill"
			if err := svc.envs.Set(envstore.Scope{Kind: envstore.ScopeSkill, Name: skillName}, "MANAGED_SECRET", "managed-only"); err != nil {
				t.Fatal(err)
			}
			key, otherKey := config.SkillDataDirEnvKey, config.PluginDataDirEnvKey
			dataDir, err := config.SkillDataDir(*cfg, skillName)
			lease := SkillLease{Name: skillName, Root: t.TempDir(), EnvName: skillName, RuntimeEnv: map[string]string{"runtime_value": "preserved"}}
			if plugin {
				key, otherKey = otherKey, key
				lease.PluginName, lease.EnvName = "demo.plugin", ""
				dataDir, err = config.PluginDataDir(*cfg, lease.PluginName)
				lease.RuntimeEnv["plugin_data_dir"] = cfg.AgentDockDefaultDir
			}
			if err != nil {
				t.Fatal(err)
			}
			acquired, released := 0, 0
			lease.Release = func() { released++ }
			svc.resolveSkill = func(context.Context, string) (SkillLease, error) {
				acquired++
				return lease, nil
			}
			request := ExecRequest{
				Cmd: "pwd", SkillRef: skillName, RuntimeOptions: RuntimeOptions{Runtime: "wsl", WSLDistribution: "Ubuntu"},
				Env: map[string]string{"WSLENV": config.SkillDataDirEnvKey + "/pw:" + config.PluginDataDirEnvKey + "/pw"},
			}
			if !plugin {
				request.SkillEnv = skillName
			}
			if environmentOnly {
				request.SkillRef = ""
			}
			invocation, err := svc.prepareCommandInvocation(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if invocation.skillRelease != nil {
				defer invocation.skillRelease()
			}
			if released != 0 {
				t.Fatal("Skill lease released before command ownership was transferred")
			}
			if environmentOnly && (acquired != 0 || invocation.skillRelease != nil) {
				t.Fatal("skill_env acquired an unrelated Skill content lease")
			}
			process := invocation.build(t.Context())
			values := commandEnvValues(process.Env)
			wslDataDir, ok := windowsPathToWSL(dataDir)
			if !ok || values[key] != wslDataDir || values[otherKey] != "" {
				t.Fatalf("private directory environment = %#v, want %s=%s", values, key, wslDataDir)
			}
			if !environmentOnly && values["runtime_value"] != "preserved" {
				t.Fatal("ordinary runtime environment changed")
			}
			if plugin && values["MANAGED_SECRET"] != "" || !plugin && values["MANAGED_SECRET"] != "managed-only" {
				t.Fatal("Plugin and managed Skill environments were mixed")
			}
			if !strings.Contains(":"+values["WSLENV"]+":", ":"+key+":") || strings.Contains(values["WSLENV"], key+"/") || strings.Contains(values["WSLENV"], otherKey) {
				t.Fatalf("private directory was not forwarded into WSL: %q", values["WSLENV"])
			}
			wslRoot, ok := windowsPathToWSL(lease.Root)
			if environmentOnly {
				wslRoot, err = svc.resolveWSLWorkdir("", "")
				ok = err == nil
			}
			wantArgs := []string{"--distribution", "Ubuntu", "--cd", wslRoot, "--exec", "bash", "-lc", "pwd"}
			if !ok || !reflect.DeepEqual(process.Args[1:], wantArgs) {
				t.Fatalf("command arguments = %#v, want %#v", process.Args[1:], wantArgs)
			}
			if info, err := os.Stat(dataDir); err != nil || !info.IsDir() {
				t.Fatalf("private Host data directory was not created: %v", err)
			}
			for _, reserved := range []string{key, strings.ToLower(key)} {
				request.Env = map[string]string{reserved: "/tmp/override"}
				_, err := svc.prepareCommandInvocation(t.Context(), request)
				var toolErr *ToolError
				if !errors.As(err, &toolErr) || toolErr.Code != "INVALID_ENV_NAME" {
					t.Fatalf("reserved environment %q accepted: %v", reserved, err)
				}
			}
			wantReleased := 0
			if !environmentOnly {
				wantReleased = 3
				request.Env = nil
				request.SkillEnv = skillName
				if !plugin {
					request.SkillEnv = "another-skill"
				}
				invocation, err := svc.prepareCommandInvocation(t.Context(), request)
				var toolErr *ToolError
				if !errors.As(err, &toolErr) || toolErr.Code != "SKILL_CONTEXT_CONFLICT" || invocation.build != nil {
					t.Fatalf("conflicting Skill environment was admitted: invocation=%#v err=%v", invocation, err)
				}
			}
			if released != wantReleased {
				t.Fatalf("rejected preparations released %d leases, want %d", released, wantReleased)
			}
		})
	}
}
