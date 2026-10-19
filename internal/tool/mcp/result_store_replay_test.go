package mcp

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

func TestResultReplayKeepsOriginalResultIdentity(t *testing.T) {
	now := time.Unix(1000, 0)
	binding := activity.Binding{SourceOwnerKey: "owner", ConversationID: "conversation", CallID: "call_original"}
	store := resultStore{
		now: func() time.Time { return now },
		entries: map[string]*storedMCPResult{
			"call_original": {
				CallID: "call_original", Binding: binding, Pages: []resultPage{{}, {}},
				NextPage: 2, LastCursor: "hostcursor_replay", LastPage: 1, NextCursor: "hostcursor_next",
				CreatedAt: now, TouchedAt: now,
			},
		},
		cursors: map[string]resultCursorState{
			"hostcursor_replay": {CallID: "call_original", PageIndex: 1},
		},
		latestScope: map[string]string{},
		rootBudgets: map[string]resultRootBudget{},
	}

	result, err := store.continueResult(binding, "call_original", "hostcursor_replay")
	if err != nil {
		t.Fatal(err)
	}
	if result["result_call_id"] != "call_original" || result["replayed"] != true || result["content_returned"] != false {
		t.Fatalf("replay receipt lost its result identity: %#v", result)
	}
}

func TestNormalizedResultKeepsDistinctLargeNumberTextAndMetadata(t *testing.T) {
	const text = `{"id":9007199254740993}`
	metadata := map[string]any{"revision": json.Number("18446744073709551615")}
	image := map[string]any{"type": "image", "data": "AQID", "mimeType": "image/png", "_meta": metadata}
	raw := map[string]any{
		"structuredContent": map[string]any{"id": json.Number("9007199254740992")},
		"content":           []any{map[string]any{"type": "text", "text": text}, image},
		"_meta":             metadata,
	}
	normalized, _, parsed := normalizeDynamicMCPResult(raw)
	blocks := resultContentBlocks(normalized["content"])
	if parsed || len(blocks) != 2 || resultMap(blocks[0])["text"] != text || !reflect.DeepEqual(blocks[1], image) || !reflect.DeepEqual(normalized["_meta"], metadata) {
		t.Fatalf("normalization lost non-equivalent text, native content or metadata: %#v", normalized)
	}
	promoted, _, parsed := normalizeDynamicMCPResult(map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}})
	if !parsed || resultMap(promoted["structuredContent"])["id"] != json.Number("9007199254740993") || len(resultContentBlocks(promoted["content"])) != 0 {
		t.Fatalf("text-only JSON result was rounded or duplicated: %#v", promoted)
	}
}

