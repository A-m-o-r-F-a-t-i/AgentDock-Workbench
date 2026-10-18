//go:build windows

package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/uvwt/agentdock/internal/config"
)

const powershellUTF8Prefix = "try { [Console]::InputEncoding=[System.Text.UTF8Encoding]::new($false); [Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); $OutputEncoding=[System.Text.UTF8Encoding]::new($false) } catch {}\n"

type executableLookup func(string) (string, error)

func shellCommand(ctx context.Context, shell, command string) *exec.Cmd {
	normalized, err := config.NormalizeWindowsCommandShell(shell)
	if err != nil {
		normalized = config.WindowsCommandShellPwsh
	}
	return windowsShellCommand(ctx, normalized, command, exec.LookPath, resolveGitBashExecutable)
}

func windowsShellCommand(ctx context.Context, shell, command string, lookPath executableLookup, gitBashPath func() string) *exec.Cmd {
	switch shell {
	case config.WindowsCommandShellCmd:
		return commandPromptCommand(ctx, command, lookPath)
	case config.WindowsCommandShellGitBash:
		return exec.CommandContext(ctx, gitBashPath(), "--noprofile", "--norc", "-c", command)
	default:
		for _, name := range []string{"pwsh.exe", "powershell.exe"} {
			if path, err := lookPath(name); err == nil {
				script := powershellUTF8Prefix + command
				if name == "pwsh.exe" {
					script = powershellUTF8Prefix + "$ErrorActionPreference = 'Stop'\n$PSNativeCommandUseErrorActionPreference = $true\ntry {\n& {\n" + command + "\n}\n} catch {\n[Console]::Error.WriteLine($_.ToString())\nif ($_.Exception.PSObject.Properties['ExitCode']) { exit $_.Exception.ExitCode }\nexit 1\n}\n"
				}
				return exec.CommandContext(ctx, path, "-NoLogo", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
			}
		}
		return commandPromptCommand(ctx, command, lookPath)
	}
}

func commandPromptCommand(ctx context.Context, command string, lookPath executableLookup) *exec.Cmd {
	path := "cmd.exe"
	if resolved, err := lookPath(path); err == nil {
		path = resolved
	}
	return exec.CommandContext(ctx, path, "/D", "/S", "/C", command)
}

func resolveGitBashExecutable() string {
	gitPath, _ := exec.LookPath("git.exe")
	candidates := gitBashCandidates(gitPath, os.Getenv)
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if len(candidates) > 0 {
		// Preserve a deterministic executable path so Start reports that Git for
		// Windows is unavailable instead of accidentally invoking WSL bash.exe.
		return candidates[0]
	}
	return `C:\Program Files\Git\bin\bash.exe`
}

func gitBashCandidates(gitPath string, getenv func(string) string) []string {
	candidates := make([]string, 0, 12)
	seen := map[string]bool{}
	add := func(path string) {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" {
			return
		}
		key := strings.ToLower(path)
		if seen[key] {
			return
		}
		seen[key] = true
		candidates = append(candidates, path)
	}
	addRoot := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		add(filepath.Join(root, "bin", "bash.exe"))
		add(filepath.Join(root, "usr", "bin", "bash.exe"))
	}

	if strings.TrimSpace(gitPath) != "" {
		gitDir := filepath.Dir(gitPath)
		add(filepath.Join(gitDir, "bash.exe"))
		addRoot(gitDir)
		addRoot(filepath.Dir(gitDir))
	}
	for _, variable := range []string{"ProgramW6432", "ProgramFiles", "ProgramFiles(x86)"} {
		if root := strings.TrimSpace(getenv(variable)); root != "" {
			addRoot(filepath.Join(root, "Git"))
		}
	}
	if root := strings.TrimSpace(getenv("LocalAppData")); root != "" {
		addRoot(filepath.Join(root, "Programs", "Git"))
	}
	return candidates
}
