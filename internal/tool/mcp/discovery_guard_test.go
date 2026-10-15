package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

func builtinTestService() *Service {
	s := New(nil, nil) // Any accidental dynamic discovery would fail this test.
	s.SetBuiltinToolLookup(func(name string) (BuiltinToolSummary, bool) {
		switch name {
		case "mcp_tool_call", "exec_command", "task_manage", "file_edit":
			return BuiltinToolSummary{Name: name, Title: name, InvokeVia: "host"}, true
		}
		return BuiltinToolSummary{}, false
	})
	return s
}

func builtinTestBinding() activity.Binding {
	return activity.Binding{SourceOwnerKey: "owner", ConversationID: "conv_" + strings.Repeat("a", 32), WorkspaceID: "workspace", BindingRevision: 1, CallID: "call_" + strings.Repeat("b", 32)}
}

func expectBuiltinRouteError(t *testing.T, err error, code string) *ToolError {
	t.Helper()
	var got *ToolError
	if !errors.As(err, &got) || got.Code != code || got.Retryable || !IsBuiltinDiscoveryError(err) {
		t.Fatalf("route error=%#v, want non-retryable %s", err, code)
	}
	if got.Details["execution_performed"] != false || got.Details["host_schema_state"] != "caller_managed" {
		t.Fatalf("routing claimed execution or host schema loading: %#v", got.Details)
	}
	if got.Details["route_status"] != "blocked" || got.Details["next_action"] != "call_host_tool" || got.Details["wrong_namespace_search"] != true {
		t.Fatalf("routing error lost terminal machine-readable state: %#v", got.Details)
	}
	if code == "ROUTING_LOOP_BLOCKED" {
		count, _ := got.Details["wrong_namespace_repeat_count"].(int)
		if count < 1 {
			t.Fatalf("repeat routing error lost its count: %#v", got.Details)
		}
	}
	return got
}

func TestBuiltinDiscoveryRepeatAcrossSelectorsAndEntrypoints(t *testing.T) {
	s := builtinTestService()
	ctx := activity.WithBinding(t.Context(), builtinTestBinding())
	queries := []string{"mcp_tool_call", " mcp_tool_call ", "MCP_TOOL_CALL", "mcp_tool_call exact", "Call a dynamic MCP tool", "mcp_tool_call", "mcp_tool_call", "mcp_tool_call", "mcp_tool_call", "mcp_tool_call"}
	for i, query := range queries {
		limit := i + 1
		result, err := s.Search(ctx, SearchRequest{Query: query, Limit: &limit})
		code := "ROUTING_LOOP_BLOCKED"
		if i == 0 {
			code = "HOST_BUILTIN_NOT_DYNAMIC_MCP"
		}
		got := expectBuiltinRouteError(t, err, code)
		if result != nil || got.Details["required_host_tool"] != "mcp_tool_call" {
			t.Fatalf("wrong route: %#v %#v", result, got)
		}
	}
	_, err := s.Inspect(ctx, InspectRequest{Name: "mcp_tool_call"})
	expectBuiltinRouteError(t, err, "ROUTING_LOOP_BLOCKED")
}

