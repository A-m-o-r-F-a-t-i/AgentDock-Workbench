package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
)

func payloadTestSource() context.Context {
	return activity.WithSource(context.Background(), activity.Source{Principal: "payload-test", Provider: "openai", Namespace: "mcp", HostConversationID: "payload-conversation"})
}

func TestExecutionRequestVisibleBeforeToolCompletes(t *testing.T) {
	r := newRuntimeValidationTestRuntime(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	spec := ToolSpec{Name: "read_file", Handler: func(ctx context.Context, _ *Runtime, _ map[string]any) (Result, error) {
		close(entered)
		select {
		case <-release:
			return Result{"text": "actual tool output", "empty": ""}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	go func() {
		_, err := r.callObserved(payloadTestSource(), spec, map[string]any{"path": "sample.txt"})
		finished <- err
	}()
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	select {
	case <-entered:
	case <-time.After(8 * time.Second):
		t.Fatal("fixture did not start")
	}
	page, err := r.activity.Calls(context.Background(), activity.CallQuery{TopLevel: true, IncludeOutput: true})
	if err != nil || len(page.Calls) != 1 {
		t.Fatalf("admitted call missing: %+v %v", page, err)
	}
	call := page.Calls[0]
	if call.Status != "running" || call.ConversationID == "" || call.Request == nil || call.Request.Ref == "" || call.Response == nil || call.Response.State != "pending" {
		t.Fatalf("in-flight observation incomplete: %+v", call)
	}
	request, err := r.activity.ReadCallPayload(context.Background(), call.CallID, "request", 0, 32768)
	if err != nil || !strings.Contains(request.Text, "sample.txt") {
		t.Fatalf("accepted arguments not saved: %+v %v", request, err)
	}
	conversations, err := r.RuntimeConversations(context.Background(), ExecutionListQuery{})
	if err != nil || len(conversations.Conversations) != 1 {
		t.Fatalf("conversation did not exist before completion: %+v %v", conversations, err)
	}
	sidebar, err := r.RuntimeConversationSidebar(context.Background(), SidebarRequest{})
	if err != nil || len(sidebar.Groups) != 1 || len(sidebar.Groups[0].Conversations) != 1 || !sidebar.Groups[0].Conversations[0].InFlight {
		t.Fatalf("running root was not projected into the default activity sidebar: %+v %v", sidebar, err)
	}
	if sidebar.Groups[0].Conversations[0].Title == "新对话" || !strings.Contains(sidebar.Groups[0].Conversations[0].Title, "read_file") {
		t.Fatal("first root did not acquire a distinguishable tool title")
	}
	close(release)
	released = true
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	response, err := r.activity.ReadCallPayload(context.Background(), call.CallID, "response", 0, 32768)
	if err != nil || response.Payload.State != "complete" || !strings.Contains(response.Text, "actual tool output") {
		t.Fatalf("actual response lost: %+v %v", response, err)
	}
	replay, err := activity.New(filepath.Join(r.cfg.AgentDockHome, "tasks", "activity"), activity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := replay.ReadCallPayload(context.Background(), call.CallID, "response", 0, 32768)
	if err != nil || restored.Text != response.Text {
		t.Fatal("history did not reproduce the live result", err)
	}
}

func TestExecutionAdapterPersistsFinalEnvelopeOnce(t *testing.T) {
	r := newRuntimeValidationTestRuntime(t)
	ctx, pending := BeginToolResponse(payloadTestSource())
	spec := ToolSpec{Name: "read_file", Handler: func(context.Context, *Runtime, map[string]any) (Result, error) {
		return Result{"text": "original result"}, nil
	}}
	result, err := r.callObserved(ctx, spec, map[string]any{"path": "sample.txt"})
	if err != nil {
		t.Fatal(err)
	}
	id := stringArg(result, "call_id")
	call, err := r.activity.Call(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if call.Response == nil || call.Response.State != "pending" {
		t.Fatalf("runtime duplicated an unfinished adapter response: %+v", call.Response)
	}
	envelope := map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": "original result"}, map[string]any{"type": "text", "text": "service catalog"}, map[string]any{"type": "text", "text": "AGENTDOCK_USER_INSERT_V1 fixture"}},
		"structuredContent": map[string]any{"result": result, "agentdock_guidance": map[string]any{"response_additions": []any{map[string]any{"insertion_id": "ins_fixture", "text": "trusted fixture"}}}},
		"isError":           false,
	}
	r.RecordToolResponse(pending, envelope)
	r.RecordToolResponse(pending, map[string]any{"replacement": "must not be written"})
	response, err := r.activity.ReadCallPayload(ctx, id, "response", 0, 32768)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(response.Text), &decoded); err != nil {
		t.Fatal(err)
	}
	blocks, ok := decoded["content"].([]any)
	if !ok || len(blocks) != 3 || !strings.Contains(response.Text, "ins_fixture") || strings.Contains(response.Text, "must not be written") {
		t.Fatal("final envelope blocks or once-only guard lost")
	}
	events, err := r.activity.Query(ctx, activity.Query{CallID: id})
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	for _, event := range events.Events {
		if event.Kind == "call.payload" && event.Response != nil {
			writes++
		}
	}
	if writes != 1 {
		t.Fatalf("response persisted %d times", writes)
	}
}

func TestExecutionFailedToolPreservesStructuredFailure(t *testing.T) {
	r := newRuntimeValidationTestRuntime(t)
	spec := ToolSpec{Name: "read_file", Handler: func(context.Context, *Runtime, map[string]any) (Result, error) {
		return Result{"isError": true, "content": []any{map[string]any{"type": "text", "text": "partial failure detail"}}}, nil
	}}
	result, err := r.callObserved(payloadTestSource(), spec, map[string]any{"path": "sample.txt"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.activity.ReadCallPayload(context.Background(), stringArg(result, "call_id"), "response", 0, 32768)
	var decoded map[string]any
	decodeErr := json.Unmarshal([]byte(page.Text), &decoded)
	if err != nil || decodeErr != nil || !strings.Contains(page.Text, "partial failure detail") || !containsJSONBoolean(decoded, "isError", true) {
		t.Fatalf("failure result lost %+v %v", page, err)
	}
}

func TestMCPRecoveryRequestPreservesExactPagedArguments(t *testing.T) {
	r := newRuntimeValidationTestRuntime(t)
	for _, redacted := range []bool{false, true} {
		name := "complete"
		if redacted {
			name = "redacted"
		}
		t.Run(name, func(t *testing.T) {
			ctx := payloadTestSource()
			callID, err := activity.NewExecutionID("call_")
			if err != nil {
				t.Fatal(err)
			}
			binding := activity.Binding{CallID: callID, SourceOwnerKey: activity.SourceOwnerKey(ctx)}
			arguments := map[string]any{
				"id":     json.Number("9007199254740993"),
				"values": []any{json.Number("18446744073709551615"), json.Number("0.10000000000000001")},
				"detail": strings.Repeat("complete original request ", 2048),
				"marker": "private-recovery-fixture",
			}
			request := map[string]any{"name": "fixture:write", "arguments": arguments, "reason": "repeat the exact original call", "task_id": "must-not-be-replayed"}
			redactor := activity.NewRedactor()
			if redacted {
				redactor = activity.NewRedactor("private-recovery-fixture")
			}
			payload := r.activity.CaptureRequestPayload(ctx, request, "complete", redactor)
			if payload.State != "complete" || payload.Bytes <= mcpRecoveryReadPageBytes || !payload.Truncated {
				t.Fatalf("fixture did not retain a complete multi-page request: %+v", payload)
			}
			if _, err := r.activity.Append(ctx, activity.Event{Binding: binding, Kind: "call.created", ToolName: "mcp_tool_call", Request: payload, ToolExecution: &activity.ToolExecutionDetails{Server: "fixture", QualifiedName: "fixture:write"}}); err != nil {
				t.Fatal(err)
			}
			origin, err := r.activity.Call(ctx, callID)
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := r.readMCPRecoveryRequest(ctx, origin)
			if redacted {
				var toolErr *ToolError
				if !errors.As(err, &toolErr) || toolErr.Code != "MCP_RECOVERY_REQUEST_UNAVAILABLE" || recovered != nil {
					t.Fatalf("redacted original became replayable: %#v %v", recovered, err)
				}
				return
			}
			if err != nil || recovered["task_id"] != nil {
				t.Fatalf("exact recovery request unavailable or inherited an untrusted task: %#v %v", recovered, err)
			}
			accepted, err := cloneExecutionArguments(recovered)
			if err != nil {
				t.Fatal(err)
			}
			var retry toolmcp.CallRequest
			if err := decodeToolInput("mcp_tool_call", accepted, &retry); err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(retry.Arguments)
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(arguments)
			if err != nil {
				t.Fatal(err)
			}
			if retry.Name != "fixture:write" || retry.Reason != request["reason"] || !bytes.Equal(got, want) {
				t.Fatal("retained request was rounded or truncated before capability replay")
			}
		})
	}
}

func containsJSONBoolean(value any, key string, expected bool) bool {
	switch current := value.(type) {
	case map[string]any:
		if actual, ok := current[key].(bool); ok && actual == expected {
			return true
		}
		for _, child := range current {
			if containsJSONBoolean(child, key, expected) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if containsJSONBoolean(child, key, expected) {
				return true
			}
		}
	}
	return false
}
