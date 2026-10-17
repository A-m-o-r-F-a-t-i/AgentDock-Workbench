package mcp

import (
	"bytes"
	"encoding/json"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSafeRequestMetaHandlesNilListToolsParams(t *testing.T) {
	request := &mcpsdk.ServerRequest[*mcpsdk.ListToolsParams]{Params: nil}
	if meta := safeRequestMeta(request); meta != nil {
		t.Fatalf("nil tools/list params returned metadata: %#v", meta)
	}
}

func TestSafeRequestMetaReadsSession(t *testing.T) {
	request := &mcpsdk.ServerRequest[*mcpsdk.ListToolsParams]{Params: &mcpsdk.ListToolsParams{Meta: mcpsdk.Meta{"openai/session": "conversation"}}}
	if got := safeRequestMeta(request)["openai/session"]; got != "conversation" {
		t.Fatalf("openai/session = %#v", got)
	}
}

func TestSDKToolDefinitionOmitsAbsentOutputSchema(t *testing.T) {
	tool := sdkToolDefinition(ToolDefinition{Name: "fixture", InputSchema: map[string]any{"type": "object"}}, false)
	encoded, err := json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("\"outputSchema\"")) {
		t.Fatalf("absent output schema was serialized: %s", encoded)
	}

	tool = sdkToolDefinition(ToolDefinition{Name: "fixture", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}}, false)
	encoded, err = json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte("\"outputSchema\":{\"type\":\"object\"}")) {
		t.Fatalf("present output schema was not serialized as an object: %s", encoded)
	}
}
