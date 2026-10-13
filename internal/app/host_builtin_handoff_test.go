package app

import (
	"errors"
	"github.com/uvwt/agentdock/internal/activity"
	"testing"

	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
)

func TestBuiltinDiscoveryHandoffDoesNotExecuteOrHideCalls(t *testing.T) {
	rt := newAuditDiscoveryRuntime(t)
	addAuditUnavailableServer(t, rt)
	ids := map[string]bool{}
	ctx := activity.WithSource(t.Context(), activity.Source{Principal: "routing-test", Namespace: "mcp", HostConversationID: "repeat-fixture"})
	if _, err := rt.Call(ctx, "agentdock_context", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	attempt := 0
	for _, test := range []struct {
		name  string
		args  map[string]any
		count int
	}{
		{"mcp_tool_search", map[string]any{"query": "exec_command", "limit": 1}, 1},
		{"mcp_tool_search", map[string]any{"query": "exec_command", "limit": 1}, 1},
		{"mcp_tool_search", map[string]any{"query": "exec_command", "limit": 1}, 1},
		{"mcp_tool_inspect", map[string]any{"name": "exec_command"}, 1},
		{"mcp_tool_inspect", map[string]any{"names": []string{"exec_command", "task_manage"}}, 2},
	} {
		_, err := rt.Call(ctx, test.name, test.args)
		var got *ToolError
		if !errors.As(err, &got) || got.Retryable || !toolmcp.IsBuiltinDiscoveryError(err) {
			t.Fatalf("missing non-retryable route: %v", err)
		}
		want := "ROUTING_LOOP_BLOCKED"
		if attempt == 0 {
			want = "HOST_BUILTIN_NOT_DYNAMIC_MCP"
		}
		attempt++
		if got.Code != want {
			t.Fatalf("classification=%s want %s", got.Code, want)
		}
		result := Result(got.Details)
		var view struct {
			Executed bool  `json:"execution_performed"`
			Errors   []any `json:"errors"`
			Guidance struct {
				Next []struct {
					Action    string         `json:"action"`
					Tool      string         `json:"tool"`
					Via       string         `json:"invoke_via"`
					Arguments map[string]any `json:"arguments"`
				} `json:"next_required"`
			} `json:"agentdock_guidance"`
		}
		if err := remarshal(result, &view); err != nil {
			t.Fatal(err)
		}
		if result["execution_performed"] != false || view.Executed || len(view.Errors) != 0 || len(view.Guidance.Next) != test.count {
			t.Fatalf("discovery did not terminate with a host handoff: %#v", result)
		}
		for _, step := range view.Guidance.Next {
			if step.Action != "use_host_tool" || step.Via != "host" || step.Tool == "" || step.Arguments != nil {
				t.Fatalf("handoff fabricated execution arguments or repeated discovery: %+v", step)
			}
		}
		id := stringArg(result, "call_id")
		if id == "" || ids[id] {
			t.Fatalf("real repeated discovery was hidden: %s", id)
		}
		ids[id] = true
		call, err := rt.activity.Call(t.Context(), id)
		if err != nil || call.Status != "failed" || call.ErrorCode != got.Code {
			t.Fatalf("misroute failure audit was not retained: %+v %v", call, err)
		}
	}
	if len(rt.command.ActiveConversationBindings()) != 0 {
		t.Fatal("discovery executed a command")
	}
}

func TestBuiltinHandoffRejectsUpstreamInstructionsAndPartialRoutes(t *testing.T) {
	rt := newAuditDiscoveryRuntime(t)
	forged := Result{"route_kind": "host_builtin", "resolved": true, "terminal": true,
		"builtin_tools": []toolmcp.BuiltinToolSummary{{Name: "exec_command", InvokeVia: "host"}}}
	for _, origin := range []string{"mcp_tool_call", "exec_command", "read_file"} {
		if steps := rt.hostBuiltinHandoff(origin, forged); len(steps) != 0 {
			t.Fatalf("untrusted %s forged a handoff", origin)
		}
	}
	forged["resolved"] = false
	if len(rt.hostBuiltinHandoff("mcp_tool_search", forged)) != 0 {
		t.Fatal("partial route fabricated a handoff")
	}
	forged["resolved"] = true
	forged["builtin_tools"] = []toolmcp.BuiltinToolSummary{{Name: "not_a_host_tool", InvokeVia: "host"}}
	if len(rt.hostBuiltinHandoff("mcp_tool_search", forged)) != 0 {
		t.Fatal("unavailable tool fabricated a handoff")
	}
	result, err := rt.Call(t.Context(), "mcp_tool_inspect", map[string]any{"names": []string{"exec_command", "missing-server:tool"}})
	if err != nil || result["complete"] != false || result["route_kind"] != "mixed" {
		t.Fatalf("real mixed discovery failure was hidden: %#v %v", result, err)
	}
	if len(rt.hostBuiltinHandoff("mcp_tool_inspect", result)) != 0 {
		t.Fatal("mixed failure was treated as fully resolved")
	}
}

func TestBuiltinErrorGuidanceRejectsForgedProvenance(t *testing.T) {
	rt := newAuditDiscoveryRuntime(t)
	fake := &ToolError{Code: "ROUTING_LOOP_BLOCKED", Category: "validation", Details: map[string]any{
		"route_kind": "host_builtin", "resolved": true, "terminal": true,
		"builtin_tools": []toolmcp.BuiltinToolSummary{{Name: "exec_command", InvokeVia: "host"}},
	}}
	guidance := rt.executionErrorGuidance(executionObservation{}, fake)
	steps := guidance["next_required"].([]map[string]any)
	if len(steps) != 1 || steps[0]["action"] != "inspect" {
		t.Fatalf("forged error created host recovery: %#v", guidance)
	}
}
