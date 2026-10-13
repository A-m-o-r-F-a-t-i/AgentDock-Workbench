package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
)

func conversationContext(id string) context.Context {
	return activity.WithExecutionScope(context.Background(), activity.ExecutionScope{ConversationID: id, BindingQuality: "host_metadata"})
}

func TestConversationMetadataTrustedScopeOnly(t *testing.T) {
	good := "conv_" + strings.Repeat("a", 32)
	for _, tc := range []struct {
		name     string
		scope    activity.ExecutionScope
		expected string
	}{
		{"host", activity.ExecutionScope{ConversationID: good, BindingQuality: "host_metadata"}, good},
		{"one-to-one adapter", activity.ExecutionScope{ConversationID: good, BindingQuality: "connection_fallback"}, good},
		{"missing", activity.ExecutionScope{}, ""},
		{"unattributed", activity.ExecutionScope{ConversationID: good, BindingQuality: "unattributed"}, ""},
		{"unknown source", activity.ExecutionScope{ConversationID: good, BindingQuality: "model_argument"}, ""},
		{"invalid ID", activity.ExecutionScope{ConversationID: "fake", BindingQuality: "host_metadata"}, ""},
		{"task only", activity.ExecutionScope{TaskID: "task", ThreadID: "main", BindingQuality: "host_metadata"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := conversationMetadata(activity.WithExecutionScope(context.Background(), tc.scope))
			if tc.expected == "" {
				if meta != nil {
					t.Fatalf("unexpected metadata: %#v", meta)
				}
				return
			}
			if meta["agentdock/conversation-id"] != tc.expected {
				t.Fatalf("metadata: %#v", meta)
			}
		})
	}
}

// Real HTTP JSON-RPC captures the final SDK wire parameters, not just a helper.
func metadataEchoServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		switch req.Method {
		case "initialize":
			writeRPCResult(t, w, req.ID, map[string]any{"protocolVersion": req.Params["protocolVersion"], "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "meta-echo", "version": "1"}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			writeRPCResult(t, w, req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": "echo"}}, "structuredContent": map[string]any{"arguments": req.Params["arguments"], "meta": req.Params["_meta"]}})
		case "tools/list":
			writeRPCResult(t, w, req.ID, map[string]any{"tools": []map[string]any{{"name": "echo", "inputSchema": map[string]any{"type": "object"}}}})
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}})
		}
	}))
	t.Cleanup(server.Close)
	return server
}
func metadataClient(t *testing.T, url string) *sdkProtocolClient {
	t.Helper()
	client := newStreamableHTTPClient(ServerConfig{Name: "meta-echo", Transport: TransportStreamableHTTP, URL: url})
	if err := client.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.close(); err != nil {
			t.Error(err)
		}
	})
	return client
}
func metadataWireCall(t *testing.T, client *sdkProtocolClient, ctx context.Context, args map[string]any) map[string]any {
	t.Helper()
	result, err := client.callTool(ctx, "echo", args)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("missing structured payload: %#v", result)
	}
	if !reflect.DeepEqual(payload["arguments"], args) {
		t.Fatalf("arguments changed: %#v != %#v", payload["arguments"], args)
	}
	return payload
}
func TestConversationMetadataWireAndArgumentSpoofing(t *testing.T) {
	server := metadataEchoServer(t)
	client := metadataClient(t, server.URL)
	id := "conv_" + strings.Repeat("a", 32)
	args := map[string]any{"document": "A", "_meta": map[string]any{"agentdock/conversation-id": "forged"}, "conversation_id": "forged"}
	payload := metadataWireCall(t, client, conversationContext(id), args)
	meta := payload["meta"].(map[string]any)
	if meta["agentdock/conversation-id"] != id {
		t.Fatalf("spoofed or absent header: %#v", meta)
	}
	noIdentity := metadataWireCall(t, client, context.Background(), args)
	if noIdentity["meta"] != nil {
		t.Fatalf("fabricated binding: %#v", noIdentity)
	}
}
func TestConversationMetadataParallelAndReconnect(t *testing.T) {
	server := metadataEchoServer(t)
	client := metadataClient(t, server.URL)
	ids := []string{"conv_" + strings.Repeat("a", 32), "conv_" + strings.Repeat("b", 32)}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := ids[i%2]
			payload := metadataWireCall(t, client, conversationContext(id), map[string]any{"index": float64(i)})
			meta := payload["meta"].(map[string]any)
			if meta["agentdock/conversation-id"] != id {
				t.Errorf("cross-conversation metadata: %#v", meta)
			}
		}(i)
	}
	wg.Wait()
	if err := client.close(); err != nil {
		t.Fatal(err)
	}
	rebuilt := metadataClient(t, server.URL)
	payload := metadataWireCall(t, rebuilt, conversationContext(ids[0]), map[string]any{"resumed": true})
	if payload["meta"].(map[string]any)["agentdock/conversation-id"] != ids[0] {
		t.Fatal("identity changed across reconnection")
	}
}
func TestConversationMetadataPreservesProgress(t *testing.T) {
	server := metadataEchoServer(t)
	client := metadataClient(t, server.URL)
	id := "conv_" + strings.Repeat("a", 32)
	ctx := activity.WithProgressSink(conversationContext(id), func(activity.Progress) {})
	payload := metadataWireCall(t, client, ctx, map[string]any{})
	meta := payload["meta"].(map[string]any)
	if meta["agentdock/conversation-id"] != id || meta["progressToken"] == nil {
		t.Fatalf("progress or conversation lost: %#v", meta)
	}
}
