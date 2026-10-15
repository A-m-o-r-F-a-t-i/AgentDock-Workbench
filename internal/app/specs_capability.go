package app

import (
	"context"

	"github.com/uvwt/agentdock/internal/config"
	toolcapability "github.com/uvwt/agentdock/internal/tool/capability"
)

func capabilityToolSpecs() []ToolSpec {
	annotations := readOnlyToolAnnotations(false)
	annotations.IdempotentHint = true
	return []ToolSpec{{
		Name: toolcapability.ToolLoad, Contract: capabilityToolContract,
		Title:       "Load built-in tool bundle",
		Description: "Load a needed built-in bundle from the agentdock_context index, once per conversation, then call its tool directly. Repeated loads return changed=false. Loading changes visibility only; external MCP discovery, permissions and plugin state remain separate.",
		Annotations: annotations,
		Handler: typedToolHandler(toolcapability.ToolLoad, func(ctx context.Context, r *Runtime, request toolcapability.Request) (Result, error) {
			return r.loadBuiltinToolBundle(ctx, request)
		}),
	}}
}

func capabilityToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolcapability.InputSchema, toolcapability.OutputSchema)
}
