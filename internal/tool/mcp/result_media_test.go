package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
)

func TestResultMediaExtractedBeforePaging(t *testing.T) {
	data := bytes.Repeat([]byte("media"), 2000)
	encoded := base64.StdEncoding.EncodeToString(data)
	raw := map[string]any{
		"_meta": map[string]any{"large": strings.Repeat("transport", 2000)},
		"icons": []any{map[string]any{"src": "data:image/png;base64," + encoded}},
		"structuredContent": map[string]any{
			"image":    "data:image/png;base64," + encoded,
			"revision": json.Number("18446744073709551615"), "viewportUnchanged": false,
			"_meta": map[string]any{"business": "preserved"},
		},
		"content": []any{map[string]any{
			"type": "image", "data": encoded, "mimeType": "image/png",
			"_meta": map[string]any{"transport": true},
		}},
	}
	bodyJSON, err := json.Marshal(raw["structuredContent"])
	if err != nil {
		t.Fatal(err)
	}
	raw["content"] = append([]any{map[string]any{"type": "text", "text": string(bodyJSON)}}, raw["content"].([]any)...)
	delete(raw, "structuredContent")
	original := cloneResultMap(raw)
	calls := 0
	service := &Service{}
	service.SetMediaPublisher(func(_ context.Context, got []byte, mimeType string) (map[string]any, error) {
		calls++
		if !bytes.Equal(got, data) || mimeType != "image/png" {
			t.Fatal("media changed during extraction")
		}
		return map[string]any{"artifact_id": "retained-image", "url": "https://example.test/artifact"}, nil
	})
	normalized, kinds, parsed := normalizeDynamicMCPResult(raw)
	normalized = service.retainModelMedia(context.Background(), normalized)
	binding := activity.Binding{SourceOwnerKey: "owner", ConversationID: "conversation", CallID: "call_media"}
	page, err := service.results.store(binding, "board:screenshot", raw, normalized, kinds, parsed)
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(page)
	body := resultMap(normalized["structuredContent"])
	ref := resultMap(body["image"])
	if !parsed || calls != 1 || page["host_cursor"] != nil || len(wire) > resultPageBudget || strings.Contains(string(wire), encoded) {
		t.Fatalf("media/transport metadata entered JSON paging: publications=%d bytes=%d page=%#v", calls, len(wire), page["host_page"])
	}
	if normalized["_meta"] != nil || normalized["icons"] != nil || resultMap(resultContentBlocks(normalized["content"])[0])["_meta"] != nil {
		t.Fatal("transport metadata leaked into the model result")
	}
	if resultMap(ref["artifact"])["artifact_id"] != "retained-image" || ref["view_image"] == nil ||
		body["revision"] != json.Number("18446744073709551615") || body["viewportUnchanged"] != false ||
		resultMap(body["_meta"])["business"] != "preserved" {
		t.Fatalf("media reference or business facts were lost: %#v", body)
	}
	stored, ok := service.RawResult(binding.CallID)
	if !ok || !resultValuesEqual(stored, original) || !resultValuesEqual(raw, original) {
		t.Fatal("projection changed the original response")
	}
}

func TestResultMediaRetentionFailureKeepsBusinessOutcome(t *testing.T) {
	service := &Service{}
	service.SetMediaPublisher(func(context.Context, []byte, string) (map[string]any, error) {
		return nil, errors.New("storage unavailable")
	})
	raw := map[string]any{
		"structuredContent": map[string]any{"ok": true, "effect_state": "complete"},
		"content":           []any{map[string]any{"type": "audio", "data": "AQID", "mimeType": "audio/wav"}},
	}
	normalized, _, _ := normalizeDynamicMCPResult(raw)
	normalized = service.retainModelMedia(context.Background(), normalized)
	ref := resultMap(resultContentBlocks(normalized["content"])[0])
	if ref["media_error"] == nil || ref["data"] != nil || ref["artifact"] != nil ||
		normalized["isError"] == true || resultMap(normalized["structuredContent"])["effect_state"] != "complete" {
		t.Fatalf("media failure hid the original business outcome: %#v", normalized)
	}
}
