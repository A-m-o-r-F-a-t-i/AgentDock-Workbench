package file

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/workspace"
)

func TestV4APatchPreservesBytesAndPreflightsEveryTarget(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	service := New(ws, nil, nil)
	original := []byte("\xef\xbb\xbfstart\r\nold\r\nuntouched\n")
	path := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	request := ApplyPatchRequest{Binding: activity.Binding{WorkspaceID: "wsp_fixture"}, Workdir: root,
		Patch: "*** Begin Patch\n*** Update File: sample.txt\n@@\n-old\n+new\n*** Add File: sample.txt\n+must fail\n*** End Patch"}
	result, err := service.ApplyPatch(context.Background(), request)
	if err == nil || result["effect_state"] != "none" {
		t.Fatalf("preflight result=%#v error=%v", result, err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, original) {
		t.Fatalf("failed preflight mutated original: %q %v", actual, err)
	}
	request.Patch = "*** Begin Patch\r\n*** Update File: sample.txt\r\n@@\r\n-old\r\n+new\r\n*** Add File: added.txt\r\n+created\r\n*** End Patch\r\n"
	result, err = service.ApplyPatch(context.Background(), request)
	if err != nil || result["effect_state"] != "complete" {
		t.Fatalf("apply result=%#v error=%v", result, err)
	}
	actual, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, []byte("\xef\xbb\xbfstart\r\nnew\r\nuntouched\n")) {
		t.Fatalf("BOM or untouched endings changed: %q %v", actual, err)
	}
	if diff, _ := result["diff"].(string); !strings.Contains(diff, "-old") || !strings.Contains(diff, "+new") {
		t.Fatalf("missing actual diff: %#v", result)
	}
	request.Patch = "*** Begin Patch\n*** Update File: added.txt\n*** Move to: moved.txt\n@@\n-created\n+moved\n*** Delete File: sample.txt\n*** End Patch"
	result, err = service.ApplyPatch(context.Background(), request)
	if err != nil || result["effect_state"] != "complete" {
		t.Fatalf("move/delete result=%#v error=%v", result, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("delete not applied: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "added.txt")); !os.IsNotExist(err) {
		t.Fatalf("move retained original target: %v", err)
	}
	actual, err = os.ReadFile(filepath.Join(root, "moved.txt"))
	if err != nil || strings.TrimSpace(string(actual)) != "moved" {
		t.Fatalf("move content=%q error=%v", actual, err)
	}
}

func TestV4APatchApprovalRevisionRejectsChangedFiles(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	service := New(ws, nil, nil)
	path := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := ApplyPatchRequest{Binding: activity.Binding{WorkspaceID: "wsp_fixture"}, Workdir: root,
		Patch: "*** Begin Patch\n*** Update File: sample.txt\n@@\n-old\n+new\n*** End Patch"}
	preview, err := service.PreviewApplyPatch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedRevision, _ = preview["source_revision"].(string)
	if request.ExpectedRevision == "" {
		t.Fatal("permission preview missing revision")
	}
	changed := []byte("old\nexternally added\n")
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := service.ApplyPatch(context.Background(), request)
	if err == nil || result["effect_state"] != "none" {
		t.Fatalf("stale approval result=%#v error=%v", result, err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, changed) {
		t.Fatalf("stale approval overwrote external edits: %q %v", actual, err)
	}
}
