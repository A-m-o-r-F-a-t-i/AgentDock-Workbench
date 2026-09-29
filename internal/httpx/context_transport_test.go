package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/mcp"
	"github.com/uvwt/agentdock/internal/requesttrace"
)

func TestContextTransportHTTPResponseAndActivityShareRequestID(t *testing.T) {
	cfg := testConfig(t)
	cfg.AuthToken = "transport-fixture-secret"
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	handler := loggingMiddleware(mcpEndpointHandler(mcp.NewServer(runtime, cfg), cfg, nil))
	id := "req_" + strings.Repeat("d", 32)
	var previousCall, previousConversation string
	for _, host := range []string{"context-A", "context-B"} {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"agentdock_context","arguments":{},"_meta":{"openai/session":"` + host + `","agentdock/context-response-v1":{"structured":true,"text":"summary"}}}}`
		req := newMCPRequest("POST", "/mcp", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		req.Header.Set(requesttrace.Header, id)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 200 || rec.Header().Get(requesttrace.Header) != id {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var response struct {
			Result struct {
				IsError    bool           `json:"isError"`
				Structured map[string]any `json:"structuredContent"`
				Meta       map[string]any `json:"_meta"`
			} `json:"result"`
			Error any `json:"error"`
		}
		if err = json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.Error != nil || response.Result.IsError {
			t.Fatalf("bad response: %s %v", rec.Body.String(), err)
		}
		guidance := response.Result.Structured["agentdock_guidance"].(map[string]any)
		transport := response.Result.Meta["agentdock/transport"].(map[string]any)
		call := guidance["call_id"].(string)
		if guidance["request_id"] != id || transport["request_id"] != id || transport["call_id"] != call || transport["handler_dispatched"] != true || call == previousCall {
			t.Fatal("correlation lost or used for execution identity")
		}
		previousCall = call
		conversation, _ := guidance["conversation_id"].(string)
		if conversation == "" || conversation == previousConversation {
			t.Fatal("request ID was used as conversation identity")
		}
		previousConversation = conversation
		store, err := activity.New(filepath.Join(cfg.AgentDockHome, "tasks", "activity"), activity.Options{})
		if err != nil {
			t.Fatal(err)
		}
		saved, err := store.Call(t.Context(), call)
		if err != nil || saved.RequestID != id || saved.ConversationID != guidance["conversation_id"] || saved.Status != "succeeded" {
			t.Fatalf("journal correlation: %+v %v", saved, err)
		}
	}
}

type failingTransportWriter struct{ header http.Header }

func (w *failingTransportWriter) Header() http.Header { return w.header }
func (w *failingTransportWriter) WriteHeader(int)     {}
func (w *failingTransportWriter) Write([]byte) (int, error) {
	return 0, errors.New("do not log transport secret")
}

func TestContextTransportIngressLogsFailureWithoutSecrets(t *testing.T) {
	previous := slog.Default()
	defer slog.SetDefault(previous)
	for _, mode := range []string{"cancel", "write", "panic", "invalid_id"} {
		t.Run(mode, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			handler := loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requesttrace.Stage(r.Context(), "authentication")
				if mode == "panic" {
					panic("do not log panic secret")
				}
				_, _ = w.Write([]byte("private body"))
			}))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			req := httptest.NewRequest("POST", "/mcp", strings.NewReader("private request")).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer auth-secret")
			req.Header.Set(requesttrace.Header, "header-secret")
			var writer http.ResponseWriter = httptest.NewRecorder()
			if mode == "write" {
				writer = &failingTransportWriter{header: make(http.Header)}
			}
			recovered := false
			func() {
				defer func() {
					if recover() != nil {
						recovered = true
					}
				}()
				handler.ServeHTTP(writer, req)
			}()
			if recovered != (mode == "panic") {
				t.Fatal("middleware altered panic ownership")
			}
			text := logs.String()
			for _, secret := range []string{"auth-secret", "header-secret", "private request", "private body", "do not log"} {
				if strings.Contains(text, secret) {
					t.Fatal("secret logged")
				}
			}
			if !strings.Contains(text, "http request received") || !strings.Contains(text, "request_id") {
				t.Fatal("missing ingress evidence")
			}
			if mode == "cancel" && !strings.Contains(text, `"request_cancelled":true`) {
				t.Fatal("cancellation hidden")
			}
			if mode == "write" && !strings.Contains(text, `"response_write_failed":true`) {
				t.Fatal("write failure hidden")
			}
			if mode == "panic" && !strings.Contains(text, `"handler_panicked":true`) {
				t.Fatal("panic hidden")
			}
		})
	}
}
