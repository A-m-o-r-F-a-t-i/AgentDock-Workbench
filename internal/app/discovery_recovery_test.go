package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
	toolcapability "github.com/uvwt/agentdock/internal/tool/capability"
)

func TestBuiltinDiscoveryRecoveryPreservesDynamicCallsAndMixedResults(t *testing.T) {
	rt := newAuditDiscoveryRuntime(t)
	var catalogs, calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		switch request.Method {
		case "server/discover":
			writePluginTestRPC(w, request.ID, nil, map[string]any{"code": -32601, "message": "Method not found"})
		case "initialize":
			writePluginTestRPC(w, request.ID, map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "recovery-fixture", "version": "1.0.0"}}, nil)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			catalogs.Add(1)
			writePluginTestRPC(w, request.ID, map[string]any{"tools": []map[string]any{{"name": "exec_command", "description": "Fixture dynamic same-name tool", "inputSchema": map[string]any{"type": "object", "additionalProperties": false}}}}, nil)
		case "tools/call":
			calls.Add(1)
			writePluginTestRPC(w, request.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": "fixture result"}}, "isError": false}, nil)
		default:
			t.Errorf("unexpected request: %s", request.Method)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	ctx := activity.WithSource(t.Context(), activity.Source{Principal: "recovery-test", Namespace: "mcp", HostConversationID: "business-after-routing-error"})
	for _, request := range []struct {
		name string
		args map[string]any
	}{
		{"agentdock_context", map[string]any{}},
		{toolcapability.ToolLoad, map[string]any{"action": toolcapability.ActionLoad, "name": toolcapability.BundleSystemManagement}},
		{"mcp_manage", map[string]any{"action": "add", "name": "fixture", "description": "Isolated routing recovery fixture", "transport": "streamable_http", "url": server.URL}},
		{"mcp_tool_list", map[string]any{"server": "fixture"}},
		{"mcp_tool_inspect", map[string]any{"names": []string{"fixture:exec_command"}}},
	} {
		if _, err := rt.Call(ctx, request.name, request.args); err != nil {
			t.Fatalf("%s: %v", request.name, err)
		}
	}
	route, err := rt.Call(ctx, "mcp_tool_search", map[string]any{"query": "mcp_tool_call"})
	var routeError *ToolError
	if route != nil || !errors.As(err, &routeError) || routeError.Code != "HOST_BUILTIN_NOT_DYNAMIC_MCP" || routeError.Details["route_status"] != "blocked" {
		t.Fatalf("initial host route was not rejected: %#v %v", route, err)
	}
	for range 20 {
		result, err := rt.Call(ctx, "mcp_tool_call", map[string]any{"name": "fixture:exec_command", "arguments": map[string]any{}})
		if err != nil || resultReportsFailure(result) {
			t.Fatalf("routing guard blocked authorized business: %#v %v", result, err)
		}
		assertToolResultMatchestestOutputSchema(t, "mcp_tool_call", result)
	}
	for _, test := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"query": "exec_command", "server": "fixture"}, "exact_name"},
		{map[string]any{"query": "fixture:exec_command", "server": "fixture"}, "exact_qualified_name"},
		{map[string]any{"query": "fixture:exec_command"}, "exact_qualified_name"},
	} {
		found, err := rt.Call(ctx, "mcp_tool_search", test.args)
		if err != nil || found["count"] != 1 || found["match_kind"] != test.want {
			t.Fatalf("scoped dynamic same-name search intercepted: %#v %v", found, err)
		}
	}
	weak, err := rt.Call(ctx, "mcp_tool_search", map[string]any{"query": "This call should not be used", "server": "fixture"})
	if err != nil || weak["count"] != 0 || weak["match_kind"] != "none" {
		t.Fatalf("weak query produced a dynamic false positive: %#v %v", weak, err)
	}
	mixed, err := rt.Call(ctx, "mcp_tool_inspect", map[string]any{"names": []string{"exec_command", "fixture:exec_command"}})
	if err != nil || mixed["complete"] != false || mixed["count"] != 1 || mixed["route_kind"] != "mixed" {
		t.Fatalf("mixed independent result lost: %#v %v", mixed, err)
	}
	problems := mixed["errors"].([]map[string]any)
	if len(problems) != 1 || problems[0]["code"] != "HOST_BUILTIN_NOT_DYNAMIC_MCP" || problems[0]["required_host_tool"] != "exec_command" {
		t.Fatalf("mixed recovery missing: %#v", mixed)
	}
	assertToolResultMatchestestOutputSchema(t, "mcp_tool_inspect", mixed)
	// A caller that lost context can deliberately reread. No permanent once-only gate.
	for range 3 {
		if _, err := rt.Call(ctx, "mcp_tool_list", map[string]any{"server": "fixture"}); err != nil {
			t.Fatal(err)
		}
		if _, err := rt.Call(ctx, "mcp_tool_inspect", map[string]any{"names": []string{"fixture:exec_command"}}); err != nil {
			t.Fatal(err)
		}
	}
	if catalogs.Load() != 1 || calls.Load() != 20 {
		t.Fatalf("upstream catalog/business calls=%d/%d want 1/20", catalogs.Load(), calls.Load())
	}
	route, err = rt.Call(ctx, "mcp_tool_search", map[string]any{"query": "mcp_tool_call"})
	if route != nil || !errors.As(err, &routeError) || routeError.Code != "ROUTING_LOOP_BLOCKED" || routeError.Details["route_status"] != "blocked" {
		t.Fatalf("repeated host route was not blocked: %#v %v", route, err)
	}
	if _, err := rt.Call(ctx, "task_manage", map[string]any{"action": "list"}); err != nil {
		t.Fatalf("host recovery tool unavailable: %v", err)
	}
}

func TestBuiltinPluginCatalogGuidanceReusesKnownDirectory(t *testing.T) {
	rt := newAuditDiscoveryRuntime(t)
	server := newPluginTestMCPServer(t)
	defer server.Close()
	source := writeAppPluginPackage(t, t.TempDir(), server.URL)
	if _, err := rt.Call(t.Context(), "plugin_manage", map[string]any{"action": "install", "source": source, "review_token": reviewAppPluginForTest(t, rt, source).ReviewToken}); err != nil {
		t.Fatal(err)
	}
	result, err := rt.Call(t.Context(), "plugin_load", map[string]any{"name": "pcb"})
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Instructions []string `json:"instructions"`
	}
	if err := remarshal(result, &view); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(view.Instructions, "\n")
	if !strings.Contains(text, "Reuse the returned MCP catalog") || strings.Contains(text, "Use mcp_tool_search") {
		t.Fatalf("known directory sends caller back into search: %s", text)
	}
}
