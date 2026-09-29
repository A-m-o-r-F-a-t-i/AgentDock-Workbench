package httpx

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/mcp"
	"github.com/uvwt/agentdock/internal/requesttrace"
)

func TestContextTransportProtocolErrorsRemainCorrelated(t *testing.T) {
	cfg := testConfig(t)
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	handler := loggingMiddleware(mcpEndpointHandler(mcp.NewServer(runtime, cfg), cfg, nil))
	for _, row := range []struct{ stage, params string }{
		{"tool_resolution", `{"name":"unknown_fixture","arguments":{}}`},
		{"mcp_metadata", `{"name":"agentdock_context","arguments":{},"_meta":{"agentdock/context-response-v1":{"structured":false,"text":"summary"}}}`},
	} {
		t.Run(row.stage, func(t *testing.T) {
			req := newMCPRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":`+row.params+`}`))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			var response struct {
				Error struct {
					Code int            `json:"code"`
					Data map[string]any `json:"data"`
				} `json:"error"`
			}
			if err = json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			data := response.Error.Data
			if response.Error.Code != -32602 || data["stage"] != row.stage || data["request_id"] != rec.Header().Get(requesttrace.Header) || data["handler_dispatched"] != false || data["retryable"] != false {
				t.Fatalf("missing pre-dispatch evidence: %s", rec.Body.String())
			}
		})
	}
}

func TestContextTransportProbeThroughRealHTTPServer(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("Python is required for the external protocol probe")
	}
	cfg := testConfig(t)
	cfg.AuthToken = "probe-fixture-token"
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	server := httptest.NewServer(loggingMiddleware(mcpEndpointHandler(mcp.NewServer(runtime, cfg), cfg, nil)))
	t.Cleanup(server.Close)
	token := filepath.Join(t.TempDir(), "token")
	if err = os.WriteFile(token, []byte(cfg.AuthToken), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), python, "../../scripts/diagnostics/probe-context.py", "--endpoint", server.URL+"/mcp", "--token-file", token, "--summary")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("probe: %v %s", err, output)
	}
	var report struct {
		OK        bool             `json:"ok"`
		RequestID string           `json:"request_id"`
		CallID    string           `json:"call_id"`
		Samples   []map[string]any `json:"samples"`
	}
	if err = json.Unmarshal(output, &report); err != nil || !report.OK || !requesttrace.ValidID(report.RequestID) || report.CallID == "" || len(report.Samples) != 4 {
		t.Fatalf("probe report: %s %v", output, err)
	}
	for _, sample := range report.Samples {
		if sample["correlation_echoed"] != true {
			t.Fatal("probe did not trace all HTTP requests")
		}
	}
	if strings.Contains(string(output), cfg.AuthToken) {
		t.Fatal("probe leaked credential")
	}
}
