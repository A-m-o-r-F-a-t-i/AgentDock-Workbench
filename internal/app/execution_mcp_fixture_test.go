package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type executionMCPFixture struct {
	server *httptest.Server
	calls  atomic.Int32
}

func newExecutionMCPFixture(t *testing.T, toolName string, inputSchema map[string]any, annotations map[string]any) *executionMCPFixture {
	t.Helper()
	fixture := &executionMCPFixture{}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
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
			t.Errorf("decode execution MCP fixture request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch rpc.Method {
		case "server/discover":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpc.ID,
				"error":   map[string]any{"code": -32601, "message": "Method not found"},
			})
		case "initialize":
			writeDynamicMCPRPCResult(t, w, rpc.ID, map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "execution-snapshot-fixture", "version": "1.0.0"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			tool := map[string]any{
				"name":        toolName,
				"description": "Execution snapshot fixture tool",
				"inputSchema": inputSchema,
			}
			if annotations != nil {
				tool["annotations"] = annotations
			}
			writeDynamicMCPRPCResult(t, w, rpc.ID, map[string]any{"tools": []map[string]any{tool}})
		case "tools/call":
			fixture.calls.Add(1)
			writeDynamicMCPRPCResult(t, w, rpc.ID, map[string]any{
				"content":           []map[string]any{{"type": "text", "text": "executed"}},
				"structuredContent": map[string]any{"executed": true, "arguments": rpc.Params["arguments"]},
			})
		default:
			t.Errorf("unexpected execution MCP fixture method %q", rpc.Method)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func executionMCPWriteSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"value"},
		"properties":           map[string]any{"value": map[string]any{"type": "string"}},
	}
}
