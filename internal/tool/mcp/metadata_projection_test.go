package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectToolMetadataDropsUIAndEmbeddedImages(t *testing.T) {
	projected := projectToolMetadata(map[string]any{
		"icons": []any{map[string]any{"src": "data:image/png;base64,AAAA"}},
		"_meta": map[string]any{
			"ui":         map[string]any{"resourceUri": "ui://fixture/tool"},
			"vendor/key": "retained",
		},
		"documentation": "https://example.invalid/tool",
	})
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "data:image") || strings.Contains(text, "resourceUri") || strings.Contains(text, "\"icons\"") || strings.Contains(text, "\"ui\"") {
		t.Fatalf("UI metadata leaked into model projection: %s", text)
	}
	if !strings.Contains(text, "vendor/key") || !strings.Contains(text, "documentation") {
		t.Fatalf("decision-relevant metadata was removed: %s", text)
	}
}
