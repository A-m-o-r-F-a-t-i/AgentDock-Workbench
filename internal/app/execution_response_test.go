package app

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/uvwt/agentdock/internal/permission"
)

func TestCompactSuccessPreservesBusinessAndTrustedGuidance(t *testing.T) {
	insertion := []any{map[string]any{"type": "activity_center_user", "text": "authenticated update"}}
	next := []map[string]any{{"action": "observe", "tool": "session_observe", "arguments": map[string]any{"session_id": "session_fixture"}}}
	result := Result{
		"permission": permission.Decision{Effect: permission.Allow}, "status": "running", "stdout": "actual output",
		"call_id": "call_fixture", "task_id": "tsk_fixture", "workspace_id": "wsp_fixture", "activity_warning": "retained warning",
		"workspace_target": map[string]any{"resolved_path": "/workspace", "rules_revision": 1},
		"output_policy":    map[string]any{"truncated": true, "continue_read": map[string]any{"path": "activity://fixture"}},
		"result":           map[string]any{"agentdock_guidance": "ordinary third-party data"},
		"agentdock_guidance": map[string]any{
			"source": "agentdock", "schema_version": 1, "call_id": "call_fixture", "task_id": "tsk_fixture", "workspace_id": "wsp_fixture",
			"target_kind": "source", "resolved_path": "/workspace", "rules_revision": 1,
			"next_required": next, "response_additions": insertion, "future_guidance": "retain",
			"constraints": []map[string]string{{"code": "SERVER_BINDING_SNAPSHOT", "text": "shared static instructions"}, {"code": "NEW_CONSTRAINT", "text": "retain"}},
		},
	}
	before, _ := json.Marshal(result)
	p := &preparedExecution{spec: ToolSpec{Name: "exec_command"}, decision: permission.Decision{Effect: permission.Allow}}
	got := compactExecutionSuccess(result, p)
	after, _ := json.Marshal(result)
	if string(before) != string(after) {
		t.Fatal("projection mutated original business result or guidance")
	}
	if got["permission"] != nil {
		t.Fatal("ordinary success repeats full permission decision")
	}
	for _, key := range []string{"status", "stdout", "call_id", "task_id", "workspace_id", "activity_warning", "workspace_target", "output_policy", "result"} {
		if !reflect.DeepEqual(got[key], result[key]) {
			t.Fatalf("lost business/control field %s", key)
		}
	}
	guidance := got["agentdock_guidance"].(map[string]any)
	if guidance["call_id"] != nil || guidance["task_id"] != nil || guidance["resolved_path"] != nil {
		t.Fatal("redundant metadata retained")
	}
	if guidance["source"] != "agentdock" || guidance["workspace_id"] != "wsp_fixture" || guidance["future_guidance"] != "retain" {
		t.Fatal("routing or unknown guidance lost")
	}
	if !reflect.DeepEqual(guidance["next_required"], next) || !reflect.DeepEqual(guidance["response_additions"], insertion) {
		t.Fatal("dynamic guidance or trusted insertion lost")
	}
	constraints := guidance["constraints"].([]map[string]string)
	if len(constraints) != 1 || constraints[0]["code"] != "NEW_CONSTRAINT" {
		t.Fatal("dynamic constraint lost")
	}
}

func TestCompactSuccessKeepsDiagnosticAndApprovalResponses(t *testing.T) {
	for _, tc := range []struct {
		name, tool, status, approval string
		failed, unexecuted           bool
	}{
		{name: "bootstrap", tool: "agentdock_context"}, {name: "workspace", tool: "workspace_context"},
		{name: "command failure", failed: true}, {name: "unexecuted", unexecuted: true},
		{name: "unknown", status: "unknown"}, {name: "partial", status: "partial"}, {name: "pending", status: "pending_approval"},
		{name: "approved call", approval: "approval_fixture"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := Result{"permission": permission.Decision{Effect: permission.Allow}, "status": tc.status}
			if tc.failed {
				result["command_ok"] = false
			}
			if tc.unexecuted {
				result["executed"] = false
			}
			p := &preparedExecution{spec: ToolSpec{Name: tc.tool}, decision: permission.Decision{Effect: permission.Allow}, approvalID: tc.approval}
			if got := compactExecutionSuccess(result, p); !reflect.DeepEqual(got, result) {
				t.Fatal("diagnostic or approval response changed")
			}
		})
	}
}