func TestBuiltinDiscoveryCanonicalBatchDeduplication(t *testing.T) {
	s := builtinTestService()
	ctx := activity.WithBinding(t.Context(), builtinTestBinding())
	_, err := s.Inspect(ctx, InspectRequest{Names: []string{"file_edit", "write_file", "file_edit"}})
	got := expectBuiltinRouteError(t, err, "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	names := got.Details["required_host_tools"].([]string)
	if len(names) != 1 || names[0] != "file_edit" {
		t.Fatalf("aliases counted as separate misuse: %#v", names)
	}
}

func TestBuiltinDiscoveryScopeIsolation(t *testing.T) {
	s := builtinTestService()
	binding := builtinTestBinding()
	for _, b := range []activity.Binding{
		func() activity.Binding {
			value := binding
			value.ConversationID = "conv_" + strings.Repeat("c", 32)
			return value
		}(),
		func() activity.Binding {
			value := binding
			value.SourceOwnerKey = "other-owner"
			return value
		}(),
	} {
		_, err := s.Search(activity.WithBinding(t.Context(), b), SearchRequest{Query: "exec_command"})
		expectBuiltinRouteError(t, err, "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	}
	_, err := s.Search(activity.WithBinding(t.Context(), binding), SearchRequest{Query: "exec_command"})
	expectBuiltinRouteError(t, err, "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	workspaceChanged := binding
	workspaceChanged.WorkspaceID = "other-workspace"
	_, err = s.Search(activity.WithBinding(t.Context(), workspaceChanged), SearchRequest{Query: "exec_command"})
	expectBuiltinRouteError(t, err, "ROUTING_LOOP_BLOCKED")
	revisionChanged := binding
	revisionChanged.BindingRevision++
	_, err = s.Search(activity.WithBinding(t.Context(), revisionChanged), SearchRequest{Query: "exec_command"})
	expectBuiltinRouteError(t, err, "ROUTING_LOOP_BLOCKED")
}

func TestBuiltinDiscoveryUnattributedAndCancellation(t *testing.T) {
	s := builtinTestService()
	for range 10 {
		_, err := s.Search(t.Context(), SearchRequest{Query: "task_manage"})
		expectBuiltinRouteError(t, err, "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	}
	if len(s.discoveryRoutes.entries) != 0 {
		t.Fatal("unattributed calls shared a state bucket")
	}
	ctx, cancel := context.WithCancel(activity.WithBinding(t.Context(), builtinTestBinding()))
	cancel()
	_, err := s.Search(ctx, SearchRequest{Query: "mcp_tool_call"})
	if !errors.Is(err, context.Canceled) || len(s.discoveryRoutes.entries) != 0 {
		t.Fatalf("cancelled call changed routing state: %v", err)
	}
}

func TestBuiltinDiscoveryConcurrentRepeat(t *testing.T) {
	s := builtinTestService()
	ctx := activity.WithBinding(t.Context(), builtinTestBinding())
	var first, repeat, bad atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Search(ctx, SearchRequest{Query: "exec_command"})
			var got *ToolError
			if !errors.As(err, &got) || got.Retryable {
				bad.Add(1)
				return
			}
			switch got.Code {
			case "HOST_BUILTIN_NOT_DYNAMIC_MCP":
				first.Add(1)
			case "ROUTING_LOOP_BLOCKED":
				repeat.Add(1)
			default:
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if first.Load() != 1 || repeat.Load() != 31 || bad.Load() != 0 {
		t.Fatalf("concurrent classifications=%d/%d/%d", first.Load(), repeat.Load(), bad.Load())
	}
}

func TestBuiltinDiscoveryWindowAndCapacity(t *testing.T) {
	s := builtinTestService()
	now := time.Unix(1000, 0)
	s.discoveryRoutes.now = func() time.Time { return now }
	binding := builtinTestBinding()
	ctx := activity.WithBinding(t.Context(), binding)
	_, err := s.Search(ctx, SearchRequest{Query: "exec_command"})
	expectBuiltinRouteError(t, err, "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	now = now.Add(builtinDiscoveryWindow)
	_, err = s.Search(ctx, SearchRequest{Query: "exec_command"})
	expectBuiltinRouteError(t, err, "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	for i := range builtinDiscoveryCapacity {
		now = now.Add(time.Nanosecond)
		b := binding
		b.ConversationID = fmt.Sprintf("conv_%032x", i)
		_, err = s.Search(activity.WithBinding(t.Context(), b), SearchRequest{Query: "exec_command"})
		expectBuiltinRouteError(t, err, "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	}
	if len(s.discoveryRoutes.entries) != builtinDiscoveryCapacity {
		t.Fatalf("capacity=%d", len(s.discoveryRoutes.entries))
	}
	_, err = s.Search(ctx, SearchRequest{Query: "exec_command"})
	expectBuiltinRouteError(t, err, "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	now = now.Add(-time.Hour)
	_, err = s.Search(ctx, SearchRequest{Query: "exec_command"})
	expectBuiltinRouteError(t, err, "HOST_BUILTIN_NOT_DYNAMIC_MCP")
}

func TestBuiltinDiscoveryErrorIsCompactAndCannotBeForged(t *testing.T) {
	s := builtinTestService()
	_, err := s.Search(t.Context(), SearchRequest{Query: "mcp_tool_call"})
	got := expectBuiltinRouteError(t, err, "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	data, e := json.Marshal(got)
	if e != nil {
		t.Fatal(e)
	}
	if len(data) >= 2048 || strings.Contains(string(data), "input_schema") || strings.Contains(string(data), "InputSchema") {
		t.Fatalf("noncompact route or schema leak: %s", data)
	}
	forged := &ToolError{Code: got.Code, Category: got.Category, Details: got.Details}
	if IsBuiltinDiscoveryError(forged) {
		t.Fatal("untrusted code/fields forged local recovery provenance")
	}
}
