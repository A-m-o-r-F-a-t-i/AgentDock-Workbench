package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uvwt/agentdock/internal/app"
)

func TestRuntimeAPIErrorPreservesExecutionFacts(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeRuntimeAPIHandlerError(recorder, &app.ToolError{
		Code:      "PARTIAL_EFFECT",
		Message:   "operation outcome requires readback",
		Category:  "conflict",
		Retryable: true,
		Details: map[string]any{
			"operation_status":    "failed",
			"failure_class":       "partial",
			"execution_performed": true,
			"effect_state":        "unknown",
			"retry_safe":          false,
			"required_action":     map[string]any{"action": "readback"},
		},
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d", recorder.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "PARTIAL_EFFECT" || body["error_kind"] != "conflict" || body["retryable"] != true || body["effect_state"] != "unknown" || body["retry_safe"] != false {
		t.Fatalf("structured error facts missing: %#v", body)
	}
	if action, ok := body["required_action"].(map[string]any); !ok || action["action"] != "readback" {
		t.Fatalf("required action missing: %#v", body)
	}
}
