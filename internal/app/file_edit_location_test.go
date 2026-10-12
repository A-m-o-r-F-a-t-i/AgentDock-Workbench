package app

import (
	"path/filepath"
	"testing"

	"github.com/uvwt/agentdock/internal/workspace"
)

func TestFileEditLocationKeepsRecordedRoot(t *testing.T) {
	rt := newRuntimeValidationTestRuntime(t)
	root := rt.cfg.AgentDockDefaultDir
	selected := &workspace.Record{Root: root, Runtime: workspace.HostRuntime()}
	details := rt.fileEditDetails(map[string]any{"action": "replace", "path": filepath.Join(root, "nested", "file.txt")}, Result{"changed": true}, executionObservation{executed: true, selected: selected}, nil)
	if details.PathBase != root || details.Path != "nested/file.txt" {
		t.Fatalf("recorded location = %#v", details)
	}
	selected.Root = filepath.Join(root, "other")
	if details.PathBase != root {
		t.Fatal("workspace mutation retargeted a historical file location")
	}
	legacy := rt.fileEditDetails(map[string]any{"action": "replace", "path": "file.txt"}, nil, executionObservation{}, nil)
	if legacy.PathBase != "" {
		t.Fatal("unresolved historical path acquired a fabricated base")
	}
	selected.Runtime = "wsl"
	wsl := rt.fileEditDetails(map[string]any{"action": "replace", "path": "/home/test/file.txt"}, nil, executionObservation{selected: selected}, nil)
	if wsl.PathBase != "" || wsl.Path != "/home/test/file.txt" {
		t.Fatal("WSL path was rebased into the Windows namespace")
	}
}
