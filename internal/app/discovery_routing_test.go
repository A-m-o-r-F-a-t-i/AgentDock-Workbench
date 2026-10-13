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
		var got *ToolError
		if result != nil || !errors.As(err, &got) || got.Code != "HOST_BUILTIN_NOT_DYNAMIC_MCP" || got.Retryable || !toolmcp.IsBuiltinDiscoveryError(err) {
			t.Fatalf("misroute not classified: %#v %v", result, err)
		}
		if got.Details["required_host_tool"] != test.want || stringArg(got.Details, "matched_alias") != test.alias || got.Details["execution_performed"] != false {
			t.Fatalf("wrong canonical recovery: %#v", got.Details)
		}
		raw, _ := json.Marshal(got.Details)
		if strings.Contains(string(raw), "input_schema") || strings.Contains(string(raw), "InputSchema") || strings.Contains(string(raw), "OutputSchema") {
			t.Fatalf("host schema leaked: %s", raw)
		}
		call, e := rt.activity.Call(t.Context(), stringArg(got.Details, "call_id"))
		if e != nil || call.Status != "failed" || call.ErrorCode != got.Code {
			t.Fatalf("misroute audit lost: %+v %v", call, e)
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
		var got *ToolError
		if result != nil || !errors.As(err, &got) || got.Code != "HOST_BUILTIN_NOT_DYNAMIC_MCP" || got.Retryable {
			t.Fatalf("inspect fabricated success: %#v %v", result, err)
		}
		count := 1
		if args["names"] != nil {
			count = 2
		}
		builtins, ok := got.Details["builtin_tools"].([]toolmcp.BuiltinToolSummary)
		if !ok || len(builtins) != count {
			t.Fatalf("host members lost: %#v", got.Details)
		}
		for _, item := range builtins {
			if item.InvokeVia != "host" {
				t.Fatalf("wrong host route: %+v", item)
			}
		}
		call, e := rt.activity.Call(t.Context(), stringArg(got.Details, "call_id"))
		if e != nil || call.Status != "failed" || call.ErrorCode != got.Code {
			t.Fatalf("inspect audit lost: %+v %v", call, e)
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
