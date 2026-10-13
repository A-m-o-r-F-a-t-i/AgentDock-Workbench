package config

import (
	"fmt"
	"strings"
)

const (
	WindowsCommandShellPwsh    = "pwsh"
	WindowsCommandShellCmd     = "cmd"
	WindowsCommandShellGitBash = "git-bash"
)

// NormalizeWindowsCommandShell validates the Windows shell used for ordinary
// command sessions. An empty value preserves the historical PowerShell default.
func NormalizeWindowsCommandShell(value string) (string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(value)); normalized {
	case "", WindowsCommandShellPwsh:
		return WindowsCommandShellPwsh, nil
	case WindowsCommandShellCmd, WindowsCommandShellGitBash:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported Windows command shell %q; expected pwsh, cmd, or git-bash", value)
	}
}
