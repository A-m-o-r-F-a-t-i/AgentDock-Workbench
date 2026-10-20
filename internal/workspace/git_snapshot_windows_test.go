//go:build windows

package workspace

import (
	"context"
	"testing"

	"golang.org/x/sys/windows"
)

func TestGitSnapshotCommandUsesHiddenConsoleTree(t *testing.T) {
	command := newGitSnapshotCommand(context.Background(), t.TempDir(), "status", "--short")

	if command.SysProcAttr == nil {
		t.Fatal("Git snapshot command did not initialize Windows process attributes")
	}
	if !command.SysProcAttr.HideWindow {
		t.Fatal("Git snapshot command did not hide its console window")
	}
	if command.SysProcAttr.CreationFlags&windows.CREATE_NEW_CONSOLE == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_NEW_CONSOLE", command.SysProcAttr.CreationFlags)
	}
	if command.SysProcAttr.CreationFlags&(windows.CREATE_NO_WINDOW|windows.DETACHED_PROCESS) != 0 {
		t.Fatalf("creation flags = %#x, hidden console tree must not detach", command.SysProcAttr.CreationFlags)
	}

	snapshot := ReadGitSnapshot(context.Background(), ".")
	if !snapshot.Available || snapshot.RepositoryRoot == "" {
		t.Fatalf("Git snapshot unavailable: %#v", snapshot)
	}
}
