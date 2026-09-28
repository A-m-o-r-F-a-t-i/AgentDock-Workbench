package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/insertion"
	"github.com/uvwt/agentdock/internal/requesttrace"
)

func assertContextMeasurements(t *testing.T, result *sdk.CallToolResult) {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	structured, _ := json.Marshal(result.StructuredContent)
	diagnostics := asMap(result.Meta[ContextResponseCapability])
	textBytes := 0
	for _, block := range result.Content {
		if text, ok := block.(*sdk.TextContent); ok {
			textBytes += len(text.Text)
		}
	}
	for key, want := range map[string]int{"response_bytes": len(encoded), "structured_bytes": len(structured), "text_content_bytes": textBytes} {
		number, _ := diagnostics[key].(float64)
		if number != float64(want) {
			t.Fatalf("%s=%v want=%d", key, diagnostics[key], want)
		}
	}
	if diagnostics["response_truncated"] != false {
		t.Fatal("complete structure marked truncated")
	}
}

func TestContextTransportNegotiationPreservesRulesAndMeasuresSDKResult(t *testing.T) {
	h := newMCPAppTestHarnessWithApps(t, config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()}, false)
	rule := strings.Repeat("中文规则必须保留。", 1000)
	if err := os.WriteFile(filepath.Join(h.runtime.Config().AgentDockDefaultDir, "AGENTS.md"), []byte(rule), 0600); err != nil {
		t.Fatal(err)
	}
	var fullBytes int
	for _, mode := range []string{"full", "summary"} {
		meta := sdk.Meta{}
		if mode == "summary" {
			meta[ContextResponseCapability] = map[string]any{"structured": true, "text": "summary"}
		}
		result, err := h.session.CallTool(t.Context(), &sdk.CallToolParams{Name: "agentdock_context", Arguments: map[string]any{}, Meta: meta})
		if err != nil || result.IsError {
			t.Fatalf("%s: result=%+v error=%v", mode, result, err)
		}
		encoded, _ := json.Marshal(result.StructuredContent)
		if !strings.Contains(string(encoded), rule) {
			t.Fatal("full rule omitted from structured result")
		}
		assertContextMeasurements(t, result)
		first := result.Content[0].(*sdk.TextContent).Text
		if mode == "full" {
			var compat any
			if json.Unmarshal([]byte(first), &compat) != nil || !reflect.DeepEqual(compat, result.StructuredContent) {
				t.Fatal("legacy text is no longer complete JSON")
			}
			data, _ := json.Marshal(result)
			fullBytes = len(data)
		} else {
			if len(first) > ContextSummaryMaxBytes || strings.Contains(first, rule) {
				t.Fatal("summary text is not bounded")
			}
			data, _ := json.Marshal(result)
			if len(data) >= fullBytes {
				t.Fatal("negotiated summary did not remove duplicate rules")
			}
		}
	}
}

func TestContextTransportNegotiationRejectsMalformedMetadata(t *testing.T) {
	for _, raw := range []any{true, "summary", map[string]any{"structured": false, "text": "summary"}, map[string]any{"structured": true, "text": "invalid"}, map[string]any{"structured": true, "text": "summary", "extra": true}} {
		if _, err := contextResponseOptions(t.Context(), map[string]any{ContextResponseCapability: raw}); err == nil {
			t.Fatal("invalid negotiation accepted")
		}
	}
	result := map[string]any{"rules": []string{"retain me"}}
	normal := responseEnvelope(WithStructuredContext(t.Context()), "list_dir", result, nil)
	if !strings.Contains(asMap(envelopeBlocks(normal["content"])[0])["text"].(string), "retain me") {
		t.Fatal("summary affected unrelated tools")
	}
	failed := responseEnvelope(WithStructuredContext(t.Context()), "agentdock_context", nil, errors.New("fixed failure"))
	if failed["isError"] != true || !strings.Contains(asMap(envelopeBlocks(failed["content"])[0])["text"].(string), "fixed failure") {
		t.Fatal("error content lost")
	}
}

