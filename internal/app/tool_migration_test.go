package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyPatchUpdate(t *testing.T) {
	rt, root := newCodeToolsRuntime(t)
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := rt.Call(context.Background(), "apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Update File: note.txt\n@@\n-alpha\n+beta\n*** End Patch\n"})
	if err != nil {
		t.Fatal(err)
	}
	if result["changed"] != true || result["files_changed"] != 1 || result["effect_state"] != "complete" {
		t.Fatalf("unexpected file_edit result: %#v", result)
	}
}

func TestApplyPatchPreflightConflictHasNoWrites(t *testing.T) {
	rt, root := newCodeToolsRuntime(t)
	path := filepath.Join(root, "patch.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0600); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Update File: patch.txt\n@@\n-alpha\n+beta\n*** Update File: absent.txt\n@@\n-missing\n+new\n*** End Patch\n"
	_, err := rt.Call(context.Background(), "apply_patch", map[string]any{"patch": patch})
	var failure *ToolError
	if !errors.As(err, &failure) || failure.Details["effect_state"] != "none" {
		t.Fatalf("preflight did not report zero effects: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "alpha\n" {
		t.Fatalf("preflight changed file: %q %v", content, err)
	}
}
func TestApplyPatchRejectsUnifiedDiffWithoutWrites(t *testing.T) {
	rt, root := newCodeToolsRuntime(t)
	path := filepath.Join(root, "unified.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0600); err != nil {
		t.Fatal(err)
	}
	patch := "diff --git a/unified.txt b/unified.txt\n--- a/unified.txt\n+++ b/unified.txt\n@@ -1 +1 @@\n-alpha\n+beta\n"
	if _, err := rt.Call(context.Background(), "apply_patch", map[string]any{"patch": patch}); err == nil {
		t.Fatal("non-V4A patch accepted")
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "alpha\n" {
		t.Fatalf("invalid patch changed file: %q %v", content, err)
	}
}

func TestApplyPatchAddMoveDelete(t *testing.T) {
	rt, root := newCodeToolsRuntime(t)
	result, err := rt.Call(context.Background(), "apply_patch", patchAddRequest("draft.txt", "hello\n"))
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, "apply_patch", result)
	if result["changed"] != true {
		t.Fatalf("expected add to change file: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "draft.txt")); err != nil {
		t.Fatalf("expected added file: %v", err)
	}
	result, err = rt.Call(context.Background(), "apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Update File: draft.txt\n*** Move to: final.txt\n@@\n hello\n*** End Patch\n"})
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, "apply_patch", result)
	if result["effect_state"] != "complete" {
		t.Fatalf("unexpected move result: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "final.txt")); err != nil {
		t.Fatalf("expected moved file: %v", err)
	}
	result, err = rt.Call(context.Background(), "apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Delete File: final.txt\n*** End Patch\n"})
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, "apply_patch", result)
	if result["changed"] != true {
		t.Fatalf("expected delete to report changed: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "final.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted, err=%v", err)
	}
}

func TestWorkflowVectorIndexUsesCanonicalMCPFields(t *testing.T) {
	rt, _ := newCodeToolsRuntime(t)
	result, err := rt.Call(context.Background(), "workflow_template_manage", map[string]any{"action": "vector_index"})
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, "workflow_template_manage", result)
	if result["action"] != "vector_index" || result["vector_index_available"] != false {
		t.Fatalf("vector_index result = %#v", result)
	}
	if _, legacy := result["available"]; legacy {
		t.Fatalf("workflow MCP result leaked REST-only available field: %#v", result)
	}
}

func TestLegacyModelEntrypointsRemovedFromRuntime(t *testing.T) {
	rt, _ := newCodeToolsRuntime(t)
	for _, name := range []string{"file_edit", "edit_file", "workspace_repos", "git_read", "git_write", "git_status", "git_diff", "git_log", "git_inspect", "git_remote", "git_clone", "git_commit", "check_github_repo_access", "browser_profile", "list_files"} {
		if _, err := rt.Call(context.Background(), name, map[string]any{}); err == nil {
			t.Fatalf("legacy tool should not be callable: %s", name)
		}
	}
}

func TestWorkflowTemplateMatchIsOnlyTemplateDiscoveryEntrypoint(t *testing.T) {
	rt, _ := newCodeToolsRuntime(t)
	result, err := rt.Call(context.Background(), "workflow_template_manage", map[string]any{"action": "match", "goal": "deploy AgentDock", "device": "DockMini"})
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, "workflow_template_manage", result)
	if result["action"] != "match" {
		t.Fatalf("unexpected workflow template match result: %#v", result)
	}
	if _, err := rt.Call(context.Background(), "task_manage", map[string]any{"action": "template_match", "goal": "deploy AgentDock"}); err == nil {
		t.Fatal("task_manage template_match should not be callable")
	}
}
