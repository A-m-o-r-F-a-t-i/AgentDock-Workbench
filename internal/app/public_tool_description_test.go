package app

import (
	"reflect"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

func TestPublicManagementPatchUsesCanonicalFieldDescriptions(t *testing.T) {
	contract, ok := mcpToolContract("mcp_manage", config.Config{})
	if !ok {
		t.Fatal("MCP management contract missing")
	}
	before := cloneJSONSchemaMap(contract.ValidationInputSchema)
	public := publicToolInputSchema("mcp_manage", contract.ValidationInputSchema)
	properties := public["properties"].(map[string]any)
	patch := properties["patch"].(map[string]any)
	for name, value := range patch["properties"].(map[string]any) {
		field := value.(map[string]any)
		root := properties[name].(map[string]any)
		if !reflect.DeepEqual(field, root) {
			t.Errorf("patch field %s differs from its public management field", name)
		}
	}
	if patch["additionalProperties"] != false || patch["minProperties"] != 1 {
		t.Fatal("public management patch lost its closed, nonempty contract")
	}
	if !reflect.DeepEqual(before, contract.ValidationInputSchema) {
		t.Fatal("public descriptions changed the validation contract")
	}
}
