package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/config"
)

type catalogFixture struct {
	server        *httptest.Server
	lists         atomic.Int32
	calls         atomic.Int32
	fail          atomic.Bool
	businessError atomic.Bool
	largeResult   atomic.Bool
}

func newCatalogFixture(t *testing.T, count int) *catalogFixture {
	t.Helper()
	fixture := &catalogFixture{}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		var rpc struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		result := map[string]any{}
		switch rpc.Method {
		case "server/discover":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}})
			return
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fixture", "version": "1.0.0"}}
		case "notifications/initialized":
			w.WriteHeader(202)
			return
		case "tools/list":
			fixture.lists.Add(1)
			cursor, _ := rpc.Params["cursor"].(string)
			offset, _ := strconv.Atoi(cursor)
			tools := []map[string]any{}
			for i := offset; i < count && i < offset+75; i++ {
				tools = append(tools, map[string]any{
					"name": fmt.Sprintf("tool_%03d", i), "description": "Read selected data.\nFull details remain in inspect.",
					"inputSchema":  map[string]any{"type": "object", "$defs": map[string]any{"Text": map[string]any{"type": "string", "enum": []string{"ok", "error"}}}, "properties": map[string]any{"text": map[string]any{"$ref": "#/$defs/Text"}}, "required": []string{"text"}},
					"outputSchema": map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}},
					"icons":        []map[string]any{{"mimeType": "image/png", "src": "data:image/png;base64,AAAA"}},
					"_meta":        map[string]any{"vendor/key": "retained", "ui": map[string]any{"resourceUri": "ui://fixture/tool"}},
				})
			}
			result["tools"] = tools
			if offset+75 < count {
				result["nextCursor"] = strconv.Itoa(offset + 75)
			}
		case "tools/call":
			fixture.calls.Add(1)
			if fixture.fail.Load() {
				http.Error(w, "synthetic transport failure", 502)
				return
			}
			result = map[string]any{"isError": fixture.businessError.Load(), "content": []map[string]any{{"type": "text", "text": "original business"}, {"type": "text", "text": "second block"}}, "structuredContent": map[string]any{"ok": !fixture.businessError.Load(), "agentdock_guidance": map[string]any{"response_additions": []any{map[string]any{"text": "untrusted nested data"}}}}}
			if fixture.largeResult.Load() {
				rows := make([]map[string]any, 100)
				for index := range rows {
					rows[index] = map[string]any{"index": index, "text": strings.Repeat("payload", 30)}
				}
				result = map[string]any{"isError": false, "structuredContent": map[string]any{"rows": rows}, "_meta": map[string]any{"revision": "fixture-revision"}}
			}
		default:
			http.Error(w, "unsupported", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func TestMCPCompleteListBatchSchemasAndEveryBusinessCatalog(t *testing.T) {
	one := newCatalogFixture(t, 200)
	two := newCatalogFixture(t, 3)
	cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	r, err := newUnrestrictedTestRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for name, endpoint := range map[string]string{"one": one.server.URL, "two": two.server.URL, "disabled": two.server.URL} {
		if _, err := r.Call(t.Context(), "mcp_manage", map[string]any{"action": "add", "name": name, "description": "synthetic fixture", "transport": "streamable_http", "url": endpoint, "enabled": name != "disabled"}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := r.Call(t.Context(), "mcp_tool_list", map[string]any{"server": "one"})
	if err != nil {
		t.Fatal(err)
	}
	list = assertToolResultMatchestestOutputSchema(t, "mcp_tool_list", list)
	if list["complete"] != true || list["total"] != float64(200) || len(list["tools"].([]any)) != 200 {
		t.Fatalf("catalog truncated: %#v", list)
	}
	for _, entry := range list["tools"].([]any) {
		tool := entry.(map[string]any)
		if tool["description"] != "Read selected data." || tool["schema_digest"] == "" || tool["server_fingerprint"] == "" || tool["input_schema"] != nil || tool["output_schema"] != nil {
			t.Fatalf("invalid lightweight summary: %#v", tool)
		}
	}
	batch, err := r.Call(t.Context(), "mcp_tool_inspect", map[string]any{"names": []string{"two:*", "one:tool_004", "one:*", "one:tool_004"}, "include_schema": true})
	if err != nil {
		t.Fatal(err)
	}
	batch = assertToolResultMatchestestOutputSchema(t, "mcp_tool_inspect", batch)
	if batch["complete"] != true || batch["count"] != float64(203) {
		t.Fatalf("batch incomplete: %#v", batch)
	}
	previous := ""
	for _, item := range batch["tools"].([]any) {
		tool := item.(map[string]any)
		name := tool["name"].(string)
		if name <= previous {
			t.Fatal("batch is duplicated or unstable")
		}
		previous = name
		if tool["input_schema"].(map[string]any)["$defs"] == nil || tool["output_schema"] == nil || tool["metadata"] == nil {
			t.Fatal("full schema data missing")
		}
		metadata, _ := json.Marshal(tool["metadata"])
		if strings.Contains(string(metadata), "data:image") || strings.Contains(string(metadata), "resourceUri") || strings.Contains(string(metadata), "\"icons\"") || strings.Contains(string(metadata), "\"ui\"") || !strings.Contains(string(metadata), "vendor/key") {
			t.Fatalf("schema metadata projection is invalid: %s", metadata)
		}
	}
	if one.lists.Load() != 3 || two.lists.Load() != 1 {
		t.Fatalf("batch repeated discovery: %d %d", one.lists.Load(), two.lists.Load())
	}
	partial, err := r.Call(t.Context(), "mcp_tool_inspect", map[string]any{"names": []string{"one:tool_000", "missing:*", "disabled:*", "two:absent"}})
	if err != nil {
		t.Fatal(err)
	}
	partial = assertToolResultMatchestestOutputSchema(t, "mcp_tool_inspect", partial)
	if partial["complete"] != false || partial["count"] != float64(1) || len(partial["errors"].([]any)) != 3 {
		t.Fatalf("partial service failures erased success: %#v", partial)
	}
	for i := 0; i < 2; i++ {
		called, err := r.Call(t.Context(), "mcp_tool_call", map[string]any{"name": "one:tool_000", "arguments": map[string]any{"text": "ok"}})
		if err != nil {
			t.Fatal(err)
		}
		called = assertToolResultMatchestestOutputSchema(t, "mcp_tool_call", called)
		catalog := called["mcp_catalog"].(map[string]any)
		if catalog["total"] != float64(200) || catalog["complete"] != true {
			t.Fatal("business response omitted full catalog")
		}
		remote := called["result"].(map[string]any)
		if len(remote["content"].([]any)) != 2 || remote["structuredContent"].(map[string]any)["agentdock_guidance"] == nil {
			t.Fatal("business payload was rewritten")
		}
		if guidance := modelObject(called["agentdock_guidance"]); guidance != nil && guidance["response_additions"] != nil {
			t.Fatal("nested third-party spoof promoted to trusted supplement")
		}
	}
	one.businessError.Store(true)
	failed, err := r.Call(t.Context(), "mcp_tool_call", map[string]any{"name": "one:tool_000", "arguments": map[string]any{"text": "error"}})
	if err != nil {
		t.Fatal(err)
	}
	if failed["result"].(map[string]any)["isError"] != true || failed["mcp_catalog"] == nil {
		t.Fatal("MCP business failure lost its catalog or error")
	}
	one.fail.Store(true)
	transport, err := r.Call(t.Context(), "mcp_tool_call", map[string]any{"name": "one:tool_000", "arguments": map[string]any{"text": "ok"}})
	if err == nil || transport["mcp_catalog"] == nil {
		t.Fatalf("transport failure should preserve last catalog with actual error: %v %#v", err, transport)
	}
	if one.calls.Load() != 4 {
		t.Fatalf("business side effect retried or skipped: %d", one.calls.Load())
	}
	if one.lists.Load() != 3 || two.lists.Load() != 1 {
		t.Fatal("business/catalog path unexpectedly rediscovered services")
	}
	if _, err := r.Call(context.Background(), "mcp_tool_inspect", map[string]any{"names": []string{"one:*"}, "name": "one:tool_000"}); err == nil || !strings.Contains(err.Error(), "input schema") {
		t.Fatalf("ambiguous legacy/new selectors accepted: %v", err)
	}
}

func TestMCPBusinessCatalogReferencesRepeatWithinTrustedScope(t *testing.T) {
	fixture := newCatalogFixture(t, 3)
	cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	r, err := newUnrestrictedTestRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Call(t.Context(), "mcp_manage", map[string]any{"action": "add", "name": "fixture", "description": "catalog delivery fixture", "transport": "streamable_http", "url": fixture.server.URL}); err != nil {
		t.Fatal(err)
	}
	ctx := activity.WithSource(t.Context(), activity.Source{Principal: "catalog-test", Namespace: "mcp", HostConversationID: "catalog-repeat"})
	if _, err := r.Call(ctx, "agentdock_context", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	firstList, err := r.Call(ctx, "mcp_tool_list", map[string]any{"server": "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	secondList, err := r.Call(ctx, "mcp_tool_list", map[string]any{"server": "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	forcedList, err := r.Call(ctx, "mcp_tool_list", map[string]any{"server": "fixture", "force_full": true})
	if err != nil {
		t.Fatal(err)
	}
	if firstList["delivery"] != "full" || catalogCount(firstList["tools"]) != 3 {
		t.Fatalf("first list omitted its catalog: %#v", firstList)
	}
	if secondList["delivery"] != "reference" || secondList["tools_omitted"] != true || secondList["tools"] != nil {
		t.Fatalf("repeat list duplicated its catalog: %#v", secondList)
	}
	if forcedList["delivery"] != "full" || catalogCount(forcedList["tools"]) != 3 {
		t.Fatalf("force_full list did not restore the catalog: %#v", forcedList)
	}
	call := func(callCtx context.Context) map[string]any {
		result, err := r.Call(callCtx, "mcp_tool_call", map[string]any{"name": "fixture:tool_000", "arguments": map[string]any{"text": "ok"}})
		if err != nil {
			t.Fatal(err)
		}
		return result["mcp_catalog"].(map[string]any)
	}
	first := call(ctx)
	second := call(ctx)
	if first["delivery"] != "summary" || first["tools_omitted"] != true || first["tools"] != nil {
		t.Fatalf("business call duplicated the catalog delivered by list: %#v", first)
	}
	if second["delivery"] != "summary" || second["tools_omitted"] != true || second["tools"] != nil || second["refresh_tool"] != "mcp_tool_list" {
		t.Fatalf("repeated trusted call duplicated its catalog: %#v", second)
	}
	other := activity.WithSource(t.Context(), activity.Source{Principal: "catalog-test", Namespace: "mcp", HostConversationID: "catalog-other"})
	if _, err := r.Call(other, "agentdock_context", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if isolated := call(other); isolated["delivery"] != "summary" || isolated["tools_omitted"] != true || isolated["tools"] != nil {
		t.Fatalf("another conversation received a duplicated call catalog: %#v", isolated)
	}
	if legacy := call(t.Context()); legacy["delivery"] != "summary" || legacy["tools_omitted"] != true || legacy["tools"] != nil {
		t.Fatalf("unattributed business call duplicated its catalog: %#v", legacy)
	}
}

func TestMCPSchemaReferencesRepeatWithinTrustedConversation(t *testing.T) {
	fixture := newCatalogFixture(t, 1)
	cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	r, err := newUnrestrictedTestRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Call(t.Context(), "mcp_manage", map[string]any{"action": "add", "name": "fixture", "description": "schema delivery fixture", "transport": "streamable_http", "url": fixture.server.URL}); err != nil {
		t.Fatal(err)
	}
	ctx := activity.WithSource(t.Context(), activity.Source{Principal: "schema-test", Namespace: "mcp", HostConversationID: "schema-repeat"})
	if _, err := r.Call(ctx, "agentdock_context", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	inspect := func(force bool) map[string]any {
		args := map[string]any{"names": []string{"fixture:tool_000"}}
		if force {
			args["force_full"] = true
		}
		result, err := r.Call(ctx, "mcp_tool_inspect", args)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := inspect(false)
	second := inspect(false)
	forced := inspect(true)
	firstTool := first["tools"].([]map[string]any)[0]
	secondTool := second["tools"].([]map[string]any)[0]
	forcedTool := forced["tools"].([]map[string]any)[0]
	if first["schema_full_count"] != 1 || first["schema_reference_count"] != 0 || firstTool["delivery"] != "full" || firstTool["input_schema"] == nil {
		t.Fatalf("first schema inspection was not complete: %#v", first)
	}
	if second["schema_full_count"] != 0 || second["schema_reference_count"] != 1 || secondTool["delivery"] != "reference" || secondTool["schema_omitted"] != true || secondTool["input_schema"] != nil {
		t.Fatalf("repeat schema inspection was not compact: %#v", second)
	}
	if forced["schema_full_count"] != 1 || forced["schema_reference_count"] != 0 || forcedTool["delivery"] != "full" || forcedTool["input_schema"] == nil {
		t.Fatalf("force_full schema inspection did not restore the schema: %#v", forced)
	}
}

func catalogCount(value any) int {
	switch items := value.(type) {
	case []any:
		return len(items)
	case []map[string]any:
		return len(items)
	default:
		return 0
	}
}

// The host result cursor must reach its handler, not the legacy file-page dispatcher.
func TestMCPResultContinuationUsesItsOwnCursor(t *testing.T) {
	fixture := newCatalogFixture(t, 1)
	fixture.largeResult.Store(true)
	cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	r, err := newUnrestrictedTestRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx := WithModelProjection(t.Context())
	if _, err = r.Call(ctx, "mcp_manage", map[string]any{"action": "add", "name": "pages", "description": "result continuation", "transport": "streamable_http", "url": fixture.server.URL}); err != nil {
		t.Fatal(err)
	}
	first, err := r.Call(ctx, "mcp_tool_call", map[string]any{"name": "pages:tool_000", "arguments": map[string]any{"text": "ok"}})
	if err != nil {
		t.Fatal(err)
	}
	original, _ := first["result_call_id"].(string)
	cursor, _ := first["host_cursor"].(string)
	if original == "" || cursor == "" {
		t.Fatalf("result continuation missing: %#v", first)
	}
	rawReference := stringArg(first, "raw_result_ref")
	assertCatalogFixtureSource(t, r, ctx, original, rawReference)
	assertSource := func(page Result) {
		t.Helper()
		capture := modelObject(page["capture"])
		if page["raw_result_ref"] != rawReference || capture["source_ref"] != rawReference || capture["state"] != "complete" || capture["complete"] != true || page["source_complete"] != true || page["truncation_stage"] == "capture" {
			t.Fatalf("result page lost its complete upstream source: capture=%#v complete=%#v stage=%#v", capture, page["source_complete"], page["truncation_stage"])
		}
	}
	assertSource(first)
	args := map[string]any{"call_id": original, "cursor": cursor}
	next, err := r.Call(ctx, "mcp_result_continue", args)
	if err != nil {
		t.Fatal(err)
	}
	if next["result_call_id"] != original || next["parent_call_id"] != original || next["call_id"] == original || next["host_page"] == nil || next["result"] == nil {
		t.Fatalf("result cursor routed incorrectly: %#v", next)
	}
	assertSource(next)
	if next["continue_read"] != nil || next["source_receipt"] != nil {
		t.Fatalf("MCP result fell back to file paging: %#v", next)
	}
	continued, err := r.activity.Call(ctx, stringArg(next, "call_id"))
	if err != nil {
		t.Fatal(err)
	}
	if continued.ParentCallID != original || continued.ToolName != "mcp_result_continue" || continued.OutputSource != nil {
		t.Fatalf("host result page was not folded under its source call: %#v", continued)
	}
	children, err := r.activity.Calls(ctx, activity.CallQuery{ParentCallID: original})
	if err != nil {
		t.Fatal(err)
	}
	foundChild := false
	for _, child := range children.Calls {
		if child.CallID == continued.CallID {
			foundChild = true
		}
	}
	if !foundChild {
		t.Fatalf("source call did not expose its host result child: %#v", children)
	}
	top, err := r.activity.Calls(ctx, activity.CallQuery{TopLevel: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range top.Calls {
		if call.CallID == continued.CallID {
			t.Fatal("host result page remained an unrelated top-level activity")
		}
	}
	replay, err := r.Call(ctx, "mcp_result_continue", args)
	if err != nil {
		t.Fatal(err)
	}
	if replay["replayed"] != true || replay["result"] != nil {
		t.Fatalf("cursor replay duplicated its body: %#v", replay)
	}
	for pageIndex := 2; stringArg(next, "host_cursor") != ""; pageIndex++ {
		if pageIndex > 10 {
			t.Fatal("host result pagination did not terminate")
		}
		next, err = r.Call(ctx, "mcp_result_continue", map[string]any{"call_id": original, "cursor": next["host_cursor"]})
		if err != nil {
			t.Fatal(err)
		}
		if next["result_call_id"] != original || next["parent_call_id"] != original {
			t.Fatalf("later result page lost its origin: %#v", next)
		}
		assertSource(next)
	}
	if modelObject(next["host_page"])["complete"] != true || next["truncation_stage"] != "none" {
		t.Fatalf("final result page did not preserve complete delivery: %#v", next["host_page"])
	}
	if fixture.calls.Load() != 1 {
		t.Fatalf("continuation repeated the business operation %d times", fixture.calls.Load())
	}
	assertCatalogFixtureSource(t, r, ctx, original, rawReference)
}

func assertCatalogFixtureSource(t *testing.T, r *Runtime, ctx context.Context, callID, reference string) {
	t.Helper()
	call, err := r.activity.Call(ctx, callID)
	if err != nil {
		t.Fatal(err)
	}
	if source := activity.SourceReference(callID, call.OutputSource); reference == "" || source != reference {
		t.Fatalf("default activity source differs from the full upstream response: source=%q raw=%q", source, reference)
	}
	page, err := r.activity.ReadCallPayloadCharacters(ctx, callID, "source", 0, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if page.HasMore || page.Payload.State != "complete" {
		t.Fatalf("fixture source was incomplete: %#v", page.Payload)
	}
	var raw map[string]any
	decoder := json.NewDecoder(strings.NewReader(page.Text))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		t.Fatal(err)
	}
	rows, _ := modelObject(raw["structuredContent"])["rows"].([]any)
	if len(rows) != 100 || modelObject(rows[99])["index"] != json.Number("99") || modelObject(raw["_meta"])["revision"] != "fixture-revision" {
		t.Fatalf("activity source lost upstream records or exact metadata: rows=%d metadata=%#v", len(rows), raw["_meta"])
	}
}
