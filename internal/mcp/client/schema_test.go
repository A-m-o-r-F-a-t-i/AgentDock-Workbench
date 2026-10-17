package client

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCompileToolInputSchemaSupportsDraft2020Keywords(t *testing.T) {
	validator, err := compileToolInputSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"$ref": "#/$defs/nonEmptyName"},
		},
		"required":             []any{"name"},
		"additionalProperties": false,
		"$defs": map[string]any{
			"nonEmptyName": map[string]any{
				"type":      "string",
				"minLength": 2,
				"pattern":   "^[A-Z]",
			},
		},
	})
	if err != nil {
		t.Fatalf("compileToolInputSchema() error = %v", err)
	}
	tool := Tool{Name: "create_user", inputValidator: validator}
	if err := validateToolArguments(tool, map[string]any{"name": "Alice"}); err != nil {
		t.Fatalf("validateToolArguments(valid) error = %v", err)
	}
	if err := validateToolArguments(tool, map[string]any{"name": "alice"}); err == nil {
		t.Fatal("validateToolArguments() accepted a value that violates pattern")
	}
	if err := validateToolArguments(tool, map[string]any{"name": "Alice", "extra": true}); err == nil {
		t.Fatal("validateToolArguments() accepted an additional property")
	}
}

func TestCompileToolInputSchemaRejectsInvalidOrNonObjectRoot(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
	}{
		{name: "empty", schema: map[string]any{}},
		{name: "array root", schema: map[string]any{"type": "array"}},
		{name: "invalid keyword value", schema: map[string]any{"type": "object", "required": "name"}},
		{name: "external reference", schema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"$ref": "https://example.invalid/schema.json"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := compileToolInputSchema(test.schema); err == nil {
				t.Fatal("compileToolInputSchema() accepted invalid schema")
			}
		})
	}
}

func TestValidateToolArgumentsPreservesNullableCompatibility(t *testing.T) {
	tool := Tool{
		Name: "update_user",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"nickname": map[string]any{"type": "string", "nullable": true},
				"metadata": map[string]any{"nullable": true},
			},
		},
	}
	if err := validateToolArguments(tool, map[string]any{"nickname": nil, "metadata": nil}); err != nil {
		t.Fatalf("validateToolArguments(nullable) error = %v", err)
	}
	if err := validateToolArguments(tool, map[string]any{"nickname": 42}); err == nil {
		t.Fatal("validateToolArguments() accepted a non-null value with the wrong type")
	}
}

func TestValidateToolArgumentsReturnsAgentDockErrorDetails(t *testing.T) {
	tool := Tool{
		Name: "echo",
		InputSchema: map[string]any{
			"type":       "object",
			"required":   []any{"text"},
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
		},
	}
	err := validateToolArguments(tool, nil)
	var mcpErr *Error
	if !errors.As(err, &mcpErr) {
		t.Fatalf("validateToolArguments() error = %T, want *Error", err)
	}
	if mcpErr.Code != "MCP_ARGUMENT_INVALID" || mcpErr.Details["tool"] != "echo" || mcpErr.Details["reason"] == "" {
		t.Fatalf("unexpected MCP error: %#v", mcpErr)
	}
}

