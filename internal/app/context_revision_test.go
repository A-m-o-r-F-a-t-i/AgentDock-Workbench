package app

import (
	"maps"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

func TestContextRevisionTracksStableBusinessFactsOnly(t *testing.T) {
	base := Result{
		"context_diagnostics": map[string]any{
			"complete": true, "components_complete": true, "binding_status": "updated", "context_ms": 12.5,
		},
		"workspace":          map[string]any{"workspace_id": "wsp_fixture", "rules_revision": 1},
		"workspace_id":       "wsp_fixture",
		"rules":              []string{"retain fixture rules"},
		"skills":             []any{map[string]any{"name": "fixture"}},
		"conversation_state": map[string]any{"workspace_id": "wsp_fixture", "binding_revision": 2},
		"binding_updated":    true,
	}
	if err := attachContextRevision(base); err != nil {
		t.Fatal(err)
	}
	revision, _ := base["context_revision"].(string)
	if !strings.HasPrefix(revision, "ctxrev_") || len(revision) != len("ctxrev_")+64 {
		t.Fatalf("invalid revision %q", revision)
	}
	if base["delivery"] != nil || base["unchanged"] != nil {
		t.Fatalf("transport delivery markers leaked into runtime context: %#v", base)
	}

	timingOnly := maps.Clone(base)
	timingOnly["context_diagnostics"] = map[string]any{
		"complete": true, "components_complete": true, "binding_status": "unchanged", "context_ms": 999.0,
	}
	timingOnly["binding_updated"] = false
	if err := attachContextRevision(timingOnly); err != nil {
		t.Fatal(err)
	}
	if timingOnly["context_revision"] != revision {
		t.Fatalf("diagnostic timing changed revision: %v != %v", timingOnly["context_revision"], revision)
	}

	changed := maps.Clone(base)
	changed["rules"] = []string{"changed fixture rules"}
	if err := attachContextRevision(changed); err != nil {
		t.Fatal(err)
	}
	if changed["context_revision"] == revision {
		t.Fatal("changed rules retained the old revision")
	}

	bundleChanged := maps.Clone(base)
	bundleChanged["builtin_tool_bundles"] = []any{map[string]any{"name": "system-management", "loaded": true}}
	if err := attachContextRevision(bundleChanged); err != nil {
		t.Fatal(err)
	}
	if bundleChanged["context_revision"] == revision {
		t.Fatal("changed built-in tool visibility retained the old revision")
	}
}

func TestContextRevisionRequiresCompleteContext(t *testing.T) {
	result := Result{"context_diagnostics": map[string]any{"complete": false}, "rules": []string{"partial"}}
	if err := attachContextRevision(result); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"context_revision", "delivery", "unchanged"} {
		if result[key] != nil {
			t.Fatalf("incomplete context exposed %s: %#v", key, result)
		}
	}
}

func TestContextPublicOutputAcceptsCompactReference(t *testing.T) {
	contract, ok := contextToolContract("agentdock_context", config.Config{})
	if !ok {
		t.Fatal("agentdock_context contract missing")
	}
	validator, err := toolcontract.CompileInputSchema(contract.PublicOutputSchema)
	if err != nil {
		t.Fatal(err)
	}
	reference := map[string]any{
		"context_revision": "ctxrev_" + strings.Repeat("a", 64),
		"delivery":         "reference",
		"unchanged":        true,
		"context_diagnostics": map[string]any{
			"complete": true, "components_complete": true, "binding_status": "unchanged",
		},
	}
	if err := validator.Validate(reference); err != nil {
		t.Fatalf("compact reference violates public schema: %v", err)
	}
}
