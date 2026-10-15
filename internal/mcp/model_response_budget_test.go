package mcp

import (
	"bytes"
	"encoding/base64"
	"testing"

	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

func TestModelResponseCountsAndCompactsNativeAttachment(t *testing.T) {
	data := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 10000))
	envelope := normalizeResponseStages("mcp_tool_call", map[string]any{}, map[string]any{
		"isError":           false,
		"structuredContent": map[string]any{"name": "fixture::image"},
		"content":           []any{map[string]any{"type": "image", "data": data, "mimeType": "image/png"}},
	})
	chars, err := modelEnvelopeChars(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if chars <= toolcontract.ModelVisibleTotalChars {
		t.Fatalf("native attachment was excluded from final JSON count: %d", chars)
	}

	compacted := compactModelResponse("mcp_tool_call", map[string]any{}, envelope, "activity://call/call_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/source/"+base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 48)))
	chars, err = modelEnvelopeChars(compacted)
	if err != nil {
		t.Fatal(err)
	}
	if chars > toolcontract.ModelVisibleTotalChars {
		t.Fatalf("native attachment receipt exceeds final budget: %d", chars)
	}
	for _, block := range envelopeBlocks(compacted["content"]) {
		kind, _ := asMap(block)["type"].(string)
		if kind == "image" || kind == "audio" || kind == "resource" {
			t.Fatalf("oversized native block remained inline: %s", kind)
		}
	}
	payload := asMap(compacted["structuredContent"])
	items, ok := payload["content_blocks_omitted"].([]map[string]any)
	if !ok || len(items) != 1 || items[0]["decoded_bytes"] != 10000 {
		t.Fatalf("native omission receipt = %#v", payload["content_blocks_omitted"])
	}
	if payload["source_ref"] == "" || asMap(payload["delivery"])["continuation_available"] != true {
		t.Fatal("native attachment source was not recoverable")
	}
}

func TestModelResponseKeepsSmallNativeAttachment(t *testing.T) {
	data := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x2a}, 128))
	envelope := singleBodyEnvelope("view_image", normalizeResponseStages("view_image", nil, map[string]any{
		"isError":           false,
		"structuredContent": map[string]any{"image": map[string]any{"size_bytes": 128}},
		"content":           []any{map[string]any{"type": "image", "data": data, "mimeType": "image/png"}},
	}))
	chars, err := modelEnvelopeChars(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if chars > toolcontract.ModelVisibleTotalChars {
		t.Fatalf("small native attachment unexpectedly exceeds budget: %d", chars)
	}
	blocks := envelopeBlocks(envelope["content"])
	if len(blocks) != 2 || asMap(blocks[1])["type"] != "image" {
		t.Fatalf("small native attachment was removed: %#v", blocks)
	}
	if got := nativeAttachmentBytes(envelope); got != 128 {
		t.Fatalf("native bytes = %d, want 128", got)
	}
}
