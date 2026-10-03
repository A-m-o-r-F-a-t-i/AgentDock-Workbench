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
	pluginregistry "github.com/uvwt/agentdock/internal/plugin"
)

func TestComputerUsePluginIsExplicitAndLaunchAppRequiresIntent(t *testing.T) {
	var businessCalls atomic.Int32
	upstream := newComputerUseTestMCPServer(t, &businessCalls)
	defer upstream.Close()

	root := t.TempDir()
	cfg := config.Config{AgentDockHome: filepath.Join(root, "home"), AgentDockDefaultDir: root}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	rt, err := newUnrestrictedTestRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	source := writeComputerUsePluginPackage(t, root, upstream.URL)
	if _, err := rt.Call(context.Background(), "plugin_manage", map[string]any{"action": "install", "source": source}); err != nil {
		t.Fatal(err)
	}

	contextResult, err := rt.Call(context.Background(), "agentdock_context", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var contextView capabilityContext
	if err := remarshal(contextResult, &contextView); err != nil {
		t.Fatal(err)
	}
	if containsCapabilitySkill(contextView.Skills, "current-browser-control") || containsCapabilityMCP(contextView.DynamicMCP, "cua-driver") {
		t.Fatalf("Computer Use members leaked into default context: %#v", contextView)
	}
	var pluginSummary *capabilityPluginItem
	for index := range contextView.Plugins {
		if contextView.Plugins[index].Name == "computer-use" {
			pluginSummary = &contextView.Plugins[index]
			break
		}
	}
	if pluginSummary == nil || !strings.Contains(pluginSummary.Description, "显式 GUI") {
		t.Fatalf("Computer Use explicit-load summary missing: %#v", contextView.Plugins)
	}
	rules := strings.Join(contextView.Rules, "\n")
	for _, want := range []string{"plugin_load(\"computer-use\")", "interaction_intent=desktop_gui", "cua-driver:launch_app", "代码、文件、Git、终端、PCB"} {
		if !strings.Contains(rules, want) {
			t.Fatalf("Computer Use context rule missing %q: %s", want, rules)
		}
	}

	loaded, err := rt.Call(context.Background(), "plugin_load", map[string]any{"name": "computer-use"})
	if err != nil {
		t.Fatal(err)
	}
	plugin, _ := loaded["plugin"].(map[string]any)
	if plugin["load_required"] != true || plugin["heavy"] != false {
		t.Fatalf("explicit-load plugin metadata = %#v", plugin)
	}
	encodedLoaded, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"current-browser-control", "cua-driver:launch_app", "不得用于能力探测", "interaction_intent=desktop_gui"} {
		if !strings.Contains(string(encodedLoaded), want) {
			t.Fatalf("plugin_load response missing %q: %s", want, encodedLoaded)
		}
	}

	broad, err := rt.Call(context.Background(), "mcp_tool_search", map[string]any{"query": "launch Windows app", "limit": 10})
	if err != nil {
		t.Fatal(err)
	}
	if broad["count"] != 0 {
		t.Fatalf("Computer Use leaked into unscoped search: %#v", broad)
	}
	broadJSON, _ := json.Marshal(broad)
	if strings.Contains(string(broadJSON), "cua-driver:launch_app") {
		t.Fatalf("unscoped search exposed launch_app: %s", broadJSON)
	}

	inspect, err := rt.Call(context.Background(), "mcp_tool_inspect", map[string]any{"name": "cua-driver:launch_app"})
	if err != nil {
		t.Fatal(err)
	}
	description, _ := inspect["description"].(string)
	for _, want := range []string{"尚未运行", "interaction_intent=desktop_gui", "不得用于能力探测"} {
		if !strings.Contains(description, want) {
			t.Fatalf("launch_app inspect description missing %q: %s", want, description)
		}
	}

	_, err = rt.Call(context.Background(), "mcp_tool_call", map[string]any{
		"name": "cua-driver:launch_app", "arguments": map[string]any{"name": "notepad"},
	})
	assertToolErrorCode(t, err, "MCP_EXPLICIT_INTENT_REQUIRED")
	if businessCalls.Load() != 0 {
		t.Fatalf("launch_app reached upstream without explicit intent: %d", businessCalls.Load())
	}

	called, err := rt.Call(context.Background(), "mcp_tool_call", map[string]any{
		"name":               "cua-driver:launch_app",
		"arguments":          map[string]any{"name": "notepad"},
		"interaction_intent": "desktop_gui",
		"reason":             "用户明确要求启动 Windows 记事本",
	})
	if err != nil {
		t.Fatal(err)
	}
	if businessCalls.Load() != 1 {
		t.Fatalf("explicit launch_app call count = %d", businessCalls.Load())
	}
	remote, _ := called["result"].(map[string]any)
	structured, _ := remote["structuredContent"].(map[string]any)
	if structured["launched"] != true {
		t.Fatalf("explicit launch_app result = %#v", called)
	}
	catalog, _ := called["mcp_catalog"].(map[string]any)
	catalogJSON, _ := json.Marshal(catalog)
	if !strings.Contains(string(catalogJSON), "不得用于能力探测") {
		t.Fatalf("business response catalog lost launch boundary: %s", catalogJSON)
	}
}

func writeComputerUsePluginPackage(t *testing.T, parent, mcpURL string) string {
	t.Helper()
	root := filepath.Join(parent, "computer-use-source")
	skillDir := filepath.Join(root, "skills", "current-browser-control")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	document := "---\nname: current-browser-control\ndescription: Control the current Edge page.\nversion: 1.0.0\n---\n\n# Current Browser Control\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	mcpData, err := json.Marshal(pluginregistry.MCPConfig{Schema: pluginregistry.MCPSchema, MCPServers: map[string]pluginregistry.MCPServer{
		"cua-driver": {Type: "streamable-http", URL: mcpURL},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, pluginregistry.MCPFilename), mcpData, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := pluginregistry.Manifest{
		Schema: pluginregistry.ManifestSchema, Name: "computer-use", Version: "1.0.0",
		Description: "Windows and browser Computer Use capabilities.",
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, pluginregistry.ManifestFilename), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func newComputerUseTestMCPServer(t *testing.T, businessCalls *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var rpc struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Errorf("decode Computer Use MCP request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch rpc.Method {
		case "server/discover":
			writeDynamicMCPRPCError(t, w, rpc.ID, -32601, "Method not found")
		case "initialize":
			writeDynamicMCPRPCResult(t, w, rpc.ID, map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "cua-driver-test", "version": "1.0.0"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeDynamicMCPRPCResult(t, w, rpc.ID, map[string]any{
				"tools": []map[string]any{{
					"name": "launch_app", "description": "Launch a Windows app hidden.",
					"inputSchema": map[string]any{
						"type": "object", "additionalProperties": false,
						"properties": map[string]any{"name": map[string]any{"type": "string"}},
					},
				}},
			})
		case "tools/call":
			businessCalls.Add(1)
			writeDynamicMCPRPCResult(t, w, rpc.ID, map[string]any{
				"content":           []map[string]any{{"type": "text", "text": "launched"}},
				"structuredContent": map[string]any{"launched": true},
			})
		default:
			t.Errorf("unexpected Computer Use MCP method %q", rpc.Method)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func writeDynamicMCPRPCError(t *testing.T, w http.ResponseWriter, id any, code int, message string) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message},
	}); err != nil {
		t.Fatalf("encode MCP error: %v", err)
	}
}
