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
		Description: "List or load a low-frequency AgentDock built-in tool bundle for the current authenticated conversation. Loading changes only that conversation's visible tools; it does not enable plugins, change permissions, or execute any bundled action.",
		Annotations: annotations,
		Handler: typedToolHandler(toolcapability.ToolLoad, func(ctx context.Context, r *Runtime, request toolcapability.Request) (Result, error) {
			return r.loadBuiltinToolBundle(ctx, request)
		}),
	}}
}

func capabilityToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolcapability.InputSchema, toolcapability.OutputSchema)
}
