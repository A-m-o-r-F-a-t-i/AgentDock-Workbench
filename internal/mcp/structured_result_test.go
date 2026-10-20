package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func textBlocks(value any) []string {
	blocks := envelopeBlocks(value)
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text, _ := asMap(block)["text"].(string); text != "" {
			texts = append(texts, text)
		}
	}
	return texts
}

func modernToolContext(ctx context.Context) context.Context {
	return contextWireProtocol(ctx, map[string]any{sdk.MetaKeyProtocolVersion: "2026-07-28"})
}

func TestStructuredResultsModernProtocolUsesBoundedSummary(t *testing.T) {
	large := strings.Repeat("complete business output ", 200)
	payload := map[string]any{"status": "ok", "content": large, "count": 7}
	envelope := responseEnvelope(modernToolContext(t.Context()), "list_dir", payload, nil)
	if !reflect.DeepEqual(envelope["structuredContent"], payload) {
		t.Fatal("summary mode changed structuredContent")
	}
	texts := textBlocks(envelope["content"])
	if len(texts) != 1 || len(texts[0]) > ToolSummaryMaxBytes || strings.Contains(texts[0], large) {
		t.Fatalf("summary text = %#v", texts)
	}
	if !strings.Contains(texts[0], "list_dir succeeded") || !strings.Contains(texts[0], "count=7") {
		t.Fatalf("summary lost useful status: %q", texts[0])
	}
}

func TestStructuredResultsLegacyAndExplicitModes(t *testing.T) {
	payload := map[string]any{"status": "ok", "content": "complete legacy text"}
	legacy := responseEnvelope(t.Context(), "read_file", payload, nil)
	if text := textBlocks(legacy["content"])[0]; strings.Contains(text, "complete legacy text") || !strings.Contains(text, "read_file succeeded") {
		t.Fatalf("default compatibility summary is not bounded: %q", text)
	}

	modern := modernToolContext(t.Context())
	full, err := contextResponseOptions(modern, map[string]any{StructuredResultsCapability: map[string]any{"structured": true, "text": responseTextFull}})
	if err != nil {
		t.Fatal(err)
	}
	if text := textBlocks(responseEnvelope(full, "read_file", payload, nil)["content"])[0]; strings.Contains(text, "complete legacy text") || !strings.Contains(text, "read_file succeeded") {
		t.Fatalf("structured full mode duplicated the complete body: %q", text)
	}

	summary, err := contextResponseOptions(t.Context(), map[string]any{StructuredResultsCapability: map[string]any{"structured": true, "text": responseTextSummary}})
	if err != nil {
		t.Fatal(err)
	}
	if text := textBlocks(responseEnvelope(summary, "read_file", payload, nil)["content"])[0]; strings.Contains(text, "complete legacy text") || !strings.Contains(text, "read_file succeeded") {
		t.Fatalf("explicit summary mode was ignored: %q", text)
	}
}

func TestStructuredResultsErrorsKeepFactsWithBoundedText(t *testing.T) {
	message := strings.Repeat("remote failure detail ", 100)
	envelope := responseEnvelope(modernToolContext(t.Context()), "read_file", nil, errors.New(message))
	if envelope["isError"] != true {
		t.Fatalf("error flag = %#v", envelope["isError"])
	}
	structured := asMap(envelope["structuredContent"])
	if structured["error"] != message || structured["tool"] != "read_file" {
		t.Fatalf("structured error facts changed: %#v", structured)
	}
	texts := textBlocks(envelope["content"])
	if len(texts) != 1 || len(texts[0]) > ToolSummaryMaxBytes || !strings.Contains(texts[0], "read_file failed") {
		t.Fatalf("bounded error summary = %#v", texts)
	}
}

