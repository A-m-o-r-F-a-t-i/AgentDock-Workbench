package app

import (
	"maps"

	"github.com/uvwt/agentdock/internal/agentinstructions"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/contextguide"
	toolcapability "github.com/uvwt/agentdock/internal/tool/capability"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

type contextRequest struct {
	Workdir              string `json:"workdir,omitempty"`
	KnownContextRevision string `json:"known_context_revision,omitempty"`
	Cursor               string `json:"cursor,omitempty"`
	MaxResponseBytes     *int   `json:"max_response_bytes,omitempty"`
	ForceRefresh         bool   `json:"force_refresh,omitempty"`
}

func contextToolSpecs() []ToolSpec {
	return []ToolSpec{{
		Name: "agentdock_context", Contract: contextToolContract, Title: "AgentDock context",
		Description: contextguide.Description,
		Handler:     ctxToolHandler((*Runtime).agentDockContextTool),
	}, {
		Name: "workspace_context", Contract: workspaceContextToolContract, Title: "Workspace context",
		Description: "Read selected workspace AGENTS.md rules and local Skills. Omitted workdir inherits the conversation workspace. Selection is read-only and never changes command defaults. Reuse complete rules; known_context_revision permits an unchanged reference after revalidation. Follow cursor or the retained read_file source for more output.",
		Handler:     ctxToolHandler((*Runtime).workspaceContextTool),
	}}
}

// Standalone context owns its profile-aware ACP summary and local indexes.
// The shared Nexus Bridge contract remains unchanged.
func contextToolContract(name string, cfg config.Config) (ToolContract, bool) {
	contract, ok := canonicalToolContract(name, cfg)
	if !ok {
		return ToolContract{}, false
	}
	contract.ValidationInputSchema = maps.Clone(contract.ValidationInputSchema)
	input := maps.Clone(contract.ValidationInputSchema["properties"].(map[string]any))
	input["workdir"] = map[string]any{
		"type": "string", "maxLength": 4096,
		"description": "Existing workspace; empty inherits the conversation or device default.",
	}
	toolcontract.StructuredPagingInput(input)
	input["force_refresh"] = map[string]any{"type": "boolean"}
	input["known_context_revision"] = map[string]any{
		"type": "string", "pattern": "^ctxrev_[0-9a-f]{64}$",
	}
	contract.ValidationInputSchema["properties"] = input
	contract.ValidationOutputSchema = maps.Clone(contract.ValidationOutputSchema)
	output := maps.Clone(contract.ValidationOutputSchema["properties"].(map[string]any))
	output["instruction_files"] = instructionFilesSchema()
	output["context_diagnostics"] = contextDiagnosticsSchema()
	output["git"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"available"}, "properties": map[string]any{
		"available": map[string]any{"type": "boolean"}, "repository_root": map[string]any{"type": "string"}, "branch": map[string]any{"type": "string"}, "head": map[string]any{"type": "string"}, "dirty": map[string]any{"type": "boolean"}, "conflicts": map[string]any{"type": "integer", "minimum": 0}, "changed_paths_total": map[string]any{"type": "integer", "minimum": 0}, "revision": map[string]any{"type": "string"}, "error_code": map[string]any{"type": "string"},
	}}
	output["running_sessions"] = map[string]any{"type": "integer", "minimum": 0}
	output["terminal_completions_pending"] = map[string]any{"type": "integer", "minimum": 0}
	output["android_executor"] = androidExecutorStatusSchema()
	output["acp"] = acpContextSchema()
	output["context_revision"] = map[string]any{"type": "string", "pattern": "^ctxrev_[0-9a-f]{64}$"}
	output["builtin_tool_bundles"] = builtinToolBundleIndexSchema()
	output["bootstrap_complete"] = toolcontract.Boolean("Routing bootstrap is complete independently of optional detail delivery.")
	output["host_tools"] = toolcontract.ObjectArray("Exact currently visible host tool routes.")
	output["dynamic_mcp_summary"] = toolcontract.OpenObject("Non-connecting catalog summary; zero candidates are authoritative only in a complete fresh selected catalog.")
	output["task_binding"] = toolcontract.OpenObject("Current task, thread and workspace binding.")
	output["tool_catalog_revision"] = toolcontract.String("Revision of the currently visible host and MCP directory.")
	output["workdir"] = toolcontract.String("Selected execution workdir.")
	if originalRuntime, ok := output["runtime"].(map[string]any); ok {
		runtimeSchema := maps.Clone(originalRuntime)
		if originalProperties, ok := runtimeSchema["properties"].(map[string]any); ok {
			properties := maps.Clone(originalProperties)
			properties["build_commit"] = map[string]any{"type": "string", "minLength": 1}
			properties["build_date"] = map[string]any{"type": "string", "minLength": 1}
			runtimeSchema["properties"] = properties
			output["runtime"] = runtimeSchema
		}
	}
	output["plugins"] = pluginIndexSchema()
	output["tasks"] = taskIndexSchema()
	output["workspace"] = map[string]any{"type": "object", "additionalProperties": true, "required": []string{"workspace_id", "root", "runtime", "rules_revision"}}
	toolcontract.StructuredPagingOutput(output)
	if cfg.NexusEndpoint == "" {
		if original, ok := output["dynamic_mcp"].(map[string]any); ok {
			index := maps.Clone(original)
			if originalItem, ok := index["items"].(map[string]any); ok {
				item := maps.Clone(originalItem)
				if originalProperties, ok := item["properties"].(map[string]any); ok {
					properties := maps.Clone(originalProperties)
					properties["scope"] = map[string]any{"type": "string", "const": "agentdock_mcp"}
					properties["origin"] = map[string]any{"type": "string", "pattern": "^agentdock_mcp:[A-Za-z0-9][A-Za-z0-9._-]{0,63}$"}
					properties["discover_via"] = map[string]any{"type": "string", "const": "mcp_tool_list"}
					properties["inspect_via"] = map[string]any{"type": "string", "const": "mcp_tool_inspect"}
					properties["invoke_via"] = map[string]any{"type": "string", "const": "mcp_tool_call"}
					properties["revision"] = map[string]any{"type": "string"}
					properties["server_version"] = map[string]any{"type": "string"}
					properties["tool_count_known"] = map[string]any{"type": "boolean"}
					properties["catalog_state"] = toolcontract.OpenObject("Discovered catalog freshness and authority.")
					if _, exists := properties["source_type"]; !exists {
						properties["source_type"] = map[string]any{"type": "string"}
					}
					if _, exists := properties["plugin_name"]; !exists {
						properties["plugin_name"] = map[string]any{"type": "string"}
					}
					item["properties"] = properties
					index["items"] = item
					output["dynamic_mcp"] = index
				}
			}
		}
	}
	contract.ValidationOutputSchema["properties"] = output
	result := newToolContract(name, contract.ValidationInputSchema, contract.ValidationOutputSchema)
	result.PublicOutputSchema = contextDeliveryPublicOutputSchema(result.PublicOutputSchema)
	return result, true
}

