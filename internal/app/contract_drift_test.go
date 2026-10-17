package app

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/evolution"
	toolacp "github.com/uvwt/agentdock/internal/tool/acp"
	toolcommand "github.com/uvwt/agentdock/internal/tool/command"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
	toolfile "github.com/uvwt/agentdock/internal/tool/file"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
	toolmedia "github.com/uvwt/agentdock/internal/tool/media"
	toolrecall "github.com/uvwt/agentdock/internal/tool/recall"
	toolskill "github.com/uvwt/agentdock/internal/tool/skill"
	tooltask "github.com/uvwt/agentdock/internal/tool/task"
	toolworkspace "github.com/uvwt/agentdock/internal/tool/workspace"
)

func TestAllToolDefinitionsHaveStrictCompilableInputContracts(t *testing.T) {
	cfg := config.Config{
		NexusEndpoint:  "http://127.0.0.1:18777",
		BrowserEnabled: true,
		ACPEnabled:     true,
	}
	for _, definition := range toolDefinitionsForConfig(cfg) {
		if got := definition.InputSchema["additionalProperties"]; got != false {
			t.Fatalf("%s root additionalProperties = %#v, want false", definition.Name, got)
		}
		if _, err := toolcontract.CompileInputSchema(definition.InputSchema); err != nil {
			t.Fatalf("compile %s public input schema: %v", definition.Name, err)
		}
		spec, _ := toolSpecByName(definition.Name)
		contract, ok := spec.Contract(definition.Name, cfg)
		if !ok {
			t.Fatalf("missing %s validation contract", definition.Name)
		}
		contract = completeToolContract(definition.Name, contract)
		if got := contract.ValidationInputSchema["additionalProperties"]; got != false {
			t.Fatalf("%s validation root additionalProperties = %#v, want false", definition.Name, got)
		}
		if _, err := toolcontract.CompileInputSchema(contract.ValidationInputSchema); err != nil {
			t.Fatalf("compile %s validation input schema: %v", definition.Name, err)
		}
		if len(contract.ValidationOutputSchema) == 0 {
			t.Fatalf("%s validation output schema is empty", definition.Name)
		}
		if _, err := toolcontract.CompileInputSchema(contract.ValidationOutputSchema); err != nil {
			t.Fatalf("compile %s validation output schema: %v", definition.Name, err)
		}
		if len(definition.OutputSchema) != 0 {
			t.Fatalf("%s unexpectedly publishes an MCP output schema", definition.Name)
		}
		if len(definition.ValidationOutputSchema) == 0 {
			t.Fatalf("%s validation output schema is empty", definition.Name)
		}
		if _, err := toolcontract.CompileInputSchema(definition.ValidationOutputSchema); err != nil {
			t.Fatalf("compile %s complete validation output schema: %v", definition.Name, err)
		}
	}
}

func TestBuiltInValidatorCacheUsesSchemaContent(t *testing.T) {
	withoutNexus := tooltask.ManageInputSchema(config.Config{})
	first, err := compileBuiltInValidator(withoutNexus)
	if err != nil {
		t.Fatal(err)
	}
	second, err := compileBuiltInValidator(tooltask.ManageInputSchema(config.Config{}))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("identical built-in schemas should reuse the compiled validator")
	}

	withNexus, err := compileBuiltInValidator(tooltask.ManageInputSchema(config.Config{NexusEndpoint: "http://127.0.0.1:18777"}))
	if err != nil {
		t.Fatal(err)
	}
	if first == withNexus {
		t.Fatal("config-aware task schemas must not share a validator when their JSON contracts differ")
	}
}