func TestResultFailureFactsSurviveFragmentPagingAndReplay(t *testing.T) {
	for _, shape := range []string{"legacy_json_text", "standard_structured_error"} {
		t.Run(shape, func(t *testing.T) {
			facts := map[string]any{
				"code": "UPSTREAM_EDIT_PARTIAL", "message": "Inspect the modified object before another edit.",
				"operation_status": "failed", "effect_state": "partial", "execution_performed": true, "retry_safe": false,
				"required_action": map[string]any{"kind": "readback", "tool": "board:read", "arguments": map[string]any{"name": "original-target"}},
			}
			body := cloneResultMap(facts)
			body["ok"], body["payload"] = false, strings.Repeat("diagnostic detail ", 600)
			raw := map[string]any{}
			if shape == "legacy_json_text" {
				encoded, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				raw["content"] = []any{map[string]any{"type": "text", "text": string(encoded)}}
			} else {
				raw["isError"] = true
				raw["structuredContent"] = map[string]any{"error": cloneResultMap(facts), "payload": body["payload"]}
			}
			normalized, kinds, parsed := normalizeDynamicMCPResult(raw)
			binding := activity.Binding{SourceOwnerKey: "owner", ConversationID: "conversation", CallID: "call_failure"}
			store := resultStore{}
			page, err := store.store(binding, "board:edit", raw, normalized, kinds, parsed)
			if err != nil {
				t.Fatal(err)
			}
			assertFacts := func(result Result) {
				t.Helper()
				if !resultValuesEqual(result["upstream_failure"], facts) {
					t.Fatalf("original failure facts changed or disappeared: %#v", result["upstream_failure"])
				}
				encoded, err := json.Marshal(result)
				if err != nil || len(encoded) > resultFailurePageBudget {
					t.Fatalf("failure facts exceeded the minimum page budget: bytes=%d err=%v", len(encoded), err)
				}
			}
			var collected strings.Builder
			pages := 0
			for {
				assertFacts(page)
				if resultMap(page["host_page"])["mode"] != "json_fragment" {
					t.Fatalf("fixture did not exercise fragmented results: %#v", page["host_page"])
				}
				if page["isError"] != (pages == 0 && raw["isError"] == true) {
					t.Fatalf("page %d confused the upstream failure with the host read outcome: %#v", pages, page)
				}
				if ResultReadSucceeded(map[string]any(page)) != (pages > 0) {
					t.Fatalf("page %d has incorrect host read provenance", pages)
				}
				fragment, _ := resultMap(page["result"])["json_fragment"].(string)
				if fragment == "" {
					t.Fatal("failure envelope displaced the actual payload")
				}
				collected.WriteString(fragment)
				pages++
				cursor, _ := page["host_cursor"].(string)
				if cursor == "" {
					break
				}
				// A caller must not be able to mutate the retained action through a page.
				resultMap(resultMap(resultMap(page["upstream_failure"])["required_action"])["arguments"])["name"] = "changed-by-reader"
				page, err = store.continueResult(binding, binding.CallID, cursor)
				if err != nil {
					t.Fatal(err)
				}
				replay, err := store.continueResult(binding, binding.CallID, cursor)
				if err != nil || replay["isError"] == true || replay["replayed"] != true || replay["content_returned"] != false || replay["result"] != nil {
					t.Fatalf("replayed cursor changed its consumption semantics: %#v %v", replay, err)
				}
				assertFacts(replay)
			}
			encoded, err := json.Marshal(normalized)
			if err != nil || pages < 2 || collected.String() != string(encoded) {
				t.Fatalf("fragmented payload was lost or rewritten: pages=%d err=%v", pages, err)
			}
			original, ok := store.rawCopy(binding.CallID)
			if !ok || !resultValuesEqual(original, raw) {
				t.Fatal("failure projection changed the retained original response")
			}
			rawPage, err := store.rawResult(binding, ResultRawRequest{CallID: binding.CallID})
			if err != nil || rawPage["data"] == "" {
				t.Fatalf("original failure is not readable: %#v %v", rawPage, err)
			}
			assertFacts(rawPage)
		})
	}
}

