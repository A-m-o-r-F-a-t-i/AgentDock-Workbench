package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

// This test is also copied to the historical failure-audit commit. Keep its
// dependencies compatible with that version; current routing contracts have
// their own tests in discovery_routing_test.go and discovery_recovery_test.go.
func TestAuditDiscoveryCredentialIsolationPortable(t *testing.T) {
	cfg := config.Config{AgentDockDefaultDir: t.TempDir(), AgentDockHome: filepath.Join(t.TempDir(), "home")}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	rt, err := newUnrestrictedTestRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	const missing = "AGENTDOCK_PORTABLE_AUDIT_MISSING_CREDENTIAL"
	t.Setenv(missing, "")
	if err := os.Unsetenv(missing); err != nil {
		t.Fatal(err)
	}
	_, err = rt.Call(t.Context(), "mcp_manage", map[string]any{
		"action": "add", "name": "unrelated-artifact-fetch", "description": "Isolated historical failure fixture",
		"transport": "stdio", "command": os.Args[0],
		"env_from_env": map[string]any{"REQUIRED": missing},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.Call(t.Context(), "mcp_tool_search", map[string]any{"query": "mcp_tool_call"})
	var classified *ToolError
	if errors.As(err, &classified) && classified.Code == "MCP_AUTH_REQUIRED" {
		t.Fatalf("unscoped built-in search was poisoned by unrelated credentials: %v", err)
	}
	// Versions may either hand off successfully or reject the wrong namespace.
	// Neither may contact the unrelated credential-dependent service.
	if err != nil && (!errors.As(err, &classified) || classified.Code != "HOST_BUILTIN_NOT_DYNAMIC_MCP" || classified.Retryable) {
		t.Fatalf("unexpected discovery error: %v", err)
	}
}