// Extend only the standalone workspace tool; the shared Bridge contract stays intact.
func workspaceContextToolContract(name string, cfg config.Config) (ToolContract, bool) {
	contract, ok := canonicalToolContract(name, cfg)
	if !ok {
		return ToolContract{}, false
	}
	input := cloneJSONSchemaMap(contract.ValidationInputSchema)
	properties := input["properties"].(map[string]any)
	properties["known_context_revision"] = map[string]any{
		"type": "string", "pattern": "^ctxrev_[0-9a-f]{64}$",
	}
	toolcontract.StructuredPagingInput(properties)
	properties["force_refresh"] = map[string]any{"type": "boolean"}
	output := cloneJSONSchemaMap(contract.ValidationOutputSchema)
	outputProperties := output["properties"].(map[string]any)
	outputProperties["context_revision"] = map[string]any{"type": "string", "pattern": "^ctxrev_[0-9a-f]{64}$"}
	outputProperties["context_diagnostics"] = map[string]any{
		"type": "object", "additionalProperties": true,
		"required": []string{"complete", "components_complete", "binding_status"},
		"properties": map[string]any{
			"complete":            map[string]any{"type": "boolean"},
			"components_complete": map[string]any{"type": "boolean"},
			"binding_status":      map[string]any{"const": "request_local"},
		},
	}
	toolcontract.StructuredPagingOutput(outputProperties)
	result := newToolContract(name, input, output)
	result.PublicOutputSchema = contextDeliveryPublicOutputSchema(result.PublicOutputSchema)
	return result, true
}

