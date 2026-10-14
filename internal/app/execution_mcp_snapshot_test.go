package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
)

func TestPreparedMCPSnapshotSummaryPersistsWithoutArgumentsOrSchemas(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := scopeHost("prepared-mcp-summary")
	fixture := newExecutionMCPFixture(t, "write", executionMCPWriteSchema(), map[string]any{"readOnlyHint": false, "destructiveHint": true})
	if _, err := r.RuntimeMCPManage(ctx, map[string]any{
		"action":      "add",
		"name":        "summary",
		"description": "Prepared-call summary fixture",
		"transport":   "streamable_http",
		"url":         fixture.server.URL,
		"enabled":     true,
	}); err != nil {
		t.Fatal(err)
	}

	const privateArgument = "private-approved-argument"
	result, err := r.Call(ctx, "mcp_tool_call", map[string]any{
		"name":      "summary:write",
		"arguments": map[string]any{"value": privateArgument},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stringArg(result, "status") != "pending_approval" || result["executed"] != false {
		t.Fatalf("expected pending approval, got %+v", result)
	}

	approvalRaw, err := json.Marshal(result["approval"])
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedMCPSummaryText(t, string(approvalRaw), privateArgument)

	page, err := r.activity.Query(ctx, activity.Query{CallID: stringArg(result, "call_id")})
	if err != nil {
		t.Fatal(err)
	}
	var parameterSummary string
	for _, event := range page.Events {
		if event.Kind == "call.pending" {
			parameterSummary = event.ParameterSummary
			break
		}
	}
	if parameterSummary == "" {
		t.Fatalf("pending activity did not retain prepared-call summary: %+v", page.Events)
	}
	assertPreparedMCPSummaryText(t, parameterSummary, privateArgument)
	var decoded map[string]any
	if err = json.Unmarshal([]byte(parameterSummary), &decoded); err != nil {
		t.Fatalf("prepared-call activity summary is not JSON: %v", err)
	}
	for _, key := range []string{"qualified_name", "server_fingerprint", "client_generation", "catalog_revision", "tool_schema_digest", "tool_annotations_digest", "prepared_at"} {
		if _, exists := decoded[key]; !exists {
			t.Fatalf("prepared-call activity summary is missing %q: %s", key, parameterSummary)
		}
	}
	if fixture.calls.Load() != 0 {
		t.Fatalf("pending approval dispatched remote MCP call %d times", fixture.calls.Load())
	}
	if _, err = r.RuntimeApprovalDecision(context.Background(), stringArg(result, "approval_id"), "reject", false); err != nil {
		t.Fatal(err)
	}
}

func assertPreparedMCPSummaryText(t *testing.T, text, privateArgument string) {
	t.Helper()
	for _, required := range []string{"qualified_name", "server_fingerprint", "client_generation", "catalog_revision", "tool_schema_digest", "tool_annotations_digest", "prepared_at"} {
		if !strings.Contains(text, required) {
			t.Fatalf("prepared-call summary is missing %q: %s", required, text)
		}
	}
	for _, forbidden := range []string{privateArgument, "properties", "additionalProperties", "readOnlyHint", "destructiveHint"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("prepared-call summary leaked %q: %s", forbidden, text)
		}
	}
}
