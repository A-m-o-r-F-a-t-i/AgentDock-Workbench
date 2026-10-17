package app

import (
	"encoding/json"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

func TestPublicToolSchemasOmitInheritedExecutionOverrides(t *testing.T) {
	for name := range executionOverrideTools {
		definition, ok := toolDefinitionForConfig(name, config.Config{})
		if !ok {
			t.Fatalf("missing tool definition for %s", name)
		}
		properties, _ := definition.InputSchema["properties"].(map[string]any)
		hidden := append([]string(nil), inheritedExecutionFields...)
		hidden = append(hidden, publicToolHiddenFields[name]...)
		for _, field := range hidden {
			if properties[field] != nil {
				t.Errorf("%s public schema exposes inherited %s", name, field)
			}
		}

		spec, _ := toolSpecByName(name)
		contract, ok := spec.Contract(name, config.Config{})
		if !ok {
			t.Fatalf("missing validation contract for %s", name)
		}
		contract = completeToolContract(name, contract)
		validationProperties, _ := contract.ValidationInputSchema["properties"].(map[string]any)
		removed := 0
		for _, field := range inheritedExecutionFields {
			if validationProperties[field] != nil {
				removed++
			}
		}
		if removed == 0 {
			t.Errorf("%s has no validation-only inherited execution field", name)
		}
	}
}

func TestAndroidPublicSchemaCollapsesBranchesWhileRuntimeStaysStrict(t *testing.T) {
	for _, name := range []string{"android_device_read", "android_device_act"} {
		definition, ok := toolDefinitionForConfig(name, config.Config{})
		if !ok {
			t.Fatalf("missing tool definition for %s", name)
		}
		if definition.InputSchema["oneOf"] != nil {
			t.Fatalf("%s public schema still repeats action branches", name)
		}
		properties, _ := definition.InputSchema["properties"].(map[string]any)
		action, _ := properties["action"].(map[string]any)
		if len(action["enum"].([]string)) == 0 {
			t.Fatalf("%s public action enum is empty", name)
		}

		spec, _ := toolSpecByName(name)
		contract, _ := spec.Contract(name, config.Config{})
		contract = completeToolContract(name, contract)
		if contract.ValidationInputSchema["oneOf"] == nil {
			t.Fatalf("%s validation schema lost action branches", name)
		}
	}

	spec, _ := toolSpecByName("android_device_act")
	contract, _ := spec.Contract("android_device_act", config.Config{})
	validator, err := compileBuiltInValidator(contract.ValidationInputSchema)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{
		{"action": "tap", "x": 1},
		{"action": "tap", "x": 1, "y": 2, "package": "com.example"},
	} {
		if err := validator.Validate(args); err == nil {
			t.Fatalf("validation schema accepted invalid Android request: %#v", args)
		}
	}
}

func TestPublicToolSchemaProjectionDoesNotMutateValidationContract(t *testing.T) {
	spec, _ := toolSpecByName("exec_command")
	contract, _ := spec.Contract("exec_command", config.Config{})
	contract = completeToolContract("exec_command", contract)
	publicProperties := contract.PublicInputSchema["properties"].(map[string]any)
	validationProperties := contract.ValidationInputSchema["properties"].(map[string]any)
	publicProperties["cmd"].(map[string]any)["description"] = "mutated public description"
	delete(publicProperties, "workspace_id")
	if validationProperties["cmd"].(map[string]any)["description"] == "mutated public description" {
		t.Fatal("public nested schema aliases validation schema")
	}
	if validationProperties["workspace_id"] == nil {
		t.Fatal("public property deletion mutated validation schema")
	}
	emptyRequired := cloneJSONSchemaMap(map[string]any{"required": []string{}})["required"].([]string)
	if emptyRequired == nil {
		t.Fatal("schema clone converted an empty required array to null")
	}
}

func TestPublicInputSchemaBudget(t *testing.T) {
	cfg := config.Config{}
	publicTotal, validationTotal, count := 0, 0, 0
	for _, spec := range toolSpecs {
		if !spec.available(cfg) {
			continue
		}
		definition := spec.definition(cfg)
		encoded, err := json.Marshal(definition.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		publicTotal += len(encoded)
		contract, ok := spec.Contract(spec.Name, cfg)
		if !ok {
			t.Fatalf("missing validation contract for %s", spec.Name)
		}
		contract = completeToolContract(spec.Name, contract)
		validation, err := json.Marshal(contract.ValidationInputSchema)
		if err != nil {
			t.Fatal(err)
		}
		validationTotal += len(validation)
		count++
		if _, err := toolcontract.CompileInputSchema(definition.InputSchema); err != nil {
			t.Fatalf("compile %s public input schema: %v", definition.Name, err)
		}
	}
	t.Logf("available_tools=%d public_input_schema_json_bytes=%d validation_input_schema_json_bytes=%d", count, publicTotal, validationTotal)
	if publicTotal > 35000 {
		t.Fatalf("public input schema budget exceeded: got=%d want<=35000", publicTotal)
	}
	if publicTotal >= validationTotal {
		t.Fatalf("public schemas were not compacted: public=%d validation=%d", publicTotal, validationTotal)
	}
}