func TestPreparedOutputValidationPreservesSideEffectsAndProtocolErrors(t *testing.T) {
	validator, err := compileToolInputSchema(map[string]any{
		"type": "object", "required": []string{"count"},
		"properties": map[string]any{"count": map[string]any{"type": "integer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared := &PreparedMCPCall{QualifiedName: "demo:write", outputValidator: validator}
	result := map[string]any{"structuredContent": map[string]any{"effect_state": "complete", "count": "invalid"}}
	err = validatePreparedOutput(prepared, result)
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "MCP_OUTPUT_SCHEMA_MISMATCH" || failure.Retryable || failure.Details["executed"] != true || failure.Details["effect_state"] != "complete" {
		t.Fatalf("unexpected output-contract failure: %#v", err)
	}
	if result["structuredContent"].(map[string]any)["count"] != "invalid" {
		t.Fatal("output validation changed the raw upstream result")
	}
	if err := validatePreparedOutput(prepared, map[string]any{"isError": true, "content": []any{}}); err != nil {
		t.Fatalf("protocol error was incorrectly validated as business success: %v", err)
	}
	for name, raw := range map[string]map[string]any{
		"operation failed":    {"structuredContent": map[string]any{"operation_status": "failed", "code": "INVALID_DOCUMENT"}},
		"nested error code":   {"structuredContent": map[string]any{"ok": false, "error": map[string]any{"code": "INVALID_DOCUMENT"}}},
		"outer error code":    {"code": "INVALID_DOCUMENT", "structuredContent": map[string]any{"ok": false}},
		"legacy text failure": {"content": []any{map[string]any{"type": "text", "text": `{"operation_status":"failed","code":"INVALID_DOCUMENT"}`}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validatePreparedOutput(prepared, raw); err != nil {
				t.Fatalf("business error was incorrectly validated as success: %v", err)
			}
			details, failed := ResultFailure(raw)
			if !failed || details["code"] != "INVALID_DOCUMENT" {
				t.Fatalf("business error information lost: %#v %v", details, failed)
			}
		})
	}
	for name, raw := range map[string]map[string]any{
		"error without failure":           {"structuredContent": map[string]any{"error": map[string]any{"code": "FYI"}}},
		"ok without error code":           {"structuredContent": map[string]any{"ok": false}},
		"text success without structured": {"content": []any{map[string]any{"type": "text", "text": `{"count":3}`}}},
		"trailing text value":             {"content": []any{map[string]any{"type": "text", "text": `{"operation_status":"failed"} {}`}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validatePreparedOutput(prepared, raw); err == nil {
				t.Fatal("non-failure bypassed the declared output schema")
			}
		})
	}
	if err := validatePreparedOutput(prepared, map[string]any{"content": []any{}}); err == nil {
		t.Fatal("missing declared structured content accepted")
	}
	if err := validatePreparedOutput(prepared, map[string]any{"structuredContent": map[string]any{"count": 3}}); err != nil {
		t.Fatalf("valid upstream structured result rejected: %v", err)
	}
}

func TestSchemaValidationPreservesExactNumberBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, kind, minimum, below string
	}{
		{"integer above float precision", "integer", "9007199254740993", "9007199254740992"},
		{"maximum uint64", "integer", "18446744073709551615", "18446744073709551614"},
		{"precise decimal", "number", "0.10000000000000001", "0.1"},
		{"exponent beyond float range", "number", "1e309", "1e308"},
	} {
		t.Run(test.name, func(t *testing.T) {
			validator, err := compileToolInputSchema(map[string]any{
				"type": "object", "properties": map[string]any{
					"value": map[string]any{"type": test.kind, "minimum": json.Number(test.minimum)},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := validator.Validate(map[string]any{"value": json.Number(test.below)}); err == nil {
				t.Fatal("number rounding bypassed the declared minimum")
			}
			if err := validator.Validate(map[string]any{"value": json.Number(test.minimum)}); err != nil {
				t.Fatalf("exact minimum rejected: %v", err)
			}
		})
	}
}

func TestSchemaValidationRejectsUnrepresentableNumbers(t *testing.T) {
	validator, err := compileToolInputSchema(map[string]any{
		"type": "object", "properties": map[string]any{
			"value": map[string]any{"type": "number", "minimum": 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"1e1000001", "1e-1000001"} {
		t.Run(value, func(t *testing.T) {
			for _, arguments := range []map[string]any{
				{"value": json.Number(value)},
				{"nested": []any{map[string]any{"value": json.Number(value)}}},
			} {
				if err := validator.Validate(arguments); err == nil {
					t.Fatal("schema validation accepted a number it cannot represent")
				}
			}
		})
	}
}

func TestSchemaCompilationRejectsUnrepresentableConstraints(t *testing.T) {
	for _, keyword := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "enum"} {
		t.Run(keyword, func(t *testing.T) {
			var constraint any = json.Number("1e1000001")
			if keyword == "enum" {
				constraint = []any{constraint}
			}
			_, err := compileToolInputSchema(map[string]any{
				"type": "object", "properties": map[string]any{
					"value": map[string]any{"type": "number", keyword: constraint},
				},
			})
			if err == nil {
				t.Fatal("schema compiler accepted a constraint it cannot represent")
			}
		})
	}
}

func TestPreparedOutputValidationRejectsUnrepresentableNumber(t *testing.T) {
	validator, err := compileToolInputSchema(map[string]any{
		"type": "object", "properties": map[string]any{
			"value": map[string]any{"type": "number", "minimum": 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared := &PreparedMCPCall{QualifiedName: "demo:write", outputValidator: validator}
	structured := map[string]any{"value": json.Number("1e1000001"), "effect_state": "complete"}
	result := map[string]any{"structuredContent": structured}
	err = validatePreparedOutput(prepared, result)
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "MCP_OUTPUT_SCHEMA_MISMATCH" || failure.Retryable || failure.Details["executed"] != true || failure.Details["effect_state"] != "complete" || failure.Details["retry_safe"] != false {
		t.Fatalf("unexpected output-contract failure: %#v", err)
	}
	if result["structuredContent"].(map[string]any)["value"] != json.Number("1e1000001") {
		t.Fatal("output validation changed the retained upstream value")
	}
}