func TestTypedToolRequestFieldsMatchPublishedSchemas(t *testing.T) {
	fullConfig := config.Config{NexusEndpoint: "http://127.0.0.1:18777"}
	tests := []struct {
		name       string
		request    any
		exact      bool
		allowExtra []string
	}{
		{name: "agentdock_context", request: contextRequest{}, exact: true},
		{name: "workspace_context", request: workspaceContextRequest{}, exact: true},
		{name: toolfile.ToolReadFile, request: toolfile.ReadRequest{}, exact: true, allowExtra: []string{"runtime", "wsl_distribution"}},
		{name: toolfile.ToolListDir, request: toolfile.ListRequest{}, exact: true, allowExtra: []string{"runtime", "wsl_distribution"}},
		{name: toolfile.ToolSearchText, request: toolfile.SearchRequest{}, exact: true, allowExtra: []string{"runtime", "wsl_distribution"}},
		{name: toolfile.ToolApplyPatch, request: toolfile.ApplyPatchRequest{}, exact: true, allowExtra: []string{"runtime", "wsl_distribution", "workdir", "expected_patch_revision"}},
		{name: toolcommand.ToolExecCommand, request: toolcommand.ExecRequest{}, exact: true, allowExtra: []string{"runtime", "wsl_distribution"}},
		{name: toolcommand.ToolSessionObserve, request: toolcommand.SessionObserveRequest{}, exact: true},
		{name: toolcommand.ToolSessionAct, request: toolcommand.SessionActRequest{}, exact: true},
		{name: tooltask.ToolTaskManage, request: tooltask.ManageRequest{}, exact: true},
		{name: toolworkspace.ToolManage, request: toolworkspace.Request{}, exact: true},
		{name: "workflow_template_manage", request: tooltask.WorkflowRequest{}},
		{name: evolution.ToolName, request: evolution.Request{}},
		{name: toolacp.ToolSession, request: toolacp.SessionRequest{}, exact: true},
		{name: toolacp.ToolPrompt, request: toolacp.PromptRequest{}, exact: true},
		{name: toolacp.ToolInteraction, request: toolacp.InteractionRequest{}, exact: true},
		{name: toolskill.ToolPackage, request: toolskill.PackageRequest{}, exact: true},
		{name: toolmcp.ToolManage, request: toolmcp.ManageRequest{}, exact: true},
		{name: toolmcp.ToolSearch, request: toolmcp.SearchRequest{}, exact: true},
		{name: toolmcp.ToolInspect, request: toolmcp.InspectRequest{}, exact: true},
		{name: toolmcp.ToolCall, request: toolmcp.CallRequest{}, exact: true},
		{name: toolmedia.ToolViewImage, request: toolmedia.ViewImageRequest{}, exact: true},
		{name: toolmedia.ToolFilePublish, request: toolmedia.FilePublishRequest{}, exact: true},
		{name: "recall_search", request: toolrecall.SearchRequest{}},
		{name: "recall_read", request: toolrecall.ReadRequest{}},
		{name: "recall_write", request: toolrecall.WriteRequest{}},
		{name: "recall_maintain", request: toolrecall.MaintainRequest{}},
		{name: "private_note_manage", request: toolrecall.PrivateNoteRequest{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec, ok := toolSpecByName(test.name)
			if !ok {
				t.Fatalf("missing tool spec for %s", test.name)
			}
			contract, ok := spec.Contract(test.name, fullConfig)
			if !ok {
				t.Fatalf("missing validation contract for %s", test.name)
			}
			contract = completeToolContract(test.name, contract)
			assertSchemaMatchesRequestType(t, test.name, contract.ValidationInputSchema, reflect.TypeOf(test.request), test.exact, test.allowExtra)
		})
	}
}

func assertSchemaMatchesRequestType(t *testing.T, path string, schema map[string]any, requestType reflect.Type, exact bool, allowExtra []string) {
	t.Helper()
	requestType = dereferenceType(requestType)
	if requestType.Kind() != reflect.Struct {
		t.Fatalf("%s request type = %s, want struct", path, requestType)
	}
	properties, _ := schema["properties"].(map[string]any)
	fields := jsonFields(requestType)
	// Existing optional execution overrides are interpreted at the ingress.
	// Only explicitly published overrides belong in the input contract.
	if !strings.Contains(path, ".") {
		common := map[string]any{}
		toolcontract.ActivityProperties(common)
		bindingFields := jsonFields(reflect.TypeOf(activity.Binding{}))
		for name := range common {
			if _, exists := fields[name]; !exists && properties[name] != nil {
				fields[name] = bindingFields[name]
			}
		}

	}

	for name, property := range properties {
		fieldType, ok := fields[name]
		if !ok {
			t.Errorf("%s schema field %q has no matching request field", path, name)
			continue
		}
		propertySchema, _ := property.(map[string]any)
		assertSchemaPropertyMatchesGoType(t, path+"."+name, propertySchema, fieldType)
	}
	if !exact {
		return
	}
	allowed := make(map[string]struct{}, len(allowExtra))
	for _, name := range allowExtra {
		allowed[name] = struct{}{}
	}
	var extras []string
	for name := range fields {
		if _, ok := properties[name]; ok {
			continue
		}
		if _, ok := allowed[name]; ok {
			continue
		}
		extras = append(extras, name)
	}
	if len(extras) > 0 {
		sort.Strings(extras)
		t.Errorf("%s request fields missing from schema: %s", path, strings.Join(extras, ", "))
	}
}

