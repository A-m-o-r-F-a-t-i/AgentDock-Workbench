package mcp

import "strings"

const deviceRuntimeInstructions = `# AgentDock Device Runtime

AgentDock executes on the device and workspace bound to this conversation.
The host model retains the user goal, semantic decisions, cross-device coordination,
and final acceptance.

Call agentdock_context when device, workspace, or rule scope is missing or changed.
Reuse existing workspace, session, cursor, revision, and retained-source identities.
Do not start parallel duplicate Context requests. A shared or unchanged Context reference
is terminal for that request unless its explicit detail cursor is needed.

Use AgentDock host tools directly. Search dynamic MCP only for external tools exposed
by the current AgentDock context. An incomplete, stale, or cache-only catalog does not
prove that a capability is unavailable.

Resolve an exact tool name before using semantic tool search. When discovery returns one
resolved tool with its input schema and next_call, invoke it directly; do not repeat search
or inspect. Do not replace a managed MCP call with shell, bridge HTTP, private endpoints,
raw RPC, source execution, process scans, or port scans. Discovery never executes a business tool.

Load a required built-in bundle with capability_load, then refresh tools/list before
retrying the original call. A registry change does not prove the host has refreshed.

Use managed-MCP recovery only after the original business tool returns a connection-class
error. Reuse the original server, tool, arguments, workspace, and origin call identity.
For target, parameter, revision, permission, or domain errors, correct or retry the same
formal MCP path. Follow its required_action when present; otherwise report the required
application state instead of bypassing the managed tool.

Use list_dir, search_text, and read_file for source inspection; use exec_command
for project commands; use apply_patch for text-file changes.

For multi-step or long-running work, use task_manage to create or resume the task,
record concrete steps and checkpoints, and keep status and next_action current.
Task records must reflect observed progress; do not mark work complete before verification.

Reuse running command sessions and delivered completion events. Determine command success
from command status and exit code. Continue independent work while a command runs and
observe the same session only when the next decision requires its result. Reconcile
unknown effects before repeating a mutating operation.

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
