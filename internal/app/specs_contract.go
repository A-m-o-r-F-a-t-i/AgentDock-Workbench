package app

import (
	mcpcontract "github.com/uvwt/agentdock-protocol/mcpcontract"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/evolution"
	toolacp "github.com/uvwt/agentdock/internal/tool/acp"
	toolbrowser "github.com/uvwt/agentdock/internal/tool/browser"
	toolcommand "github.com/uvwt/agentdock/internal/tool/command"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
	toolfile "github.com/uvwt/agentdock/internal/tool/file"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
	toolmedia "github.com/uvwt/agentdock/internal/tool/media"
	toolplugin "github.com/uvwt/agentdock/internal/tool/plugin"
	toolskill "github.com/uvwt/agentdock/internal/tool/skill"
	tooltask "github.com/uvwt/agentdock/internal/tool/task"
)

func staticToolContract(
	name string,
	input func(string) (map[string]any, bool),
	output func(string) (map[string]any, bool),
) (ToolContract, bool) {
	inputSchema, ok := input(name)
	if !ok {
		return ToolContract{}, false
	}
	outputSchema, ok := output(name)
	if !ok {
		return ToolContract{}, false
	}
	return newToolContract(name, inputSchema, outputSchema), true
}

func canonicalToolContract(name string, _ config.Config) (ToolContract, bool) {
	inputSchema, ok := mcpcontract.InputSchema(name)
	if !ok {
		return ToolContract{}, false
	}
	if name == mcpcontract.ToolAgentDockContext {
		return newToolContract(name, inputSchema, mcpcontract.LocalAgentDockContextOutputSchema()), true
	}
	outputSchema, ok := mcpcontract.OutputSchema(name)
	if !ok {
		return ToolContract{}, false
	}
	return newToolContract(name, inputSchema, outputSchema), true
}

func fileToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolfile.InputSchema, toolfile.OutputSchema)
}

func commandToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolcommand.InputSchema, toolcommand.OutputSchema)
}

func taskToolContract(name string, cfg config.Config) (ToolContract, bool) {
	inputSchema, ok := tooltask.InputSchema(name, cfg)
	if !ok {
		return ToolContract{}, false
	}
	if properties, ok := inputSchema["properties"].(map[string]any); ok {
		toolcontract.StructuredPagingInput(properties)
	}
	outputSchema, ok := tooltask.OutputSchema(name, cfg)
	if !ok {
		return ToolContract{}, false
	}
	return newToolContract(name, inputSchema, outputSchema), true
}

func evolutionToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, evolution.InputSchema, evolution.OutputSchema)
}

func acpToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolacp.InputSchema, toolacp.OutputSchema)
}

func skillToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolskill.InputSchema, toolskill.OutputSchema)
}

func mcpToolContract(name string, _ config.Config) (ToolContract, bool) {
	inputSchema, ok := toolmcp.InputSchema(name)
	if !ok {
		return ToolContract{}, false
	}
	outputSchema, ok := toolmcp.OutputSchema(name)
	if !ok {
		return ToolContract{}, false
	}
	outputSchema = cloneJSONSchemaMap(outputSchema)
	stripSchemaDescriptions(outputSchema)
	contract := newToolContract(name, inputSchema, outputSchema)
	contract.PublicInputSchema = compactMCPPublicInput(contract.PublicInputSchema)
	return contract, true
}

func compactMCPPublicInput(schema map[string]any) map[string]any {
	value := cloneJSONSchemaMap(schema)
	stripSchemaDescriptions(value)
	return value
}

func stripSchemaDescriptions(value any) {
	schema, ok := value.(map[string]any)
	if !ok {
		return
	}
	delete(schema, "description")
	// Names in properties and definitions are data, including the name "description".
	for key, child := range schema {
		switch key {
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
			if entries, ok := child.(map[string]any); ok {
				for _, entry := range entries {
					stripSchemaDescriptions(entry)
				}
			}
		case "allOf", "anyOf", "oneOf", "prefixItems":
			if entries, ok := child.([]any); ok {
				for _, entry := range entries {
					stripSchemaDescriptions(entry)
				}
			}
		case "items", "additionalItems", "additionalProperties", "unevaluatedProperties", "unevaluatedItems", "contains", "propertyNames", "not", "if", "then", "else":
			stripSchemaDescriptions(child)
		}
	}
}

func pluginToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolplugin.InputSchema, toolplugin.OutputSchema)
}

func mediaToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolmedia.InputSchema, toolmedia.OutputSchema)
}

func browserToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolbrowser.InputSchema, toolbrowser.OutputSchema)
}
