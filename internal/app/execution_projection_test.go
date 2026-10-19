package app

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

func TestMCPResponseRecordsFullDiagnosticsSeparatelyFromModelEnvelope(t *testing.T) {
	fixture := newCatalogFixture(t, 1)
	cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := newUnrestrictedTestRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	ctx := WithModelProjection(t.Context())
	if _, err := runtime.Call(ctx, "mcp_manage", map[string]any{"action": "add", "name": "fixture", "description": "MCP response projection fixture", "transport": "streamable_http", "url": fixture.server.URL}); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Call(ctx, "mcp_tool_call", map[string]any{"name": "fixture:tool_000", "arguments": map[string]any{"text": "ok"}})
	if err != nil {
		t.Fatal(err)
	}
	callID := stringArg(result, "call_id")
	call, err := runtime.activity.Call(ctx, callID)
	if err != nil {
		t.Fatal(err)
	}
	result["model_projection"] = map[string]any{"chars": 100, "budget_chars": 12000, "mode": "full", "complete": true}
	logical := map[string]any{"isError": false, "structuredContent": result}
	compact := map[string]any{"isError": false, "structuredContent": map[string]any{"result": result["result"], "continuation": map[string]any{}, "error": nil}}
	before, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	pending := &ToolResponse{auditBinding: call.Binding, auditName: "mcp_tool_call", auditRedactor: runtime.executionRedactor(nil)}
	runtime.RecordToolResponse(pending, compact, logical)
	page, err := runtime.activity.ReadCallPayload(ctx, callID, "response", 0, 64000)
	if err != nil {
		t.Fatal(err)
	}
	var recorded map[string]any
	if err := json.Unmarshal([]byte(page.Text), &recorded); err != nil {
		t.Fatal(err)
	}
	audit := modelObject(recorded["structuredContent"])
	if audit["mcp_catalog"] == nil || audit["managed_target"] == nil || audit["capture"] == nil || audit["call_id"] != callID || audit["model_projection"] == nil {
		t.Fatalf("Activity lost full host diagnostics: %#v", audit)
	}
	updated, err := runtime.activity.Call(ctx, callID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.OutputStages == nil || updated.ModelProjection == nil || updated.ModelProjection.Chars != 100 || updated.OutputSource == nil || fixture.calls.Load() != 1 {
		t.Fatal("Activity lost measurements/raw source or repeated the business call")
	}
	after, _ := json.Marshal(compact)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("recording diagnostic output modified the model result")
	}
}
