package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/config"
)

func TestDynamicMCPForwardsResolvedConversationMetadata(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			writeDynamicMCPRPCResult(t, w, req.ID, map[string]any{"protocolVersion": req.Params["protocolVersion"], "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "wire-meta", "version": "1"}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeDynamicMCPRPCResult(t, w, req.ID, map[string]any{"tools": []map[string]any{{"name": "echo", "inputSchema": map[string]any{"type": "object"}}}})
		case "tools/call":
			writeDynamicMCPRPCResult(t, w, req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": "echo"}}, "structuredContent": map[string]any{"meta": req.Params["_meta"], "arguments": req.Params["arguments"]}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}})
		}
	}))
	defer upstream.Close()
	cfg := config.Config{AgentDockDefaultDir: t.TempDir(), AgentDockHome: filepath.Join(t.TempDir(), ".agentdock")}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := newUnrestrictedTestRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := runtime.Call(context.Background(), "mcp_manage", map[string]any{"action": "add", "name": "metadata", "description": "Metadata forwarding test", "transport": "streamable_http", "url": upstream.URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Call(context.Background(), "mcp_tool_search", map[string]any{"server": "metadata", "query": "echo"}); err != nil {
		t.Fatal(err)
	}
	call := func(host string) string {
		ctx := activity.WithSource(context.Background(), activity.Source{HostConversationID: host})
		args := map[string]any{"target": "board-" + host, "_meta": map[string]any{"agentdock/conversation-id": "forged"}}
		called, err := runtime.Call(ctx, "mcp_tool_call", map[string]any{"name": "metadata:echo", "arguments": args})
		if err != nil {
			t.Errorf("forward: %v", err)
			return ""
		}
		remote, _ := called["result"].(map[string]any)
		structured, _ := remote["structuredContent"].(map[string]any)
		meta, _ := structured["meta"].(map[string]any)
		if !reflect.DeepEqual(structured["arguments"], args) {
			t.Errorf("third-party arguments changed: %#v", structured)
		}
		conversation, _ := called["conversation_id"].(string)
		if host == "" {
			if meta["agentdock/conversation-id"] != nil {
				t.Errorf("fabricated scope: %#v", meta)
			}
			return ""
		}
		if conversation == "" || meta["agentdock/conversation-id"] != conversation {
			t.Errorf("host scope did not reach wire: conv=%s meta=%#v", conversation, meta)
		}
		return conversation
	}
	a, b := call("host-A"), call("host-B")
	if a == "" || b == "" || a == b {
		t.Fatalf("conversation isolation: %q %q", a, b)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			host, expected := "host-A", a
			if i%2 == 1 {
				host, expected = "host-B", b
			}
			if got := call(host); got != expected {
				t.Errorf("conversation changed: %s != %s", got, expected)
			}
		}(i)
	}
	wg.Wait()
	call("")
}