func TestResultFailureSemanticContinuationSeparatesReadOutcome(t *testing.T) {
	facts := map[string]any{
		"code": "UPSTREAM_EDIT_PARTIAL", "effect_state": "partial", "retry_safe": false,
		"required_action": map[string]any{"tool": "board:read", "arguments": map[string]any{"target": "original-target"}},
	}
	items := make([]any, 24)
	for index := range items {
		items[index] = map[string]any{"index": index, "detail": strings.Repeat("diagnostic ", 40)}
	}
	raw := map[string]any{"isError": true, "structuredContent": map[string]any{"error": cloneResultMap(facts), "items": items}}
	normalized, kinds, parsed := normalizeDynamicMCPResult(raw)
	binding := activity.Binding{SourceOwnerKey: "owner", ConversationID: "conversation", CallID: "call_semantic_failure"}
	store := resultStore{}
	page, err := store.store(binding, "board:edit", raw, normalized, kinds, parsed)
	if err != nil {
		t.Fatal(err)
	}
	cursor, _ := page["host_cursor"].(string)
	if cursor == "" {
		t.Fatal("fixture did not produce multiple pages")
	}
	otherBinding := binding
	otherBinding.ConversationID = "other-conversation"
	for _, invalid := range []struct {
		binding activity.Binding
		cursor  string
		code    string
	}{
		{binding, "hostcursor_invalid", "MCP_RESULT_CURSOR_EXPIRED"},
		{otherBinding, cursor, "MCP_RESULT_SCOPE_MISMATCH"},
	} {
		result, err := store.continueResult(invalid.binding, binding.CallID, invalid.cursor)
		var toolErr *ToolError
		if result != nil || !errors.As(err, &toolErr) || toolErr.Code != invalid.code {
			t.Fatalf("a failed continuation was hidden: result=%#v err=%v", result, err)
		}
	}
	collected := []any{}
	for pageIndex := 0; ; pageIndex++ {
		if page["isError"] != (pageIndex == 0) || !resultValuesEqual(page["upstream_failure"], facts) {
			t.Fatalf("page %d lost the distinction between read status and original failure: %#v", pageIndex, page)
		}
		if ResultReadSucceeded(map[string]any(page)) != (pageIndex > 0) {
			t.Fatalf("page %d has incorrect host read provenance", pageIndex)
		}
		if resultMap(page["host_page"])["mode"] != "semantic_records" || resultMap(page["result"])["isError"] != true {
			t.Fatalf("semantic paging rewrote the original failure: %#v", page)
		}
		body := resultMap(resultMap(page["result"])["structuredContent"])
		if !resultValuesEqual(body["error"], facts) {
			t.Fatal("semantic paging changed the upstream error details")
		}
		collected = append(collected, body["items"].([]any)...)
		cursor, _ = page["host_cursor"].(string)
		if cursor == "" {
			break
		}
		page, err = store.continueResult(binding, binding.CallID, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if page["result_call_id"] != binding.CallID {
			t.Fatal("continuation changed the originating result identity")
		}
	}
	if !resultValuesEqual(collected, items) {
		t.Fatal("continuation skipped or repeated semantic records")
	}
	original, ok := store.rawCopy(binding.CallID)
	if !ok || !resultValuesEqual(original, raw) {
		t.Fatal("continuation modified the retained original response")
	}
}

func TestResultFailureOmitsWholeOversizedAction(t *testing.T) {
	raw := map[string]any{"isError": true, "structuredContent": map[string]any{
		"code": "UPSTREAM_EDIT_PARTIAL", "effect_state": "partial", "retry_safe": false,
		"message":         strings.Repeat("large diagnostic ", 500),
		"required_action": map[string]any{"tool": "board:read", "arguments": map[string]any{"objects": strings.Repeat("x", 4096)}},
		"unknown_control": "must remain only in the raw result",
	}}
	normalized, kinds, parsed := normalizeDynamicMCPResult(raw)
	binding := activity.Binding{SourceOwnerKey: "owner", CallID: "call_oversized_failure"}
	store := resultStore{}
	page, err := store.store(binding, "board:edit", raw, normalized, kinds, parsed)
	if err != nil {
		t.Fatal(err)
	}
	facts := resultMap(page["upstream_failure"])
	if facts["code"] != "UPSTREAM_EDIT_PARTIAL" || facts["effect_state"] != "partial" || facts["retry_safe"] != false || facts["required_action"] != nil || facts["unknown_control"] != nil {
		t.Fatalf("bounded projection replaced original control facts or truncated an executable action: %#v", facts)
	}
	omitted := map[string]bool{}
	for _, value := range facts["omitted_fields"].([]any) {
		field, _ := value.(string)
		omitted[field] = true
	}
	encoded, err := json.Marshal(facts)
	if err != nil || len(encoded) > resultFailureBudget || !omitted["required_action"] || !omitted["message"] {
		t.Fatalf("oversized fields were silently dropped or exceeded their budget: %#v bytes=%d err=%v", facts, len(encoded), err)
	}
	original, ok := store.rawCopy(binding.CallID)
	if !ok || !resultValuesEqual(original, raw) {
		t.Fatal("oversized original action was not retained intact")
	}
}
