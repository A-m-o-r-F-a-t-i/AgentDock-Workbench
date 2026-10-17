package app

import (
	"maps"
	"reflect"
	"testing"

	"github.com/uvwt/agentdock-protocol/mcpcontract"
	"github.com/uvwt/agentdock/internal/config"
)

func TestCanonicalToolDefinitionsMatchSharedContract(t *testing.T) {
	definitions := make(map[string]ToolDefinition, len(mcpcontract.ToolNames()))
	for _, definition := range ToolDefinitions() {
		if mcpcontract.IsCanonicalTool(definition.Name) {
			definitions[definition.Name] = definition
		}
	}
	if len(definitions) != len(mcpcontract.ToolNames()) {
		t.Fatalf("canonical tool count=%d want=%d", len(definitions), len(mcpcontract.ToolNames()))
	}

	for _, name := range mcpcontract.ToolNames() {
		definition, ok := definitions[name]
		if !ok {
			t.Fatalf("canonical tool %s missing", name)
		}
		wantInput, _ := mcpcontract.InputSchema(name)
		spec, _ := toolSpecByName(name)
		base, _ := spec.Contract(name, config.Config{})
		base = completeToolContract(name, base)
		actualInput, actualOutput := base.ValidationInputSchema, base.ValidationOutputSchema
		if !reflect.DeepEqual(base.PublicInputSchema, definition.InputSchema) {
			t.Fatalf("%s public input contract drifted from its projection", name)
		}
		if len(definition.OutputSchema) != 0 {
			t.Fatalf("%s unexpectedly publishes an MCP output schema", name)
		}
		if !reflect.DeepEqual(executionOutputSchema(base.ValidationOutputSchema), definition.ValidationOutputSchema) {
			t.Fatalf("%s complete validation output extension drifted", name)
		}
		if name == mcpcontract.ToolAgentDockContext {
			// Standalone AgentDock owns its optional local indexes and ACP profile
			// summary. Other fields retain the shared protocol contract.
			actualInput = withoutLocalContextProperty(t, actualInput, "workdir")
			actualInput = withoutLocalContextProperty(t, actualInput, "cursor")
			actualInput = withoutLocalContextProperty(t, actualInput, "max_response_bytes")
			actualInput = withoutLocalContextProperty(t, actualInput, "known_context_revision")
			actualInput = withoutLocalContextProperty(t, actualInput, "force_refresh")
			actualOutput = withoutLocalContextProperty(t, actualOutput, "instruction_files")
			actualOutput = withoutLocalContextProperty(t, actualOutput, "plugins")
			actualOutput = withoutLocalContextProperty(t, actualOutput, "tasks")
			actualOutput = withoutLocalContextProperty(t, actualOutput, "workspace")
			for _, field := range []string{"context_revision", "bootstrap_complete", "dynamic_mcp_summary", "git", "host_tools", "running_sessions", "task_binding", "terminal_completions_pending", "tool_catalog_revision", "workdir"} {
				actualOutput = withoutLocalContextProperty(t, actualOutput, field)
			}
			if bundles := actualOutput["properties"].(map[string]any)["builtin_tool_bundles"]; !reflect.DeepEqual(bundles, builtinToolBundleIndexSchema()) {
				t.Fatalf("built-in tool bundle schema drifted: %#v", bundles)
			}
			actualOutput = withoutLocalContextProperty(t, actualOutput, "builtin_tool_bundles")
			if provider := actualOutput["properties"].(map[string]any)["android_executor"]; !reflect.DeepEqual(provider, androidExecutorStatusSchema()) {
				t.Fatal("Android provider metadata must use its explicit bounded schema")
			}
			actualOutput = withoutLocalContextProperty(t, actualOutput, "android_executor")
			if diagnostic := actualOutput["properties"].(map[string]any)["context_diagnostics"]; !reflect.DeepEqual(diagnostic, contextDiagnosticsSchema()) {
				t.Fatalf("context diagnostics schema drifted: %#v", diagnostic)
			}
			actualOutput = withoutLocalContextProperty(t, actualOutput, "context_diagnostics")
			actualOutput = withoutLocalContextProperty(t, actualOutput, "next_cursor")
			actualOutput = withoutLocalContextProperty(t, actualOutput, "structured_output")
			actualOutput = maps.Clone(actualOutput)
			rootProperties := maps.Clone(actualOutput["properties"].(map[string]any))
			runtimeSchema := maps.Clone(rootProperties["runtime"].(map[string]any))
			for _, field := range []string{"build_commit", "build_date"} {
				property := runtimeSchema["properties"].(map[string]any)[field]
				if !reflect.DeepEqual(property, map[string]any{"type": "string", "minLength": 1}) {
					t.Fatalf("invalid local runtime metadata schema %s: %#v", field, property)
				}
				runtimeSchema = withoutLocalContextProperty(t, runtimeSchema, field)
			}
			rootProperties["runtime"] = runtimeSchema
			dynamic := maps.Clone(rootProperties["dynamic_mcp"].(map[string]any))
			item := dynamic["items"].(map[string]any)
			item = withoutLocalContextProperty(t, item, "catalog_state")
			for field, kind := range map[string]string{"revision": "string", "server_version": "string", "tool_count_known": "boolean"} {
				property := item["properties"].(map[string]any)[field]
				if !reflect.DeepEqual(property, map[string]any{"type": kind}) {
					t.Fatalf("invalid local MCP metadata schema %s: %#v", field, property)
				}
				item = withoutLocalContextProperty(t, item, field)
			}
			for field, expected := range map[string]map[string]any{
				"scope":        {"type": "string", "const": "agentdock_mcp"},
				"origin":       {"type": "string", "pattern": "^agentdock_mcp:[A-Za-z0-9][A-Za-z0-9._-]{0,63}$"},
				"discover_via": {"type": "string", "const": "mcp_tool_list"},
				"inspect_via":  {"type": "string", "const": "mcp_tool_inspect"},
				"invoke_via":   {"type": "string", "const": "mcp_tool_call"},
			} {
				property := item["properties"].(map[string]any)[field]
				if !reflect.DeepEqual(property, expected) {
					t.Fatalf("invalid local MCP route schema %s: %#v", field, property)
				}
				item = withoutLocalContextProperty(t, item, field)
			}
			dynamic["items"] = item
			rootProperties["dynamic_mcp"] = dynamic
			actualOutput["properties"] = rootProperties
		}
		if name == mcpcontract.ToolWorkspaceContext {
			// Direct AgentDock adds optional revision and continuation controls,
			// while preserving every shared Workspace Context field unchanged.
			actualInput = withoutLocalContextProperty(t, actualInput, "known_context_revision")
			actualInput = withoutLocalContextProperty(t, actualInput, "force_refresh")
			actualInput = withoutLocalContextProperty(t, actualInput, "cursor")
			actualInput = withoutLocalContextProperty(t, actualInput, "max_response_bytes")
			actualOutput = withoutLocalContextProperty(t, actualOutput, "context_revision")
			actualOutput = withoutLocalContextProperty(t, actualOutput, "context_diagnostics")
			actualOutput = withoutLocalContextProperty(t, actualOutput, "next_cursor")
			actualOutput = withoutLocalContextProperty(t, actualOutput, "structured_output")
		}
		if !reflect.DeepEqual(actualInput, wantInput) {
			t.Fatalf("%s input schema drifted from shared contract", name)
		}
		wantOutput, _ := workbenchCanonicalOutputSchema(name)
		if name == mcpcontract.ToolAgentDockContext {
			wantProperties := maps.Clone(wantOutput["properties"].(map[string]any))
			wantProperties["acp"] = acpContextSchema()
			wantOutput["properties"] = wantProperties
		}
		if !reflect.DeepEqual(actualOutput, wantOutput) {
			t.Fatalf("%s output schema drifted from shared contract", name)
		}

		wantAnnotations, _ := mcpcontract.AnnotationContract(name)
		annotations := definition.Annotations
		if annotations == nil ||
			annotations.ReadOnlyHint != wantAnnotations.ReadOnlyHint ||
			!reflect.DeepEqual(annotations.DestructiveHint, wantAnnotations.DestructiveHint) ||
			!reflect.DeepEqual(annotations.OpenWorldHint, wantAnnotations.OpenWorldHint) {
			t.Fatalf("%s annotations drifted: got=%#v want=%#v", name, annotations, wantAnnotations)
		}
		wantIdempotent := wantAnnotations.IdempotentHint != nil && *wantAnnotations.IdempotentHint
		if annotations.IdempotentHint != wantIdempotent {
			t.Fatalf("%s idempotentHint=%v want=%v", name, annotations.IdempotentHint, wantIdempotent)
		}
	}
}

func withoutLocalContextProperty(t *testing.T, schema map[string]any, name string) map[string]any {
	t.Helper()
	copy := maps.Clone(schema)
	properties := maps.Clone(schema["properties"].(map[string]any))
	if properties[name] == nil {
		t.Fatalf("local context extension %q missing", name)
	}
	requiredFields, _ := schema["required"].([]string)
	for _, required := range requiredFields {
		if required == name {
			t.Fatalf("local extension %q must remain optional", name)
		}
	}
	delete(properties, name)
	copy["properties"] = properties
	return copy
}
