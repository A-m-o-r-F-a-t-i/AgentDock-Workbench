package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/permission"
)

func executionTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{AgentDockDefaultDir: root, AgentDockHome: filepath.Join(root, ".agentdock")}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	r, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}
func TestExecutionConversationAllReadToolsWithoutTask(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := activity.WithSource(context.Background(), activity.Source{Principal: "test", Namespace: "chatgpt", HostConversationID: "chat-a"})
	if err := os.WriteFile(filepath.Join(r.cfg.AgentDockDefaultDir, "sample.txt"), []byte("example needle\n"), 0600); err != nil {
		t.Fatal(err)
	}
	boot, err := r.Call(ctx, "agentdock_context", nil)
	if err != nil {
		t.Fatal(err)
	}
	id := stringArg(boot, "conversation_id")
	if id == "" {
		t.Fatal("context did not return conversation")
	}
	for _, call := range []struct {
		name string
		args map[string]any
	}{{"read_file", map[string]any{"path": "sample.txt"}}, {"list_dir", map[string]any{"path": "."}}, {"search_text", map[string]any{"path": ".", "query": "needle"}}} {
		result, err := r.Call(ctx, call.name, call.args)
		if err != nil {
			t.Fatalf("%s: %v", call.name, err)
		}
		if result["conversation_id"] != id || stringArg(result, "call_id") == "" {
			t.Fatalf("lost binding %+v", result)
		}
	}
	page, err := r.activity.Calls(ctx, activity.CallQuery{ConversationID: id})
	if err != nil || len(page.Calls) != 4 {
		t.Fatalf("read calls: %d %v", len(page.Calls), err)
	}
	for _, c := range page.Calls {
		if c.TaskID != "" || c.Status != "succeeded" || c.OutputPreview != "" {
			t.Fatalf("invalid read projection %+v", c)
		}
	}
}
func TestExecutionApprovalNoWriteUntilFixedRequestApproved(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := activity.WithSource(context.Background(), activity.Source{Principal: "test", Provider: "openai", Namespace: "mcp", HostConversationID: "approval-fixture"})
	boot, err := r.Call(ctx, "agentdock_context", nil)
	if err != nil {
		t.Fatal(err)
	}
	id := stringArg(boot, "conversation_id")
	if id == "" {
		t.Fatal("missing trusted conversation binding")
	}
	args := patchAddRequest("fixed.txt", "original")
	result, err := r.Call(ctx, "apply_patch", args)
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "pending_approval" || result["executed"] != false {
		t.Fatalf("not pending: %+v", result)
	}
	target := filepath.Join(r.cfg.AgentDockDefaultDir, "fixed.txt")
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("write happened before approval")
	}
	args["patch"] = patchAddRequest("other.txt", "modified after approval snapshot")["patch"]
	approvalID := stringArg(result, "approval_id")
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.RuntimeApprovalDecision(ctx, approvalID, "approve", false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	waitExecutionTerminal(t, r, stringArg(result, "call_id"))
	data, err := os.ReadFile(target)
	if err != nil || normalizePatchText(data) != "original\n" {
		t.Fatalf("request was not immutable %q %v", data, err)
	}
	saved, err := r.permissions.Approval(ctx, approvalID)
	if err != nil || saved.DispatchCount != 1 {
		t.Fatalf("duplicate dispatch %+v %v", saved, err)
	}
}
func waitExecutionTerminal(t *testing.T, r *Runtime, id string) activity.ExecutionCall {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		call, err := r.activity.Call(context.Background(), id)
		if err == nil && activity.CallTerminal(call.Status) {
			return call
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("call did not finish")
	return activity.ExecutionCall{}
}
func TestExecutionInvalidParamsAndPolicyRejectionAreRecorded(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := context.Background()
	if _, err := r.Call(ctx, "read_file", map[string]any{"invalid": true}); err == nil {
		t.Fatal("invalid params accepted")
	}
	if _, err := r.permissions.Update(ctx, permission.Change{Scope: "global", Mode: permission.ReadOnly, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Call(ctx, "apply_patch", patchAddRequest("denied.txt", "no")); err == nil {
		t.Fatal("read-only allowed a write")
	}
	page, err := r.activity.Calls(ctx, activity.CallQuery{Unattributed: true})
	if err != nil || len(page.Calls) != 2 {
		t.Fatal("rejected calls are invisible")
	}
	for _, c := range page.Calls {
		if c.Status != "failed" {
			t.Fatalf("rejected call status %s", c.Status)
		}
	}
}
func TestExecutionBatchProtectsPendingTaskAndPreservesWorkspace(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := scopeHost("pending-task-batch")
	create := func(title string) string {
		result, err := r.Call(ctx, "task_manage", map[string]any{"action": "create", "title": title, "goal": "test", "completion_conditions": []any{"verified"}})
		if err != nil {
			t.Fatal(err)
		}
		return stringArg(result, "task_id")
	}
	pendingID := create("pending")
	idleID := create("idle")
	if _, err := r.Call(ctx, "task_manage", map[string]any{"action": "resume", "task_id": pendingID}); err != nil {
		t.Fatal(err)
	}
	result, err := r.Call(ctx, "apply_patch", patchAddRequest("pending.txt", "x"))
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "pending_approval" {
		t.Fatal("missing approval")
	}
	page, err := r.RuntimeManagementBatch(ctx, "task", BatchRequest{IDs: []string{pendingID, idleID}, Action: "trash"})
	if err != nil || page.Skipped != 1 || page.Succeeded != 1 {
		t.Fatalf("bad mixed batch %+v %v", page, err)
	}
	sentinel := filepath.Join(r.cfg.AgentDockDefaultDir, "source.c")
	if err = os.WriteFile(sentinel, []byte("source remains"), 0600); err != nil {
		t.Fatal(err)
	}
	removed, err := r.RuntimeManagementBatch(ctx, "task", BatchRequest{IDs: []string{idleID}, Action: "delete", ConfirmPermanent: true})
	if err != nil || removed.Succeeded != 1 {
		t.Fatalf("delete %+v %v", removed, err)
	}
	if _, err = os.Stat(sentinel); err != nil {
		t.Fatal("deleting metadata affected source")
	}
}

// Fixtures exercise the public patch-only request. Execution metadata is selected
// through task/workspace management, just as it is for a real model caller.
func patchAddRequest(path, content string) map[string]any {
	body := strings.ReplaceAll(strings.TrimSuffix(content, "\n"), "\n", "\n+")
	return map[string]any{"patch": "*** Begin Patch\n*** Add File: " + filepath.ToSlash(path) + "\n+" + body + "\n*** End Patch\n"}
}

func normalizePatchText(content []byte) string {
	return strings.ReplaceAll(string(content), "\r\n", "\n")
}
