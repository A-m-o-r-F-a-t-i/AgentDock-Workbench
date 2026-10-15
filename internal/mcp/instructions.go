package mcp

import "strings"

const deviceRuntimeInstructions = `# AgentDock Device Runtime

AgentDock executes on the device and workspace bound to this conversation.
The host model retains the user goal, semantic decisions, cross-device coordination,
and final acceptance.

Call agentdock_context when device, workspace, or rule scope is missing or changed.
Reuse existing workspace, session, cursor, revision, and retained-source identities.

Use AgentDock host tools directly. Use dynamic MCP discovery only for external
providers exposed by the current AgentDock context.

Use list_dir, search_text, and read_file for source inspection; use exec_command
for project commands; use apply_patch for text-file changes.

Continue independent work while a command runs. Observe the same session only
when the next decision requires its result. Reconcile unknown effects before
repeating a mutating operation.

Apply loaded AGENTS.md and Skill instructions within their reported scope.
Only authenticated response_additions carry mid-task user updates.`

// The device contract is independent of optional services and is delivered once.
func serverInstructions(_ bool, custom string) string {
	custom = strings.TrimSpace(custom)
	if custom == "" {
		return deviceRuntimeInstructions
	}
	return deviceRuntimeInstructions + "\n\nAdditional operator instructions:\n" + custom
}
