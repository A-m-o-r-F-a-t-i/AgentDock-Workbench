package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
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

func TestDynamicMCPModelProjectionUsesCanonicalStructuredResult(t *testing.T) {
	items := make([]any, 0, 120)
	for index := range 120 {
		items = append(items, map[string]any{"index": index, "ref": "U1", "detail": strings.Repeat("payload", 20)})
	}
	business := map[string]any{"schema": "easyeda-pcb-result-page/v1", "total": 120, "items": items}
	encoded, err := json.Marshal(business)
	if err != nil {
		t.Fatal(err)
	}
	envelope := map[string]any{
		"isError": false,
		"content": []any{map[string]any{"type": "text", "text": "mcp_tool_call succeeded"}},
		"structuredContent": map[string]any{
			"name": "easyeda-pcb:pcb_read", "executed": true,
			"mcp_catalog": map[string]any{"server": "easyeda-pcb", "delivery": "reference", "tools_omitted": true},
			"result": map[string]any{
				"isError":           false,
				"content":           []any{map[string]any{"type": "text", "text": string(encoded)}},
				"structuredContent": business,
			},
		},
	}
	before, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	projected := normalizeResponseStages("mcp_tool_call", nil, envelope)
	after, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	result := asMap(asMap(projected["structuredContent"])["result"])
	if !reflect.DeepEqual(result["structuredContent"], business) {
		t.Fatal("canonical upstream structuredContent changed")
	}
	if result["content"] != nil {
		t.Fatalf("duplicate upstream JSON text remained: %#v", result["content"])
	}
	if len(after)*2 >= len(before) {
		t.Fatalf("canonical projection did not remove the duplicated body: before=%d after=%d", len(before), len(after))
	}
	if retained := responseRetentionValue("mcp_tool_call", envelope); !reflect.DeepEqual(retained, business) {
		t.Fatalf("retained source was not the canonical business result: %#v", retained)
	}
}

func TestDynamicMCPModelProjectionPreservesNonEquivalentTextAndMetadata(t *testing.T) {
	business := map[string]any{"number": 42, "url": "https://example.invalid/issues/42"}
	envelope := map[string]any{
		"structuredContent": map[string]any{
			"name": "github:create_issue", "executed": true,
			"result": map[string]any{
				"isError": false,
				"content": []any{
					map[string]any{"type": "text", "text": `{"ok":true,"structuredContent":true,"resultId":"42"}`},
					map[string]any{"type": "text", "text": `{"structuredContent":true,"domain":"business"}`},
					map[string]any{"type": "text", "text": "Created issue #42"},
				},
				"structuredContent": business,
				"_meta":             map[string]any{"large_internal_metadata": strings.Repeat("x", 2000)},
			},
		},
	}
	projected := normalizeResponseStages("mcp_tool_call", nil, envelope)
	result := asMap(asMap(projected["structuredContent"])["result"])
	content := envelopeBlocks(result["content"])
	if len(content) != 3 || asMap(content[0])["text"] != `{"ok":true,"structuredContent":true,"resultId":"42"}` || asMap(content[1])["text"] != `{"structuredContent":true,"domain":"business"}` || asMap(content[2])["text"] != "Created issue #42" {
		t.Fatalf("unique business text was not preserved exactly: %#v", content)
	}
	if asMap(result["_meta"])["large_internal_metadata"] != strings.Repeat("x", 2000) {
		t.Fatalf("upstream standard metadata was altered: %#v", result["_meta"])
	}
	retained := asMap(responseRetentionValue("mcp_tool_call", envelope))
	if !reflect.DeepEqual(retained["structuredContent"], business) || len(envelopeBlocks(retained["content"])) != 3 {
		t.Fatalf("canonical retained result lost unique business text: %#v", retained)
	}
}
