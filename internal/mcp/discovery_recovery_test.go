package mcp

import (
	"os"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/config"
)

func TestBuiltinDiscoveryReturnsTerminalHostHandoff(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "recovery.txt"), []byte("unchanged fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	harness := newMCPAppTestHarnessWithApps(t, config.Config{AgentDockDefaultDir: root, AgentDockHome: filepath.Join(root, ".agentdock")}, false)
	tools := map[string]*mcpsdk.Tool{}
	for tool, err := range harness.session.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		tools[tool.Name] = tool
	}
	for _, name := range []string{"mcp_tool_call", "exec_command", "read_file", "task_manage"} {
		if tools[name] == nil || tools[name].InputSchema == nil {
			t.Fatalf("host tools/list did not expose executable schema for %s", name)
		}
	}
	for index, request := range []*mcpsdk.CallToolParams{
		{Name: "mcp_tool_search", Arguments: map[string]any{"query": "read_file"}},
		{Name: "mcp_tool_inspect", Arguments: map[string]any{"name": "read_file"}},
		{Name: "mcp_tool_inspect", Arguments: map[string]any{"names": []string{"read_file"}}},
	} {
		result, err := harness.session.CallTool(t.Context(), request)
		if err != nil || result.IsError {
			t.Fatalf("host handoff must be a successful routing result: %#v %v", result, err)
		}
		body, ok := result.StructuredContent.(map[string]any)
		if !ok || body["route_kind"] != "host_builtin" || body["resolved"] != true || body["terminal"] != true {
			t.Fatalf("terminal host route classification lost: %#v", body)
		}
		wantStatus := "resolved"
		if index > 0 {
			wantStatus = "already_routed"
		}
		if body["route_status"] != wantStatus || body["required_host_tool"] != "read_file" || body["execution_performed"] != false || body["filesystem_lookup_required"] != false || body["dynamic_discovery_required"] != false || body["host_directory_visibility"] != "unknown" {
			t.Fatalf("wire host handoff missing: %#v", body)
		}
		action := asMap(body["required_action"])
		if action["action"] != "call_host_tool" || action["category"] != "protocol" || action["tool"] != "read_file" || action["arguments"] != nil || action["required"] != true || action["execution_performed"] != false || action["schema_source"] != "caller_tools_list" {
			t.Fatalf("wire handoff loops or fabricates arguments: %#v", action)
		}
	}
	for range 20 {
		result, err := harness.session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "read_file", Arguments: map[string]any{"path": "recovery.txt"}})
		if err != nil || result.IsError {
			t.Fatalf("host tool unavailable after routing handoff: %#v %v", result, err)
		}
		body := result.StructuredContent.(map[string]any)
		if body["content"] != "unchanged fixture" {
			t.Fatalf("host read returned wrong content: %#v", body)
		}
	}
}