func assertSchemaPropertyMatchesGoType(t *testing.T, path string, schema map[string]any, goType reflect.Type) {
	t.Helper()
	if len(schema) == 0 {
		return
	}
	goType = dereferenceType(goType)
	if goType.Kind() == reflect.Interface {
		return
	}

	schemaType, _ := schema["type"].(string)
	matches := true
	switch schemaType {
	case "string":
		matches = goType.Kind() == reflect.String
	case "integer":
		matches = goType.Kind() >= reflect.Int && goType.Kind() <= reflect.Int64 || goType.Kind() >= reflect.Uint && goType.Kind() <= reflect.Uint64
	case "number":
		matches = goType.Kind() == reflect.Float32 || goType.Kind() == reflect.Float64
	case "boolean":
		matches = goType.Kind() == reflect.Bool
	case "array":
		matches = goType.Kind() == reflect.Slice || goType.Kind() == reflect.Array
	case "object":
		matches = goType.Kind() == reflect.Struct || goType.Kind() == reflect.Map
	case "":
		return
	}
	if !matches {
		t.Errorf("%s schema type %q does not match Go type %s", path, schemaType, goType)
		return
	}

	if schemaType == "object" && schema["additionalProperties"] == false && goType.Kind() == reflect.Struct {
		assertSchemaMatchesRequestType(t, path, schema, goType, true, nil)
		return
	}
	if schemaType == "array" && (goType.Kind() == reflect.Slice || goType.Kind() == reflect.Array) {
		items, _ := schema["items"].(map[string]any)
		itemType := dereferenceType(goType.Elem())
		if itemType.Kind() == reflect.Struct && len(items) > 0 {
			assertSchemaPropertyMatchesGoType(t, path+"[]", items, itemType)
		}
	}
}

func jsonFields(value reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	var visit func(reflect.Type)
	visit = func(current reflect.Type) {
		current = dereferenceType(current)
		if current.Kind() != reflect.Struct {
			return
		}
		for i := 0; i < current.NumField(); i++ {
			field := current.Field(i)
			if field.PkgPath != "" && !field.Anonymous {
				continue
			}
			tag := field.Tag.Get("json")
			name := strings.Split(tag, ",")[0]
			if name == "-" {
				continue
			}
			if field.Anonymous && name == "" {
				visit(field.Type)
				continue
			}
			if name == "" {
				name = field.Name
			}
			fields[name] = field.Type
		}
	}
	visit(value)
	return fields
}

func dereferenceType(value reflect.Type) reflect.Type {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	return value
}

// Runtime and workspace are inherited for every host, including Windows/WSL.
func TestApplyPatchPublicInputIsPatchOnly(t *testing.T) {
	var patch *ToolDefinition
	for _, definition := range toolDefinitionsForConfig(config.Config{}) {
		if definition.Name == "file_edit" {
			t.Fatal("legacy file_edit must not be model-facing")
		}
		if definition.Name == "apply_patch" {
			copy := definition
			patch = &copy
		}
	}
	if patch == nil {
		t.Fatal("apply_patch is missing from the model directory")
	}
	properties, _ := patch.InputSchema["properties"].(map[string]any)
	if len(properties) != 1 || properties["patch"] == nil {
		t.Fatalf("apply_patch must inherit execution metadata: %#v", properties)
	}
	validator, err := toolcontract.CompileInputSchema(patch.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(map[string]any{}); err == nil {
		t.Fatal("apply_patch accepted a missing patch")
	}
}
