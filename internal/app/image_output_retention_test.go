package app

import (
	"encoding/json"
	"testing"

	toolmedia "github.com/uvwt/agentdock/internal/tool/media"
)

func TestActivitySafeOutputRemovesViewImageTransportBytes(t *testing.T) {
	result := Result{
		"image":                          map[string]any{"artifact_id": "artifact", "url": "https://agentdock.example/image", "filename": "image.jpg", "mime_type": "image/jpeg"},
		toolmedia.NativeImageBase64Key:   "bmF0aXZlIGltYWdlIGJ5dGVz",
		toolmedia.NativeImageMIMETypeKey: "image/jpeg",
	}
	safe, ok := activitySafeOutput("view_image", result).(Result)
	if !ok {
		t.Fatalf("safe result type = %T", activitySafeOutput("view_image", result))
	}
	if safe[toolmedia.NativeImageBase64Key] != nil || safe[toolmedia.NativeImageMIMETypeKey] != nil {
		t.Fatalf("activity result retained transport fields: %#v", safe)
	}
	if safe["image"] == nil {
		t.Fatal("activity result lost public image metadata")
	}
	if result[toolmedia.NativeImageBase64Key] == nil {
		t.Fatal("activity projection mutated the transport result")
	}
}

func TestActivitySafeOutputReplacesNativeImageBlockWithResourceLink(t *testing.T) {
	uri := "https://agentdock.example/artifacts/public/image.jpg?sig=test"
	envelope := map[string]any{
		"structuredContent": map[string]any{"image": map[string]any{"url": uri, "filename": "image.jpg", "mime_type": "image/jpeg"}},
		"content":           []map[string]any{{"type": "image", "data": "bmF0aXZl", "mimeType": "image/jpeg"}},
	}
	safe := activitySafeOutput("view_image", envelope).(map[string]any)
	encoded, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || json.Valid(encoded) == false {
		t.Fatalf("invalid activity envelope: %s", encoded)
	}
	blocks, ok := safe["content"].([]any)
	if !ok || len(blocks) != 1 {
		t.Fatalf("activity content = %#v", safe["content"])
	}
	link := modelObject(blocks[0])
	if link["type"] != "resource_link" || link["uri"] != uri || link["mimeType"] != "image/jpeg" {
		t.Fatalf("activity resource link = %#v", link)
	}
	if _, exists := link["data"]; exists {
		t.Fatalf("activity link retained image data: %#v", link)
	}
}
