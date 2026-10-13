package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

func TestPublicOutputSchemasShareGuidanceOnce(t *testing.T) {
	count, total := 0, 0
	for _, spec := range toolSpecs {
		if !spec.available(config.Config{}) {
			continue
		}
		definition := spec.definition(config.Config{})
		encoded, err := json.Marshal(definition.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		count++
		total += len(encoded)
		if _, err := toolcontract.CompileInputSchema(definition.OutputSchema); err != nil {
			t.Errorf("%s output schema: %v", spec.Name, err)
		}
		if bytes.Contains(encoded, []byte(InsertionResponseInstructions)) {
			t.Errorf("%s repeats the shared insertion policy in its schema", spec.Name)
		}
		if n := bytes.Count(encoded, []byte(`"response_additions"`)); n != 1 {
			t.Errorf("%s defines response_additions %d times, want once", spec.Name, n)
		}
	}
	t.Logf("available_tools=%d output_schema_json_bytes=%d", count, total)
}

func TestCompactOutputSchemaPreservesSuccessAndApprovalValidation(t *testing.T) {
	original := map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"status", "payload"},
		"properties": map[string]any{
			"status": map[string]any{"const": "ok"},
			"payload": map[string]any{"type": "object", "required": []string{"count"}, "additionalProperties": false,
				"properties": map[string]any{"count": map[string]any{"type": "integer", "minimum": 1}}},
		},
	}
	before, _ := json.Marshal(original)
	schema := executionOutputSchema(original)
	after, _ := json.Marshal(original)
	if !bytes.Equal(before, after) {
		t.Fatal("compaction mutated the capability-owned schema")
	}
	validator, err := toolcontract.CompileInputSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		value map[string]any
		valid bool
	}{
		{"success", map[string]any{"status": "ok", "payload": map[string]any{"count": 1}}, true},
		{"missing payload", map[string]any{"status": "ok"}, false},
		{"wrong nested type", map[string]any{"status": "ok", "payload": map[string]any{"count": "1"}}, false},
		{"unknown success field", map[string]any{"status": "ok", "payload": map[string]any{"count": 1}, "unknown": true}, false},
		{"pending", map[string]any{"status": "pending_approval", "executed": false, "approval_id": "approval_" + strings.Repeat("a", 32), "call_id": "call_" + strings.Repeat("b", 32)}, true},
		{"pending executed", map[string]any{"status": "pending_approval", "executed": true, "approval_id": "approval_" + strings.Repeat("a", 32), "call_id": "call_" + strings.Repeat("b", 32)}, false},
		{"incomplete pending", map[string]any{"status": "pending_approval", "executed": false}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validator.Validate(tc.value); (err == nil) != tc.valid {
				t.Fatalf("validation = %v; valid=%t", err, tc.valid)
			}
		})
	}
}

func TestCompactGuidanceKeepsAuthenticatedInsertionContract(t *testing.T) {
	validator, err := toolcontract.CompileInputSchema(responseGuidanceSchema(nil))
	if err != nil {
		t.Fatal(err)
	}
	addition := map[string]any{
		"type": "activity_center_user", "version": 1, "insertion_id": "ins_" + strings.Repeat("a", 32),
		"sequence": 1, "conversation_id": "conv_fixture", "text": "Continue the current task",
		"attention": InsertionAttention, "required_response": InsertionResponseInstructions,
		"receipt_token": strings.Repeat("b", 32), "delivery_attempt": 1,
	}
	result := map[string]any{"response_additions": []any{addition}}
	if err := validator.Validate(result); err != nil {
		t.Fatal(err)
	}
	addition["version"] = 2
	if err := validator.Validate(result); err == nil {
		t.Fatal("unexpected insertion version accepted")
	}
	addition["version"] = 1
	delete(addition, "text")
	if err := validator.Validate(result); err == nil {
		t.Fatal("missing insertion text accepted")
	}
	if !strings.Contains(InsertionInstructions, InsertionResponseInstructions) {
		t.Fatal("bootstrap lost shared insertion policy")
	}
}
