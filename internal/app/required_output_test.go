package app

import (
	"testing"

	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

func TestRequiredSourceReadOnlyWhenRecoveryIsNeeded(t *testing.T) {
	for _, test := range []struct {
		name           string
		hasMore        bool
		sourceComplete bool
		page           Result
		want           bool
	}{
		{name: "complete single page", sourceComplete: true, page: Result{}, want: false},
		{name: "remaining required page", hasMore: true, sourceComplete: true, page: Result{}, want: true},
		{name: "partial source", sourceComplete: false, page: Result{}, want: true},
		{name: "indivisible unit", sourceComplete: true, page: Result{"required_unit_unavailable": true}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := requiredSourceReadNeeded(test.hasMore, test.sourceComplete, test.page); got != test.want {
				t.Fatalf("requiredSourceReadNeeded() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestContextRequiredOutputUsesServerOwnedBudget(t *testing.T) {
	want := toolcontract.ModelVisibleBodyChars
	for _, name := range []string{"agentdock_context", "workspace_context"} {
		for _, requested := range []int{4096, 65536} {
			if got := requiredOutputPageBudget(name, map[string]any{"max_response_bytes": requested}); got != want {
				t.Fatalf("%s budget with request %d = %d, want %d", name, requested, got, want)
			}
		}
	}
	if low, high := requiredOutputPageBudget("mcp_tool_inspect", map[string]any{"max_response_bytes": 4096}), requiredOutputPageBudget("mcp_tool_inspect", map[string]any{"max_response_bytes": 65536}); low >= high {
		t.Fatalf("non-Context tools should retain caller budget control: low=%d high=%d", low, high)
	}
}

func TestContextStructuredContinuationUsesOnlyCursor(t *testing.T) {
	cursor := "scur_1234567890abcdef1234567890abcdef"
	for _, name := range []string{"agentdock_context", "workspace_context"} {
		steps := structuredOutputFollowups(name, map[string]any{
			"workdir": "C:\\workspace", "known_context_revision": "ctxrev_example", "max_response_bytes": 4096,
		}, Result{
			"has_more": true, "next_cursor": cursor,
			"structured_output": map[string]any{"minimum_response_bytes": 65536},
		})
		if len(steps) != 1 {
			t.Fatalf("%s continuation steps = %#v", name, steps)
		}
		arguments := modelObject(steps[0]["arguments"])
		if len(arguments) != 1 || arguments["cursor"] != cursor {
			t.Fatalf("%s continuation arguments = %#v", name, arguments)
		}
	}
}

func TestContextReplayReceiptUsesOnlyNextCursor(t *testing.T) {
	cursor := "scur_abcdef1234567890abcdef1234567890"
	for _, name := range []string{"agentdock_context", "workspace_context"} {
		receipt := (&Runtime{}).structuredCursorReplayReceipt(&structuredOutputSnapshot{
			ID: name, Tool: name,
			ResumeArguments: map[string]any{"workdir": "C:\\workspace", "max_response_bytes": 4096},
		}, structuredOutputCursor{NextCursor: cursor})
		arguments := modelObject(receipt["next_arguments"])
		if len(arguments) != 1 || arguments["cursor"] != cursor {
			t.Fatalf("%s replay continuation arguments = %#v", name, arguments)
		}
	}
}
