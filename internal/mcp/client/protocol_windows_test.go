//go:build windows

package client

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerStdioFlowThroughCommandScript(t *testing.T) {
	t.Setenv("TEST_MCP_HELPER_MODE", "1")
	root := t.TempDir()
	script := "@echo off\r\n\"%TEST_EXECUTABLE%\" -test.run=TestMCPStdioHelperProcess\r\n"
	if err := os.WriteFile(filepath.Join(root, "run-server.cmd"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_EXECUTABLE", os.Args[0])

	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	_, err = manager.Add(ServerConfig{
		Name:        "cmd-wrapper",
		Description: "Command script stdio MCP",
		Transport:   TransportStdio,
		Command:     "cmd.exe",
		Args:        []string{"/d", "/s", "/c", "call", "run-server.cmd"},
		Cwd:         root,
		EnvFromEnv: map[string]string{
			"GO_WANT_MCP_HELPER": "TEST_MCP_HELPER_MODE",
			"TEST_EXECUTABLE":    "TEST_EXECUTABLE",
		},
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Refresh(context.Background(), "cmd-wrapper"); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Call(context.Background(), "cmd-wrapper:echo", map[string]any{"text": "stdio-cmd"})
	if err != nil {
		t.Fatal(err)
	}
	structured, _ := result["structuredContent"].(map[string]any)
	if structured["echo"] != "stdio-cmd" {
		t.Fatalf("unexpected command-script stdio result: %#v", result)
	}
}
