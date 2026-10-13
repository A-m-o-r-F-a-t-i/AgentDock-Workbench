package config

import (
	"strings"
	"testing"
)

func TestNormalizeWindowsCommandShell(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty keeps historical default", input: "", want: WindowsCommandShellPwsh},
		{name: "PowerShell canonical", input: " PWSH ", want: WindowsCommandShellPwsh},
		{name: "Command Prompt canonical", input: " CMD ", want: WindowsCommandShellCmd},
		{name: "Git Bash canonical", input: " Git-Bash ", want: WindowsCommandShellGitBash},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeWindowsCommandShell(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("NormalizeWindowsCommandShell(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestFromEnvReadsWindowsCommandShell(t *testing.T) {
	t.Setenv("AGENTDOCK_WINDOWS_COMMAND_SHELL", "git-bash")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WindowsCommandShell != WindowsCommandShellGitBash {
		t.Fatalf("WindowsCommandShell = %q, want %q", cfg.WindowsCommandShell, WindowsCommandShellGitBash)
	}
}

func TestFromEnvRejectsUnsupportedWindowsCommandShell(t *testing.T) {
	t.Setenv("AGENTDOCK_WINDOWS_COMMAND_SHELL", "powershell")
	_, err := FromEnv()
	if err == nil || !strings.Contains(err.Error(), "AGENTDOCK_WINDOWS_COMMAND_SHELL") {
		t.Fatalf("FromEnv() error = %v, want Windows shell validation error", err)
	}
}
