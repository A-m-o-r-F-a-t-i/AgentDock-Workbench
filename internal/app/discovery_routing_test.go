package app

import (
	"context"
	"encoding/json"
	"errors"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

func newAuditDiscoveryRuntime(t *testing.T) *Runtime {
	t.Helper()
	cfg := config.Config{AgentDockDefaultDir: t.TempDir(), AgentDockHome: filepath.Join(t.TempDir(), "home")}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	rt, err := newUnrestrictedTestRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func addAuditUnavailableServer(t *testing.T, rt *Runtime) {
	t.Helper()
	const missing = "AGENTDOCK_AUDIT_MISSING_MCP_CREDENTIAL"
	t.Setenv(missing, "")
	if err := os.Unsetenv(missing); err != nil {
		t.Fatal(err)
	}
	_, err := rt.Call(t.Context(), "mcp_manage", map[string]any{
		"action": "add", "name": "unrelated-artifact-fetch", "description": "Old temporary artifact service",
		"transport": "stdio", "command": os.Args[0],
		"env_from_env": map[string]any{"REQUIRED": missing},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuditBuiltinSearchNeverInitializesDynamicServers(t *testing.T) {
	rt := newAuditDiscoveryRuntime(t)
	addAuditUnavailableServer(t, rt)
	for _, test := range []struct{ query, want, alias string }{
		{"task_manage", "task_manage", ""}, {"exec_command", "exec_command", ""},
		{"mcp_tool_call", "mcp_tool_call", ""}, {"session_observe", "session_observe", ""},
		{"exec_command exact", "exec_command", ""}, {"host task_manage", "task_manage", ""},
		{"write_file", "file_edit", "write_file"}, {"Call a dynamic MCP tool", "mcp_tool_call", "call a dynamic mcp tool"},
	} {
		result, err := rt.Call(t.Context(), "mcp_tool_search", map[string]any{"query": test.query})
		var route *ToolError
		if result != nil || !errors.As(err, &route) {
			t.Fatalf("host namespace misuse was not a tool error: %#v %v", result, err)
		}
		wantCode := "HOST_BUILTIN_NOT_DYNAMIC_MCP"
		if route.Code != wantCode || route.Retryable || route.Details["route_status"] != "blocked" {
			t.Fatalf("wrong route classification: %#v", route)
		}
		if route.Details["required_host_tool"] != test.want || stringArg(route.Details, "matched_alias") != test.alias || route.Details["execution_performed"] != false || route.Details["filesystem_lookup_required"] != false || route.Details["dynamic_discovery_required"] != false {
			t.Fatalf("wrong canonical recovery: %#v", route.Details)
		}
		raw, _ := json.Marshal(route.Details)
		if strings.Contains(string(raw), "input_schema") || strings.Contains(string(raw), "InputSchema") || strings.Contains(string(raw), "OutputSchema") {
			t.Fatalf("host schema leaked: %s", raw)
		}
		call, e := rt.activity.Call(t.Context(), stringArg(route.Details, "call_id"))
		if e != nil || call.Status != "failed" || call.ErrorCode != wantCode || call.Title != "错误工具命名空间 · "+test.want {
			t.Fatalf("wrong-namespace route audit is not failed: %+v %v", call, e)
		}
	}
	if len(rt.command.ActiveConversationBindings()) != 0 {
		t.Fatal("discovery started a command")
	}
	_, err := rt.Call(t.Context(), "mcp_tool_search", map[string]any{"query": "exec_command", "server": "unrelated-artifact-fetch"})
	assertToolErrorCode(t, err, "MCP_AUTH_REQUIRED")
}

func TestAuditInspectRoutesHostBuiltinsWithoutDynamicQualification(t *testing.T) {
	rt := newAuditDiscoveryRuntime(t)
	for _, args := range []map[string]any{{"names": []string{"exec_command", "task_manage"}}, {"name": "exec_command"}} {
		result, err := rt.Call(t.Context(), "mcp_tool_inspect", args)
		var route *ToolError
		if result != nil || !errors.As(err, &route) || route.Code != "HOST_BUILTIN_NOT_DYNAMIC_MCP" || route.Details["terminal"] != true {
			t.Fatalf("inspect host route was not rejected: %#v %v", result, err)
		}
		count := 1
		if args["names"] != nil {
			count = 2
		}
		builtins, ok := route.Details["builtin_tools"].([]toolmcp.BuiltinToolSummary)
		if !ok || len(builtins) != count {
			t.Fatalf("host members lost: %#v", route.Details)
		}
		for _, item := range builtins {
			if item.Scope != toolmcp.ScopeAgentDockBuiltin || item.Origin != toolmcp.ScopeAgentDockBuiltin || item.InvokeVia != toolmcp.InvokeDirect {
				t.Fatalf("wrong host route: %+v", item)
			}
		}
		call, e := rt.activity.Call(t.Context(), stringArg(route.Details, "call_id"))
		if e != nil || call.Status != "failed" || call.ErrorCode != route.Code {
			t.Fatalf("inspect route audit is not failed: %+v %v", call, e)
		}
	}
}

func TestAuditBroadSearchReportsColdCatalogWithoutConnecting(t *testing.T) {
	rt := newAuditDiscoveryRuntime(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "must not connect", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	_, err := rt.Call(t.Context(), "mcp_manage", map[string]any{
		"action": "add", "name": "offline", "description": "PCB editor",
		"transport": "streamable_http", "url": server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		result, err := rt.Call(t.Context(), "mcp_tool_search", map[string]any{"query": "PCB"})
		if err != nil || result["complete"] != false || result["cache_only"] != true || result["count"] != 0 {
			t.Fatalf("cold directory must be explicit partial discovery: %#v %v", result, err)
		}
		data, _ := json.Marshal(result)
		if !strings.Contains(string(data), "offline") || !strings.Contains(string(data), "mcp_tool_list") {
			t.Fatalf("cold discovery lost the selected-service recovery path: %s", data)
		}
		assertToolResultMatchestestOutputSchema(t, "mcp_tool_search", result)
	}
	if requests.Load() != 0 {
		t.Fatalf("broad discovery initialized an unrelated server %d times", requests.Load())
	}
}

func TestAuditKnownCatalogSurvivesUnrelatedMissingCredential(t *testing.T) {
	rt := newAuditDiscoveryRuntime(t)
	server := newPluginTestMCPServer(t)
	defer server.Close()
	_, err := rt.Call(t.Context(), "mcp_manage", map[string]any{
		"action": "add", "name": "pcb", "description": "Audit PCB catalog", "transport": "streamable_http", "url": server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rt.Call(t.Context(), "mcp_tool_list", map[string]any{"server": "pcb"}); err != nil {
		t.Fatal(err)
	}
	addAuditUnavailableServer(t, rt)
	for _, query := range []string{"route", "no_match"} {
		result, err := rt.Call(context.Background(), "mcp_tool_search", map[string]any{"query": query})
		if err != nil || result["complete"] != false || result["cache_only"] != true {
			t.Fatalf("unrelated credential poisoned cached discovery: %#v %v", result, err)
		}
		want := 0
		if query == "route" {
			want = 1
		}
		if result["count"] != want {
			t.Fatalf("cached matches = %#v, want %d", result, want)
		}
	}
}
