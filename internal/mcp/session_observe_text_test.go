package mcp

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
)

func TestSessionObserveEnvelopeKeepsStructuredResultAndUsesCompactText(t *testing.T) {
	structured := map[string]any{
		"count":    0,
		"sessions": []map[string]any{},
		"permission": map[string]any{
			"settings":            strings.Repeat("configured-permission-", 100),
			"configured_settings": strings.Repeat("configured-permission-", 100),
		},
		"agentdock_guidance": map[string]any{"call_id": "call_fixture", "conversation_id": "conv_fixture"},
	}

	envelope := toolEnvelope("session_observe", structured, nil)
	if !reflect.DeepEqual(envelope["structuredContent"], structured) {
		t.Fatal("session_observe compact text changed the structured result")
	}
	text := firstEnvelopeText(t, envelope)
	if !strings.Contains(text, "No command sessions are available") || !strings.Contains(text, "exec_command returns status=running") {
		t.Fatalf("empty-session guidance = %q", text)
	}
	if strings.Contains(text, "permission") || strings.Contains(text, "call_fixture") {
		t.Fatalf("compact text leaked repeated control metadata: %q", text)
	}

	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(map[string]any{
		"isError":           false,
		"structuredContent": structured,
		"content":           []map[string]any{{"type": "text", "text": pretty(structured)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded)*100 >= len(legacy)*60 {
		t.Fatalf("compact envelope bytes = %d, legacy = %d; expected at least 40%% reduction", len(encoded), len(legacy))
	}
	t.Logf("compact envelope bytes=%d legacy=%d reduction=%.1f%%", len(encoded), len(legacy), 100-float64(len(encoded))*100/float64(len(legacy)))
}

func TestSessionObserveStatusTextPreservesCommandOutput(t *testing.T) {
	envelope := toolEnvelope("session_observe", map[string]any{
		"session_id":       "session-fixture",
		"status":           "exited",
		"elapsed_ms":       42,
		"timed_out":        false,
		"exit_code":        7,
		"command_ok":       false,
		"command_error":    "exit status 7",
		"stdout":           "ordinary output",
		"stderr":           "failure output",
		"stdout_truncated": true,
	}, nil)
	text := firstEnvelopeText(t, envelope)
	for _, expected := range []string{
		"session_id=session-fixture",
		"status=exited",
		"exit_code=7",
		"command_ok=false",
		"ordinary output",
		"failure output",
		"Output is truncated",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("status text missing %q: %q", expected, text)
		}
	}
}

func TestSessionObserveErrorTextIsCompact(t *testing.T) {
	envelope := toolEnvelope("session_observe", nil, errors.New("session not found"))
	if envelope["isError"] != true {
		t.Fatalf("error envelope = %#v", envelope)
	}
	text := firstEnvelopeText(t, envelope)
	if text != "session_observe failed: session not found" {
		t.Fatalf("error text = %q", text)
	}
	if strings.Contains(text, "permission") || strings.Contains(text, "agentdock_guidance") {
		t.Fatalf("error text repeated structured metadata: %q", text)
	}
}

func TestSessionObserveServerInvokeReturnsCompactEmptyList(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		AgentDockHome:       filepath.Join(root, "home"),
		AgentDockDefaultDir: root,
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("Runtime.Close() error = %v", err)
		}
	})

	envelope, err := NewServer(runtime, cfg).Invoke(t.Context(), "session_observe", map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("Invoke(session_observe) error = %v", err)
	}
	normalized := normalizedEnvelope(t, envelope)
	if normalized["isError"] == true {
		t.Fatalf("session_observe returned error: %#v", normalized)
	}
	structured := asMap(normalized["structuredContent"])
	if structured["count"] != float64(0) {
		t.Fatalf("structured count = %#v, want 0", structured["count"])
	}
	text := firstEnvelopeText(t, normalized)
	if len(text) > 256 || !strings.Contains(text, "Do not call session_observe again") {
		t.Fatalf("compact MCP text = %q", text)
	}
	if strings.Contains(text, "permission") || strings.Contains(text, "configured_settings") {
		t.Fatalf("MCP text repeated structured control metadata: %q", text)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("empty-list MCP envelope bytes=%d text_bytes=%d", len(encoded), len(text))
}

func firstEnvelopeText(t *testing.T, envelope map[string]any) string {
	t.Helper()
	normalized := normalizedEnvelope(t, envelope)
	blocks, ok := normalized["content"].([]any)
	if !ok || len(blocks) == 0 {
		t.Fatalf("content = %#v", normalized["content"])
	}
	text, _ := asMap(blocks[0])["text"].(string)
	if text == "" {
		t.Fatalf("first content block = %#v", blocks[0])
	}
	return text
}
