package mcp

import (
	"os"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/config"
)

func TestBuiltinDiscoveryWireErrorsAndHostSchemaRecovery(t *testing.T) {
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
	for _, request := range []*mcpsdk.CallToolParams{
		{Name: "mcp_tool_search", Arguments: map[string]any{"query": "read_file"}},
		{Name: "mcp_tool_inspect", Arguments: map[string]any{"name": "read_file"}},
		{Name: "mcp_tool_inspect", Arguments: map[string]any{"names": []string{"read_file"}}},
	} {
		result, err := harness.session.CallTool(t.Context(), request)
		if err != nil || !result.IsError {
			t.Fatalf("wrong namespace must be an MCP tool error: %#v %v", result, err)
		}
		body, ok := result.StructuredContent.(map[string]any)
		if !ok || body["code"] == nil || body["retryable"] != false {
			t.Fatalf("wire route classification lost: %#v", body)
		}
		details := body["details"].(map[string]any)
		if details["route_status"] != "blocked" || details["required_host_tool"] != "read_file" || details["execution_performed"] != false || details["filesystem_lookup_required"] != false || details["dynamic_discovery_required"] != false {
			t.Fatalf("wire recovery missing: %#v", details)
		}
		action := asMap(body["required_action"])
		if len(action) == 0 {
			action = asMap(details["required_action"])
		}
		if action["category"] != "protocol" || action["tool"] != "read_file" || action["arguments"] != nil || action["required"] != true || action["execution_performed"] != false {
			t.Fatalf("wire recovery loops or fabricates arguments: %#v", action)
		}
		if details["agentdock_guidance"] != nil || body["agentdock_guidance"] != nil {
			t.Fatalf("wrong-namespace failure repeated legacy guidance: body=%#v details=%#v", body, details)
		}
	}
	for range 20 {
		result, err := harness.session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "read_file", Arguments: map[string]any{"path": "recovery.txt"}})
		if err != nil || result.IsError {
			t.Fatalf("host tool unavailable after routing errors: %#v %v", result, err)
		}
		body := result.StructuredContent.(map[string]any)
		if body["content"] != "unchanged fixture" {
			t.Fatalf("host read returned wrong content: %#v", body)
		}
	}
}
