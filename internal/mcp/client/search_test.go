package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuditCachedDiscoveryKeepsStaleResultsWithoutRefresh(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		requests.Add(1)
		var rpc struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result any
		switch rpc.Method {
		case "server/discover":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}})
			return
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "audit", "version": "1.0"}}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{
				{"name": "echo_a", "inputSchema": map[string]any{"type": "object"}},
				{"name": "echo_b", "inputSchema": map[string]any{"type": "object"}},
			}}
		default:
			http.Error(w, "unexpected operation", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
	}))
	defer server.Close()
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Add(ServerConfig{Name: "demo", Description: "Audit cached discovery fixture", Transport: TransportStreamableHTTP, URL: server.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Catalog(t.Context(), "demo"); err != nil {
		t.Fatal(err)
	}
	before := requests.Load()
	m.mu.RLock()
	state := m.states["demo"]
	m.mu.RUnlock()
	state.mu.Lock()
	state.refreshedAt = time.Now().Add(-2 * catalogMaxAge)
	publishStateLocked(state)
	// Broad discovery must not wait for a busy server's business-call lock.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	result, err := m.SearchCatalogsFiltered(ctx, "echo", "", 1, nil)
	cancel()
	state.mu.Unlock()
	if err != nil || !result.CacheOnly || result.Complete || !result.Truncated || len(result.Tools) != 1 || result.Tools[0].QualifiedName != "demo:echo_a" {
		t.Fatalf("stale bounded discovery = %#v, %v", result, err)
	}
	if len(result.Catalogs) != 1 || result.Catalogs[0]["stale"] != true || result.Catalogs[0]["tool_count_known"] != true || requests.Load() != before {
		t.Fatalf("stale discovery refreshed or hid metadata: %#v requests=%d", result, requests.Load())
	}
	result, err = m.SearchCatalogsFiltered(t.Context(), "echo", "", 10, func(string) bool { return false })
	if err != nil || len(result.Tools) != 0 || len(result.Catalogs) != 0 || requests.Load() != before {
		t.Fatalf("hidden service leaked through discovery: %#v %v", result, err)
	}
	if _, err := m.SearchCatalogsFiltered(t.Context(), "echo", "demo", 10, func(string) bool { return false }); err == nil {
		t.Fatal("explicit hidden service was accepted")
	}
	result, err = m.SearchCatalogsFiltered(t.Context(), "echo", "demo", 10, nil)
	if err != nil || !result.Complete || result.CacheOnly || len(result.Tools) != 2 || requests.Load() <= before {
		t.Fatalf("explicit selected service was not refreshed: %#v %v", result, err)
	}
}

func TestAuditDiscoveryPreservesRegistryAndCancellationErrors(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.SearchCatalogsFiltered(ctx, "echo", "", 10, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was hidden: %v", err)
	}
	if err := m.SetExternalServerProvider(func(context.Context) (map[string]ServerConfig, error) {
		return nil, errors.New("damaged plugin registry")
	}); err == nil {
		t.Fatal("damaged registry provider was accepted")
	}
	_, err = m.SearchCatalogsFiltered(t.Context(), "echo", "", 10, nil)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "MCP_PLUGIN_REGISTRY_READ_FAILED" || !strings.Contains(err.Error(), "plugin") {
		t.Fatalf("registry failure was converted to an empty catalog: %v", err)
	}
}
