package mcp

import (
	"context"
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
	s := New(nil, nil)
	s.SetBuiltinToolLookup(func(name string) (BuiltinToolSummary, bool) {
		switch name {
		case "mcp_tool_call", "exec_command", "task_manage", "apply_patch":
			return BuiltinToolSummary{Name: name, Title: name, InvokeVia: "host"}, true
		}
		return BuiltinToolSummary{}, false
	})
	return s
}

func builtinTestBinding() activity.Binding {
	return activity.Binding{SourceOwnerKey: "owner", ConversationID: "conv_" + strings.Repeat("a", 32), WorkspaceID: "workspace", BindingRevision: 1, CallID: "call_" + strings.Repeat("b", 32)}
}

func expectBuiltinRoute(t *testing.T, result Result, err error, status, code string) {
	t.Helper()
	if err != nil || result == nil || result["route_status"] != status || result["route_code"] != code ||
		result["resolved"] != true || result["terminal"] != true || result["execution_performed"] != false ||
		result["host_directory_visibility"] != "unknown" || result["route_kind"] != routeKindHostBuiltin {
		t.Fatalf("route = %#v, %v; want %s/%s", result, err, status, code)
	}
	action, ok := result["required_action"].(map[string]any)
	if !ok || action["reason_code"] != "host_tool_handoff" || action["required"] != true ||
		action["execution_performed"] != false || action["arguments"] != nil {
		t.Fatalf("route lost non-executing host action: %#v", result)
	}
	if result["already_delivered"] != (status == "already_routed") {
		t.Fatalf("incorrect repeated route receipt: %#v", result)
	}
}

func TestBuiltinDiscoveryRepeatAcrossSelectorsAndEntrypoints(t *testing.T) {
	s := builtinTestService()
	ctx := activity.WithBinding(t.Context(), builtinTestBinding())
	queries := []string{"mcp_tool_call", " mcp_tool_call ", "MCP_TOOL_CALL", "mcp_tool_call exact", "Call a dynamic MCP tool", "mcp_tool_call"}
	for i, query := range queries {
		limit := i + 1
		result, err := s.Search(ctx, SearchRequest{Query: query, Limit: &limit})
		status, code := "already_routed", "ROUTING_LOOP_BLOCKED"
		if i == 0 {
			status, code = "resolved", "HOST_BUILTIN_NOT_DYNAMIC_MCP"
		}
		expectBuiltinRoute(t, result, err, status, code)
		if result["required_host_tool"] != "mcp_tool_call" {
			t.Fatalf("wrong routed host: %#v", result)
		}
	}
	result, err := s.Inspect(ctx, InspectRequest{Name: "mcp_tool_call"})
	expectBuiltinRoute(t, result, err, "already_routed", "ROUTING_LOOP_BLOCKED")
}

func TestBuiltinDiscoveryCanonicalBatchDeduplication(t *testing.T) {
	s := builtinTestService()
	ctx := activity.WithBinding(t.Context(), builtinTestBinding())
	result, err := s.Inspect(ctx, InspectRequest{Names: []string{"apply_patch", "write_file", "apply_patch"}})
	expectBuiltinRoute(t, result, err, "resolved", "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	names := result["required_host_tools"].([]string)
	if len(names) != 1 || names[0] != "apply_patch" {
		t.Fatalf("aliases counted as separate routes: %#v", names)
	}
}

func TestBuiltinDiscoveryScopeIsolation(t *testing.T) {
	s := builtinTestService()
	binding := builtinTestBinding()
	for _, b := range []activity.Binding{
		func() activity.Binding { value := binding; value.ConversationID = "conv_" + strings.Repeat("c", 32); return value }(),
		func() activity.Binding { value := binding; value.SourceOwnerKey = "other-owner"; return value }(),
	} {
		result, err := s.Search(activity.WithBinding(t.Context(), b), SearchRequest{Query: "exec_command"})
		expectBuiltinRoute(t, result, err, "resolved", "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	}
	result, err := s.Search(activity.WithBinding(t.Context(), binding), SearchRequest{Query: "exec_command"})
	expectBuiltinRoute(t, result, err, "resolved", "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	otherWorkspace := binding
	otherWorkspace.WorkspaceID = "other-workspace"
	result, err = s.Search(activity.WithBinding(t.Context(), otherWorkspace), SearchRequest{Query: "exec_command"})
	expectBuiltinRoute(t, result, err, "already_routed", "ROUTING_LOOP_BLOCKED")
}

func TestBuiltinDiscoveryUnattributedAndCancellation(t *testing.T) {
	s := builtinTestService()
	for range 10 {
		result, err := s.Search(t.Context(), SearchRequest{Query: "task_manage"})
		expectBuiltinRoute(t, result, err, "resolved", "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	}
	if len(s.discoveryRoutes.entries) != 0 {
		t.Fatal("unattributed calls shared a state bucket")
	}
	ctx, cancel := context.WithCancel(activity.WithBinding(t.Context(), builtinTestBinding()))
	cancel()
	_, err := s.Search(ctx, SearchRequest{Query: "mcp_tool_call"})
	if !errors.Is(err, context.Canceled) || len(s.discoveryRoutes.entries) != 0 {
		t.Fatalf("cancelled search changed routing state: %v", err)
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
			result, err := s.Search(ctx, SearchRequest{Query: "exec_command"})
			if err != nil || result == nil { bad.Add(1); return }
			switch result["route_status"] {
			case "resolved": first.Add(1)
			case "already_routed": repeat.Add(1)
			default: bad.Add(1)
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
	result, err := s.Search(ctx, SearchRequest{Query: "exec_command"})
	expectBuiltinRoute(t, result, err, "resolved", "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	now = now.Add(builtinDiscoveryWindow)
	result, err = s.Search(ctx, SearchRequest{Query: "exec_command"})
	expectBuiltinRoute(t, result, err, "resolved", "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	for i := range builtinDiscoveryCapacity {
		now = now.Add(time.Nanosecond)
		b := binding
		b.ConversationID = fmt.Sprintf("conv_%032x", i)
		result, err = s.Search(activity.WithBinding(t.Context(), b), SearchRequest{Query: "exec_command"})
		expectBuiltinRoute(t, result, err, "resolved", "HOST_BUILTIN_NOT_DYNAMIC_MCP")
	}
	if len(s.discoveryRoutes.entries) != builtinDiscoveryCapacity {
		t.Fatalf("capacity=%d", len(s.discoveryRoutes.entries))
	}
	result, err = s.Search(ctx, SearchRequest{Query: "exec_command"})
	expectBuiltinRoute(t, result, err, "resolved", "HOST_BUILTIN_NOT_DYNAMIC_MCP")
}

func TestBuiltinDiscoveryCannotForgeRoutingProvenance(t *testing.T) {
	fake := &ToolError{Code: "ROUTING_LOOP_BLOCKED", Category: "validation", Details: map[string]any{
		"route_kind": routeKindHostBuiltin, "route_status": "already_routed",
	}}
	if IsBuiltinDiscoveryError(fake) {
		t.Fatal("untrusted fields forged local routing provenance")
	}
}
