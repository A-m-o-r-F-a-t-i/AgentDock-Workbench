package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
	registry "github.com/uvwt/agentdock/internal/plugin"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

func TestManageInstallsAndLoadsDirectPluginMembers(t *testing.T) {
	home := t.TempDir()
	store, err := registry.New(home)
	if err != nil {
		t.Fatal(err)
	}
	source := writeToolPluginPackage(t, t.TempDir(), "pcb", []string{"layout"}, []string{"easyeda"})
	service := testPluginService(store, false)
	reviewToken := reviewTokenForSource(t, service, source)

	result, err := service.Manage(context.Background(), ManageRequest{Action: "install", Source: source, ReviewToken: reviewToken})
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := result["plugin"].(registry.Definition)
	if !ok || stored.Name != "pcb" || !stored.Enabled || stored.Path != filepath.Join(home, "plugins", "pcb") {
		t.Fatalf("stored plugin = %#v", result["plugin"])
	}
	if _, err := os.Stat(filepath.Join(stored.Path, registry.ManifestDirectory, registry.ManifestFilename)); err != nil {
		t.Fatalf("direct manifest missing: %v", err)
	}

	loaded, err := service.Load(context.Background(), LoadRequest{Name: "pcb"})
	if err != nil {
		t.Fatal(err)
	}
	skills, ok := loaded["skills"].([]SkillItem)
	if !ok || len(skills) != 1 || skills[0].File != "skill://layout/SKILL.md" {
		t.Fatalf("loaded skills = %#v", loaded["skills"])
	}
	servers, ok := loaded["mcp_servers"].([]MCPItem)
	if !ok || len(servers) != 1 || servers[0].Name != "easyeda" || len(servers[0].Tools) != 1 || servers[0].Tools[0].QualifiedName != "easyeda:route" {
		t.Fatalf("loaded MCP servers = %#v", loaded["mcp_servers"])
	}
	unavailable, ok := loaded["unavailable_members"].([]map[string]any)
	if !ok || len(unavailable) != 0 {
		t.Fatalf("unavailable members = %#v", loaded["unavailable_members"])
	}
}

