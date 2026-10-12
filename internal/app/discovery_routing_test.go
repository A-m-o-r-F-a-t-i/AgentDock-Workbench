package app

import (
	"context"
	"encoding/json"
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
	for _, test := range []struct {
		query string
		want  string
		alias string
	}{
		{query: "task_manage", want: "task_manage"},
		{query: "exec_command", want: "exec_command"},
		{query: "mcp_tool_call", want: "mcp_tool_call"},
		{query: "session_observe", want: "session_observe"},
		{query: "exec_command exact", want: "exec_command"},
		{query: "host task_manage", want: "task_manage"},
		{query: "write_file", want: "file_edit", alias: "write_file"},
	} {
		result, err := rt.Call(t.Context(), "mcp_tool_search", map[string]any{"query": test.query})
		if err != nil {
			t.Fatalf("unscoped built-in search was poisoned by unrelated credentials: %v", err)
		}
		var view struct {
			RouteKind         string `json:"route_kind"`
			Resolved          bool   `json:"resolved"`
			Terminal          bool   `json:"terminal"`
			Count             int    `json:"count"`
			BuiltinMatchCount int    `json:"builtin_match_count"`
			DynamicMatchCount int    `json:"dynamic_match_count"`
			MatchedTool       string `json:"matched_tool"`
			MatchedAlias      string `json:"matched_alias"`
			RouteSummary      string `json:"route_summary"`
			Builtin           []struct {
				Name        string `json:"name"`
				Title       string `json:"title"`
				Description string `json:"description"`
				InvokeVia   string `json:"invoke_via"`
			} `json:"builtin_tools"`
			Tools    []any `json:"tools"`
			Catalogs []any `json:"catalogs"`
		}
		if err := remarshal(result, &view); err != nil {
			t.Fatal(err)
		}
		if view.RouteKind != "host_builtin" || !view.Resolved || !view.Terminal || view.Count != 1 ||
			view.BuiltinMatchCount != 1 || view.DynamicMatchCount != 0 || view.MatchedTool != test.want ||
			view.MatchedAlias != test.alias || view.RouteSummary != "定位内置工具 · "+test.want ||
			len(view.Builtin) != 1 || view.Builtin[0].Name != test.want || view.Builtin[0].InvokeVia != "host" ||
			len(view.Tools) != 0 || len(view.Catalogs) != 0 {
			t.Fatalf("wrong canonical built-in route: %#v", result)
		}
		data, _ := json.Marshal(result)
		if len(data) >= 2048 || strings.Contains(string(data), "InputSchema") || strings.Contains(string(data), "OutputSchema") {
			t.Fatalf("built-in route leaked full host schema (%d bytes): %s", len(data), data)
		}
		call, err := rt.activity.Call(t.Context(), stringArg(result, "call_id"))
		if err != nil {
			t.Fatal(err)
		}
		if call.DisplayTitle != "定位内置工具 · "+test.want || call.OutputSummary != call.DisplayTitle || strings.Contains(call.OutputSummary, "count=0") {
			t.Fatalf("ambiguous built-in activity: %+v", call)
		}
		assertToolResultMatchestestOutputSchema(t, "mcp_tool_search", result)
	}
	// Discovery does not execute the built-in task action or create a command.
	if len(rt.command.ActiveConversationBindings()) != 0 {
		t.Fatal("discovery started a command")
	}
	_, err := rt.Call(t.Context(), "mcp_tool_search", map[string]any{"query": "exec_command", "server": "unrelated-artifact-fetch"})
	assertToolErrorCode(t, err, "MCP_AUTH_REQUIRED")
}

func TestAuditInspectRoutesHostBuiltinsWithoutDynamicQualification(t *testing.T) {
	rt := newAuditDiscoveryRuntime(t)
	result, err := rt.Call(t.Context(), "mcp_tool_inspect", map[string]any{"names": []string{"exec_command", "task_manage"}})
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		RouteKind         string `json:"route_kind"`
		Resolved          bool   `json:"resolved"`
		Terminal          bool   `json:"terminal"`
		BuiltinMatchCount int    `json:"builtin_match_count"`
		DynamicMatchCount int    `json:"dynamic_match_count"`
		RouteSummary      string `json:"route_summary"`
		Builtin           []struct {
			Name      string `json:"name"`
			InvokeVia string `json:"invoke_via"`
		} `json:"builtin_tools"`
		Errors []struct {
			Selector  string `json:"selector"`
			Code      string `json:"code"`
			Status    string `json:"status"`
			RouteKind string `json:"route_kind"`
			ToolName  string `json:"tool_name"`
		} `json:"errors"`
	}
	if err := remarshal(result, &view); err != nil {
		t.Fatal(err)
	}
	if view.RouteKind != "host_builtin" || !view.Resolved || !view.Terminal || view.BuiltinMatchCount != 2 ||
		view.DynamicMatchCount != 0 || view.RouteSummary != "识别内置工具 · exec_command, task_manage" || len(view.Builtin) != 2 || len(view.Errors) != 0 {
		t.Fatalf("wrong host inspect route: %#v", result)
	}
	for index, name := range []string{"exec_command", "task_manage"} {
		if view.Builtin[index].Name != name || view.Builtin[index].InvokeVia != "host" {
			t.Fatalf("wrong host inspect member: %#v", result)
		}
	}
	call, err := rt.activity.Call(t.Context(), stringArg(result, "call_id"))
	if err != nil {
		t.Fatal(err)
	}
	if call.DisplayTitle != view.RouteSummary || call.OutputSummary != view.RouteSummary || strings.Contains(call.OutputSummary, "count=0") {
		t.Fatalf("ambiguous inspect activity: %+v", call)
	}
	assertToolResultMatchestestOutputSchema(t, "mcp_tool_inspect", result)
	single, err := rt.Call(t.Context(), "mcp_tool_inspect", map[string]any{"name": "exec_command"})
	if err != nil || single["route_kind"] != "host_builtin" || single["terminal"] != true || single["execution_performed"] != false {
		t.Fatalf("single host selector must hand off successfully: %#v %v", single, err)
	}
	assertToolResultMatchestestOutputSchema(t, "mcp_tool_inspect", single)
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
