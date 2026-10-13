package mcp

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/androidbridge"
	"github.com/uvwt/agentdock/internal/config"
)

func listedToolNames(t *testing.T, h *mcpAppTestHarness) map[string]bool {
	t.Helper()
	result, err := h.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	return names
}

func assertAndroidToolsVisible(t *testing.T, names map[string]bool, visible bool) {
	t.Helper()
	for _, name := range []string{"android_device_read", "android_device_act"} {
		if names[name] != visible {
			t.Fatalf("tool %s visibility=%t, want %t", name, names[name], visible)
		}
	}
}

func TestAndroidToolsEnterAndLeaveLiveMCPDirectoryWithBackend(t *testing.T) {
	root := t.TempDir()
	h := newMCPAppTestHarnessWithApps(t, config.Config{
		AgentDockDefaultDir: root,
		AgentDockHome:       filepath.Join(root, ".agentdock"),
	}, false)
	assertAndroidToolsVisible(t, listedToolNames(t, h), false)

	uid := 2000
	caps := map[string]androidbridge.Capability{"android_shizuku": {Ready: true, State: "verified", UID: &uid}}
	registration, err := h.runtime.AndroidExecutor().Register(androidbridge.RegisterRequest{
		Protocol: 1, WorkerID: strings.Repeat("d", 32), Backends: caps,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAndroidToolsVisible(t, listedToolNames(t, h), true)

	if err := h.runtime.AndroidExecutor().Disconnect(registration.LeaseToken); err != nil {
		t.Fatal(err)
	}
	assertAndroidToolsVisible(t, listedToolNames(t, h), false)
}
