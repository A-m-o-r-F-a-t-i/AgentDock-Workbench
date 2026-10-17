package mcp

import (
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
)

func initialServerInstructions(runtime *app.Runtime, cfg config.Config) string {
	custom := cfg.Instructions
	if runtime != nil && cfg.InstructionsFile != "" {
		// Configured instruction files and AGENTS.md have a single authoritative
		// delivery path through agentdock_context, with scope and revision.
		custom = ""
	}
	return serverInstructions(cfg.NexusEndpoint != "", custom)
}
