package mcp

import (
	"encoding/json"
	"reflect"
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
