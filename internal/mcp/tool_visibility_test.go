package mcp

import (
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/config"
	toolcapability "github.com/uvwt/agentdock/internal/tool/capability"
)

func listToolsForHost(t *testing.T, harness *mcpAppTestHarness, host string) map[string]*mcpsdk.Tool {
	t.Helper()
	var params *mcpsdk.ListToolsParams
	if host != "" {
		params = &mcpsdk.ListToolsParams{Meta: mcpsdk.Meta{"openai/session": host}}
	}
	listed, err := harness.session.ListTools(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	tools := make(map[string]*mcpsdk.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		tools[tool.Name] = tool
	}
	return tools
}

func callToolForHost(t *testing.T, harness *mcpAppTestHarness, host, name string, arguments map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	result, err := harness.session.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: name, Arguments: arguments, Meta: mcpsdk.Meta{"openai/session": host},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTrustedToolDirectoriesLoadLowFrequencyBundlesPerConversation(t *testing.T) {
	root := t.TempDir()
	harness := newMCPAppTestHarness(t, config.Config{
		AgentDockDefaultDir: root,
		AgentDockHome:       filepath.Join(root, ".agentdock"),
	})

	legacy := listToolsForHost(t, harness, "")
	for _, name := range []string{"mcp_manage", "plugin_manage", "skill_package", "mcp_result_raw", "file_publish"} {
		if legacy[name] == nil {
			t.Fatalf("legacy directory lost %s", name)
		}
	}

	hostA := "bundle-tools-a"
	hostB := "bundle-tools-b"
	initialA := listToolsForHost(t, harness, hostA)
	if initialA[toolcapability.ToolLoad] == nil {
		t.Fatal("trusted directory is missing capability_load")
	}
	for _, name := range []string{"mcp_manage", "plugin_manage", "skill_package", "mcp_result_raw", "file_publish"} {
		if initialA[name] != nil {
			t.Fatalf("trusted initial directory exposed %s", name)
		}
	}
	t.Logf("legacy_tools=%d trusted_initial_tools=%d", len(legacy), len(initialA))
	if got, want := len(initialA), len(legacy)-5; got != want {
		t.Fatalf("trusted initial tool count=%d want=%d", got, want)
	}
	if len(initialA) > 20 {
		t.Fatalf("trusted initial tool count=%d exceeds budget 20", len(initialA))
	}

	blocked := callToolForHost(t, harness, hostA, "mcp_manage", map[string]any{"action": "list"})
	if !blocked.IsError {
		t.Fatalf("hidden direct call unexpectedly succeeded: %#v", blocked)
	}
	blockedData, _ := blocked.StructuredContent.(map[string]any)
	blockedDetails, _ := blockedData["details"].(map[string]any)
	if blockedData["code"] != "TOOL_BUNDLE_REQUIRED" || blockedDetails["executed"] != false {
		t.Fatalf("hidden direct call result = %#v", blockedData)
	}

	loaded := callToolForHost(t, harness, hostA, toolcapability.ToolLoad, map[string]any{
		"action": toolcapability.ActionLoad, "name": toolcapability.BundleSystemManagement,
	})
	if loaded.IsError {
		t.Fatalf("system bundle load failed: %#v", loaded)
	}
	afterSystem := listToolsForHost(t, harness, hostA)
	for _, name := range []string{"mcp_manage", "plugin_manage", "skill_package", "mcp_result_raw"} {
		if afterSystem[name] == nil {
			t.Fatalf("loaded directory is missing %s", name)
		}
	}
	if afterSystem["file_publish"] != nil {
		t.Fatal("system bundle revealed file_publish")
	}
	other := listToolsForHost(t, harness, hostB)
	for _, name := range []string{"mcp_manage", "plugin_manage", "skill_package", "mcp_result_raw", "file_publish"} {
		if other[name] != nil {
			t.Fatalf("bundle visibility leaked to second conversation for %s", name)
		}
	}

	published := callToolForHost(t, harness, hostA, toolcapability.ToolLoad, map[string]any{
		"action": toolcapability.ActionLoad, "name": toolcapability.BundleArtifactPublishing,
	})
	if published.IsError {
		t.Fatalf("artifact bundle load failed: %#v", published)
	}
	if listToolsForHost(t, harness, hostA)["file_publish"] == nil {
		t.Fatal("artifact bundle did not reveal file_publish")
	}
}
