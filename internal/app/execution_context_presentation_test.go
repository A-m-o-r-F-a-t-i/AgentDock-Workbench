package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContextExecutionTitlesIdentifyRequestKind(t *testing.T) {
	runtime := &Runtime{}
	cursor := "scur_1234567890abcdef1234567890abcdef"
	revision := "ctxrev_1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	for _, test := range []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"context", "agentdock_context", map[string]any{}, "加载上下文"},
		{"context_detail", "agentdock_context", map[string]any{"cursor": cursor}, "读取上下文详情 · scur_1234567890abc…"},
		{"context_refresh", "agentdock_context", map[string]any{"force_refresh": true}, "刷新上下文"},
		{"context_revision", "agentdock_context", map[string]any{"known_context_revision": revision}, "检查上下文版本 · ctxrev_1234567890a…"},
		{"workspace", "workspace_context", map[string]any{}, "读取工作区规则"},
		{"workspace_detail", "workspace_context", map[string]any{"cursor": cursor}, "读取工作区规则详情 · scur_1234567890abc…"},
		{"workspace_refresh", "workspace_context", map[string]any{"force_refresh": true}, "刷新工作区规则"},
		{"workspace_revision", "workspace_context", map[string]any{"known_context_revision": revision}, "检查工作区规则版本 · ctxrev_1234567890a…"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := runtime.describeExecution(test.tool, test.args, executionObservation{}); got != test.want {
				t.Fatalf("describeExecution() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestContextExecutionParametersKeepSupportedSelectors(t *testing.T) {
	runtime := &Runtime{}
	args := map[string]any{
		"cursor":                 "scur_1234567890abcdef1234567890abcdef",
		"known_context_revision": "ctxrev_1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		"force_refresh":          true,
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(runtime.executionParameters(args)), &summary); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"cursor", "known_context_revision", "force_refresh"} {
		if summary[key] == nil {
			t.Fatalf("parameter summary omitted %s: %#v", key, summary)
		}
	}
	if got, _ := summary["cursor"].(string); !strings.HasPrefix(got, "scur_") {
		t.Fatalf("cursor was not preserved: %#v", summary)
	}
}