func contextDeliveryPublicOutputSchema(full map[string]any) map[string]any {
	full = cloneJSONSchemaMap(full)
	full["additionalProperties"] = true
	fullProperties, _ := full["properties"].(map[string]any)
	fullProperties = cloneJSONSchemaMap(fullProperties)
	fullProperties["delivery"] = map[string]any{"const": "full"}
	fullProperties["unchanged"] = map[string]any{"const": false}
	full["properties"] = fullProperties
	visibleProperties := cloneJSONSchemaMap(fullProperties)
	visibleProperties["delivery"] = map[string]any{"type": "string", "enum": []string{"full", "reference"}}
	visibleProperties["unchanged"] = map[string]any{"type": "boolean"}
	visibleProperties["context_diagnostics"] = map[string]any{"type": "object", "additionalProperties": true}
	referenceProperties := map[string]any{
		"context_revision": map[string]any{"type": "string", "pattern": "^ctxrev_[0-9a-f]{64}$"},
		"delivery":         map[string]any{"const": "reference"},
		"unchanged":        map[string]any{"const": true},
		"context_diagnostics": map[string]any{
			"type": "object", "additionalProperties": true,
			"required": []string{"complete", "binding_status"},
			"properties": map[string]any{
				"complete":            map[string]any{"const": true},
				"components_complete": map[string]any{"type": "boolean"},
				"binding_status":      map[string]any{"type": "string"},
			},
		},
	}
	reference := map[string]any{
		"type": "object", "additionalProperties": true,
		"required":   []string{"context_revision", "delivery", "unchanged", "context_diagnostics"},
		"properties": referenceProperties,
	}
	return map[string]any{
		"type": "object", "properties": visibleProperties,
		"anyOf": []any{full, reference},
	}
}

func acpContextSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"enabled", "default_profile", "profiles", "description"},
		"properties": map[string]any{
			"enabled":         toolcontract.Boolean("Whether ACP is enabled."),
			"default_profile": toolcontract.String("Default configured ACP profile id."),
			"description":     toolcontract.String("Short ACP usage orientation."),
			"profiles": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{"id", "kind"},
					"properties": map[string]any{
						"id":   toolcontract.String("Enabled ACP profile id."),
						"kind": toolcontract.String("Configured ACP provider kind."),
					},
				},
			},
		},
	}
}

func instructionFilesSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"auto_load", "workdir", "workspace_root", "files"},
		"properties": map[string]any{
			"complete":       map[string]any{"type": "boolean"},
			"revision":       map[string]any{"type": "string"},
			"auto_load":      map[string]any{"type": "boolean"},
			"workdir":        map[string]any{"type": "string"},
			"workspace_root": map[string]any{"type": "string"},
			"files": map[string]any{
				"type": "array", "maxItems": agentinstructions.MaxDirectories + 1,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{"scope", "path", "status"},
					"properties": map[string]any{
						"scope":        map[string]any{"type": "string", "enum": []string{"global", "workspace"}},
						"path":         map[string]any{"type": "string"},
						"status":       map[string]any{"type": "string", "enum": []string{"loaded", "not_found", "empty", "duplicate", "skipped", "error"}},
						"content":      map[string]any{"type": "string", "maxLength": agentinstructions.MaxFileBytes},
						"sha256":       map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"},
						"size_bytes":   map[string]any{"type": "integer", "minimum": 0},
						"reason":       map[string]any{"type": "string"},
						"duplicate_of": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}

// Describe the opt-in provider without weakening the closed context contract.
func androidExecutorStatusSchema() map[string]any {
	capability := map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"ready", "state"},
		"properties": map[string]any{
			"ready": map[string]any{"type": "boolean"},
			"state": map[string]any{"type": "string", "maxLength": 80},
			"uid":   map[string]any{"type": "integer", "minimum": 0, "maximum": 2147483647},
		},
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"protocol", "node_id", "connected", "backends", "inflight"},
		"properties": map[string]any{
			"protocol":  map[string]any{"type": "integer", "const": 1},
			"node_id":   map[string]any{"type": "string"},
			"connected": map[string]any{"type": "boolean"},
			"inflight":  map[string]any{"type": "integer", "minimum": 0, "maximum": 32},
			"backends": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"termux_host": capability, "android_shizuku": capability},
			},
		},
	}
}

func builtinToolBundleIndexSchema() map[string]any {
	return map[string]any{
		"type": "array", "maxItems": len(toolcapability.Bundles()),
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "description", "loaded", "tool_count"},
			"properties": map[string]any{
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"loaded":      map[string]any{"type": "boolean"},
				"tool_count":  map[string]any{"type": "integer", "minimum": 0},
				"tools":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
	}
}
