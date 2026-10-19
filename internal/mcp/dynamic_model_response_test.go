package mcp

import (
	"encoding/json"
	"maps"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/uvwt/agentdock/internal/config"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

func compactMCPFixture() map[string]any {
	business := map[string]any{"target": "board", "revision": "r1", "items": []any{}}
	encoded, _ := json.Marshal(business)
	return map[string]any{
		"isError": false,
		"content": []any{map[string]any{"type": "text", "text": "old task_id=hidden"}},
		"structuredContent": map[string]any{
			"name": "fixture:objects", "executed": true,
			"call_id": "call_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "conversation_id": "conv-hidden", "task_id": "task-hidden", "workspace_id": "workspace-hidden",
			"capture":    map[string]any{"state": "complete", "complete": true, "source_ref": "activity://hidden"},
			"source_ref": "activity://hidden", "managed_target": map[string]any{"server": "fixture"},
			"catalog_revision": "r1", "server_fingerprint": "fingerprint", "schema_digest": "schema", "annotations_digest": "annotations",
			"mcp_catalog":      map[string]any{"catalog_revision": "r1", "server_fingerprint": "fingerprint", "complete": true, "stale": false},
			"host_page":        map[string]any{"mode": "full", "complete": true, "page_index": 0, "page_count": 1, "returned_bytes": 71},
			"model_projection": map[string]any{"wire_chars": 3000},
			"result":           map[string]any{"isError": false, "content": []any{map[string]any{"type": "text", "text": string(encoded)}}, "structuredContent": business},
		},
	}
}

func TestMCPModelSuccessHasBoundedEnvelopeAndOneBusinessCopy(t *testing.T) {
	for _, textOnly := range []bool{false, true} {
		ctx := WithStructuredResults(t.Context())
		if textOnly {
			var err error
			ctx, err = contextResponseOptions(ctx, map[string]any{StructuredResultsCapability: map[string]any{"structured": false, "text": responseTextOnly}})
			if err != nil {
				t.Fatal(err)
			}
		}
		original := compactMCPFixture()
		before, _ := json.Marshal(original)
		logical := normalizeResponseStages("mcp_tool_call", nil, original)
		wire, err := modelWireEnvelope(ctx, "mcp_tool_call", logical)
		if err != nil {
			t.Fatal(err)
		}
		payload := asMap(wire["structuredContent"])
		if textOnly {
			if wire["structuredContent"] != nil {
				t.Fatal("text-only delivery included a second structured body")
			}
			if err := json.Unmarshal([]byte(textBlocks(wire["content"])[0]), &payload); err != nil {
				t.Fatal(err)
			}
		}
		if len(payload) != 3 || payload["error"] != nil || len(asMap(payload["continuation"])) != 0 {
			t.Fatalf("small successful wrapper is not compact: %#v", payload)
		}
		result := asMap(payload["result"])
		business := asMap(asMap(original["structuredContent"])["result"])["structuredContent"]
		if !reflect.DeepEqual(result["structuredContent"], business) || result["content"] != nil {
			t.Fatalf("business result changed or duplicate text survived: %#v", result)
		}
		body, _ := json.Marshal(business)
		chars, err := modelEnvelopeChars(wire)
		if err != nil || chars-utf8.RuneCount(body) > 800 {
			t.Fatalf("small-result wrapping exceeds 800 characters: total=%d business=%d err=%v", chars, utf8.RuneCount(body), err)
		}
		after, _ := json.Marshal(original)
		if string(before) != string(after) {
			t.Fatal("model projection changed the retained Activity envelope")
		}
		for _, text := range textBlocks(wire["content"]) {
			if strings.Contains(text, "task-hidden") || strings.Contains(text, "workspace-hidden") || strings.Contains(text, "task_id=hidden") {
				t.Fatal("the summary leaked omitted Activity diagnostics")
			}
		}
	}
}

func TestMCPModelProjectionPreservesHostPagingAndDomainCursor(t *testing.T) {
	original := compactMCPFixture()
	full := asMap(original["structuredContent"])
	asMap(asMap(full["result"])["structuredContent"])["cursor"] = "domain-cursor"
	full["host_page"] = map[string]any{"mode": "semantic_records", "complete": false, "page_index": 0, "page_count": 3, "returned_bytes": 4000}
	full["continue_result"] = map[string]any{"tool": "mcp_result_continue", "arguments": map[string]any{"call_id": "original-call", "cursor": "host-next"}}
	projected := projectMCPModelEnvelope("mcp_tool_call", original)
	payload := asMap(projected["structuredContent"])
	continuation := asMap(payload["continuation"])
	if continuation["tool"] != "mcp_result_continue" || !reflect.DeepEqual(continuation["arguments"], asMap(full["continue_result"])["arguments"]) || continuation["complete"] != false || continuation["page_count"] != 3 {
		t.Fatalf("exact host continuation was lost: %#v", continuation)
	}
	if continuation["returned_bytes"] != nil || asMap(asMap(payload["result"])["structuredContent"])["cursor"] != "domain-cursor" {
		t.Fatal("host diagnostics or domain cursor changed")
	}
	if repeated := projectMCPModelEnvelope("mcp_tool_call", projected); !reflect.DeepEqual(asMap(repeated["structuredContent"])["continuation"], continuation) {
		t.Fatal("reapplying the presentation boundary lost the host cursor")
	}
	last := maps.Clone(full)
	last["host_page"] = map[string]any{"mode": "semantic_records", "complete": true, "page_index": 2, "page_count": 3}
	delete(last, "continue_result")
	lastPayload := asMap(projectMCPModelEnvelope("mcp_result_continue", map[string]any{"structuredContent": last})["structuredContent"])
	if asMap(lastPayload["continuation"])["complete"] != true || asMap(lastPayload["continuation"])["tool"] != nil {
		t.Fatal("terminal host page advertised another call")
	}
	replay := map[string]any{"replayed": true, "content_returned": false, "complete": false, "result_call_id": "original-call", "resume_cursor": "host-next"}
	replayed := asMap(projectMCPModelEnvelope("mcp_result_continue", map[string]any{"structuredContent": replay})["structuredContent"])
	if replayed["replayed"] != true || replayed["content_returned"] != false || asMap(asMap(replayed["continuation"])["arguments"])["call_id"] != "original-call" {
		t.Fatalf("replayed page lost its original result identity: %#v", replayed)
	}
	// Some upstream services report failure in structuredContent while leaving
	// isError=false. Continuation must retain the frozen failure independently.
	last["upstream_failure"] = map[string]any{"code": "PARTIAL_WRITE", "operation_status": "failed", "retry_safe": false}
	last["isError"] = false
	continuedFailure := asMap(projectMCPModelEnvelope("mcp_result_continue", map[string]any{"isError": false, "structuredContent": last})["structuredContent"])
	if !reflect.DeepEqual(continuedFailure["upstream_failure"], last["upstream_failure"]) {
		t.Fatal("a continuation with isError=false lost the original structured failure")
	}
}

func TestMCPModelProjectionKeepsFailuresApprovalsCatalogChangesAndAttachments(t *testing.T) {
	original := compactMCPFixture()
	full := asMap(original["structuredContent"])
	metadata := map[string]any{"mcp/www_authenticate": []any{"Bearer"}, "id": json.Number("9007199254740993")}
	image := map[string]any{"type": "image", "mimeType": "image/png", "data": "AQID", "_meta": metadata}
	resource := map[string]any{"type": "resource_link", "uri": "file:///artifact.txt", "name": "artifact"}
	text := map[string]any{"type": "text", "text": `{"id":9007199254740993}`}
	upstream := asMap(full["result"])
	upstream["structuredContent"] = map[string]any{"id": json.Number("9007199254740992"), "_meta": metadata}
	upstream["content"], upstream["_meta"] = []any{text, image, resource}, metadata
	original["content"] = []any{image, resource}
	full["agentdock_guidance"] = map[string]any{"response_additions": []any{map[string]any{"text": "user instruction"}}}
	full["catalog_changed"] = true
	projected := projectMCPModelEnvelope("mcp_tool_call", original)
	payload := asMap(projected["structuredContent"])
	if !reflect.DeepEqual(payload["result"], upstream) || !reflect.DeepEqual(payload["agentdock_guidance"], full["agentdock_guidance"]) {
		t.Fatal("unique upstream text, exact numbers, metadata or trusted guidance changed")
	}
	blocks := envelopeBlocks(projected["content"])
	if len(blocks) != 3 || !reflect.DeepEqual(blocks[1], image) || !reflect.DeepEqual(blocks[2], resource) {
		t.Fatal("native attachments changed")
	}
	if payload["catalog_changed"] != true || asMap(payload["mcp_catalog"])["catalog_revision"] != "r1" {
		t.Fatal("a changed catalog became invisible")
	}
	original["isError"] = true
	full["error"], full["code"], full["executed"] = "partial write", "MCP_ERROR", true
	full["effect_state"], full["retry_safe"] = "partial", false
	full["required_action"] = map[string]any{"tool": "inspect", "arguments": map[string]any{"receipt": "fixed-receipt"}}
	full["upstream_failure"] = map[string]any{"code": "PARTIAL_WRITE", "execution_performed": true}
	failure := asMap(projectMCPModelEnvelope("mcp_tool_call", original)["structuredContent"])
	for _, key := range []string{"error", "code", "executed", "effect_state", "retry_safe", "required_action", "upstream_failure"} {
		if !reflect.DeepEqual(failure[key], full[key]) {
			t.Fatalf("failure decision field %s changed", key)
		}
	}
	pending := map[string]any{"status": "pending_approval", "executed": false, "approval_id": "approval-fixed", "call_id": "pending-fixed"}
	approval := asMap(projectMCPModelEnvelope("mcp_tool_call", map[string]any{"structuredContent": pending})["structuredContent"])
	for key, value := range pending {
		if approval[key] != value {
			t.Fatalf("pending approval lost %s", key)
		}
	}
	if raw := projectMCPModelEnvelope("mcp_result_raw", original); !reflect.DeepEqual(raw, original) {
		t.Fatal("explicit raw diagnostics were projected")
	}
}

func TestMCPModelResponseBudgetMeasuresProjectedBody(t *testing.T) {
	h := newMCPAppTestHarness(t, config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()})
	original := compactMCPFixture()
	full := asMap(original["structuredContent"])
	full["managed_target"] = map[string]any{"diagnostics": strings.Repeat("x", 20000)}
	logical, err := h.server.boundModelResponse(WithStructuredResults(t.Context()), "mcp_tool_call", nil, nil, original)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(asMap(logical["structuredContent"])["managed_target"], full["managed_target"]) {
		t.Fatal("host diagnostics were not retained for Activity")
	}
	wire, err := modelWireEnvelope(WithStructuredResults(t.Context()), "mcp_tool_call", logical)
	if err != nil {
		t.Fatal(err)
	}
	chars, err := modelEnvelopeChars(wire)
	if err != nil || chars > toolcontract.ModelVisibleTotalChars {
		t.Fatalf("projected response exceeds wire budget: chars=%d err=%v", chars, err)
	}
	if asMap(asMap(wire["structuredContent"])["result"])["structuredContent"] == nil {
		t.Fatal("large hidden diagnostics forced the small business result into a source receipt")
	}
	measurement := asMap(asMap(logical["structuredContent"])["model_projection"])
	if measurement["wire_chars"] != chars {
		t.Fatalf("Activity measured diagnostics instead of actual wire: %#v wire=%d", measurement, chars)
	}
}
