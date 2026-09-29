//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func directoryDACL(t *testing.T, path string) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil || sd == nil {
		t.Fatalf("read DACL %s: %v", path, err)
	}
	return sd
}

func TestNormalizePreservesExistingWindowsWorkspacePermissions(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "shared-project")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	// A shared disposable project must retain its owner-selected read grant.
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:P(A;OICI;FA;;;%s)(A;OICI;FA;;;SY)(A;OICI;FR;;;WD)", user.User.Sid.String()))
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(workspace, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(workspace, "project.txt")
	if err := os.WriteFile(child, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	beforeRoot, beforeChild := directoryDACL(t, workspace).String(), directoryDACL(t, child).String()
	cfg := Config{AgentDockHome: filepath.Join(root, "state"), AgentDockDefaultDir: workspace}
	for i := 0; i < 2; i++ {
		if err := cfg.Normalize(); err != nil {
			t.Fatal(err)
		}
	}
	if got := directoryDACL(t, workspace).String(); got != beforeRoot {
		t.Fatalf("existing workspace DACL changed: before=%s after=%s", beforeRoot, got)
	}
	if got := directoryDACL(t, child).String(); got != beforeChild {
		t.Fatalf("existing workspace child DACL changed: before=%s after=%s", beforeChild, got)
	}
	control, _, err := directoryDACL(t, cfg.AgentDockHome).Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("private state boundary not protected: control=%#x err=%v", control, err)
	}
}

func TestNormalizeCreatesPrivateWindowsDirectories(t *testing.T) {
	root := t.TempDir()
	cfg := Config{AgentDockHome: filepath.Join(root, "state"), AgentDockDefaultDir: filepath.Join(root, "new", "workspace")}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{cfg.AgentDockHome, cfg.AgentDockDefaultDir} {
		control, _, err := directoryDACL(t, path).Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			t.Fatalf("new directory %s not protected: control=%#x err=%v", path, control, err)
		}
	}
}
