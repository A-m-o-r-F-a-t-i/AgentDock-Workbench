package mcp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/agentinstructions"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
)

func writeBootstrapInstructions(t *testing.T, dir, text string) string {
	t.Helper()
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func connectInstructionClient(t *testing.T, server *Server) *mcpsdk.ClientSession {
	t.Helper()
	clientInput, serverOutput := io.Pipe()
	serverInput, clientOutput := io.Pipe()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.ServeStdio(serverInput, serverOutput) }()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "instruction-test", Version: "1.0.0"}, nil)
	// T.Context is canceled before Cleanup; this session must remain alive
	// until Cleanup closes it and observes the server's orderly shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	session, err := client.Connect(ctx, &mcpsdk.IOTransport{Reader: clientInput, Writer: clientOutput}, nil)
	if err != nil {
		cancel()
		_ = clientInput.Close()
		_ = clientOutput.Close()
		_ = serverInput.Close()
		_ = serverOutput.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer cancel()
		defer clientInput.Close()
		defer clientOutput.Close()
		defer serverInput.Close()
		defer serverOutput.Close()
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		select {
		case err := <-serverDone:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("instruction test server did not stop")
		}
	})
	return session
}

// Scoped file assertions use the same retained-context reconstruction as the
// transport contract tests; initialize carries only the device contract.
func readMCPInstructionSnapshot(t *testing.T, session *mcpsdk.ClientSession, workdir string) agentinstructions.Snapshot {
	t.Helper()
	arguments := map[string]any{"workdir": workdir}
	result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "agentdock_context", Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("MCP context failed: %#v", result)
	}
	encoded, err := json.Marshal(requiredSDKPayload(t, session, result, arguments, nil))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Instructions agentinstructions.Snapshot `json:"instruction_files"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	return got.Instructions
}

func TestInstructionBootstrapAndLiveContextThroughMCP(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "automatic"
		if explicit {
			name = "explicit global override"
		}
		t.Run(name, func(t *testing.T) {
			cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()}
			global := writeBootstrapInstructions(t, cfg.AgentDockHome, "global-before-marker")
			writeBootstrapInstructions(t, cfg.AgentDockDefaultDir, "workspace-before-marker")
			if explicit {
				cfg.InstructionsFile = global
			}
			if err := cfg.Normalize(); err != nil {
				t.Fatal(err)
			}
			rt, err := app.NewRuntime(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = rt.Close() })
			session := connectInstructionClient(t, NewServer(rt, cfg))
			initialized := session.InitializeResult()
			if initialized == nil || initialized.Instructions != deviceRuntimeInstructions {
				t.Fatalf("initialize must carry one stable device contract: %#v", initialized)
			}
			initial := initialized.Instructions
			for _, invariant := range []string{"agentdock_context", "tools/list", "apply_patch", "origin call identity", "response_additions"} {
				if !strings.Contains(initial, invariant) {
					t.Fatalf("startup guidance missing %s", invariant)
				}
			}
			for _, marker := range []string{"global-before-marker", "workspace-before-marker"} {
				if strings.Contains(initial, marker) {
					t.Fatalf("initialize duplicated scoped file %q: %s", marker, initial)
				}
			}
			before := readMCPInstructionSnapshot(t, session, cfg.AgentDockDefaultDir)
			if len(before.Files) != 2 || before.Files[0].Content != "global-before-marker" || before.Files[1].Content != "workspace-before-marker" {
				t.Fatalf("context must preserve global-before-workspace rule order: %#v", before.Files)
			}
			writeBootstrapInstructions(t, cfg.AgentDockHome, "global-after-marker")
			selected := t.TempDir()
			writeBootstrapInstructions(t, selected, "selected-workspace-marker")
			result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "agentdock_context", Arguments: map[string]any{"workdir": selected}})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("MCP context failed: %#v", result)
			}
			encoded, err := json.Marshal(requiredSDKPayload(t, session, result, map[string]any{"workdir": selected}, nil))
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Instructions agentinstructions.Snapshot `json:"instruction_files"`
			}
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Instructions.Files) != 2 || got.Instructions.Files[0].Content != "global-after-marker" || got.Instructions.Files[1].Content != "selected-workspace-marker" {
				t.Fatalf("live MCP instructions=%s", encoded)
			}
			for _, old := range []string{"global-before-marker", "workspace-before-marker"} {
				if strings.Contains(string(encoded), old) {
					t.Fatalf("live response leaked stale text: %s", old)
				}
			}
			if rt.Workspace().DefaultCWD() == selected {
				t.Fatal("MCP request changed workspace default")
			}
		})
	}
}

func TestInstructionBootstrapOptOutAndFileErrors(t *testing.T) {
	cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir(), AgentsAutoLoadDisabled: true}
	writeBootstrapInstructions(t, cfg.AgentDockHome, "disabled-global-marker")
	writeBootstrapInstructions(t, cfg.AgentDockDefaultDir, "disabled-workspace-marker")
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	rt, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	text := initialServerInstructions(rt, cfg)
	if text != deviceRuntimeInstructions {
		t.Fatal("initialize must carry only the device contract")
	}
	disabledSession := connectInstructionClient(t, NewServer(rt, cfg))
	disabledSnapshot := readMCPInstructionSnapshot(t, disabledSession, cfg.AgentDockDefaultDir)
	encoded, err := json.Marshal(disabledSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if disabledSnapshot.AutoLoad || strings.Contains(string(encoded), "disabled-global-marker") || strings.Contains(string(encoded), "disabled-workspace-marker") {
		t.Fatalf("opt-out still loaded automatic files: %s", encoded)
	}
	cfg.AgentsAutoLoadDisabled = false
	writeBootstrapInstructions(t, cfg.AgentDockHome, strings.Repeat("x", agentinstructions.MaxFileBytes+1))
	enabled, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = enabled.Close() })
	text = initialServerInstructions(enabled, cfg)
	if text != deviceRuntimeInstructions {
		t.Fatal("initialize duplicated a scoped file or file diagnostic")
	}
	enabledSession := connectInstructionClient(t, NewServer(enabled, cfg))
	failedSnapshot := readMCPInstructionSnapshot(t, enabledSession, cfg.AgentDockDefaultDir)
	encoded, err = json.Marshal(failedSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !failedSnapshot.AutoLoad || !strings.Contains(string(encoded), "file_size_limit") {
		t.Fatalf("context did not disclose the scoped file failure: %s", encoded)
	}
	if strings.Contains(string(encoded), strings.Repeat("x", 100)) {
		t.Fatal("oversized scoped file body leaked through context")
	}
}
