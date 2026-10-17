package activity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapturePayloadUsesCompactJSONWithoutLosingStructure(t *testing.T) {
	store := testStore(t, Options{})
	value := map[string]any{
		"schema": "easyeda-pcb-result-page/v1",
		"items": []any{
			map[string]any{"id": "a", "ref": "U1", "at": []any{12.5, -8.25}},
			map[string]any{"id": "b", "ref": "C1", "at": []any{4.0, 9.0}},
		},
	}
	payload := store.CapturePayload(t.Context(), value, "complete", NewRedactor())
	if payload.Ref == "" {
		t.Fatal(payload)
	}
	data, err := os.ReadFile(filepath.Join(store.root, "payloads", payload.Ref+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\n") || payload.Lines != 1 {
		t.Fatalf("retained JSON was not compact: lines=%d data=%q", payload.Lines, data)
	}
	if payload.Bytes != int64(len(data)) {
		t.Fatalf("retained byte count=%d, actual=%d", payload.Bytes, len(data))
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["schema"] != value["schema"] || len(decoded["items"].([]any)) != 2 {
		t.Fatalf("compact source changed business data: %#v", decoded)
	}
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) >= len(pretty) {
		t.Fatalf("compact source did not reduce bytes: compact=%d pretty=%d", len(data), len(pretty))
	}
}
