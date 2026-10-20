//go:build windows

package plugin

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPreparePluginGitCommandHidesConsoleWindow(t *testing.T) {
	command := exec.Command("git")

	preparePluginGitCommand(command)

	if command.SysProcAttr == nil {
		t.Fatal("Plugin Git command did not initialize Windows process attributes")
	}
	if !command.SysProcAttr.HideWindow {
		t.Fatal("Plugin Git command did not hide its console window")
	}
	if command.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_NO_WINDOW", command.SysProcAttr.CreationFlags)
	}
}