func TestStructuredResultsRemoveDynamicDuplicatesAndKeepNonText(t *testing.T) {
	payload := map[string]any{
		"name": "figma:get_screenshot",
		"mcp_catalog": map[string]any{
			"server": "figma", "catalog_revision": "r1", "complete": true,
			"stale": false, "total": 1, "tools": []any{map[string]any{"name": "figma:get_screenshot"}},
		},
		"result": map[string]any{
			"isError": false,
			"content": []any{
				map[string]any{"type": "text", "text": `{"node_id":"1:2"}`},
				map[string]any{"type": "image", "data": "abc123", "mimeType": "image/png"},
			},
			"structuredContent": map[string]any{"node_id": "1:2"},
		},
	}
	envelope := responseEnvelope(WithStructuredResults(t.Context()), "mcp_tool_call", payload, nil)
	texts := textBlocks(envelope["content"])
	if len(texts) != 1 || !strings.Contains(texts[0], "figma:get_screenshot succeeded") {
		t.Fatalf("duplicate JSON was not replaced by summary: %#v", texts)
	}
	if strings.Contains(texts[0], "AGENTDOCK_MCP_CATALOG") || strings.Contains(texts[0], "node_id") {
		t.Fatalf("structured duplicate leaked into summary: %q", texts[0])
	}
	blocks := envelopeBlocks(envelope["content"])
	if len(blocks) != 2 || asMap(blocks[1])["type"] != "image" || asMap(blocks[1])["data"] != "abc123" {
		t.Fatalf("non-text MCP content changed: %#v", blocks)
	}
	if asMap(asMap(envelope["structuredContent"])["result"])["structuredContent"] == nil {
		t.Fatal("remote structuredContent was removed")
	}
}

func TestStructuredResultsKeepUniqueDynamicBusinessText(t *testing.T) {
	payload := map[string]any{
		"name": "github:create_issue",
		"mcp_catalog": map[string]any{
			"server": "github", "catalog_revision": "r1", "complete": true,
			"stale": false, "total": 1, "tools": []any{map[string]any{"name": "github:create_issue"}},
		},
		"result": map[string]any{
			"isError":           false,
			"content":           []any{map[string]any{"type": "text", "text": "Created issue #42"}},
			"structuredContent": map[string]any{"number": 42},
		},
	}
	envelope := responseEnvelope(WithStructuredResults(t.Context()), "mcp_tool_call", payload, nil)
	texts := textBlocks(envelope["content"])
	if len(texts) != 1 || texts[0] != "Created issue #42" {
		t.Fatalf("unique business text changed: %#v", texts)
	}
}

func TestBoundedToolSummaryPreservesUTF8(t *testing.T) {
	text := boundedToolSummary(strings.Repeat("中文结果", 200))
	if len(text) > ToolSummaryMaxBytes || !strings.HasSuffix(text, "...") || strings.ContainsRune(text, '\uFFFD') {
		t.Fatalf("invalid bounded UTF-8 summary: bytes=%d text=%q", len(text), text)
	}
}

func TestSDKToolResultKeepsCanonicalNumbersAndMeasuresActualJSON(t *testing.T) {
	envelope := map[string]any{
		"structuredContent": map[string]any{"id": json.Number("9007199254740993"), "amount": json.Number("0.10000000000000001")},
		"_meta":             map[string]any{"revision": json.Number("18446744073709551615")},
		"content":           []any{map[string]any{"type": "image", "data": "AQID", "mimeType": "image/png"}},
	}
	result, err := sdkToolResult(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.StructuredContent, envelope["structuredContent"]) || result.Meta["revision"] != json.Number("18446744073709551615") {
		t.Fatalf("SDK conversion rounded canonical dynamic fields: %#v", result)
	}
	if len(result.Content) != 1 {
		t.Fatalf("native content changed: %#v", result.Content)
	}
	image, ok := result.Content[0].(*sdk.ImageContent)
	if !ok || string(image.Data) != "\x01\x02\x03" || image.MIMEType != "image/png" {
		t.Fatalf("native image changed: %#v", result.Content[0])
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, exact := range []string{"9007199254740993", "0.10000000000000001", "18446744073709551615"} {
		if !strings.Contains(string(encoded), exact) {
			t.Fatalf("final SDK output omitted exact number %s: %s", exact, encoded)
		}
	}
	measuredEnvelope := asMap(withoutNativeAttachmentData(envelope))
	measuredResult, err := sdkToolResult(measuredEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	measuredEncoded, err := json.Marshal(measuredResult)
	if err != nil {
		t.Fatal(err)
	}
	if chars, err := modelEnvelopeChars(envelope); err != nil || chars != utf8.RuneCount(measuredEncoded) || chars >= utf8.RuneCount(encoded) {
		t.Fatalf("budget did not preserve exact numbers while excluding native payload data: chars=%d measured=%d actual=%d err=%v", chars, utf8.RuneCount(measuredEncoded), utf8.RuneCount(encoded), err)
	}
}
