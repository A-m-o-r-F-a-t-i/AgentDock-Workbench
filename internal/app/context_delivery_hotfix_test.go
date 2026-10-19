package app

import (
	"strings"
	"testing"
	"time"
)

func TestContextCompleteDeliveryIsClaimedOncePerSharedBuild(t *testing.T) {
	snapshots := &contextSnapshots{deliveries: map[uint64]time.Time{}}
	now := time.Now()
	if !snapshots.claimCompleteDelivery(42, now) {
		t.Fatal("first waiter did not claim the shared Context delivery")
	}
	if snapshots.claimCompleteDelivery(42, now.Add(time.Second)) {
		t.Fatal("duplicate waiter was allowed to create a second full Context sequence")
	}
	if !snapshots.claimCompleteDelivery(42, now.Add(contextDeliveryClaimTTL+time.Second)) {
		t.Fatal("expired delivery claim was not released")
	}
}

func TestContextReferenceResumeRequiresKnownRevision(t *testing.T) {
	for _, test := range []struct {
		name string
		args map[string]any
		want bool
	}{
		{name: "empty", args: map[string]any{}, want: false},
		{name: "known_revision", args: map[string]any{"known_context_revision": "ctxrev_" + strings.Repeat("a", 64)}, want: true},
		{name: "explicit_cursor", args: map[string]any{"cursor": "scur_123"}, want: false},
		{name: "force_refresh", args: map[string]any{"known_context_revision": "ctxrev_" + strings.Repeat("b", 64), "force_refresh": true}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := contextReferenceRequested(test.args); got != test.want {
				t.Fatalf("contextReferenceRequested(%#v) = %v, want %v", test.args, got, test.want)
			}
		})
	}
}

func TestSharedContextReferenceBypassesRequiredFullPagination(t *testing.T) {
	runtime := executionTestRuntime(t)
	p := &preparedExecution{
		modelOutput: true,
		spec:        ToolSpec{Name: "agentdock_context"},
		args:        map[string]any{},
	}
	result := Result{
		"context_delivery": "shared_reference",
		"context_revision": "ctxrev_" + strings.Repeat("a", 64),
		"unchanged":        true,
		"context_ready":    true,
		"context_diagnostics": map[string]any{
			"complete": true, "components_complete": true, "binding_status": "shared",
		},
		"operation": map[string]any{"state": "completed", "ok": true, "executed": false},
		"capture":   map[string]any{"state": "complete", "complete": true},
	}
	projected := runtime.applyStructuredOutputPolicy(p, result)
	if projected["structured_output"] != nil || projected["next_cursor"] != nil || stringArg(projected, "context_delivery") != "shared_reference" {
		t.Fatalf("shared Context reference was expanded by required_full pagination: %#v", projected)
	}
}
