//go:build darwin || linux

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizePreservesExistingUnixWorkspacePermissions(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "shared-project")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(workspace, 0770); err != nil {
		t.Fatal(err)
	}
	cfg := Config{AgentDockHome: filepath.Join(root, "state"), AgentDockDefaultDir: workspace}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{workspace: 0770, cfg.AgentDockHome: 0700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("directory permissions changed or unsecured: %s mode=%v err=%v", path, mode, err)
		}
	}
	cfg.AgentDockDefaultDir = filepath.Join(root, "new-workspace")
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cfg.AgentDockDefaultDir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("new workspace is not private: %v", err)
	}
}
