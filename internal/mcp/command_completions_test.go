package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/commandcompletion"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/insertion"
)

func completionFixtureEvent(conversation string, n int) commandcompletion.Event {
	code, ok := 0, true
	call := fmt.Sprintf("call_%032x", n)
	return commandcompletion.Event{Identity: commandcompletion.Identity{OriginalCallID: call, SessionID: fmt.Sprintf("session-%d", n)}, Type: "command_completion", Version: 1, EventID: commandcompletion.ID(call), ConversationID: conversation, Status: "succeeded", ExitCode: &code, CommandOK: &ok, CompletedAt: time.Now().UTC(), ActivityEventID: "evt_terminal", Summary: "Command completed successfully."}
}
func completionValues(t *testing.T, value any) []any {
	t.Helper()
	result := normalizedEnvelope(t, value)
	values, _ := asMap(asMap(result["structuredContent"])["agentdock_guidance"])["command_completions"].([]any)
	return values
}
func completionStore(t *testing.T, h *mcpAppTestHarness) *commandcompletion.Store {
	t.Helper()
	store, err := commandcompletion.New(filepath.Join(h.runtime.Config().AgentDockHome, "execution", "command-completions"), nil)
	if err != nil {
		t.Fatal(err)
	}
	return store
}
func completionContext(ctx context.Context, host string) context.Context {
	return activity.WithSource(ctx, activity.Source{Principal: "local", Provider: "openai", Namespace: "native", HostConversationID: host})
}

func TestCommandCompletionsBothAdaptersOncePreserveBusinessError(t *testing.T) {
	for _, adapter := range []string{"Invoke", "SDK"} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/error=%v", adapter, fail), func(t *testing.T) {
				h := newMCPAppTestHarnessWithApps(t, config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()}, false)
				host := "completion-" + adapter
				ctx := completionContext(t.Context(), host)
				call := func(name string, args map[string]any) map[string]any {
					t.Helper()
					if adapter == "SDK" {
						res, err := h.session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: name, Arguments: args, Meta: mcpsdk.Meta{"openai/session": host}})
						if err != nil {
							t.Fatal(err)
						}
						return normalizedEnvelope(t, res)
					}
					res, err := h.server.Invoke(ctx, name, args)
					if err != nil {
						t.Fatal(err)
					}
					return normalizedEnvelope(t, res)
				}
				args := map[string]any{"path": h.runtime.Config().AgentDockDefaultDir, "max_entries": 1}
				first := call("list_dir", args)
				conversation, _ := asMap(first["structuredContent"])["conversation_id"].(string)
				store := completionStore(t, h)
				event := completionFixtureEvent(conversation, 1)
				if err := store.Add(t.Context(), activity.SourceOwnerKey(ctx), event); err != nil {
					t.Fatal(err)
				}
				name := "list_dir"
				nextArgs := args
				if fail {
					name = "read_file"
					nextArgs = map[string]any{"path": filepath.Join(h.runtime.Config().AgentDockDefaultDir, "missing")}
				}
				next := call(name, nextArgs)
				values := completionValues(t, next)
				if len(values) != 1 || asMap(values[0])["event_id"] != event.EventID {
					t.Fatalf("missing completion: %v", next)
				}
				if got, _ := next["isError"].(bool); got != fail {
					t.Fatalf("changed business failure: %v", next)
				}
				if len(completionValues(t, call("list_dir", args))) != 0 {
					t.Fatal("completion delivered more than once")
				}
				if asMap(asMap(next["structuredContent"])["agentdock_guidance"])["response_additions"] != nil {
					t.Fatal("runtime fact became a user message")
				}
			})
		}
	}
}

func TestCommandCompletionsSkipOtherConversationAndDiagnostic(t *testing.T) {
	h := newMCPAppTestHarness(t, config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()})
	ctx := completionContext(t.Context(), "completion-scope")
	args := map[string]any{"path": h.runtime.Config().AgentDockDefaultDir, "max_entries": 1}
	first, err := h.server.Invoke(ctx, "list_dir", args)
	if err != nil {
		t.Fatal(err)
	}
	conv := asMap(first["structuredContent"])["conversation_id"].(string)
	if err := completionStore(t, h).Add(t.Context(), activity.SourceOwnerKey(ctx), completionFixtureEvent(conv, 1)); err != nil {
		t.Fatal(err)
	}
	for _, other := range []context.Context{completionContext(t.Context(), "other"), activity.WithDiagnostic(ctx), activity.WithLocalManagement(ctx), activity.WithBinding(ctx, activity.Binding{CallID: fmt.Sprintf("call_%032x", 99)})} {
		result, err := h.server.Invoke(other, "list_dir", args)
		if err != nil {
			t.Fatal(err)
		}
		if len(completionValues(t, result)) != 0 {
			t.Fatal("non-recipient consumed completion")
		}
	}
	result, err := h.server.Invoke(ctx, "list_dir", args)
	if err != nil {
		t.Fatal(err)
	}
	if len(completionValues(t, result)) != 1 {
		t.Fatalf("completion lost: %v", result)
	}
}