func TestReconcileRetainsUnchangedMCPGeneration(t *testing.T) {
	home := t.TempDir()
	store, err := registry.New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := mcpclient.NewManager(home)
	if err != nil {
		t.Fatal(err)
	}
	service := testPluginService(store, false)
	service.SetMCPManager(manager)
	defer service.ReleaseMCPLeases()
	defer manager.Close()
	source := writeToolPluginPackage(t, t.TempDir(), "retained", []string{"retained-skill"}, []string{"retained-mcp"})
	if _, err := service.Manage(t.Context(), ManageRequest{Action: "install", Source: source, ReviewToken: reviewTokenForSource(t, service, source)}); err != nil {
		t.Fatal(err)
	}
	before, summaryBefore, err := manager.Inspect("retained-mcp")
	if err != nil {
		t.Fatal(err)
	}
	other := writeToolPluginPackage(t, t.TempDir(), "unrelated", nil, []string{"unrelated-mcp"})
	if _, err := service.Manage(t.Context(), ManageRequest{Action: "install", Source: other, ReviewToken: reviewTokenForSource(t, service, other)}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Manage(t.Context(), ManageRequest{Action: "member_disable", Name: "retained", MemberType: "skill", Member: "retained-skill"}); err != nil {
		t.Fatal(err)
	}
	after, summaryAfter, err := manager.Inspect("retained-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if before.PluginRoot != after.PluginRoot || summaryBefore.Revision != summaryAfter.Revision {
		t.Fatalf("unrelated change replaced MCP generation: root=%q -> %q revision=%q -> %q", before.PluginRoot, after.PluginRoot, summaryBefore.Revision, summaryAfter.Revision)
	}
	if err := store.PrepareMCPData(t.Context(), after); err != nil {
		t.Fatalf("retained runtime lease was released during reconcile: %v", err)
	}
}

func TestRetiredMCPCallRetainsPluginRuntimeDuringReconcile(t *testing.T) {
	type cancelLeaseInitialization struct{}
	for _, action := range []string{"update", "remove", "member_disable"} {
		t.Run(action, func(t *testing.T) {
			home := t.TempDir()
			store, err := registry.New(home)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(store.Close)
			manager, err := mcpclient.NewManager(home)
			if err != nil {
				t.Fatal(err)
			}
			service := testPluginService(store, false)
			service.SetMCPManager(manager)
			var acquired, released atomic.Int32
			manager.SetExternalServerRuntimeLease(func(ctx context.Context, cfg mcpclient.ServerConfig) (func(), error) {
				release, err := store.AcquireMCPRuntime(ctx, cfg)
				if err != nil {
					return nil, err
				}
				acquired.Add(1)
				if cancel, ok := ctx.Value(cancelLeaseInitialization{}).(context.CancelFunc); ok {
					cancel()
				}
				return func() {
					released.Add(1)
					release()
				}, nil
			})
			t.Cleanup(service.ReleaseMCPLeases)
			t.Cleanup(func() { _ = manager.Close() })
			manager.SetExternalServerPreparation(store.PrepareMCPData)
			const pluginName, serverName = "leased-runtime", "leased-mcp"
			source := writeToolPluginPackage(t, t.TempDir(), pluginName, nil, []string{serverName})
			writeJSON := func(path string, value any) {
				t.Helper()
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			const helperRun = "-test.run=^TestPluginMCPLeaseHelperProcess$"
			writeJSON(filepath.Join(source, registry.MCPFilename), registry.MCPConfig{Schema: registry.MCPSchema, MCPServers: map[string]registry.MCPServer{
				serverName: {Type: "stdio", Command: "test-helper", Args: []string{helperRun}, Env: map[string]string{"GO_WANT_PLUGIN_RUNTIME_HELPER": "1"}},
			}})
			if err = os.WriteFile(filepath.Join(source, "lease.txt"), []byte("original generation"), 0o600); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			writeJSON(filepath.Join(home, "plugins", ".config", pluginName+".json"), registry.HostConfig{MCPServers: map[string]registry.MCPHostConfig{serverName: {Command: executable}}})
			if _, err = service.Manage(t.Context(), ManageRequest{Action: "install", Source: source, ReviewToken: reviewTokenForSource(t, service, source)}); err != nil {
				t.Fatal(err)
			}
			cfg, _, err := manager.Inspect(serverName)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = manager.SearchCatalogsFiltered(t.Context(), "read_after_gate", "", 1, nil); err != nil {
				t.Fatal(err)
			}
			if err = service.ReconcileMCP(t.Context()); err != nil {
				t.Fatal(err)
			}
			if acquired.Load() != 0 || released.Load() != 0 {
				t.Fatal("read-only catalog discovery or reconciliation acquired a connection lease")
			}
			for range 2 {
				if _, _, err = manager.Refresh(t.Context(), serverName); err != nil {
					t.Fatal(err)
				}
			}
			if acquired.Load() != 2 || released.Load() != 1 {
				t.Fatalf("successful refresh did not transfer exactly one lease: acquired=%d released=%d", acquired.Load(), released.Load())
			}
			_, active, err := manager.Inspect(serverName)
			if err != nil {
				t.Fatal(err)
			}
			failedArgs, _ := json.Marshal([]string{helperRun, "--", "fail-initialize"})
			if _, err = manager.Update(t.Context(), serverName, active.Revision, "runtime", mcpclient.ConfigPatch{"args": failedArgs}, false); err == nil {
				t.Fatal("failed candidate initialization was accepted")
			}
			cancelCtx, cancel := context.WithCancel(t.Context())
			_, _, err = manager.Refresh(context.WithValue(cancelCtx, cancelLeaseInitialization{}, cancel), serverName)
			cancel()
			if err == nil || acquired.Load() != 4 || released.Load() != 3 {
				t.Fatalf("failed/cancelled initialization leaked a candidate lease or released the active one: acquired=%d released=%d err=%v", acquired.Load(), released.Load(), err)
			}
			entered, unblock := make(chan struct{}), make(chan struct{})
			var enteredOnce, unblockOnce sync.Once
			gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				enteredOnce.Do(func() { close(entered) })
				select {
				case <-unblock:
					w.WriteHeader(http.StatusNoContent)
				case <-r.Context().Done():
				}
			}))
			t.Cleanup(gate.Close)
			releaseCall := func() { unblockOnce.Do(func() { close(unblock) }) }
			t.Cleanup(releaseCall)
			type outcome struct {
				result map[string]any
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := manager.Call(t.Context(), serverName+":read_after_gate", map[string]any{"gate": gate.URL})
				done <- outcome{result: result, err: err}
			}()
			select {
			case <-entered:
			case result := <-done:
				t.Fatalf("old MCP call did not reach its gate: %#v %v", result.result, result.err)
			case <-t.Context().Done():
				t.Fatal(t.Context().Err())
			}
			_, summary, err := manager.Inspect(serverName)
			if err != nil {
				t.Fatal(err)
			}
			// A connection-changing override retires the blocked client without
			// waiting for it. Reconciliation must retain its runtime independently
			// of the current server state and the Plugin directory's own lease.
			args, _ := json.Marshal([]string{helperRun, "--", "replacement"})
			if _, err = manager.Update(t.Context(), serverName, summary.Revision, "runtime", mcpclient.ConfigPatch{"args": args, "lazy": json.RawMessage("true")}, false); err != nil {
				t.Fatal(err)
			}
			request := ManageRequest{Action: action, Name: pluginName, MemberType: "mcp_server", Member: serverName}
			if action == "update" {
				writeJSON(filepath.Join(source, registry.ManifestDirectory, registry.ManifestFilename), registry.Manifest{Schema: registry.ManifestSchema, Name: pluginName, Version: "2.0.0", Description: "Updated leased runtime."})
				if err = os.WriteFile(filepath.Join(source, "lease.txt"), []byte("new generation"), 0o600); err != nil {
					t.Fatal(err)
				}
				request.Source, request.ReviewToken = source, reviewTokenForSource(t, service, source)
			}
			if _, err = service.Manage(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			if staleRelease, err := store.AcquireMCPRuntime(t.Context(), cfg); err == nil {
				staleRelease()
				t.Error("a new connection acquired a lease for a removed, disabled, or replaced runtime")
			}
			if _, err = os.Stat(filepath.Join(cfg.PluginRoot, "lease.txt")); err != nil {
				t.Errorf("retired call lost its leased runtime before completion: %v", err)
			}
			if acquired.Load() != 4 || released.Load() != 3 {
				t.Errorf("reconciliation released the active retired client: acquired=%d released=%d", acquired.Load(), released.Load())
			}
			releaseCall()
			result := <-done
			structured, _ := result.result["structuredContent"].(map[string]any)
			if result.err != nil || structured["value"] != "original generation" || structured["read_error"] != "" {
				t.Errorf("old call failed to read its original runtime: %#v %v", result.result, result.err)
			}
			if err = manager.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(cfg.PluginRoot); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("retired runtime was not released after client closure: %v", err)
			}
			if err = manager.Close(); err != nil || acquired.Load() != 4 || released.Load() != 4 {
				t.Fatalf("client close did not release each lease exactly once: acquired=%d released=%d err=%v", acquired.Load(), released.Load(), err)
			}
		})
	}
}

func TestPluginMCPLeaseHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_PLUGIN_RUNTIME_HELPER") != "1" {
		return
	}
	failInitialize := false
	for _, arg := range os.Args {
		failInitialize = failInitialize || arg == "fail-initialize"
	}
	input, output := bufio.NewScanner(os.Stdin), json.NewEncoder(os.Stdout)
	for input.Scan() {
		var request struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(input.Bytes(), &request) != nil {
			os.Exit(2)
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "notifications/initialized":
			continue
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "lease-helper", "version": "1.0.0"}}
			if failInitialize {
				delete(response, "result")
				response["error"] = map[string]any{"code": -32603, "message": "fixture initialization failure"}
			}
		case "tools/list":
			response["result"] = map[string]any{"tools": []map[string]any{{"name": "read_after_gate", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"gate": map[string]any{"type": "string"}}, "required": []string{"gate"}}}}}
		case "tools/call":
			args, _ := request.Params["arguments"].(map[string]any)
			gate, _ := args["gate"].(string)
			wait, err := http.Get(gate)
			if err != nil {
				os.Exit(3)
			}
			_ = wait.Body.Close()
			content, readErr := os.ReadFile(filepath.Join(os.Getenv("PLUGIN_ROOT"), "lease.txt"))
			failure := ""
			if readErr != nil {
				failure = readErr.Error()
			}
			response["result"] = map[string]any{"content": []any{}, "structuredContent": map[string]any{"value": string(content), "read_error": failure}}
		default:
			response["error"] = map[string]any{"code": -32601, "message": "Method not found"}
		}
		if output.Encode(response) != nil {
			os.Exit(4)
		}
	}
	os.Exit(0)
}