func TestContextTransportLargeResponsePreservesFullStructure(t *testing.T) {
	payload := map[string]any{"rules": []string{strings.Repeat("字", 350000)}, "skills": []any{}}
	for _, ctx := range []context.Context{t.Context(), WithStructuredContext(t.Context())} {
		envelope, err := measureContextResponse(ctx, "agentdock_context", responseEnvelope(ctx, "agentdock_context", payload, nil))
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(envelope)
		var result sdk.CallToolResult
		if err = json.Unmarshal(encoded, &result); err != nil {
			t.Fatal(err)
		}
		assertContextMeasurements(t, &result)
		body := asMap(result.StructuredContent)
		if len(body["rules"].([]any)[0].(string)) != 1050000 {
			t.Fatal("large structure truncated")
		}
	}
}

func TestContextTransportProjectionCannotStripBootstrap(t *testing.T) {
	h := newMCPAppTestHarness(t, config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()})
	host := insertion.Transport{HostType: "fixture", OuterCallID: "one", Passthrough: true}
	final, err := h.server.InvokeProjected(WithStructuredContext(t.Context()), "agentdock_context", map[string]any{}, host, countOnlyProjection, nil)
	if err != nil || final["isError"] == true {
		t.Fatalf("context invocation: %v %v", final, err)
	}
	structured := asMap(final["structuredContent"])
	for _, key := range []string{"rules", "instruction_files", "runtime", "skills", "workspace"} {
		if structured[key] == nil {
			t.Fatalf("projection lost %s", key)
		}
	}
}

func TestContextTransportProjectionPanicsPreserveOutcomeAndReceipts(t *testing.T) {
	for _, stage := range []string{"host_projection", "host_context_commit"} {
		t.Run(stage, func(t *testing.T) {
			h, ctx, conversation, id := insertionProjectionFixture(t)
			project := countOnlyProjection
			commit := func(context.Context, map[string]any) error { return nil }
			if stage == "host_projection" {
				project = func(map[string]any) (map[string]any, error) { panic("secret callback detail") }
			} else {
				commit = func(context.Context, map[string]any) error { panic("secret callback detail") }
			}
			final, err := h.server.InvokeProjected(ctx, "session_observe", map[string]any{"action": "list"}, insertion.Transport{HostType: "fixture", OuterCallID: "panic", Passthrough: true, ContextAcknowledgement: true}, project, commit)
			var failure *InvocationError
			if !errors.As(err, &failure) || failure.Stage != stage || failure.CallID == "" || !requesttrace.ValidID(failure.RequestID) || failure.Retryable || !failure.HandlerDispatched {
				t.Fatalf("missing failure facts: %+v %v", failure, err)
			}
			if strings.Contains(err.Error(), "secret callback detail") {
				t.Fatal("callback panic value leaked")
			}
			if final["isError"] == true {
				t.Fatal("adapter changed successful business outcome")
			}
			assertResponseSupplement(t, final, id, "你好，我是帅哥")
			item := insertionQueueItem(t, h, conversation)
			if item.Status != "delivery_unknown" || item.AcknowledgedAt != nil {
				t.Fatal("panic fabricated receiver acknowledgement")
			}
		})
	}
}

func TestContextTransportPreDispatchAndEncodeErrorsAreTyped(t *testing.T) {
	var server *Server
	_, err := server.InvokeProjected(t.Context(), "agentdock_context", nil, insertion.Transport{}, nil, nil)
	var failure *InvocationError
	if !errors.As(err, &failure) || failure.Stage != "host_validation" || failure.CallID != "" || failure.HandlerDispatched {
		t.Fatalf("bad validation: %v", err)
	}
	h := newMCPAppTestHarness(t, config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()})
	ctx, _ := requesttrace.Ensure(t.Context(), "")
	ctx, pending := app.BeginToolResponse(ctx)
	_, err = h.server.finishResponse(ctx, "agentdock_context", nil, pending, map[string]any{"content": make(chan int)})
	if !errors.As(err, &failure) || failure.Stage != "response_encode" || failure.Retryable {
		t.Fatalf("bad encode error: %v", err)
	}
}