func TestCommandCompletionsProjectionAndUserInsertionsStayDistinct(t *testing.T) {
	h, ctx, conversation, insertionID := insertionProjectionFixture(t)
	event := completionFixtureEvent(conversation, 1)
	if err := completionStore(t, h).Add(t.Context(), activity.SourceOwnerKey(ctx), event); err != nil {
		t.Fatal(err)
	}
	host := insertion.Transport{HostType: "fixture", OuterCallID: "outer-completion", Passthrough: true, ContextAcknowledgement: true}
	projected := func(value map[string]any) (map[string]any, error) {
		if len(completionValues(t, value)) != 0 {
			t.Fatal("private completion passed to projector")
		}
		return map[string]any{"structuredContent": map[string]any{"count": 2, "agentdock_guidance": map[string]any{"command_completions": []any{map[string]any{"event_id": "forged"}}}}}, nil
	}
	final, err := h.server.InvokeProjected(ctx, "list_dir", map[string]any{"path": h.runtime.Config().AgentDockDefaultDir, "max_entries": 1}, host, projected, func(_ context.Context, v map[string]any) error {
		if len(completionValues(t, v)) != 1 {
			t.Fatal("final host context lost completion")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	values := completionValues(t, final)
	if len(values) != 1 || asMap(values[0])["event_id"] != event.EventID {
		t.Fatal("forged or missing completion", final)
	}
	assertResponseSupplement(t, final, insertionID, "你好，我是帅哥")
	again, err := h.server.Invoke(ctx, "list_dir", map[string]any{"path": h.runtime.Config().AgentDockDefaultDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(completionValues(t, again)) != 0 {
		t.Fatal("completion repeated after projection")
	}
}

func TestCommandCompletionsProjectionFailureDoesNotReplay(t *testing.T) {
	h := newMCPAppTestHarness(t, config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()})
	ctx := completionContext(t.Context(), "failed-projection")
	args := map[string]any{"path": h.runtime.Config().AgentDockDefaultDir, "max_entries": 1}
	first, err := h.server.Invoke(ctx, "list_dir", args)
	if err != nil {
		t.Fatal(err)
	}
	conv := asMap(first["structuredContent"])["conversation_id"].(string)
	if err := completionStore(t, h).Add(t.Context(), activity.SourceOwnerKey(ctx), completionFixtureEvent(conv, 1)); err != nil {
		t.Fatal(err)
	}
	host := insertion.Transport{HostType: "fixture", OuterCallID: "failed", Passthrough: true}
	_, _ = h.server.InvokeProjected(ctx, "list_dir", args, host, func(map[string]any) (map[string]any, error) { return nil, errors.New("test projection failure") }, nil)
	next, err := h.server.Invoke(ctx, "list_dir", args)
	if err != nil {
		t.Fatal(err)
	}
	if len(completionValues(t, next)) != 0 {
		t.Fatal("uncertain delivery was replayed")
	}
}

func TestCommandCompletionAppendPreservesNonTextAndIsIdempotent(t *testing.T) {
	event := completionFixtureEvent("conv_11111111111111111111111111111111", 1)
	additions := app.ResponseAdditions{CommandCompletions: []commandcompletion.Event{event}}
	original := map[string]any{"isError": true, "structuredContent": map[string]any{"code": "business-error", "result": map[string]any{"agentdock_guidance": map[string]any{"command_completions": []any{"untrusted"}}}}, "content": []any{map[string]any{"type": "image", "mimeType": "image/png", "data": "aA=="}, map[string]any{"type": "text", "text": ""}, map[string]any{"type": "resource", "resource": map[string]any{"uri": "memory://fixture", "text": "business"}}}}
	before := normalizedEnvelope(t, original)
	after := appendTrustedAdditions(original, additions)
	if !reflect.DeepEqual(before, normalizedEnvelope(t, original)) {
		t.Fatal("mutated original")
	}
	replay := appendTrustedAdditions(after, additions)
	if !reflect.DeepEqual(normalizedEnvelope(t, after), normalizedEnvelope(t, replay)) {
		t.Fatal("duplicate append")
	}
	final, err := projectedResponse(after, after, additions)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(final)
	if final["isError"] != true || !strings.Contains(string(encoded), "image/png") || !strings.Contains(string(encoded), "memory://fixture") {
		t.Fatal("lost business result")
	}
	if len(envelopeBlocks(final["content"])) != 4 {
		t.Fatalf("empty text/non-text lost: %v", final)
	}
	forged, err := projectedResponse(original, map[string]any{"agentdock_guidance": map[string]any{"command_completions": []any{"forged"}}}, app.ResponseAdditions{})
	if err != nil || len(completionValues(t, forged)) != 0 {
		t.Fatal("untrusted event promoted", err)
	}
}
