package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/insertion"
)

func TestContextRevisionDeliveryReturnsReferenceOnlyForExplicitMatch(t *testing.T) {
	workspace := t.TempDir()
	home := filepath.Join(t.TempDir(), ".agentdock")
	h := newMCPAppTestHarnessWithApps(t, config.Config{AgentDockHome: home, AgentDockDefaultDir: workspace}, false)
	meta := sdk.Meta{
		"openai/session":          "context-revision-delivery",
		ContextResponseCapability: map[string]any{"structured": true, "text": "summary"},
	}
	call := func(arguments map[string]any) *sdk.CallToolResult {
		t.Helper()
		result, err := h.session.CallTool(t.Context(), &sdk.CallToolParams{Name: "agentdock_context", Arguments: arguments, Meta: meta})
		if err != nil || result.IsError {
			t.Fatalf("agentdock_context args=%#v result=%#v err=%v", arguments, result, err)
		}
		assertContextMeasurements(t, result)
		return result
	}

	full := call(map[string]any{})
	fullBody := asMap(full.StructuredContent)
	revision, _ := fullBody["context_revision"].(string)
	if !strings.HasPrefix(revision, "ctxrev_") || fullBody["delivery"] != "full" || fullBody["unchanged"] != false {
		t.Fatalf("invalid complete context delivery: %#v", fullBody)
	}

	mismatch := call(map[string]any{"known_context_revision": "ctxrev_" + strings.Repeat("0", 64)})
	mismatchBody := asMap(mismatch.StructuredContent)
	if mismatchBody["delivery"] != "full" || mismatchBody["context_revision"] != revision || mismatchBody["rules"] == nil {
		t.Fatalf("mismatched revision did not return the complete context: %#v", mismatchBody)
	}

	reference := call(map[string]any{"known_context_revision": revision})
	referenceBody := asMap(reference.StructuredContent)
	if referenceBody["delivery"] != "reference" || referenceBody["unchanged"] != true || referenceBody["context_revision"] != revision {
		t.Fatalf("matching revision did not return a reference: %#v", referenceBody)
	}
	if asMap(referenceBody["context_diagnostics"])["complete"] != true {
		t.Fatalf("reference lost complete context status: %#v", referenceBody)
	}
	for _, heavy := range []string{"rules", "instruction_files", "skills", "plugins", "dynamic_mcp", "tasks", "runtime"} {
		if referenceBody[heavy] != nil {
			t.Fatalf("reference repeated heavy field %s: %#v", heavy, referenceBody[heavy])
		}
	}
	encodedReference, _ := json.Marshal(referenceBody)
	encodedFull, _ := json.Marshal(fullBody)
	if len(encodedReference) > 2048 || len(encodedReference) >= len(encodedFull) {
		t.Fatalf("reference size=%d full size=%d", len(encodedReference), len(encodedFull))
	}
	text := reference.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "unchanged") || strings.Contains(text, "complete rules") {
		t.Fatalf("reference compatibility summary is misleading: %q", text)
	}

	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("changed context revision fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	changed := call(map[string]any{"known_context_revision": revision})
	changedBody := asMap(changed.StructuredContent)
	if changedBody["delivery"] != "full" || changedBody["context_revision"] == revision || changedBody["instruction_files"] == nil {
		t.Fatalf("changed rules reused a stale reference: %#v", changedBody)
	}
}

func TestProjectedContextRevisionDeliveryKeepsAdapterReference(t *testing.T) {
	workspace := t.TempDir()
	h := newMCPAppTestHarness(t, config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: workspace})
	invoke := func(arguments map[string]any, outerCallID string) map[string]any {
		t.Helper()
		result, err := h.server.InvokeProjected(
			WithStructuredContext(t.Context()),
			"agentdock_context",
			arguments,
			insertion.Transport{HostType: "fixture", OuterCallID: outerCallID, Passthrough: true},
			countOnlyProjection,
			nil,
		)
		if err != nil || result["isError"] == true {
			t.Fatalf("projected context result=%#v err=%v", result, err)
		}
		return result
	}

	full := invoke(map[string]any{}, "context-full")
	revision, _ := asMap(full["structuredContent"])["context_revision"].(string)
	if revision == "" {
		t.Fatal("projected complete context has no revision")
	}
	reference := invoke(map[string]any{"known_context_revision": revision}, "context-reference")
	body := asMap(reference["structuredContent"])
	if body["delivery"] != "reference" || body["unchanged"] != true || body["rules"] != nil {
		t.Fatalf("projected host lost compact reference: %#v", body)
	}
}