func TestLoadReportsDisabledDirectPluginMembers(t *testing.T) {
	store, err := registry.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := writeToolPluginPackage(t, t.TempDir(), "domain", []string{"workflow"}, []string{"remote"})
	service := testPluginService(store, false)
	if _, err := service.Manage(context.Background(), ManageRequest{Action: "install", Source: source, ReviewToken: reviewTokenForSource(t, service, source)}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Manage(context.Background(), ManageRequest{Action: "member_disable", Name: "domain", MemberType: "skill", Member: "workflow"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Manage(context.Background(), ManageRequest{Action: "member_disable", Name: "domain", MemberType: "mcp_server", Member: "remote"}); err != nil {
		t.Fatal(err)
	}

	loaded, err := service.Load(context.Background(), LoadRequest{Name: "domain"})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(loaded["skills"].([]SkillItem)); got != 0 {
		t.Fatalf("enabled skills = %d, want 0", got)
	}
	if got := len(loaded["mcp_servers"].([]MCPItem)); got != 0 {
		t.Fatalf("enabled MCP servers = %d, want 0", got)
	}
	unavailable := loaded["unavailable_members"].([]map[string]any)
	if len(unavailable) != 2 || unavailable[0]["reason"] != "disabled" || unavailable[1]["reason"] != "disabled" {
		t.Fatalf("unavailable = %#v", unavailable)
	}
}

func TestLoadKeepsSkillsWhenPluginMCPToolDiscoveryFails(t *testing.T) {
	store, err := registry.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := writeToolPluginPackage(t, t.TempDir(), "domain", []string{"workflow"}, []string{"remote"})
	service := testPluginService(store, true)
	if _, err := service.Manage(context.Background(), ManageRequest{Action: "install", Source: source, ReviewToken: reviewTokenForSource(t, service, source)}); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Load(context.Background(), LoadRequest{Name: "domain"})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(loaded["skills"].([]SkillItem)); got != 1 {
		t.Fatalf("loaded Skills = %d, want 1", got)
	}
	servers := loaded["mcp_servers"].([]MCPItem)
	if len(servers) != 1 || servers[0].ToolLoadError == "" {
		t.Fatalf("loaded MCP servers = %#v", servers)
	}
	unavailable := loaded["unavailable_members"].([]map[string]any)
	if len(unavailable) != 1 || unavailable[0]["reason"] != "tool_discovery_failed" || unavailable[0]["code"] != "MCP_TIMEOUT" {
		t.Fatalf("unavailable members = %#v", unavailable)
	}
}

func TestDisabledDirectPluginCannotLoad(t *testing.T) {
	store, err := registry.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := writeToolPluginPackage(t, t.TempDir(), "domain", []string{"workflow"}, nil)
	service := testPluginService(store, false)
	if _, err := service.Manage(context.Background(), ManageRequest{Action: "install", Source: source, ReviewToken: reviewTokenForSource(t, service, source)}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Manage(context.Background(), ManageRequest{Action: "disable", Name: "domain"}); err != nil {
		t.Fatal(err)
	}
	_, err = service.Load(context.Background(), LoadRequest{Name: "domain"})
	var toolErr *toolcore.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "PLUGIN_DISABLED" {
		t.Fatalf("Load disabled plugin error = %#v", err)
	}
}

func TestManageValidatesAndUpdatesDirectPlugin(t *testing.T) {
	store, err := registry.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := testPluginService(store, false)
	v1 := writeToolPluginPackageVersion(t, t.TempDir(), "domain", "1.0.0", []string{"workflow"}, nil)
	validated, err := service.Manage(context.Background(), ManageRequest{Action: "validate", Source: v1})
	if err != nil || validated["valid"] != true {
		t.Fatalf("validate result=%#v err=%v", validated, err)
	}
	review := validated["review"].(registry.Review)
	if review.ReviewToken == "" {
		t.Fatal("validate did not return review_token")
	}
	if _, err := service.Manage(context.Background(), ManageRequest{Action: "install", Source: v1, ReviewToken: review.ReviewToken}); err != nil {
		t.Fatal(err)
	}
	v2 := writeToolPluginPackageVersion(t, t.TempDir(), "domain", "2.0.0", []string{"workflow", "review"}, nil)
	v2Token := reviewTokenForSource(t, service, v2)
	if _, err := service.Manage(t.Context(), ManageRequest{Action: "update", Name: "domain", Source: v2, ReviewToken: v2Token}); err == nil {
		t.Fatal("source rebinding without confirmation was accepted")
	}
	updated, err := service.Manage(context.Background(), ManageRequest{Action: "update", Name: "domain", Source: v2, ReviewToken: v2Token, ConfirmedSourceChange: true})
	if err != nil {
		t.Fatal(err)
	}
	definition := updated["plugin"].(registry.Definition)
	if definition.Version != "2.0.0" || len(definition.Skills) != 2 {
		t.Fatalf("updated plugin = %#v", definition)
	}
	other := writeToolPluginPackageVersion(t, t.TempDir(), "other", "2.0.0", []string{"other-workflow"}, nil)
	_, err = service.Manage(context.Background(), ManageRequest{Action: "update", Name: "domain", Source: other, ReviewToken: reviewTokenForSource(t, service, other)})
	var toolErr *toolcore.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "PLUGIN_UPDATE_NAME_MISMATCH" {
		t.Fatalf("mismatched update error = %#v", err)
	}
}

func TestManageBindsInstallToReviewedCandidate(t *testing.T) {
	store, err := registry.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := testPluginService(store, false)
	source := writeToolPluginPackage(t, t.TempDir(), "reviewed", []string{"workflow"}, nil)

	firstToken := reviewTokenForSource(t, service, source)
	if secondToken := reviewTokenForSource(t, service, source); secondToken != firstToken {
		t.Fatalf("stable candidate returned different review tokens: %q != %q", secondToken, firstToken)
	}
	_, err = service.Manage(t.Context(), ManageRequest{Action: "install", Source: source, Confirmed: true})
	assertPluginToolErrorCode(t, err, "PLUGIN_REVIEW_REQUIRED")

	skillPath := filepath.Join(source, "skills", "workflow", "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("---\nname: workflow\ndescription: Changed after review.\n---\n\n# Changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = service.Manage(t.Context(), ManageRequest{Action: "install", Source: source, ReviewToken: firstToken})
	assertPluginToolErrorCode(t, err, "PLUGIN_REVIEW_CHANGED")

	refreshedToken := reviewTokenForSource(t, service, source)
	if refreshedToken == firstToken {
		t.Fatal("changed candidate kept the previous review token")
	}
	if _, err := service.Manage(t.Context(), ManageRequest{Action: "install", Source: source, ReviewToken: refreshedToken}); err != nil {
		t.Fatal(err)
	}
}

func reviewTokenForSource(t *testing.T, service *Service, source string) string {
	t.Helper()
	result, err := service.Manage(t.Context(), ManageRequest{Action: "validate", Source: source})
	if err != nil {
		t.Fatal(err)
	}
	review, ok := result["review"].(registry.Review)
	if !ok || !review.Valid || review.ReviewToken == "" {
		t.Fatalf("validate result = %#v", result)
	}
	return review.ReviewToken
}

func assertPluginToolErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var toolErr *toolcore.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != code {
		t.Fatalf("error = %#v, want %s", err, code)
	}
}

func testPluginService(store *registry.Store, discoveryFailure bool) *Service {
	return New(
		store,
		func(name string) (SkillItem, bool, error) {
			member, found, err := store.Skill(name)
			if err != nil || !found {
				return SkillItem{}, found, err
			}
			return SkillItem{
				Name: name, Description: "Plugin workflow", File: "skill://" + name + "/SKILL.md",
				Enabled: member.Enabled, Plugin: member.Plugin,
			}, true, nil
		},
		func(_ context.Context, name string, expand bool) (MCPItem, bool, error) {
			membership, found, err := store.MCPMembership(name)
			if err != nil || !found {
				return MCPItem{}, found, err
			}
			item := MCPItem{Name: name, Description: "Plugin MCP", Plugin: membership.Plugin, Status: "ready", ToolCount: 1, Enabled: membership.Enabled}
			if expand && membership.Enabled {
				if discoveryFailure {
					item.Status = "error"
					item.LastErrorCode = "MCP_TIMEOUT"
					item.ToolLoadError = "tools/list timed out"
				} else {
					item.Tools = []MCPToolItem{{Name: "route", QualifiedName: name + ":route", Description: "Route PCB traces", Server: name}}
				}
			}
			return item, true, nil
		},
	)
}

func writeToolPluginPackage(t *testing.T, parent, name string, skills, servers []string) string {
	t.Helper()
	return writeToolPluginPackageVersion(t, parent, name, "1.0.0", skills, servers)
}

func writeToolPluginPackageVersion(t *testing.T, parent, name, version string, skillNames, serverNames []string) string {
	t.Helper()
	root := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(root, registry.ManifestDirectory), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, skillName := range skillNames {
		dir := filepath.Join(root, "skills", skillName)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		doc := fmt.Sprintf("---\nname: %s\ndescription: Plugin Skill %s.\nversion: 1.0.0\n---\n\n# %s\n", skillName, skillName, skillName)
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mcpServers := map[string]registry.MCPServer{}
	for _, serverName := range serverNames {
		mcpServers[serverName] = registry.MCPServer{Type: "stdio", Command: "test-server"}
	}
	mcpData, err := json.Marshal(registry.MCPConfig{Schema: registry.MCPSchema, MCPServers: mcpServers})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, registry.MCPFilename), mcpData, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := registry.Manifest{Schema: registry.ManifestSchema, Name: name, Description: "Plugin " + name + ".", Version: version,
		Extensions: map[string]any{registry.ExtensionNamespace: map[string]any{"heavy": true}}}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, registry.ManifestDirectory, registry.ManifestFilename), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
