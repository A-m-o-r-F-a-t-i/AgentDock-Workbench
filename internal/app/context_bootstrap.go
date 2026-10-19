package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"math"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/agentinstructions"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
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
	revision := ""
	if r.capabilityManager != nil {
		revision = r.capabilityManager.DirectoryRevision()
	}
	servers, _ := result["dynamic_mcp"].([]any)
	result["dynamic_mcp_summary"] = contextCatalogSummary(servers, result["warnings"], revision)
}

func contextCatalogSummary(servers []any, warnings any, revision string) map[string]any {
	state, total, known, count := "fresh", 0, true, 0
	priorities := map[string]int{"fresh": 0, "cached": 1, "stale": 2, "refresh_failed": 3, "unavailable": 4}
	for _, raw := range servers {
		server := modelObject(raw)
		count++
		if server["tool_count_known"] != true {
			known = false
		}
		value := -1
		switch server["tool_count"].(type) {
		case int, float64, json.Number:
			value = toolcore.IntArg(server, "tool_count", -1)
		}
		if value < 0 || value > math.MaxInt-total {
			known = false
		} else {
			total += value
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
	for _, raw := range contextWarningObjects(warnings) {
		if raw["source"] == "dynamic_mcp" || raw["source"] == "plugins" {
			state, known = "unavailable", false
		}
	}
	// An unscoped index never establishes a selected-server negative lookup.
	return map[string]any{
		"scope": toolmcp.ScopeAgentDockMCP, "origin": toolmcp.ScopeAgentDockMCP,
		"discover_via": toolmcp.ToolList, "inspect_via": toolmcp.ToolInspect, "invoke_via": toolmcp.ToolCall,
		"catalog_revision": revision, "state": state, "absence_authoritative": false,
		"server_count": count, "tool_count": total, "tool_count_known": known,
	}
}

// Build a compact reference from the already validated immutable Bootstrap.
// Context reuse was admitted with the current cache stamp, including the MCP
// directory revision, so recomputing its server counts through another manager
// can only lose provider-owned facts or mix revisions.
func (r *Runtime) refreshContextBootstrapReference(ctx context.Context, reference, original Result) {
	for _, key := range []string{"bootstrap_complete", "task_binding", "workdir", "workspace_id"} {
		if value, exists := original[key]; exists {
			reference[key] = value
		}
	}
	reference["tool_catalog_revision"] = r.ToolCatalogRevision(ctx)
	if instructions := modelObject(original["instruction_files"]); instructions != nil {
		reference["instruction_files"] = map[string]any{"complete": instructions["complete"], "revision": instructions["revision"]}
	}
	if summary := maps.Clone(modelObject(original["dynamic_mcp_summary"])); summary != nil {
		reference["dynamic_mcp_summary"] = summary
		return
	}
	servers, _ := original["dynamic_mcp"].([]any)
	revision := ""
	if r.capabilityManager != nil {
		revision = r.capabilityManager.DirectoryRevision()
	}
	reference["dynamic_mcp_summary"] = contextCatalogSummary(servers, original["warnings"], revision)
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
