package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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
	for _, mode := range []string{"full", "summary"} {
		meta := sdk.Meta{"openai/session": "context-negotiation-" + mode, ContextResponseCapability: map[string]any{"structured": true, "text": mode}}
		args := map[string]any{}
		result, err := h.session.CallTool(t.Context(), &sdk.CallToolParams{Name: "agentdock_context", Arguments: args, Meta: meta})
		if err != nil || result.IsError {
			t.Fatalf("%s: result=%+v error=%v", mode, result, err)
		}
		assertContextMeasurements(t, result)
		wire, _ := json.Marshal(result)
		if utf8.RuneCount(wire) > 12000 {
			t.Fatal("negotiated response exceeded the fixed final budget")
		}
		full := requiredSDKPayload(t, h.session, result, args, meta)
		encoded, _ := json.Marshal(full)
		if !strings.Contains(string(encoded), rule) {
			t.Fatal("required sequence lost original rules")
		}
		first := result.Content[0].(*sdk.TextContent).Text
		if mode == "summary" && (utf8.RuneCountInString(first) > 512 || strings.Contains(first, rule)) {
			t.Fatal("summary duplicated the contract")
		}
	}
}

func TestContextTransportNegotiationRejectsMalformedMetadata(t *testing.T) {
	for _, capability := range []string{ContextResponseCapability, StructuredResultsCapability} {
		for _, raw := range []any{true, "summary", map[string]any{"structured": false, "text": "summary"}, map[string]any{"structured": true, "text": "invalid"}, map[string]any{"structured": true, "text": "summary", "extra": true}} {
			if _, err := contextResponseOptions(t.Context(), map[string]any{capability: raw}); err == nil {
				t.Fatalf("invalid %s negotiation accepted", capability)
			}
		}
	}
	result := map[string]any{"rules": []string{"retain me"}}
	normal := responseEnvelope(WithStructuredContext(t.Context()), "list_dir", result, nil)
	if asMap(normal["structuredContent"])["rules"].([]string)[0] != "retain me" || utf8.RuneCountInString(asMap(envelopeBlocks(normal["content"])[0])["text"].(string)) > 512 {
		t.Fatal("default summary lost its structured body or exceeded the compatibility budget")
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
	for _, key := range []string{"workspace", "workdir", "runtime", "host_tools", "builtin_tool_bundles", "dynamic_mcp_summary", "instruction_files", "task_binding", "context_revision"} {
		if structured[key] == nil {
			t.Fatalf("projection lost Bootstrap field %s: %#v", key, structured)
		}
	}
	if structured["bootstrap_complete"] != true || structured["context_ready"] != true {
		t.Fatalf("projected Bootstrap is not usable: %#v", structured)
	}
	if structured["has_more"] == true {
		delivery := asMap(structured["delivery"])
		action := asMap(structured["required_action"])
		if delivery["sequence_complete"] != false || delivery["continuation_required"] != false || action["category"] != "detail" || action["required"] != false {
			t.Fatalf("optional detail paging became a global blocker: delivery=%#v action=%#v", delivery, action)
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

func TestModelV2ContextFinalBudgetAndAutomaticPaging(t *testing.T) {
	h := newMCPAppTestHarnessWithApps(t, config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()}, false)
	rule := strings.Repeat("新规则💡\"\\\n", 2400)
	if err := os.WriteFile(filepath.Join(h.runtime.Config().AgentDockDefaultDir, "AGENTS.md"), []byte(rule), 0600); err != nil {
		t.Fatal(err)
	}
	revision := ""
	args := map[string]any{"force_refresh": true}
	sequenceComplete := false
	for index := 0; index < 80; index++ {
		result, err := h.session.CallTool(t.Context(), &sdk.CallToolParams{Name: "agentdock_context", Arguments: args, Meta: sdk.Meta{"openai/session": "model-v2-context-sequence", ContextResponseCapability: map[string]any{"structured": true, "text": "summary"}}})
		if err != nil || result.IsError {
			t.Fatalf("page %d err=%v result=%+v", index, err, result)
		}
		encoded, err := json.Marshal(result)
		if err != nil || utf8.RuneCount(encoded) > 12000 {
			t.Fatalf("final escaped budget exceeded: chars=%d err=%v", utf8.RuneCount(encoded), err)
		}
		if text, ok := result.Content[0].(*sdk.TextContent); !ok || utf8.RuneCountInString(text.Text) > 512 {
			t.Fatal("compatibility summary exceeded")
		}
		value := asMap(result.StructuredContent)
		current, _ := value["context_revision"].(string)
		if index == 0 {
			revision = current
			if value["bootstrap_complete"] != true || value["context_ready"] != true {
				t.Fatalf("first page did not deliver a usable Bootstrap: %#v", value)
			}
		} else if current != revision {
			t.Fatal("Context revision changed during frozen paging")
		}
		delivery := asMap(value["delivery"])
		if value["has_more"] == true {
			cursor, _ := value["next_cursor"].(string)
			if delivery["sequence_complete"] != false || delivery["continuation_required"] != false || cursor == "" {
				t.Fatalf("optional detail continuation is inconsistent: %+v", value)
			}
			actionArgs := asMap(asMap(value["required_action"])["arguments"])
			if len(actionArgs) != 1 || actionArgs["cursor"] != cursor {
				t.Fatalf("Context continuation should contain only the immutable cursor: %#v", actionArgs)
			}
			args = map[string]any{"cursor": cursor}
			continue
		}
		if delivery["sequence_complete"] == true {
			sequenceComplete = true
			break
		}
		t.Fatalf("final Context page did not complete its detail sequence: %#v", value)
	}
	if !sequenceComplete {
		t.Fatal("Context automatic detail paging did not finish")
	}
	result, err := h.session.CallTool(t.Context(), &sdk.CallToolParams{Name: "agentdock_context", Arguments: map[string]any{}, Meta: sdk.Meta{"openai/session": "model-v2-context-sequence", ContextResponseCapability: map[string]any{"structured": true, "text": "summary"}}})
	if err != nil || result.IsError || asMap(result.StructuredContent)["unchanged"] != true {
		t.Fatalf("same revision not reused: %+v err=%v", result, err)
	}
}

func TestModelV2ExplicitTextOnlyBudgetAndContinuation(t *testing.T) {
	h := newMCPAppTestHarnessWithApps(t, config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()}, false)
	body := strings.Repeat("只读💡\"\\\n", 2600)
	file := filepath.Join(h.runtime.Config().AgentDockDefaultDir, "text-only-page.txt")
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	arguments := map[string]any{"path": file}
	assembled := ""
	for index := 0; index < 100; index++ {
		result, err := h.session.CallTool(t.Context(), &sdk.CallToolParams{Name: "read_file", Arguments: arguments, Meta: sdk.Meta{"openai/session": "text-only-budget", StructuredResultsCapability: map[string]any{"structured": false, "text": "full"}}})
		if err != nil || result.IsError {
			t.Fatalf("text-only read failed: %v %+v", err, result)
		}
		raw, err := json.Marshal(result)
		if err != nil || utf8.RuneCount(raw) > 12000 {
			t.Fatalf("text-only escaped final size=%d: %v", utf8.RuneCount(raw), err)
		}
		if result.StructuredContent != nil {
			t.Fatal("text-only response duplicated its body")
		}
		var page map[string]any
		text, ok := result.Content[0].(*sdk.TextContent)
		if !ok || json.Unmarshal([]byte(text.Text), &page) != nil {
			t.Fatal("text-only response was not one complete JSON document")
		}
		if source, _ := page["source_ref"].(string); page["content"] == nil && source != "" {
			// A text-only legacy client may have extra JSON escaping overhead. Its
			// bounded recovery receipt must still identify the retained full source.
			if asMap(page["delivery"])["continuation_available"] != true {
				t.Fatal("text-only recovery source unavailable")
			}
			return
		}
		content, _ := page["content"].(string)
		assembled += content
		if page["has_more"] == false {
			if assembled != body {
				t.Fatal("text-only byte pagination changed file content")
			}
			return
		}
		arguments = asMap(asMap(page["continue_read"])["arguments"])
	}
	t.Fatal("text-only paging did not finish")
}
