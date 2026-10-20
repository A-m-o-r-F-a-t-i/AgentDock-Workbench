//go:build windows

package plugin

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPreparePluginGitCommandUsesHiddenConsoleTree(t *testing.T) {
	command := exec.Command("git")

	preparePluginGitCommand(command)

	if command.SysProcAttr == nil {
		t.Fatal("Plugin Git command did not initialize Windows process attributes")
	}
	if !command.SysProcAttr.HideWindow {
		t.Fatal("Plugin Git command did not hide its console window")
	}
	if command.SysProcAttr.CreationFlags&windows.CREATE_NEW_CONSOLE == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_NEW_CONSOLE", command.SysProcAttr.CreationFlags)
	}
	if command.SysProcAttr.CreationFlags&(windows.CREATE_NO_WINDOW|windows.DETACHED_PROCESS) != 0 {
		t.Fatalf("creation flags = %#x, hidden console tree must not detach", command.SysProcAttr.CreationFlags)
	}
}
