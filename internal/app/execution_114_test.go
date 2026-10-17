package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
)

func Test114PatchOperationsShareOneObservedRoot(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	ctx := activity.WithSource(context.Background(), activity.Source{
		Principal:          "file-edit-test",
		Provider:           "openai",
		Namespace:          "mcp",
		HostConversationID: "conversation-114-file-edit",
	})
	boot, err := runtime.Call(ctx, "agentdock_context", nil)
	if err != nil {
		t.Fatal(err)
	}
	conversationID := stringArg(boot, "conversation_id")
	if conversationID == "" {
		t.Fatal("missing conversation binding")
	}

	patch := "*** Begin Patch\n*** Update File: note.txt\n@@\n-beta\n+gamma\n*** Add File: extra.txt\n+extra\n*** End Patch\n"
	actions := []struct {
		name string
		args map[string]any
	}{
		{name: "add", args: patchAddRequest("note.txt", "alpha\n")},
		{name: "update", args: map[string]any{"patch": "*** Begin Patch\n*** Update File: note.txt\n@@\n-alpha\n+beta\n*** End Patch\n"}},
		{name: "patch", args: map[string]any{"patch": patch}},
		{name: "move", args: map[string]any{"patch": "*** Begin Patch\n*** Update File: note.txt\n*** Move to: moved.txt\n@@\n gamma\n*** End Patch\n"}},
		{name: "delete", args: map[string]any{"patch": "*** Begin Patch\n*** Delete File: moved.txt\n*** End Patch\n"}},
	}
	for _, test := range actions {
		t.Run(test.name, func(t *testing.T) {
			result, err := runtime.Call(ctx, "apply_patch", test.args)
			if err != nil {
				t.Fatal(err)
			}
			callID := stringArg(result, "call_id")
			if callID == "" {
				t.Fatalf("missing call id: %+v", result)
			}
			call, err := runtime.activity.Call(ctx, callID)
			if err != nil {
				t.Fatal(err)
			}
			if call.ConversationID != conversationID || call.ToolName != "apply_patch" || call.Status != "succeeded" || call.ParentCallID != "" {
				t.Fatalf("bad file-edit root: %+v", call)
			}
			if !strings.Contains(call.DisplayTitle, "apply_patch") || strings.Contains(call.DisplayTitle, "EDIT_FILE") || call.FileEdit == nil || call.FileEdit.Action != "patch" || call.FileEdit.EffectState != "complete" || call.FileEdit.AffectedCount == nil || *call.FileEdit.AffectedCount < 1 || !call.FileEdit.Executed {
				t.Fatalf("missing file-edit observation: %+v", call)
			}
			if call.RequestReceivedAt == nil || call.RPCCompletedAt == nil || call.RPCElapsedMS == nil || call.RPCStatus != "succeeded" || call.ExecutionElapsedMS == nil || call.WaitElapsedMS == nil {
				t.Fatalf("missing lifecycle measurements: %+v", call.CallMeasurements)
			}
		})
	}

	if _, err = os.Stat(filepath.Join(runtime.cfg.AgentDockDefaultDir, "moved.txt")); !os.IsNotExist(err) {
		t.Fatalf("delete did not remove moved file: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(runtime.cfg.AgentDockDefaultDir, "extra.txt")); err != nil || normalizePatchText(data) != "extra\n" {
		t.Fatalf("patch result = %q, %v", data, err)
	}
}

func Test114PatchApprovalPreviewAndPreflightFailureRemainVisible(t *testing.T) {
	runtime := executionTestRuntime(t)
	ctx := scopeHost("file-preview-114")
	if _, err := runtime.Call(ctx, "agentdock_context", nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runtime.cfg.AgentDockDefaultDir, "preview.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := runtime.Call(ctx, "apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Update File: preview.txt\n@@\n-before\n+after\n*** End Patch\n"})
	if err != nil || preview["status"] != "pending_approval" {
		t.Fatalf("approval preview: %+v %v", preview, err)
	}
	previewCall, err := runtime.activity.Call(ctx, stringArg(preview, "call_id"))
	if err != nil {
		t.Fatal(err)
	}
	if previewCall.FileEdit == nil || previewCall.FileEdit.Executed || previewCall.FileEdit.Changed == nil || *previewCall.FileEdit.Changed {
		t.Fatalf("preview invented a write: %+v", previewCall.FileEdit)
	}
	approval, err := runtime.permissions.Approval(ctx, stringArg(preview, "approval_id"))
	if err != nil || !strings.Contains(approval.Operation, "-before") || !strings.Contains(approval.Operation, "+after") {
		t.Fatalf("proposed diff missing: %+v %v", approval, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "before\n" {
		t.Fatalf("approval preview changed file: %q %v", data, err)
	}
	if _, err := runtime.RuntimeApprovalDecision(ctx, approval.ID, "reject", false); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Add File: preflight-only.txt\n+not written\n*** Update File: missing.txt\n*** Move to: target.txt\n@@\n-old\n+new\n*** End Patch\n"
	if _, err := runtime.Call(ctx, "apply_patch", map[string]any{"patch": patch}); err == nil {
		t.Fatal("missing source patch succeeded")
	}
	page, err := runtime.activity.Calls(ctx, activity.CallQuery{ConversationID: previewCall.ConversationID, TopLevel: true, IncludeOutput: true, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	var failed *activity.ExecutionCall
	for index := range page.Calls {
		call := &page.Calls[index]
		if call.ToolName == "apply_patch" && call.Status == "failed" && call.FileEdit != nil {
			failed = call
			break
		}
	}
	if failed == nil || failed.FileEdit.Executed || failed.FileEdit.Changed == nil || *failed.FileEdit.Changed || failed.FileEdit.EffectState != "none" || failed.RPCStatus != "failed" {
		t.Fatalf("preflight facts: %+v", failed)
	}
	for _, name := range []string{"preflight-only.txt", "target.txt"} {
		if _, err := os.Stat(filepath.Join(runtime.cfg.AgentDockDefaultDir, name)); !os.IsNotExist(err) {
			t.Fatalf("preflight created %s", name)
		}
	}
	if _, err := runtime.Call(ctx, "apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Add File: \n+invalid\n*** End Patch\n"}); err == nil {
		t.Fatal("missing path accepted")
	}
	page, err = runtime.activity.Calls(ctx, activity.CallQuery{ConversationID: previewCall.ConversationID, TopLevel: true, IncludeOutput: true, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Calls) == 0 || page.Calls[0].FileEdit == nil || page.Calls[0].FileEdit.Executed || page.Calls[0].FileEdit.Changed == nil || *page.Calls[0].FileEdit.Changed || page.Calls[0].Status != "failed" {
		t.Fatal("validation rejection disappeared or invented a write")
	}
}

func Test114LocalManagementDoesNotRefreshConversationActivity(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	ctx := activity.WithSource(context.Background(), activity.Source{
		Principal:          "activity-test",
		Provider:           "openai",
		Namespace:          "mcp",
		HostConversationID: "conversation-114-activity",
	})
	boot, err := runtime.Call(ctx, "agentdock_context", nil)
	if err != nil {
		t.Fatal(err)
	}
	conversationID := stringArg(boot, "conversation_id")
	_, before, err := runtime.activity.CallStatistics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	last := before[conversationID].LastToolCallAt
	if last == nil {
		t.Fatal("real tool request did not set activity time")
	}
	if _, err = runtime.Call(activity.WithLocalManagement(ctx), "read_file", map[string]any{"path": "missing.txt"}); err == nil {
		t.Fatal("local read fixture unexpectedly succeeded")
	}
	_, after, err := runtime.activity.CallStatistics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := after[conversationID].LastToolCallAt; got == nil || !got.Equal(*last) {
		t.Fatalf("local UI action refreshed tool activity: before=%v after=%v", last, got)
	}
}
