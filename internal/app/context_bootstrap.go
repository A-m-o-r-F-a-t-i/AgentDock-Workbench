package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/agentinstructions"
)

// ToolCatalogRevision identifies only published directory and conversation
// visibility state. It does not connect to a provider or execute a tool.
func (r *Runtime) ToolCatalogRevision(ctx context.Context) string {
	names := []string{}
	for _, name := range r.ToolNames() {
		if r.BuiltinToolVisible(ctx, name) {
			names = append(names, name)
		}
	}
	mcpRevision := ""
	if r.capabilityManager != nil {
		mcpRevision = r.capabilityManager.DirectoryRevision()
	}
	encoded, _ := json.Marshal(map[string]any{"tools": names, "bundles": r.builtinToolBundleItems(ctx), "mcp": mcpRevision})
	return fmt.Sprintf("tcat_%x", sha256.Sum256(encoded))
}

// Bootstrap is prepared from the exact canonical result. Rule text, Skill
// directories and task histories remain in the retained semantic pages.
func (r *Runtime) prepareContextBootstrap(ctx context.Context, result Result, binding activity.Binding) {
	result["tool_catalog_revision"] = r.ToolCatalogRevision(ctx)
	instructions := maps.Clone(modelObject(result["instruction_files"]))
	if instructions != nil {
		encoded, _ := json.Marshal(instructions)
		var snapshot agentinstructions.Snapshot
		complete := remarshal(instructions, &snapshot) == nil && instructionSnapshotComplete(snapshot)
		instructions["complete"] = complete
		instructions["revision"] = fmt.Sprintf("rules_%x", sha256.Sum256(encoded))
		result["instruction_files"] = instructions
		result["workdir"] = snapshot.Workdir
	}
	workspaceID := stringArg(result, "workspace_id")
	if workspaceID == "" {
		workspaceID = binding.WorkspaceID
	}
	result["task_binding"] = map[string]any{"task_id": binding.TaskID, "thread_id": binding.ThreadID, "workspace_id": workspaceID}
	state, total, known, count := "fresh", 0, true, 0
	priorities := map[string]int{"fresh": 0, "cached": 1, "stale": 2, "refresh_failed": 3, "unavailable": 4}
	servers, _ := result["dynamic_mcp"].([]any)
	for _, raw := range servers {
		server := modelObject(raw)
		count++
		if server["tool_count_known"] != true {
			known = false
		}
		if value, ok := server["tool_count"].(float64); ok {
			total += int(value)
		}
		catalog := modelObject(server["catalog_state"])
		candidate, _ := catalog["state"].(string)
		if candidate == "" {
			candidate = "cached"
		}
		if priorities[candidate] > priorities[state] {
			state = candidate
		}
	}
	for _, raw := range contextWarningObjects(result["warnings"]) {
		if raw["source"] == "dynamic_mcp" || raw["source"] == "plugins" {
			state, known = "unavailable", false
		}
	}
	revision := ""
	if r.capabilityManager != nil {
		revision = r.capabilityManager.DirectoryRevision()
	}
	// This is an unscoped index, not a selected-server negative lookup.
	result["dynamic_mcp_summary"] = map[string]any{"catalog_revision": revision, "state": state, "absence_authoritative": false, "server_count": count, "tool_count": total, "tool_count_known": known}
}

func contextWarningObjects(value any) []map[string]any {
	items := []map[string]any{}
	if values, ok := value.([]any); ok {
		for _, item := range values {
			if object := modelObject(item); object != nil {
				items = append(items, object)
			}
		}
	}
	return items
}

var contextBootstrapHeaderKeys = []string{"runtime", "workspace", "workdir", "workspace_id", "host_tools", "builtin_tool_bundles", "dynamic_mcp_summary", "task_binding", "tool_catalog_revision"}
