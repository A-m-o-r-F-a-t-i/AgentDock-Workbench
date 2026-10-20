package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

func TestExecutionSchemaStoresBusinessDefinitionsOnceWithoutWideningSuccess(t *testing.T) {
	original := map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"status", "executed", "approval", "payload/~"},
		"properties": map[string]any{
			"status":   map[string]any{"const": "ok"},
			"executed": map[string]any{"const": true},
			"approval": map[string]any{
				"type": "object", "required": []string{"approved"}, "additionalProperties": false,
				"properties": map[string]any{"approved": map[string]any{"const": true}},
			},
			"payload/~": map[string]any{"$ref": "#/$defs/payload"},
		},
		"$defs": map[string]any{
			"payload": map[string]any{
				"type": "object", "required": []string{"count"}, "additionalProperties": false,
				"properties": map[string]any{"count": map[string]any{"type": "integer", "minimum": 1}},
			},
		},
	}
	before, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	schema := executionOutputSchema(original)
	validator, err := toolcontract.CompileInputSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	success := schema["anyOf"].([]any)[0].(map[string]any)
	properties := success["properties"].(map[string]any)
	if properties["payload/~"] != true {
		t.Fatalf("success branch repeated business schema: %#v", properties["payload/~"])
	}
	valid := map[string]any{
		"status": "ok", "executed": true, "approval": map[string]any{"approved": true},
		"payload/~": map[string]any{"count": 1},
	}
	for _, tc := range []struct {
		name  string
		key   string
		value any
	}{
		{"status", "status", "pending_approval"},
		{"executed", "executed", false},
		{"approval", "approval", map[string]any{}},
		{"nested constraint", "payload/~", map[string]any{"count": 0}},
		{"nested type", "payload/~", map[string]any{"count": "1"}},
		{"unknown field", "unexpected", true},
		{"legacy delivery", "delivery", "full"},
		{"schema summary delivery", "delivery", "summary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := cloneJSONSchemaMap(valid)
			value[tc.key] = tc.value
			if err := validator.Validate(value); err == nil {
				t.Fatalf("invalid success accepted: %#v", value)
			}
		})
	}
	if err := validator.Validate(valid); err != nil {
		t.Fatalf("valid success rejected: %v", err)
	}
	if err := validator.Validate(map[string]any{
		"status": "pending_approval", "executed": false,
		"approval_id": "approval_" + strings.Repeat("a", 32), "call_id": "call_" + strings.Repeat("b", 32),
	}); err != nil {
		t.Fatalf("valid pending approval rejected: %v", err)
	}
	after, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("schema composition mutated the capability-owned contract")
	}
}

func TestExecutionSchemaStoresLargeBusinessFieldOnce(t *testing.T) {
	marker := strings.Repeat("unique-business-contract-", 100)
	original := map[string]any{
		"type": "object", "properties": map[string]any{
			"payload": map[string]any{"type": "string", "description": marker},
		},
	}
	encoded, err := json.Marshal(executionOutputSchema(original))
	if err != nil {
		t.Fatal(err)
	}
	if count := bytes.Count(encoded, []byte(marker)); count != 1 {
		t.Fatalf("large business field stored %d times, want once", count)
	}
}
